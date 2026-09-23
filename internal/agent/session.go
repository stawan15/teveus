package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Session is a saved API-engine conversation, stored as one JSON file so it
// can be resumed later with /resume.
type Session struct {
	ID      string    `json:"id"`
	Cwd     string    `json:"cwd"`
	Model   string    `json:"model"`
	Title   string    `json:"title"`
	Updated time.Time `json:"updated"`
	History []Message `json:"history"`
}

func sessionPath(dir, id string) string { return filepath.Join(dir, id+".json") }

func SaveSession(dir string, s Session) error {
	if dir == "" || len(s.History) == 0 {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tmp := sessionPath(dir, s.ID) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, sessionPath(dir, s.ID))
}

func LoadSession(dir, id string) (Session, error) {
	var s Session
	b, err := os.ReadFile(sessionPath(dir, filepath.Base(id)))
	if err != nil {
		return s, err
	}
	err = json.Unmarshal(b, &s)
	return s, err
}

// ListSessions returns the sessions started in cwd, newest first, without
// their histories.
func ListSessions(dir, cwd string) []Session {
	entries, _ := os.ReadDir(dir)
	var out []Session
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		s, err := LoadSession(dir, strings.TrimSuffix(e.Name(), ".json"))
		if err != nil || s.Cwd != cwd {
			continue
		}
		s.History = nil
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out
}

func sessionTitle(history []Message) string {
	for _, m := range history {
		if m.Role == "user" && strings.TrimSpace(m.Text) != "" {
			t := strings.Join(strings.Fields(m.Text), " ")
			if r := []rune(t); len(r) > 80 {
				t = string(r[:80]) + "…"
			}
			return t
		}
	}
	return "untitled"
}

// checkpoint remembers file contents before a turn's edits so /undo can put
// them back. A nil entry means the file didn't exist.
type checkpoint struct {
	historyLen int
	files      map[string][]byte
	order      []string
}

func (c *checkpoint) remember(path string) {
	if _, seen := c.files[path]; seen {
		return
	}
	b, err := os.ReadFile(path)
	if err != nil {
		b = nil
	}
	c.files[path] = b
	c.order = append(c.order, path)
}

func (c *checkpoint) restore() ([]string, error) {
	var restored []string
	for _, p := range c.order {
		orig := c.files[p]
		var err error
		if orig == nil {
			err = os.Remove(p)
			if os.IsNotExist(err) {
				err = nil
			}
		} else {
			err = os.WriteFile(p, orig, 0o644)
		}
		if err != nil {
			return restored, err
		}
		restored = append(restored, p)
	}
	return restored, nil
}

// PruneSessions deletes saved conversations last used more than maxAge ago
// and returns how many it removed.
func PruneSessions(dir string, maxAge time.Duration) int {
	entries, _ := os.ReadDir(dir)
	cutoff := time.Now().Add(-maxAge)
	n := 0
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		if os.Remove(filepath.Join(dir, e.Name())) == nil {
			n++
		}
	}
	return n
}
