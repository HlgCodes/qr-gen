package main

import (
	"embed"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

//go:embed web/index.html web/cuenta.html
var webFS embed.FS

func servePage(w http.ResponseWriter, name string) {
	data, err := webFS.ReadFile(name)
	if err != nil {
		http.Error(w, "pagina no encontrada", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(data)
}

func handleIndex(w http.ResponseWriter, r *http.Request) {
	servePage(w, "web/index.html")
}

func handleCuentaPage(w http.ResponseWriter, r *http.Request) {
	servePage(w, "web/cuenta.html")
}

func handleQR(w http.ResponseWriter, r *http.Request) {
	params, err := paramsFromRequest(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	data, ct, err := renderQR(params)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	// Sugerencia de nombre de archivo cuando se descarga.
	if r.URL.Query().Get("download") == "1" {
		ext := "png"
		if params.Format == "svg" {
			ext = "svg"
		}
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="qr.%s"`, ext))
	}
	w.Write(data)
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte("ok"))
}

func handleRobots(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte("User-agent: *\nAllow: /\nSitemap: /sitemap.xml\n"))
}

// helper para strings (evita importar strings en handler si no se usa).
func lower(s string) string { return strings.ToLower(s) }
