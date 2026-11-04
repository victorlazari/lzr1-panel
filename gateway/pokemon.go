package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// The Pokémon strip (client.js) reads what the panel already knows from its state directory (--state): the sessions
// the last refresh listed (web-sessions.json, written by list_rows), and the sprites cut for them (pokemon-art/NNNN/).
// A click on a Pokémon asks the panel it belongs to to show that session, through the same .follow file the panel's
// own ticker already watches. Nothing here starts, ends or types into a session.
const (
	sessionsPath  = "lzr1-sessions"
	pickPath      = "lzr1-pick"
	pokemonPrefix = "lzr1-pokemon/"

	sessionsFile = "web-sessions.json"
	artDir       = "pokemon-art"

	maxSessionsFile = 256 << 10
	maxPickBody     = 4 << 10
	maxArtFile      = 2 << 20
	maxKey          = 256
	maxSessions     = 200
	dexMax          = 151
)

// artFiles is every file a browser may ask of a Pokémon's art directory.
var artFiles = map[string]string{
	"walkR.gif": "image/gif",
	"walkL.gif": "image/gif",
	"idleR.gif": "image/gif",
	"idleL.gif": "image/gif",
	"sleep.gif": "image/gif",
	"meta.json": "application/json",
}

var (
	reDex = regexp.MustCompile(`^[0-9]{4}$`)
	rePID = regexp.MustCompile(`^[0-9]{1,10}$`)
)

type webSession struct {
	Key   string `json:"key"`   // PROFILE/NAME, as the panel's rows are keyed
	State string `json:"state"` // work, wait or idle
	Idle  int64  `json:"idle"`  // seconds since its last change
	Done  bool   `json:"done"`  // finished and not looked at yet
	Dex   int    `json:"dex"`   // its Pokémon, 1 to 151
}

type webSessions struct {
	At       int64        `json:"at"`
	Sessions []webSession `json:"sessions"`
}

// readSessions returns the panel's last snapshot, cleaned: anything that does not look like what the panel writes is
// dropped, so a browser never gets text the panel did not produce. No state directory or no file: no sessions.
func (s *server) readSessions() webSessions {
	out := webSessions{Sessions: []webSession{}}
	if s.state == "" {
		return out
	}
	f, err := os.Open(filepath.Join(s.state, sessionsFile))
	if err != nil {
		return out
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxSessionsFile))
	if err != nil {
		return out
	}
	var in webSessions
	if json.Unmarshal(b, &in) != nil {
		return out
	}
	out.At = in.At
	for _, w := range in.Sessions {
		if len(out.Sessions) >= maxSessions {
			break
		}
		if w.Dex < 1 || w.Dex > dexMax || !validKey(w.Key) {
			continue
		}
		switch w.State {
		case "work", "wait", "idle":
		default:
			continue
		}
		if w.Idle < 0 {
			w.Idle = 0
		}
		out.Sessions = append(out.Sessions, w)
	}
	return out
}

// validKey: PROFILE/NAME with something on both sides, no control characters, a bounded length.
func validKey(k string) bool {
	i := strings.IndexByte(k, '/')
	if i < 1 || i == len(k)-1 || len(k) > maxKey {
		return false
	}
	for _, r := range k {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func methodGetOnly(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	return true
}

// serveSessions answers the strip's poll; ServeHTTP has checked the identity, Origin and session first.
func (s *server) serveSessions(w http.ResponseWriter, r *http.Request) {
	if !methodGetOnly(w, r) {
		return
	}
	body, _ := json.Marshal(s.readSessions())
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	if r.Method == http.MethodHead {
		return
	}
	w.Write(body)
}

// servePick takes {"id": the panel's id, "key": a session's key} and leaves the key in the panel's .follow file, which
// its ticker turns into a move of the highlight (and so of the viewer). The panel must belong to this remote, and the
// key must be a session the last refresh listed.
func (s *server) servePick(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.state == "" {
		http.NotFound(w, r)
		return
	}
	var in struct {
		ID  string `json:"id"`
		Key string `json:"key"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxPickBody)).Decode(&in); err != nil || !rePID.MatchString(in.ID) {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	known := false
	for _, ss := range s.readSessions().Sessions {
		if ss.Key == in.Key {
			known = true
			break
		}
	}
	if !known {
		http.Error(w, "unknown session", http.StatusNotFound)
		return
	}
	uuid := strings.Trim(s.base, "/")
	mine, err := os.ReadFile(filepath.Join(s.state, in.ID+".remote"))
	if err != nil || strings.TrimSpace(string(mine)) != uuid {
		http.Error(w, "unknown panel", http.StatusNotFound)
		return
	}
	tmp, err := os.CreateTemp(s.state, "."+in.ID+".follow-")
	if err != nil {
		s.log.Printf("pick: %v", err)
		http.Error(w, "cannot save", http.StatusInternalServerError)
		return
	}
	_, werr := tmp.WriteString(in.Key)
	cerr := tmp.Close()
	if werr != nil || cerr != nil || os.Chmod(tmp.Name(), 0o600) != nil ||
		os.Rename(tmp.Name(), filepath.Join(s.state, in.ID+".follow")) != nil {
		os.Remove(tmp.Name())
		http.Error(w, "cannot save", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// servePokemon serves one file of one Pokémon's art: lzr1-pokemon/NNNN/NAME, NAME from a short list, a regular file
// under the state directory's pokemon-art (never a link out of it).
func (s *server) servePokemon(w http.ResponseWriter, r *http.Request, rest string) {
	if !methodGetOnly(w, r) {
		return
	}
	dex, name, ok := strings.Cut(rest, "/")
	ctype, listed := artFiles[name]
	if s.state == "" || !ok || !reDex.MatchString(dex) || !listed {
		http.NotFound(w, r)
		return
	}
	if n, _ := strconv.Atoi(dex); n < 1 || n > dexMax {
		http.NotFound(w, r)
		return
	}
	p := filepath.Join(s.state, artDir, dex, name)
	fi, err := os.Lstat(p)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > maxArtFile {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(p)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", "private, max-age=3600") // the art never changes once cut
	w.Header().Set("Content-Length", strconv.FormatInt(fi.Size(), 10))
	if r.Method == http.MethodHead {
		return
	}
	io.Copy(w, f)
}
