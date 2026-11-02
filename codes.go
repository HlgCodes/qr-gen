package main

import (
	"crypto/rand"
	"encoding/json"

	"fmt"
	"html"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const slugAlphabet = "23456789abcdefghjkmnpqrstuvwxyz" // sin 0,1,l,o,i para que no se confundan

// publicBaseURL es el dominio con el que se arman los enlaces de los QR dinamicos.
func publicBaseURL(r *http.Request) string {
	if v := strings.TrimRight(envOr("PUBLIC_BASE_URL", ""), "/"); v != "" {
		return v
	}
	scheme := "http"
	if isHTTPS(r) {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func newSlug(n int) (string, error) {
	buf := make([]byte, n)
	raw := make([]byte, n)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	for i := range buf {
		buf[i] = slugAlphabet[int(raw[i])%len(slugAlphabet)]
	}
	return string(buf), nil
}

// destination devuelve lo que va DENTRO del QR.
// Estatico: el contenido tal cual. Dinamico: el enlace corto que redirige.
func destination(baseURL string, c *Code) string {
	if c.Kind == "dynamic" {
		return baseURL + "/r/" + c.Slug
	}
	return c.Data
}

// ---------- guardar un codigo ----------

type saveCodeReq struct {
	Kind   string          `json:"kind"` // static | dynamic
	Name   string          `json:"name"`
	Data   string          `json:"data"`
	Design json.RawMessage `json:"design"`
}

func handleSaveCode(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if u == nil {
		writeErr(w, http.StatusUnauthorized, "inicia sesión para guardar")
		return
	}
	var in saveCodeReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 3<<20)).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "datos invalidos")
		return
	}
	kind := "static"
	if in.Kind == "dynamic" {
		kind = "dynamic"
	}
	data := strings.TrimSpace(in.Data)
	if data == "" {
		writeErr(w, http.StatusBadRequest, "falta el contenido del código")
		return
	}
	if len(data) > 2000 {
		writeErr(w, http.StatusBadRequest, "el contenido es demasiado largo")
		return
	}

	c := &Code{
		UserID: u.ID,
		Kind:   kind,
		Name:   strings.TrimSpace(in.Name),
		Data:   data,
		Design: "{}",
		Active: true,
	}
	if c.Name == "" {
		c.Name = nombrePorDefecto(data)
	}
	if len(in.Design) > 0 {
		// solo se guarda si es un objeto JSON
		var probe map[string]any
		if err := json.Unmarshal(in.Design, &probe); err == nil {
			c.Design = string(in.Design)
		}
	}

	for intento := 0; intento < 5; intento++ {
		slug, err := newSlug(7)
		if err != nil {
			break
		}
		c.Slug = slug
		if err := createCode(c); err == nil {
			writeJSON(w, http.StatusCreated, map[string]any{
				"id":   c.ID,
				"slug": c.Slug,
				"kind": c.Kind,
				"url":  destination(publicBaseURL(r), c),
			})
			return
		}
	}
	writeErr(w, http.StatusInternalServerError, "no se pudo guardar el código")
}

func nombrePorDefecto(data string) string {
	s := strings.TrimSpace(data)
	if u, err := url.Parse(s); err == nil && u.Host != "" {
		s = u.Host + u.Path
	}
	s = strings.TrimSpace(strings.TrimPrefix(s, "www."))
	if len([]rune(s)) > 40 {
		s = string([]rune(s)[:40]) + "…"
	}
	if s == "" {
		s = "Código sin nombre"
	}
	return s
}

// ---------- listar ----------

type codeView struct {
	Code
	Target string `json:"target"` // contenido que va dentro del QR
}

func handleListCodes(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if u == nil {
		writeErr(w, http.StatusUnauthorized, "inicia sesión")
		return
	}
	list, err := codesByUser(u.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "no se pudieron cargar tus códigos")
		return
	}
	base := publicBaseURL(r)
	out := make([]codeView, 0, len(list))
	for _, c := range list {
		out = append(out, codeView{Code: c, Target: destination(base, &c)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"codes": out, "base": base})
}

