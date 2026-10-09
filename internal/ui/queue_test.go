package ui

import "testing"

func TestEditQueued(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true})
	m.queue = []string{"a", "b", "c"}
	m.editQueued(1)
	if m.input.Value() != "b" || len(m.queue) != 2 || m.queue[1] != "c" {
		t.Fatalf("got %q %v", m.input.Value(), m.queue)
	}
	m.editQueued(0) // input has text: the queue stays as it is
	if len(m.queue) != 2 {
		t.Fatal("queue changed while the input had text")
	}
}
