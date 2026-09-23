package ui

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/stawan15/teveus/internal/claude"
)

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestSplitPathsLikeADroppedFile(t *testing.T) {
	for in, want := range map[string][]string{
		`/Users/me/Desktop/ภาพถ่ายหน้าจอ\ 2569-09-23\ เวลา\ 08.33.45.png`: {"/Users/me/Desktop/ภาพถ่ายหน้าจอ 2569-09-23 เวลา 08.33.45.png"},
		`'/tmp/a b.png' /tmp/c.jpg`: {"/tmp/a b.png", "/tmp/c.jpg"},
		`file:///tmp/a%20b.png`:     {"/tmp/a b.png"},
	} {
		if got := splitPaths(in); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %q", in, got)
		}
	}
}

type imageBackend struct {
	claude.Backend
	text   string
	images []claude.Image
}

func (b *imageBackend) SendImages(text string, images []claude.Image) error {
	b.text, b.images = text, images
	return nil
}

func TestDroppedImageBecomesAPlaceholder(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	// A name with SARA AM: the path must be read before termSafe rewrites it.
	path := filepath.Join(t.TempDir(), "ภาพ หน้าจอทำ.png")
	if err := os.WriteFile(path, pngBytes(t, 20, 10), 0o644); err != nil {
		t.Fatal(err)
	}
	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	b := &imageBackend{}
	m.client = b
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("look "), Paste: false})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(`"` + path + `"`), Paste: true})
	if v := m.input.Value(); v != "look [Image #1]" {
		t.Fatalf("input = %q", v)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if b.text != "look [Image #1]" || len(b.images) != 1 || b.images[0].MediaType != "image/png" {
		t.Fatalf("sent %q with %d images", b.text, len(b.images))
	}
}

func TestPasteOfOtherPathsStaysText(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/tmp/missing.png"), Paste: true})
	if v := m.input.Value(); v != "/tmp/missing.png" {
		t.Fatalf("input = %q", v)
	}
}

func TestBigImagesAreScaledDown(t *testing.T) {
	img, err := prepareImage(pngBytes(t, 3000, 1500))
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(img.Data))
	if err != nil || cfg.Width != maxImageEdge || cfg.Height != maxImageEdge/2 {
		t.Fatalf("scaled to %dx%d (%v)", cfg.Width, cfg.Height, err)
	}
	if _, err := prepareImage([]byte("not an image")); err == nil {
		t.Fatal("accepted text as an image")
	}
}
