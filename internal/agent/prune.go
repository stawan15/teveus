package agent

import (
	"fmt"
	"os"
	"time"
)

// Every request resends the whole conversation, so a big file read or test
// log costs its tokens again on every later step. Old tool results are
// replaced, in what the model is sent, by a one-line note; the model can run
// the tool again if it needs the output. Stored history keeps the originals
// (for /resume and the transcript).
//
// Changing an earlier message breaks the provider's prompt cache from that
// point on, so results are dropped in batches, and only once enough has
// piled up to be worth it: between batches the sent prefix doesn't change.

const (
	keepResults   = 8     // the newest tool results are always sent whole
	pruneMinChars = 1500  // shorter results aren't worth a note
	pruneBatch    = 40000 // characters that must pile up before a batch is dropped
)

// pruneResults marks old tool results as dropped, once at least pruneBatch
// characters of them exist (or any, when force is set), and returns how many
// characters it dropped.
func (e *Engine) pruneResults(force bool) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	var idx []int
	total, seen := 0, 0
	for i := len(e.history) - 1; i >= 0; i-- {
		m := e.history[i]
		if m.Role != "tool" {
			continue
		}
		if seen++; seen > keepResults && !m.Pruned && len(m.Result) >= pruneMinChars {
			idx = append(idx, i)
			total += len(m.Result)
		}
	}
	if total == 0 || (!force && total < pruneBatch) {
		return 0
	}
	for _, i := range idx {
		e.history[i].Pruned = true
	}
	e.seen = map[string]readSig{} // those Reads' output is no longer in the conversation
	return total
}

// withoutPruned is the conversation as it is sent: dropped results become notes.
func withoutPruned(msgs []Message) []Message {
	out := make([]Message, len(msgs))
	for i, m := range msgs {
		if m.Pruned {
			m.Result = fmt.Sprintf("[Earlier tool output (%d characters) left out to save tokens. Run the tool again if you need it.]", len(m.Result))
		}
		out[i] = m
	}
	return out
}

// A Read of a file that hasn't changed since the model read it, same range,
// is answered with a note instead of the file again.
type readSig struct {
	mod           time.Time
	size          int64
	offset, limit int
}

func (e *Engine) readSignature(in map[string]any) (string, readSig, bool) {
	p := e.box.abs(str(in, "file_path"))
	info, err := os.Stat(p)
	if err != nil || info.IsDir() {
		return "", readSig{}, false
	}
	return p, readSig{info.ModTime(), info.Size(), num(in, "offset", 1), num(in, "limit", 2000)}, true
}

func (e *Engine) unchangedRead(path string, sig readSig) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.seen[path] == sig
}

func (e *Engine) noteRead(path string, sig readSig) {
	e.mu.Lock()
	e.seen[path] = sig
	e.mu.Unlock()
}

const unchangedNote = "Unchanged since you read it earlier in this conversation (same range): that output is above."
