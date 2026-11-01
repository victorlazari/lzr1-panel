package main

import (
	"bytes"
	"encoding/json"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServeApp(t *testing.T) {
	rec := httptest.NewRecorder()
	if !serveApp(rec, httptest.NewRequest("GET", "/x/"+manifestPath, nil), manifestPath) || rec.Code != 200 {
		t.Fatalf("manifest: %d", rec.Code)
	}
	var m struct {
		Display string `json:"display"`
		Start   string `json:"start_url"`
		Icons   []struct{ Src, Sizes string }
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil || m.Display != "standalone" || m.Start != "./" {
		t.Fatalf("manifest %v %+v", err, m)
	}
	for _, sz := range []string{"192", "512"} {
		rec := httptest.NewRecorder()
		if !serveApp(rec, httptest.NewRequest("GET", "/", nil), iconPath+sz+".png") {
			t.Fatalf("icon %s not served", sz)
		}
		img, err := png.Decode(bytes.NewReader(rec.Body.Bytes()))
		if err != nil || img.Bounds().Dx() != map[string]int{"192": 192, "512": 512}[sz] {
			t.Fatalf("icon %s: %v", sz, err)
		}
	}
	rec = httptest.NewRecorder()
	if serveApp(rec, httptest.NewRequest("GET", "/", nil), "other") {
		t.Fatal("served an unknown name")
	}
	rec = httptest.NewRecorder()
	serveApp(rec, httptest.NewRequest(http.MethodPost, "/", nil), manifestPath)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST: %d", rec.Code)
	}
	if !strings.Contains(clientTag, `rel="manifest"`) {
		t.Fatal("page does not link the manifest")
	}
}
