package demo

import "testing"

func TestTotal(t *testing.T) {
	if got := Total([]float64{10, 20}, 0.1); got != 27 {
		t.Fatalf("Total = %v, want 27", got)
	}
}
