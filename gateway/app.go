package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
	"net/http"
	"strconv"
	"sync"
)

// What makes the page installable as an app (Chrome's "Install app", Safari's "Add to Home Screen"): a manifest in
// standalone display, which drops the browser's bars for the panel's own, and its icons. Served after the session
// check like client.js; the manifest link carries crossorigin="use-credentials" so the browser sends the cookie.
const (
	manifestPath = "lzr1-manifest.json"
	iconPath     = "lzr1-icon-" // + 192.png, 512.png
)

const manifestJSON = `{
  "name": "lzr1 panel",
  "short_name": "lzr1",
  "description": "Claude Code and opencode sessions, from anywhere",
  "start_url": "./",
  "scope": "./",
  "display": "standalone",
  "orientation": "any",
  "background_color": "#000000",
  "theme_color": "#1c1c1e",
  "icons": [
    {"src": "` + iconPath + `192.png", "sizes": "192x192", "type": "image/png", "purpose": "any"},
    {"src": "` + iconPath + `512.png", "sizes": "512x512", "type": "image/png", "purpose": "any"},
    {"src": "` + iconPath + `512.png", "sizes": "512x512", "type": "image/png", "purpose": "maskable"}
  ]
}
`

var (
	iconMu    sync.Mutex
	iconCache = map[int][]byte{}
)

// drawIcon draws the app icon at size x size: a dark square with a yellow prompt (">_") inside the maskable safe zone.
func drawIcon(size int) []byte {
	iconMu.Lock()
	defer iconMu.Unlock()
	if b, ok := iconCache[size]; ok {
		return b
	}
	bg, fg := color.RGBA{0x1c, 0x1c, 0x1e, 0xff}, color.RGBA{0xff, 0xd6, 0x0a, 0xff}
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	n := float64(size)
	// Segments in unit coordinates and a half thickness: the chevron's two strokes, then the underscore.
	type seg struct{ x1, y1, x2, y2 float64 }
	segs := []seg{{0.30, 0.34, 0.47, 0.50}, {0.47, 0.50, 0.30, 0.66}, {0.54, 0.68, 0.72, 0.68}}
	half := 0.045
	dist := func(px, py float64, s seg) float64 {
		dx, dy := s.x2-s.x1, s.y2-s.y1
		t := math.Max(0, math.Min(1, ((px-s.x1)*dx+(py-s.y1)*dy)/(dx*dx+dy*dy)))
		return math.Hypot(px-(s.x1+t*dx), py-(s.y1+t*dy))
	}
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			px, py := (float64(x)+0.5)/n, (float64(y)+0.5)/n
			c := bg
			for _, s := range segs {
				if d := dist(px, py, s); d < half {
					c = fg
				}
			}
			img.SetRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	iconCache[size] = buf.Bytes()
	return iconCache[size]
}

// serveApp serves the manifest and the icons; ServeHTTP has checked the identity, Origin and session first.
func serveApp(w http.ResponseWriter, r *http.Request, name string) bool {
	var body []byte
	var typ string
	switch name {
	case manifestPath:
		body, typ = []byte(manifestJSON), "application/manifest+json; charset=utf-8"
	case iconPath + "192.png":
		body, typ = drawIcon(192), "image/png"
	case iconPath + "512.png":
		body, typ = drawIcon(512), "image/png"
	default:
		return false
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return true
	}
	w.Header().Set("Content-Type", typ)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	if r.Method != http.MethodHead {
		w.Write(body)
	}
	return true
}