// ---------- editar / borrar ----------

func handleUpdateCode(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if u == nil {
		writeErr(w, http.StatusUnauthorized, "inicia sesión")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "id invalido")
		return
	}
	c, err := codeByID(u.ID, id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "ese código no existe")
		return
	}

	var in struct {
		Name   *string `json:"name"`
		Data   *string `json:"data"`
		Active *bool   `json:"active"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "datos invalidos")
		return
	}

	name, data, active := c.Name, c.Data, c.Active
	if in.Name != nil {
		name = strings.TrimSpace(*in.Name)
		if len(name) > 80 {
			writeErr(w, http.StatusBadRequest, "el nombre es demasiado largo")
			return
		}
	}
	if in.Data != nil {
		if c.Kind != "dynamic" {
			// un QR estatico lleva el contenido impreso: cambiarlo lo rompe
			writeErr(w, http.StatusBadRequest, "un código estático no se puede editar")
			return
		}
		data = strings.TrimSpace(*in.Data)
		if data == "" {
			writeErr(w, http.StatusBadRequest, "el destino no puede quedar vacío")
			return
		}
		if len(data) > 2000 {
			writeErr(w, http.StatusBadRequest, "el destino es demasiado largo")
			return
		}
	}
	if in.Active != nil {
		active = *in.Active
	}

	if err := updateCode(u.ID, id, name, data, active); err != nil {
		writeErr(w, http.StatusNotFound, "ese código no existe")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func handleDeleteCode(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if u == nil {
		writeErr(w, http.StatusUnauthorized, "inicia sesión")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "id invalido")
		return
	}
	if err := deleteCode(u.ID, id); err != nil {
		writeErr(w, http.StatusNotFound, "ese código no existe")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---------- imagen de un codigo guardado ----------

func handleCodeImage(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if u == nil {
		http.Error(w, "no autorizado", http.StatusUnauthorized)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "id invalido", http.StatusBadRequest)
		return
	}
	c, err := codeByID(u.ID, id)
	if err != nil {
		http.Error(w, "no encontrado", http.StatusNotFound)
		return
	}

	p, err := paramsFromDesign(c.Design, destination(publicBaseURL(r), c))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// la miniatura de la lista pide un tamaño chico y en SVG
	if v := r.URL.Query().Get("size"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 64 && n <= 2048 {
			p.Size = n
		}
	}
	if r.URL.Query().Get("format") == "svg" {
		p.Format = "svg"
	}

	data, ct, err := renderQR(p)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "private, max-age=60")
	w.Write(data)
}

// ---------- redireccion publica de los QR dinamicos ----------

func handleRedirect(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	c, err := codeBySlug(slug)
	if err != nil || c.Kind != "dynamic" {
		paginaNoEncontrada(w, "Este enlace no existe.")
		return
	}
	if !c.Active {
		paginaNoEncontrada(w, "Este código está desactivado.")
		return
	}
	go addScan(c.ID)

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, c.Data, http.StatusFound)
}

func paginaNoEncontrada(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	fmt.Fprintf(w, `<!doctype html><html lang="es"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Enlace no disponible</title>
<style>
:root{color-scheme:light}
body{margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;background:#f9fafb;
font-family:Inter,system-ui,sans-serif;color:#111827}
main{max-width:22rem;padding:2rem;text-align:center}
h1{font-size:1.125rem;margin:0 0 .5rem}
p{margin:0;color:#6b7280;font-size:.875rem;line-height:1.5}
a{display:inline-block;margin-top:1.5rem;padding:.625rem 1rem;border-radius:.75rem;background:#1a1a1a;
color:#fff;text-decoration:none;font-size:.8125rem;font-weight:600}
</style></head><body><main>
<h1>No disponible</h1><p>%s</p>
<a href="/">Crear un código QR</a>
</main></body></html>`, html.EscapeString(msg))
}
