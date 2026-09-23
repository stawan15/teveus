package ui

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"

	"github.com/stawan15/teveus/internal/claude"
)

// Images reach the input the way Claude Code takes them: a dropped or pasted
// file path, or ctrl+v with a picture on the clipboard. Each shows as an
// "[Image #N]" placeholder and goes with the message when it is sent.

// maxImageEdge is the longest side the API uses as is; bigger pictures are
// scaled down here, which also keeps them under the 5 MB request limit.
const (
	maxImageEdge  = 1568
	maxImageBytes = 5 << 20
)

var (
	imageRef = regexp.MustCompile(`\[Image #(\d+)\]`)
	imageExt = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true}
)

// attachImage stores a picture and returns its placeholder.
func (m *Model) attachImage(img claude.Image) string {
	if m.images == nil {
		m.images = map[int]claude.Image{}
	}
	m.imageN++
	m.images[m.imageN] = img
	return fmt.Sprintf("[Image #%d]", m.imageN)
}

// imagesIn returns the pictures a message refers to, in order.
func (m *Model) imagesIn(s string) []claude.Image {
	var out []claude.Image
	seen := map[int]bool{}
	for _, ref := range imageRef.FindAllStringSubmatch(s, -1) {
		n, _ := strconv.Atoi(ref[1])
		if img, ok := m.images[n]; ok && !seen[n] {
			seen[n] = true
			out = append(out, img)
		}
	}
	return out
}

// pastedImages turns a paste made only of image file paths (what dropping
// files on the terminal types) into placeholders.
func (m *Model) pastedImages(text string) (string, bool, error) {
	paths := splitPaths(strings.TrimSpace(text))
	if len(paths) == 0 {
		return "", false, nil
	}
	for _, p := range paths {
		if !imageExt[strings.ToLower(filepath.Ext(p))] {
			return "", false, nil
		}
		if st, err := os.Stat(p); err != nil || st.IsDir() {
			return "", false, nil
		}
	}
	var refs []string
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			return "", true, err
		}
		img, err := prepareImage(data)
		if err != nil {
			return "", true, fmt.Errorf("%s: %w", filepath.Base(p), err)
		}
		refs = append(refs, m.attachImage(img))
	}
	return strings.Join(refs, " "), true, nil
}

// splitPaths splits text as a shell would: backslash escapes, quotes, and
// file:// URLs, which is how terminals type dropped files.
func splitPaths(s string) []string {
	var out []string
	var cur strings.Builder
	have := false
	var quote rune
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\\' && i+1 < len(rs):
			i++
			cur.WriteRune(rs[i])
			have = true
		case r == '\'' || r == '"':
			quote, have = r, true
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			if have {
				out = append(out, cur.String())
				cur.Reset()
				have = false
			}
		default:
			cur.WriteRune(r)
			have = true
		}
	}
	if have {
		out = append(out, cur.String())
	}
	for i, p := range out {
		if u, err := url.Parse(p); err == nil && u.Scheme == "file" {
			out[i] = u.Path
		} else if strings.HasPrefix(p, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				out[i] = filepath.Join(home, p[2:])
			}
		}
	}
	return out
}

// prepareImage checks the picture's type and scales it down when it is
// larger than the API uses.
func prepareImage(data []byte) (claude.Image, error) {
	mt := http.DetectContentType(data)
	switch mt {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
	default:
		return claude.Image{}, errors.New("not a PNG, JPEG, GIF or WebP image")
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return claude.Image{}, err
	}
	if max(cfg.Width, cfg.Height) <= maxImageEdge && len(data) <= maxImageBytes && mt != "image/webp" {
		return claude.Image{MediaType: mt, Data: data}, nil
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return claude.Image{}, err
	}
	w, h := cfg.Width, cfg.Height
	if long := max(w, h); long > maxImageEdge {
		w, h = max(w*maxImageEdge/long, 1), max(h*maxImageEdge/long, 1)
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Over, nil)
	var buf bytes.Buffer
	if mt == "image/jpeg" {
		err = jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 90})
	} else {
		mt = "image/png"
		err = png.Encode(&buf, dst)
	}
	if err == nil && buf.Len() > maxImageBytes {
		buf.Reset()
		mt, err = "image/jpeg", jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 85})
	}
	if err != nil {
		return claude.Image{}, err
	}
	if buf.Len() > maxImageBytes {
		return claude.Image{}, errors.New("image is over 5 MB even when scaled down")
	}
	return claude.Image{MediaType: mt, Data: buf.Bytes()}, nil
}

type clipImageMsg struct {
	img claude.Image
	err error
}

// clipboardImage reads a picture from the system clipboard.
func clipboardImage() tea.Msg {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var data []byte
	var err error
	switch runtime.GOOS {
	case "darwin":
		// osascript prints the PNG as «data PNGf89504E47…».
		var out []byte
		out, err = exec.CommandContext(ctx, "osascript", "-e", "the clipboard as «class PNGf»").Output()
		s := strings.TrimSpace(string(out))
		if err == nil && strings.HasPrefix(s, "«data PNGf") {
			data, err = hex.DecodeString(strings.TrimSuffix(strings.TrimPrefix(s, "«data PNGf"), "»"))
		} else {
			err = errors.New("no image on the clipboard")
		}
	case "linux":
		data, err = exec.CommandContext(ctx, "wl-paste", "--type", "image/png").Output()
		if err != nil || len(data) == 0 {
			data, err = exec.CommandContext(ctx, "xclip", "-selection", "clipboard", "-t", "image/png", "-o").Output()
		}
		if err != nil || len(data) == 0 {
			err = errors.New("no image on the clipboard")
		}
	default:
		err = errors.New("pasting images from the clipboard isn't supported here; drop the file instead")
	}
	if err != nil {
		return clipImageMsg{err: err}
	}
	img, err := prepareImage(data)
	return clipImageMsg{img: img, err: err}
}
