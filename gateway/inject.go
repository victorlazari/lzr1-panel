package main

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
)

// clientJS is added to ttyd's own page (copy to the browser, no browser menu over the terminal, back to the login
// form when the session is gone): adding a tag to the index ttyd serves keeps ttyd's client and server in step, where
// a copy of the index kept here (ttyd -I) would break on a ttyd upgrade.
//
//go:embed client.js
var clientJS []byte

const (
	clientPath = "lzr1-client.js"
	clientTag  = `<script src="` + clientPath + `"></script>`
	maxPage    = 4 << 20 // a bigger page is passed through unchanged
)

// serveClient serves client.js; ServeHTTP has checked the identity, Origin and session first.
func serveClient(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(clientJS)))
	if r.Method == http.MethodHead {
		return
	}
	w.Write(clientJS)
}

// isPage reports whether the proxied request is ttyd's index, the one response the gateway changes.
func (s *server) isPage(r *http.Request) bool {
	return r.Method == http.MethodGet && r.URL.Path == s.base
}

// injectClient adds clientTag to ttyd's index right after its first <head> (or at the very start without one),
// decoding a gzipped answer first. Anything but a 200 text/html answer is left alone, and a page over maxPage is
// passed through unchanged.
func (s *server) injectClient(resp *http.Response) error {
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	if mt, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type")); mt != "text/html" {
		return nil
	}
	body := io.Reader(resp.Body)
	switch enc := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Encoding"))); enc {
	case "", "identity":
	case "gzip":
		zr, err := gzip.NewReader(resp.Body)
		if err != nil {
			return fmt.Errorf("ttyd's page: %v", err)
		}
		body = zr
		resp.Header.Del("Content-Encoding")
		resp.Header.Del("Content-Length")
		resp.ContentLength = -1
	default:
		s.log.Printf("ttyd's page: Content-Encoding %q, sent without lzr1-client.js", enc)
		return nil
	}
	page, err := io.ReadAll(io.LimitReader(body, maxPage+1))
	if err != nil {
		return fmt.Errorf("ttyd's page: %v", err)
	}
	if len(page) > maxPage {
		s.log.Printf("ttyd's page is over %d bytes: sent without lzr1-client.js", maxPage)
		resp.Body = struct {
			io.Reader
			io.Closer
		}{io.MultiReader(bytes.NewReader(page), body), resp.Body}
		return nil
	}
	resp.Body.Close()
	page = insertTag(page)
	resp.Body = io.NopCloser(bytes.NewReader(page))
	resp.ContentLength = int64(len(page))
	resp.Header.Set("Content-Length", strconv.Itoa(len(page)))
	resp.Header.Del("ETag")
	return nil
}

// insertTag puts clientTag right after the first <head> start tag, or at the very start when there is none.
func insertTag(page []byte) []byte {
	at := 0
	lower := bytes.ToLower(page)
	for i := 0; ; {
		j := bytes.Index(lower[i:], []byte("<head"))
		if j < 0 {
			break
		}
		j += i
		k := j + len("<head")
		if k < len(lower) && (lower[k] == '>' || lower[k] == ' ' || lower[k] == '\t' || lower[k] == '\n' ||
			lower[k] == '\r' || lower[k] == '/') {
			if end := bytes.IndexByte(lower[k:], '>'); end >= 0 {
				at = k + end + 1
			}
			break
		}
		i = k // <header> and the like
	}
	out := make([]byte, 0, len(page)+len(clientTag))
	out = append(out, page[:at]...)
	out = append(out, clientTag...)
	return append(out, page[at:]...)
}
