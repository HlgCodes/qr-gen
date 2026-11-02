package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"

	_ "golang.org/x/image/webp"

	qrcode "github.com/skip2/go-qrcode"
)


// QRParams agrupa lo que el usuario puede pedir.
type QRParams struct {
	Data    string
	Size    int
	Fg      color.RGBA
	Bg      color.RGBA
	Level   qrcode.RecoveryLevel
	Format  string // "png" o "svg"
	Title   string
	Logo    image.Image
	LogoB64 string // entrada cruda (base64 o data URL)
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

func decodeLogo(b64str string) (image.Image, error) {
	b64str = strings.TrimSpace(b64str)
	if i := strings.Index(b64str, ","); i >= 0 && strings.HasPrefix(b64str, "data:") {
		b64str = b64str[i+1:]
	}
	raw, err := base64.StdEncoding.DecodeString(b64str)
	if err != nil {
		return nil, fmt.Errorf("logo: base64 invalido")
	}
	if len(raw) > 2*1024*1024 {
		return nil, fmt.Errorf("logo demasiado grande (max 2 MB)")
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("logo: formato no soportado (PNG, JPG, WebP)")
	}
	return img, nil
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
	if v := q.Get("title"); v != "" {
		p.Title = v
	}
	if v := q.Get("logo"); v != "" {
		p.LogoB64 = v
	}

	if r.Method == http.MethodPost && r.Body != nil {
		var body struct {
			Data   string `json:"data"`
			Size   int    `json:"size"`
			Fg     string `json:"fg"`
			Bg     string `json:"bg"`
			Level  string `json:"level"`
			Format string `json:"format"`
			Title  string `json:"title"`
			Logo   string `json:"logo"`
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
			if body.Title != "" {
				p.Title = body.Title
			}
			if body.Logo != "" {
				p.LogoB64 = body.Logo
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
	if p.LogoB64 != "" {
		img, err := decodeLogo(p.LogoB64)
		if err != nil {
			return p, err
		}
		p.Logo = img
		// con logo, subir la correccion de error para que siga escaneando
		if p.Level == qrcode.Medium {
			p.Level = qrcode.Highest
		}
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
		return []byte(qrToSVG(p, qr)), "image/svg+xml", nil
	}
	data, err := renderPNG(p, qr)
	if err != nil {
		return nil, "", err
	}
	return data, "image/png", nil
}

func renderPNG(p QRParams, qr *qrcode.QRCode) ([]byte, error) {
	qrSize := p.Size
	titleH := 0
	if strings.TrimSpace(p.Title) != "" {
		titleH = 48
	}
	totalH := qrSize + titleH

	qrImg := qr.Image(qrSize)

	canvas := image.NewRGBA(image.Rect(0, 0, qrSize, totalH))
	draw.Draw(canvas, canvas.Bounds(), &image.Uniform{p.Bg}, image.Point{}, draw.Src)
	draw.Draw(canvas, image.Rect(0, 0, qrSize, qrSize), qrImg, image.Point{}, draw.Over)

	// logo centrado con pad blanco
	if p.Logo != nil {
		logoSize := qrSize / 4
		if logoSize < 32 {
			logoSize = 32
		}
		resized := resizeImage(p.Logo, logoSize, logoSize)
		pad := logoSize/8 + 6
		cx, cy := qrSize/2, qrSize/2
		padRect := image.Rect(cx-logoSize/2-pad, cy-logoSize/2-pad, cx+logoSize/2+pad, cy+logoSize/2+pad)
		draw.Draw(canvas, padRect, &image.Uniform{p.Bg}, image.Point{}, draw.Src)
		off := image.Point{X: cx - logoSize/2, Y: cy - logoSize/2}
		draw.Draw(canvas, resized.Bounds().Add(off), resized, image.Point{}, draw.Over)
	}

	if strings.TrimSpace(p.Title) != "" {
		drawTitle(canvas, p.Title, qrSize, qrSize, titleH)
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, canvas); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func resizeImage(src image.Image, w, h int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Over, nil)
	return dst
}

func drawTitle(dst *image.RGBA, title string, width, yStart, height int) {
	f, err := opentype.Parse(fontBytes)
	if err != nil {
		return
	}
	face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: 20, DPI: 72})
	if err != nil {
		return
	}
	defer face.Close()

	src := image.NewUniform(color.RGBA{0x11, 0x18, 0x27, 255})
	d := &font.Drawer{Dst: dst, Src: src, Face: face}
	adv := font.MeasureString(face, title)
	x := (width - adv.Ceil()) / 2
	if x < 8 {
		x = 8
	}
	y := yStart + height/2 + 7
	d.Dot = fixed.Point26_6{X: fixed.I(x), Y: fixed.I(y)}
	d.DrawString(title)
}

func qrToSVG(p QRParams, qr *qrcode.QRCode) string {
	bm := qr.Bitmap()
	n := len(bm)
	quiet := 4
	total := n + quiet*2
	scale := p.Size / total
	if scale < 1 {
		scale = 1
	}
	offset := quiet * scale
	qrSize := p.Size

	titleH := 0
	if strings.TrimSpace(p.Title) != "" {
		titleH = 48
	}
	totalH := qrSize + titleH

	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" shape-rendering="crispEdges" font-family="system-ui, -apple-system, sans-serif">`, qrSize, totalH, qrSize, totalH)
	fmt.Fprintf(&b, `<rect width="%d" height="%d" fill="%s"/>`, qrSize, totalH, colorHex(p.Bg))
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			if bm[y][x] {
				fmt.Fprintf(&b, `<rect x="%d" y="%d" width="%d" height="%d" fill="%s"/>`, offset+x*scale, offset+y*scale, scale, scale, colorHex(p.Fg))
			}
		}
	}

	// logo centrado con pad blanco
	if p.Logo != nil {
		logoSize := qrSize / 4
		if logoSize < 32 {
			logoSize = 32
		}
		pad := logoSize/8 + 6
		cx, cy := qrSize/2, qrSize/2
		fmt.Fprintf(&b, `<rect x="%d" y="%d" width="%d" height="%d" fill="%s" rx="4"/>`, cx-logoSize/2-pad, cy-logoSize/2-pad, logoSize+pad*2, logoSize+pad*2, colorHex(p.Bg))

		resized := resizeImage(p.Logo, logoSize, logoSize)
		var buf bytes.Buffer
		if err := png.Encode(&buf, resized); err == nil {
			b64 := base64.StdEncoding.EncodeToString(buf.Bytes())
			fmt.Fprintf(&b, `<image x="%d" y="%d" width="%d" height="%d" href="data:image/png;base64,%s"/>`, cx-logoSize/2, cy-logoSize/2, logoSize, logoSize, b64)
		}
	}

	if strings.TrimSpace(p.Title) != "" {
		fmt.Fprintf(&b, `<text x="%d" y="%d" text-anchor="middle" font-size="20" fill="%s">%s</text>`, qrSize/2, qrSize+titleH/2+7, colorHex(p.Fg), escapeXML(p.Title))
	}
	b.WriteString(`</svg>`)
	return b.String()
}

func colorHex(c color.Color) string {
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8)
}

func escapeXML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	return s
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
