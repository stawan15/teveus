package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stawan15/teveus/internal/claude"
)

func TestUserImagesReachBothProtocols(t *testing.T) {
	msgs := []Message{{Role: "user", Text: "what is this", Images: []claude.Image{{MediaType: "image/png", Data: []byte("PNG")}}}}
	oa, _ := json.Marshal(openaiMessages(msgs))
	if !strings.Contains(string(oa), `"image_url":{"url":"data:image/png;base64,UE5H"}`) {
		t.Fatalf("openai: %s", oa)
	}
	an, _ := json.Marshal(anthropicMessages(msgs))
	if !strings.Contains(string(an), `"source":{"data":"UE5H","media_type":"image/png","type":"base64"}`) {
		t.Fatalf("anthropic: %s", an)
	}
}
