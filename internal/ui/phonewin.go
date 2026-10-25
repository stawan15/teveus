package ui

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Several teveus windows can be open at once, each in its own terminal tab.
// A page only knows the process that serves it, so every window that serves a
// phone page writes a small handle to the config dir; a page then lists the
// other windows too, and tapping one opens that window's page.

// phoneHandle is one window's advertisement: where its page is, the token for
// it, and its pid so a crashed window's handle can be cleaned up.
type phoneHandle struct {
	PID    int    `json:"pid"`
	Addr   string `json:"addr"`   // host:port as a phone reaches it
	Token  string `json:"token"`  // that window's page token
	Title  string `json:"title"`  // the session it shows
	Folder string `json:"folder"` // its project, for telling windows apart
	Model  string `json:"model"`
	At     int64  `json:"at"` // when the handle was last written
}

func handleDir() string { return filepath.Join(configDir(), "phone") }

// writeHandle records this window, so other windows' pages can list it.
func (p *phoneServer) writeHandle(m *Model) {
	h := phoneHandle{PID: os.Getpid(), Addr: p.publicAddr, Token: p.token,
		Title: sessionTitle(m.blocks), Folder: shortPath(m.cwd), Model: shortModel(m.model),
		At: time.Now().Unix()}
	b, _ := json.Marshal(h)
	dir := handleDir()
	os.MkdirAll(dir, 0o700)
	os.Chmod(dir, 0o700)
	p.handle = filepath.Join(dir, strconv.Itoa(h.PID)+".json")
	os.WriteFile(p.handle, b, 0o600)
	os.Chmod(p.handle, 0o600)
	p.handleTitle = h.Title
}

func (p *phoneServer) removeHandle() {
	if p.handle != "" {
		os.Remove(p.handle)
	}
}

// refreshHandle keeps this window's title current, so another window's page
// names it after the conversation it is having.
func (p *phoneServer) refreshHandle(m *Model) {
	if t := sessionTitle(m.blocks); t != p.handleTitle {
		p.writeHandle(m)
	}
}

// readHandles lists the live windows, dropping handles whose process is gone
// and the ones too old to be believed.
func readHandles() []phoneHandle {
	dir := handleDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []phoneHandle
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var h phoneHandle
		if json.Unmarshal(b, &h) != nil || h.Addr == "" || h.Token == "" || h.PID == 0 ||
			!processAlive(h.PID) || time.Since(time.Unix(h.At, 0)) > 24*time.Hour {
			os.Remove(path)
			continue
		}
		out = append(out, h)
	}
	return out
}

// pollWindows asks the other windows what they are doing, and keeps the
// answer for the next snapshot: a page must not wait on other processes.
func (p *phoneServer) pollWindows() {
	var out []phoneTab
	for _, h := range readHandles() {
		if h.PID == os.Getpid() {
			continue
		}
		var v remoteView
		if !remoteState(h, 1500*time.Millisecond, &v) {
			continue // it isn't answering: a stale handle is no use
		}
		tab := phoneTab{Title: windowTitle(h, v), State: "idle", Win: h.Addr, Tok: h.Token,
			URL: "http://" + h.Addr + "/p/" + h.Token}
		switch {
		case v.Perm != nil:
			tab.State = "approval"
		case v.Busy:
			tab.State = "working"
		}
		if v.Where != "" {
			tab.Where = v.Where
		}
		out = append(out, tab)
	}
	p.mu.Lock()
	p.others = out
	p.mu.Unlock()
}

// remoteState asks another window for its state.
func remoteState(h phoneHandle, timeout time.Duration, out *remoteView) bool {
	c := &http.Client{Timeout: timeout}
	resp, err := c.Get("http://" + h.Addr + "/state?t=" + h.Token)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	return json.NewDecoder(resp.Body).Decode(out) == nil
}

// remoteView is the part of another window's state a page needs to draw a row
// for it.
type remoteView struct {
	Busy  bool       `json:"busy"`
	Tab   string     `json:"tab"`
	Perm  *phonePerm `json:"perm"`
	Where string     `json:"where"`
}

// windowTitle names another window: its session, then its folder, so two
// windows in different projects read apart.
func windowTitle(h phoneHandle, v remoteView) string {
	t := v.Tab
	if t == "" {
		t = h.Title
	}
	if t == "" || t == "New session" {
		t = "new session"
	}
	folder := h.Folder
	if v.Where != "" {
		folder = v.Where
	}
	if folder != "" && folder != "~" {
		t += " · " + filepath.Base(folder)
	}
	return t
}
