package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
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

// go:embed is not available in this toolchain; font bytes live in fontdata.go.

// QRParams agrupa lo que el usuario puede pedir.
type QRParams struct {
	Data       string
	Size       int
	Fg         color.RGBA
	Fg2        color.RGBA
	Bg         color.RGBA
	Level      qrcode.RecoveryLevel
	Format     string // "png" o "svg"
	Title      string
	Logo       image.Image
	LogoB64    string
	Shape      string // "square", "dots", "rounded"
	Frame      string // "none", "simple", "banner"
	FrameText  string
	FrameColor color.RGBA
	Gradient   string // "none", "vertical", "horizontal", "radial"
	LogoBg     bool   // pad blanco detras del logo
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
		Size:       512,
		Fg:         color.RGBA{0, 0, 0, 255},
		Fg2:        color.RGBA{0, 0, 0, 255},
		Bg:         color.RGBA{255, 255, 255, 255},
		Level:      qrcode.Medium,
		Format:     "png",
		Shape:      "square",
		Frame:      "none",
		FrameText:  "SCAN ME",
		FrameColor: color.RGBA{0, 0, 0, 255},
		Gradient:   "none",
		LogoBg:     true,
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
	if v := q.Get("fg2"); v != "" {
		p.Fg2 = parseColor(v, p.Fg2)
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
	if v := q.Get("shape"); v != "" {
		p.Shape = strings.ToLower(v)
	}
	if v := q.Get("frame"); v != "" {
		p.Frame = strings.ToLower(v)
	}
	if v := q.Get("frameText"); v != "" {
		p.FrameText = v
	}
	if v := q.Get("frameColor"); v != "" {
		p.FrameColor = parseColor(v, p.FrameColor)
	}
	if v := q.Get("gradient"); v != "" {
		p.Gradient = strings.ToLower(v)
	}
	if v := q.Get("logoBg"); v != "" {
		p.LogoBg = v != "0" && v != "false"
	}
	if v := q.Get("logo"); v != "" {
		p.LogoB64 = v
	}

	if r.Method == http.MethodPost && r.Body != nil {
		var body struct {
			Data       string `json:"data"`
			Size       int    `json:"size"`
			Fg         string `json:"fg"`
			Fg2        string `json:"fg2"`
			Bg         string `json:"bg"`
			Level      string `json:"level"`
			Format     string `json:"format"`
			Title      string `json:"title"`
			Shape      string `json:"shape"`
			Frame      string `json:"frame"`
			FrameText  string `json:"frameText"`
			FrameColor string `json:"frameColor"`
			Gradient   string `json:"gradient"`
			LogoBg     *bool  `json:"logoBg"`
			Logo       string `json:"logo"`
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
			if body.Fg2 != "" {
				p.Fg2 = parseColor(body.Fg2, p.Fg2)
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
			if body.Shape != "" {
				p.Shape = strings.ToLower(body.Shape)
			}
			if body.Frame != "" {
				p.Frame = strings.ToLower(body.Frame)
			}
			if body.FrameText != "" {
				p.FrameText = body.FrameText
			}
			if body.FrameColor != "" {
				p.FrameColor = parseColor(body.FrameColor, p.FrameColor)
			}
			if body.Gradient != "" {
				p.Gradient = strings.ToLower(body.Gradient)
			}
			if body.LogoBg != nil {
				p.LogoBg = *body.LogoBg
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

// ---------- PNG ----------

func renderPNG(p QRParams, qr *qrcode.QRCode) ([]byte, error) {
	// qr.Bitmap() ya trae la zona de silencio (4 modulos por lado):
	// no hay que sumarla ni desplazar el dibujo, solo escalar y centrar.
	bm := qr.Bitmap()
	n := len(bm)

	scale := p.Size / n
	if scale < 1 {
		scale = 1
	}
	qrSize := scale * n

	// marco
	framePad := 0
	frameBottom := 0
	if p.Frame != "none" {
		framePad = p.Size / 28
		if framePad < 18 {
			framePad = 18
		}
		if p.Frame == "banner" {
			frameBottom = p.Size / 11
			if frameBottom < 44 {
				frameBottom = 44
			}
		}
	}

	// titulo debajo del codigo (solo cuando el marco no trae banner)
	caption := strings.TrimSpace(p.Title)
	captionH := 0
	if p.Frame != "banner" && caption != "" {
		captionH = p.Size / 11
		if captionH < 44 {
			captionH = 44
		}
	}

	totalW := qrSize + framePad*2
	totalH := qrSize + framePad*2 + frameBottom + captionH
	// sin marco el lienzo mide exactamente lo pedido, para que el QR quede
	// centrado con el margen sobrante repartido a partes iguales
	if p.Frame == "none" && totalW < p.Size {
		totalW = p.Size
	}
	if p.Frame == "none" && totalH < p.Size+captionH {
		totalH = p.Size + captionH
	}

	canvas := image.NewRGBA(image.Rect(0, 0, totalW, totalH))
	// fondo
	draw.Draw(canvas, canvas.Bounds(), &image.Uniform{p.Bg}, image.Point{}, draw.Src)

	// area disponible para el codigo (sin banner ni titulo)
	qrAreaH := totalH - frameBottom - captionH
	qrOffX := (totalW - qrSize) / 2
	qrOffY := (qrAreaH - qrSize) / 2
	drawModules(canvas, bm, p, qrOffX, qrOffY, scale, n)

	// logo
	if p.Logo != nil {
		logoSize := qrSize / 4
		if logoSize < 32 {
			logoSize = 32
		}
		resized := resizeImage(p.Logo, logoSize, logoSize)
		cx := qrOffX + qrSize/2
		cy := qrOffY + qrSize/2
		if p.LogoBg {
			pad := logoSize/8 + 6
			padRect := image.Rect(cx-logoSize/2-pad, cy-logoSize/2-pad, cx+logoSize/2+pad, cy+logoSize/2+pad)
			draw.Draw(canvas, padRect, &image.Uniform{p.Bg}, image.Point{}, draw.Src)
		}
		off := image.Point{X: cx - logoSize/2, Y: cy - logoSize/2}
		draw.Draw(canvas, resized.Bounds().Add(off), resized, image.Point{}, draw.Over)
	}

	// marco: borde
	if p.Frame != "none" {
		fc := p.FrameColor
		bw := p.Size / 128
		if bw < 4 {
			bw = 4
		}
		drawRectBorder(canvas, 0, 0, totalW, totalH, bw*3, bw, fc)
	}
	// texto del marco
	if p.Frame == "banner" && strings.TrimSpace(p.FrameText) != "" {
		drawTitle(canvas, p.FrameText, totalW, qrAreaH, frameBottom, frameBottom/2-2, p.FrameColor)
	}
	// titulo debajo del codigo
	if captionH > 0 {
		drawTitle(canvas, caption, totalW, totalH-captionH, captionH, captionH/2-2, p.Fg)
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, canvas); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func drawModules(canvas *image.RGBA, bm [][]bool, p QRParams, offX, offY, scale, n int) {
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			if !bm[y][x] {
				continue
			}
			col := moduleColor(p, x, y, n)
			px := offX + x*scale
			py := offY + y*scale
			switch p.Shape {
			case "dots":
				cx := px + scale/2
				cy := py + scale/2
				r := scale/2 - 1
				if r < 1 {
					r = 1
				}
				for dy := -r; dy <= r; dy++ {
					for dx := -r; dx <= r; dx++ {
						if dx*dx+dy*dy <= r*r {
							canvas.Set(cx+dx, cy+dy, col)
						}
					}
				}
			case "rounded":
				r := scale / 4
				if r < 1 {
					r = 1
				}
				drawRoundedRect(canvas, px, py, scale, scale, r, col)
			default:
				for dy := 0; dy < scale; dy++ {
					for dx := 0; dx < scale; dx++ {
						canvas.Set(px+dx, py+dy, col)
					}
				}
			}
		}
	}
}

func moduleColor(p QRParams, x, y, n int) color.Color {
	if p.Gradient == "none" {
		return p.Fg
	}
	t := 0.0
	switch p.Gradient {
	case "vertical":
		if n > 1 {
			t = float64(y) / float64(n-1)
		}
	case "horizontal":
		if n > 1 {
			t = float64(x) / float64(n-1)
		}
	case "radial":
		dx := float64(x) - float64(n-1)/2
		dy := float64(y) - float64(n-1)/2
		maxd := float64(n) / 2 * 1.414
		t = math.Sqrt(dx*dx+dy*dy) / maxd
		if t > 1 {
			t = 1
		}
	}
	return lerpColor(p.Fg, p.Fg2, t)
}

func lerpColor(a, b color.RGBA, t float64) color.RGBA {
	return color.RGBA{
		R: uint8(float64(a.R)*(1-t) + float64(b.R)*t),
		G: uint8(float64(a.G)*(1-t) + float64(b.G)*t),
		B: uint8(float64(a.B)*(1-t) + float64(b.B)*t),
		A: 255,
	}
}

func drawRoundedRect(img *image.RGBA, x, y, w, h, r int, c color.Color) {
	for dy := 0; dy < h; dy++ {
		for dx := 0; dx < w; dx++ {
			if inRoundedRect(dx, dy, w, h, r) {
				img.Set(x+dx, y+dy, c)
			}
		}
	}
}

func inRoundedRect(px, py, w, h, r int) bool {
	if px < 0 || py < 0 || px >= w || py >= h {
		return false
	}
	if r <= 0 {
		return true
	}
	if px >= r && px < w-r {
		return true
	}
	if py >= r && py < h-r {
		return true
	}
	var cx, cy float64
	left := px < r
	right := px >= w-r
	top := py < r
	bottom := py >= h-r
	if left && top {
		cx, cy = float64(r)-0.5, float64(r)-0.5
	} else if right && top {
		cx, cy = float64(w)-float64(r)-0.5, float64(r)-0.5
	} else if left && bottom {
		cx, cy = float64(r)-0.5, float64(h)-float64(r)-0.5
	} else if right && bottom {
		cx, cy = float64(w)-float64(r)-0.5, float64(h)-float64(r)-0.5
	} else {
		return true
	}
	dx := float64(px) + 0.5 - cx
	dy := float64(py) + 0.5 - cy
	return dx*dx+dy*dy <= float64(r)*float64(r)
}

func drawRectBorder(img *image.RGBA, x, y, w, h, radius, thickness int, c color.Color) {
	// borde exterior
	for i := 0; i < thickness; i++ {
		// top
		for dx := 0; dx < w; dx++ {
			if inRoundedRect(dx, i, w, h, radius) && !inRoundedRect(dx, i+1, w, h, radius-thickness+1) {
				img.Set(x+dx, y+i, c)
			}
		}
		// bottom
		for dx := 0; dx < w; dx++ {
			if inRoundedRect(dx, h-1-i, w, h, radius) && !inRoundedRect(dx, h-2-i, w, h, radius-thickness+1) {
				img.Set(x+dx, y+h-1-i, c)
			}
		}
		// left
		for dy := 0; dy < h; dy++ {
			if inRoundedRect(i, dy, w, h, radius) && !inRoundedRect(i+1, dy, w, h, radius-thickness+1) {
				img.Set(x+i, y+dy, c)
			}
		}
		// right
		for dy := 0; dy < h; dy++ {
			if inRoundedRect(w-1-i, dy, w, h, radius) && !inRoundedRect(w-2-i, dy, w, h, radius-thickness+1) {
				img.Set(x+w-1-i, y+dy, c)
			}
		}
	}
}

func drawTitle(img *image.RGBA, title string, width, yStart, height, fontSize int, col color.Color) {
	if fontSize < 8 {
		fontSize = 8
	}
	f, err := opentype.Parse(fontBytes)
	if err != nil {
		return
	}
	face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: float64(fontSize), DPI: 72})
	if err != nil {
		return
	}
	defer face.Close()

	src := image.NewUniform(col)
	d := &font.Drawer{Dst: img, Src: src, Face: face}
	adv := font.MeasureString(face, title)
	x := (width - adv.Ceil()) / 2
	if x < 8 {
		x = 8
	}
	y := yStart + height/2 + fontSize*7/20
	d.Dot = fixed.Point26_6{X: fixed.I(x), Y: fixed.I(y)}
	d.DrawString(title)
}

func resizeImage(src image.Image, w, h int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Over, nil)
	return dst
}

func qrToSVG(p QRParams, qr *qrcode.QRCode) string {
	// qr.Bitmap() ya incluye la zona de silencio: solo escala y centra.
	bm := qr.Bitmap()
	n := len(bm)

	scale := p.Size / n
	if scale < 1 {
		scale = 1
	}
	qrSize := scale * n

	framePad := 0
	frameBottom := 0
	if p.Frame != "none" {
		framePad = p.Size / 28
		if framePad < 18 {
			framePad = 18
		}
		if p.Frame == "banner" {
			frameBottom = p.Size / 11
			if frameBottom < 44 {
				frameBottom = 44
			}
		}
	}

	caption := strings.TrimSpace(p.Title)
	captionH := 0
	if p.Frame != "banner" && caption != "" {
		captionH = p.Size / 11
		if captionH < 44 {
			captionH = 44
		}
	}

	totalW := qrSize + framePad*2
	totalH := qrSize + framePad*2 + frameBottom + captionH
	if p.Frame == "none" && totalW < p.Size {
		totalW = p.Size
	}
	if p.Frame == "none" && totalH < p.Size+captionH {
		totalH = p.Size + captionH
	}

	qrAreaH := totalH - frameBottom - captionH
	qrOffX := (totalW - qrSize) / 2
	qrOffY := (qrAreaH - qrSize) / 2

	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" shape-rendering="crispEdges" font-family="system-ui, -apple-system, sans-serif">`, totalW, totalH, totalW, totalH)
	fmt.Fprintf(&b, `<rect width="%d" height="%d" fill="%s"/>`, totalW, totalH, colorHex(p.Bg))

	// modulos
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			if !bm[y][x] {
				continue
			}
			col := moduleColor(p, x, y, n)
			px := qrOffX + x*scale
			py := qrOffY + y*scale
			switch p.Shape {
			case "dots":
				cx := px + scale/2
				cy := py + scale/2
				r := scale/2 - 1
				if r < 1 {
					r = 1
				}
				fmt.Fprintf(&b, `<circle cx="%d" cy="%d" r="%d" fill="%s"/>`, cx, cy, r, colorHex(col))
			case "rounded":
				r := scale / 4
				if r < 1 {
					r = 1
				}
				fmt.Fprintf(&b, `<rect x="%d" y="%d" width="%d" height="%d" rx="%d" ry="%d" fill="%s"/>`, px, py, scale, scale, r, r, colorHex(col))
			default:
				fmt.Fprintf(&b, `<rect x="%d" y="%d" width="%d" height="%d" fill="%s"/>`, px, py, scale, scale, colorHex(col))
			}
		}
	}

	// logo
	if p.Logo != nil {
		logoSize := qrSize / 4
		if logoSize < 32 {
			logoSize = 32
		}
		pad := logoSize/8 + 6
		cx := qrOffX + qrSize/2
		cy := qrOffY + qrSize/2
		if p.LogoBg {
			fmt.Fprintf(&b, `<rect x="%d" y="%d" width="%d" height="%d" fill="%s" rx="4"/>`, cx-logoSize/2-pad, cy-logoSize/2-pad, logoSize+pad*2, logoSize+pad*2, colorHex(p.Bg))
		}
		resized := resizeImage(p.Logo, logoSize, logoSize)
		var buf bytes.Buffer
		if err := png.Encode(&buf, resized); err == nil {
			b64 := base64.StdEncoding.EncodeToString(buf.Bytes())
			fmt.Fprintf(&b, `<image x="%d" y="%d" width="%d" height="%d" href="data:image/png;base64,%s"/>`, cx-logoSize/2, cy-logoSize/2, logoSize, logoSize, b64)
		}
	}

	// marco: borde
	if p.Frame != "none" {
		fc := p.FrameColor
		bw := p.Size / 128
		if bw < 4 {
			bw = 4
		}
		fmt.Fprintf(&b, `<rect x="%d" y="%d" width="%d" height="%d" rx="%d" ry="%d" fill="none" stroke="%s" stroke-width="%d"/>`, bw/2, bw/2, totalW-bw, totalH-bw, bw*3, bw*3, colorHex(fc), bw)
	}
	// texto del marco
	if p.Frame == "banner" && strings.TrimSpace(p.FrameText) != "" {
		fmt.Fprintf(&b, `<text x="%d" y="%d" text-anchor="middle" font-size="%d" font-weight="600" fill="%s">%s</text>`, totalW/2, qrAreaH+frameBottom/2+frameBottom*7/40, frameBottom/2-2, colorHex(p.FrameColor), escapeXML(p.FrameText))
	}
	// titulo debajo del codigo
	if captionH > 0 {
		fmt.Fprintf(&b, `<text x="%d" y="%d" text-anchor="middle" font-size="%d" font-weight="600" fill="%s">%s</text>`, totalW/2, totalH-captionH/2+captionH*7/40, captionH/2-2, colorHex(p.Fg), escapeXML(caption))
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

// ---------- rate limiting ----------

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
