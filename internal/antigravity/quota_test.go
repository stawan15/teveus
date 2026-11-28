package antigravity

import (
	"math"
	"strings"
	"testing"
)

func TestParseAgyUsage(t *testing.T) {
	raw := strings.Join([]string{
		"Gemini Models\tWeekly Limit Remaining\t48%\t2026-12-03T07:10:29Z",
		"Gemini Models\tFive Hour Limit Remaining\t97%\t2026-11-28T19:05:49Z",
		"Claude and GPT models\tWeekly Limit Remaining\t100%\t1970-01-01T00:00:00Z",
		"Claude and GPT models\tFive Hour Limit Remaining\t100%\t1970-01-01T00:00:00Z",
	}, "\n")

	snap, ok := parseAgyUsage(raw)
	if !ok {
		t.Fatal("parseAgyUsage returned false")
	}
	if snap.Gemini.FiveHour == nil || math.Abs(snap.Gemini.FiveHour.Utilization-0.03) > 1e-6 {
		t.Fatalf("Gemini 5h = %+v, want 0.03 used", snap.Gemini.FiveHour)
	}
	if snap.Gemini.SevenDay == nil || math.Abs(snap.Gemini.SevenDay.Utilization-0.52) > 1e-6 {
		t.Fatalf("Gemini 7d = %+v, want 0.52 used", snap.Gemini.SevenDay)
	}
	if snap.Gemini.SevenDay.ResetsAt == 0 {
		t.Fatal("expected non-zero ResetsAt for Gemini weekly limit")
	}
	if snap.ClaudeGPT.FiveHour == nil || snap.ClaudeGPT.FiveHour.Utilization != 0 {
		t.Fatalf("ClaudeGPT 5h = %+v, want 0 used", snap.ClaudeGPT.FiveHour)
	}
	if snap.ClaudeGPT.FiveHour.ResetsAt != 0 {
		t.Fatalf("expected 0 ResetsAt for 1970 epoch, got %d", snap.ClaudeGPT.FiveHour.ResetsAt)
	}
}
