package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image/color"
	"image/png"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	qrcode "github.com/skip2/go-qrcode"
)

// QRParams agrupa lo que el usuario puede pedir.
type QRParams struct {
	Data   string
	Size   int
	Fg     color.RGBA
	Bg     color.RGBA
	Level  qrcode.RecoveryLevel
	Format string // "png" o "svg"
}

var hexRe = regexp.MustCompile(`^#?[0-9a-fA-F]{6}$`)

func parseColor(s string, def color.RGBA) color.RGBA {
	s = strings.TrimSpace(s)
	if !hexRe.MatchString(s) {
		return def
	}
	s = strings.TrimPrefix(s, "#")
	v, _ := strconv.ParseUint(s, 16, 32)
	return color.RGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 255}
}

func parseLevel(s string) qrcode.RecoveryLevel {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "L":
		return qrcode.Low
	case "Q":
		return qrcode.High
	case "H":
		return qrcode.Highest
	default:
		return qrcode.Medium
	}
}

func paramsFromRequest(r *http.Request) (QRParams, error) {
	p := QRParams{
		Size:   512,
		Fg:     color.RGBA{0, 0, 0, 255},
		Bg:     color.RGBA{255, 255, 255, 255},
		Level:  qrcode.Medium,
		Format: "png",
	}
	q := r.URL.Query()
	if v := q.Get("data"); v != "" {
		p.Data = v
	}
	if v := q.Get("size"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			p.Size = n
		}
	}
	if v := q.Get("fg"); v != "" {
		p.Fg = parseColor(v, p.Fg)
	}
	if v := q.Get("bg"); v != "" {
		p.Bg = parseColor(v, p.Bg)
	}
	if v := q.Get("level"); v != "" {
		p.Level = parseLevel(v)
	}
	if v := q.Get("format"); v != "" {
		p.Format = strings.ToLower(v)
	}

	if r.Method == http.MethodPost && r.Body != nil {
		var body struct {
			Data   string `json:"data"`
			Size   int    `json:"size"`
			Fg     string `json:"fg"`
			Bg     string `json:"bg"`
			Level  string `json:"level"`
			Format string `json:"format"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
			if body.Data != "" {
				p.Data = body.Data
			}
			if body.Size != 0 {
				p.Size = body.Size
			}
			if body.Fg != "" {
				p.Fg = parseColor(body.Fg, p.Fg)
			}
			if body.Bg != "" {
				p.Bg = parseColor(body.Bg, p.Bg)
			}
			if body.Level != "" {
				p.Level = parseLevel(body.Level)
			}
			if body.Format != "" {
				p.Format = strings.ToLower(body.Format)
			}
		}
	}

	p.Data = strings.TrimSpace(p.Data)
	if p.Data == "" {
		return p, fmt.Errorf("falta el texto o la URL")
	}
	if p.Size < 64 {
		p.Size = 64
	}
	if p.Size > 2048 {
		p.Size = 2048
	}
	if p.Format != "svg" {
		p.Format = "png"
	}
	return p, nil
}

func renderQR(p QRParams) ([]byte, string, error) {
	qr, err := qrcode.New(p.Data, p.Level)
	if err != nil {
		return nil, "", err
	}
	qr.ForegroundColor = p.Fg
	qr.BackgroundColor = p.Bg

	if p.Format == "svg" {
		return []byte(qrToSVG(qr, p.Size)), "image/svg+xml", nil
	}

	img := qr.Image(p.Size)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), "image/png", nil
}

// qrToSVG dibuja el codigo con zona de silencio de 4 modulos, igual que la PNG.
func qrToSVG(qr *qrcode.QRCode, size int) string {
	bm := qr.Bitmap()
	n := len(bm)
	quiet := 4
	total := n + quiet*2
	scale := size / total
	if scale < 1 {
		scale = 1
	}
	offset := quiet * scale

	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" shape-rendering="crispEdges">`, size, size, size, size)
	fmt.Fprintf(&b, `<rect width="%d" height="%d" fill="%s"/>`, size, size, colorHex(qr.BackgroundColor))
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			if bm[y][x] {
				fmt.Fprintf(&b, `<rect x="%d" y="%d" width="%d" height="%d" fill="%s"/>`,
					offset+x*scale, offset+y*scale, scale, scale, colorHex(qr.ForegroundColor))
			}
		}
	}
	b.WriteString(`</svg>`)
	return b.String()
}

func colorHex(c color.Color) string {
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8)
}

// --- rate limiting por IP ---

type bucket struct {
	count int
	reset time.Time
}

var (
	rlMu      sync.Mutex
	rlBuckets = map[string]*bucket{}
)

func init() {
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			rlMu.Lock()
			now := time.Now()
			for ip, b := range rlBuckets {
				if now.After(b.reset) {
					delete(rlBuckets, ip)
				}
			}
			rlMu.Unlock()
		}
	}()
}

func clientIP(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		if i := strings.Index(fwd, ","); i >= 0 {
			fwd = fwd[:i]
		}
		return strings.TrimSpace(fwd)
	}
	return r.RemoteAddr
}

func rateLimit(next http.HandlerFunc, limit int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		rlMu.Lock()
		b, ok := rlBuckets[ip]
		now := time.Now()
		if !ok || now.After(b.reset) {
			b = &bucket{count: 0, reset: now.Add(time.Minute)}
			rlBuckets[ip] = b
		}
		b.count++
		over := b.count > limit
		rlMu.Unlock()
		if over {
			w.Header().Set("Retry-After", "60")
			http.Error(w, "demasiadas solicitudes, intenta en un minuto", http.StatusTooManyRequests)
			return
		}
		next(w, r)
	}
}

// base64 helper por si se necesita en la API JSON.
func b64(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}
