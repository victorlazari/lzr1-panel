package main

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"time"
)

// statusPath is what the phone's Remote tab (client.js) reads: who is signed in, until when the remote runs and its
// idle limit. Nothing it returns lets a browser change the remote.
const statusPath = "lzr1-status"

type status struct {
	You         string   `json:"you"`
	SignedIn    []string `json:"signedIn"`    // every login with a live session, once each
	Expires     int64    `json:"expires"`     // Unix seconds when --ttl ends the remote; 0 before it is known
	IdleSeconds int64    `json:"idleSeconds"` // a browser with no input for this long is logged out
}

// serveStatus answers for the session tok; ServeHTTP has checked the identity, Origin and session first.
func (s *server) serveStatus(w http.ResponseWriter, r *http.Request, tok string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	st := status{IdleSeconds: int64(s.idle / time.Second)}
	seen := map[string]bool{}
	s.mu.Lock()
	if sess := s.sessions[tok]; sess != nil {
		st.You = sess.login
	}
	for _, sess := range s.sessions {
		if !seen[sess.login] {
			seen[sess.login] = true
			st.SignedIn = append(st.SignedIn, sess.login)
		}
	}
	if !s.deadline.IsZero() {
		st.Expires = s.deadline.Unix()
	}
	s.mu.Unlock()
	sort.Strings(st.SignedIn)
	body, _ := json.Marshal(st)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	if r.Method == http.MethodHead {
		return
	}
	w.Write(body)
}
