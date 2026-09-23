// Command screencast renders frames captured by internal/ui's TestCapture
// into a GIF and PNG screenshots for the README.
//
//	go run . -in frames.json -out ../
//
// It draws the terminal cell by cell: text through real fonts (with symbol
// fallbacks), and box-drawing, block and braille characters as exact shapes,
// the way terminals do, so borders and the logo line up perfectly.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

type frame struct {
	T    int64  `json:"t"`
	S    string `json:"s"`
	Mark string `json:"mark"`
}

type cell struct {
	r      rune
	fg, bg color.RGBA
	bold   bool
	hasBg  bool
}

var (
	defFG  = rgb(0xEC, 0xE6, 0xDF)
	defBG  = rgb(0x1E, 0x1C, 0x22)
	pageBG = rgb(0x14, 0x13, 0x17)
)

func rgb(r, g, b uint8) color.RGBA { return color.RGBA{r, g, b, 255} }

// ---------------------------------------------------------------- ANSI ----

func xterm256(n int) color.RGBA {
	base := []color.RGBA{
		rgb(0, 0, 0), rgb(205, 49, 49), rgb(13, 188, 121), rgb(229, 229, 16), rgb(36, 114, 200), rgb(188, 63, 188), rgb(17, 168, 205), rgb(229, 229, 229),
		rgb(102, 102, 102), rgb(241, 76, 76), rgb(35, 209, 139), rgb(245, 245, 67), rgb(59, 142, 234), rgb(214, 112, 214), rgb(41, 184, 219), rgb(255, 255, 255),
	}
	switch {
	case n < 16:
		return base[n]
	case n < 232:
		n -= 16
		lv := func(v int) uint8 {
			if v == 0 {
				return 0
			}
			return uint8(55 + v*40)
		}
		return rgb(lv(n/36), lv(n/6%6), lv(n%6))
	}
	v := uint8(8 + (n-232)*10)
	return rgb(v, v, v)
}

func parse(s string, cols, rows int) [][]cell {
	grid := make([][]cell, rows)
	for i := range grid {
		grid[i] = make([]cell, cols)
		for j := range grid[i] {
			grid[i][j] = cell{r: ' ', fg: defFG, bg: defBG}
		}
	}
	fg, bg, bold, hasBg, reverse := defFG, defBG, false, false, false
	row, col := 0, 0
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case r == 0x1b && i+1 < len(rs) && rs[i+1] == '[':
			j := i + 2
			for j < len(rs) && !(rs[j] >= 0x40 && rs[j] <= 0x7e) {
				j++
			}
			if j < len(rs) && rs[j] == 'm' {
				params := strings.Split(string(rs[i+2:j]), ";")
				for k := 0; k < len(params); k++ {
					p, _ := strconv.Atoi(params[k])
					switch {
					case params[k] == "" || p == 0:
						fg, bg, bold, hasBg, reverse = defFG, defBG, false, false, false
					case p == 1:
						bold = true
					case p == 22:
						bold = false
					case p == 7:
						reverse = true
					case p == 27:
						reverse = false
					case p >= 30 && p <= 37:
						fg = xterm256(p - 30)
					case p >= 90 && p <= 97:
						fg = xterm256(p - 90 + 8)
					case p == 39:
						fg = defFG
					case p >= 40 && p <= 47:
						bg, hasBg = xterm256(p-40), true
					case p >= 100 && p <= 107:
						bg, hasBg = xterm256(p-100+8), true
					case p == 49:
						bg, hasBg = defBG, false
					case (p == 38 || p == 48) && k+1 < len(params):
						var c color.RGBA
						if params[k+1] == "5" && k+2 < len(params) {
							n, _ := strconv.Atoi(params[k+2])
							c, k = xterm256(n), k+2
						} else if params[k+1] == "2" && k+4 < len(params) {
							r, _ := strconv.Atoi(params[k+2])
							g, _ := strconv.Atoi(params[k+3])
							b, _ := strconv.Atoi(params[k+4])
							c, k = rgb(uint8(r), uint8(g), uint8(b)), k+4
						}
						if p == 38 {
							fg = c
						} else {
							bg, hasBg = c, true
						}
					}
				}
			}
			i = j
		case r == 0x1b:
			i++ // other escapes: skip one byte
		case r == '\n':
			row, col = row+1, 0
		case r == '\r':
			col = 0
		default:
			if row < rows && col < cols {
				c := cell{r: r, fg: fg, bg: bg, bold: bold, hasBg: hasBg}
				if reverse {
					c.fg, c.bg, c.hasBg = bg, fg, true
				}
				grid[row][col] = c
			}
			col++
		}
	}
	return grid
}

// --------------------------------------------------------------- fonts ----

type face struct {
	f    *sfnt.Font
	face font.Face
}

type fonts struct {
	regular, bold []face // primary first, then fallbacks
	cw, ch, asc   int
	buf           sfnt.Buffer
}

func loadFace(path string, size float64) (face, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return face{}, err
	}
	var f *sfnt.Font
	if strings.HasSuffix(strings.ToLower(path), ".ttc") {
		c, err := opentype.ParseCollection(b)
		if err != nil {
			return face{}, err
		}
		f, err = c.Font(0)
		if err != nil {
			return face{}, err
		}
	} else if f, err = opentype.Parse(b); err != nil {
		return face{}, err
	}
	fc, err := opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
	return face{f, fc}, err
}

func (fs *fonts) pick(r rune, bold bool) font.Face {
	list := fs.regular
	if bold {
		list = fs.bold
	}
	for _, f := range list {
		if i, err := f.f.GlyphIndex(&fs.buf, r); err == nil && i != 0 {
			return f.face
		}
	}
	return list[0].face
}

// ------------------------------------------------------------ drawing ----

func fill(img *image.RGBA, r image.Rectangle, c color.RGBA) {
	draw.Draw(img, r, &image.Uniform{c}, image.Point{}, draw.Src)
}

// drawSpecial draws box-drawing, block, braille and a few gutter glyphs as
// exact shapes. It returns false for characters left to the fonts.
func drawSpecial(img *image.RGBA, x, y, w, h int, r rune, c color.RGBA, scale int) bool {
	cx, cy := x+w/2, y+h/2
	light, heavy := max(1, scale), max(2, 2*scale)
	hline := func(x0, x1, t int) { fill(img, image.Rect(x0, cy-t/2, x1, cy-t/2+t), c) }
	vline := func(y0, y1, t int) { fill(img, image.Rect(cx-t/2, y0, cx-t/2+t, y1), c) }
	arc := func(dx, dy int) { // rounded corner joining toward (dx,dy)
		rad := float64(min(w, h)) / 2
		ox, oy := float64(cx)+float64(dx)*rad, float64(cy)+float64(dy)*rad
		for a := 0.0; a <= math.Pi/2; a += 0.02 {
			px := ox - float64(dx)*rad*math.Cos(a)
			py := oy - float64(dy)*rad*math.Sin(a)
			fill(img, image.Rect(int(px)-light/2, int(py)-light/2, int(px)-light/2+light, int(py)-light/2+light), c)
		}
		// straight runs to the cell edges
		if dx > 0 {
			hline(int(ox), x+w, light)
		} else {
			hline(x, int(ox)+1, light)
		}
		if dy > 0 {
			vline(int(oy), y+h, light)
		} else {
			vline(y, int(oy)+1, light)
		}
	}
	switch r {
	case '─':
		hline(x, x+w, light)
	case '━':
		hline(x, x+w, heavy)
	case '│':
		vline(y, y+h, light)
	case '┃':
		vline(y, y+h, heavy)
	case '╭':
		arc(1, 1)
	case '╮':
		arc(-1, 1)
	case '╰':
		arc(1, -1)
	case '╯':
		arc(-1, -1)
	case '⎿': // tool output gutter: down then right
		fill(img, image.Rect(x+w/3, y, x+w/3+light, y+h*2/3), c)
		fill(img, image.Rect(x+w/3, y+h*2/3-light, x+w, y+h*2/3), c)
	case '█':
		fill(img, image.Rect(x, y, x+w, y+h), c)
	case '▀':
		fill(img, image.Rect(x, y, x+w, y+h/2), c)
	case '▄':
		fill(img, image.Rect(x, y+h/2, x+w, y+h), c)
	case '▌':
		fill(img, image.Rect(x, y, x+w/2, y+h), c)
	case '▐':
		fill(img, image.Rect(x+w/2, y, x+w, y+h), c)
	case '▏':
		fill(img, image.Rect(x, y+2*scale, x+max(1, w/8), y+h-2*scale), c)
	case '▍':
		fill(img, image.Rect(x, y+2*scale, x+w*3/8, y+h-2*scale), c)
	default:
		if r >= 0x2800 && r <= 0x28FF { // braille: 2x4 dots
			bits := int(r - 0x2800)
			pos := [8][2]int{{0, 0}, {0, 1}, {0, 2}, {1, 0}, {1, 1}, {1, 2}, {0, 3}, {1, 3}}
			d := max(2, w/4)
			for i, p := range pos {
				if bits&(1<<i) != 0 {
					px := x + w/4 + p[0]*w/2 - d/2
					py := y + h/8 + p[1]*h/4 + h/16 - d/2
					fill(img, image.Rect(px, py, px+d, py+d), c)
				}
			}
			return true
		}
		return false
	}
	return true
}

type renderer struct {
	fs               *fonts
	cols, rows       int
	scale            int
	pad, bar, margin int
	title            string
}

func (rd *renderer) size() (int, int) {
	w := rd.cols*rd.fs.cw + 2*rd.pad + 2*rd.margin
	h := rd.rows*rd.fs.ch + 2*rd.pad + rd.bar + 2*rd.margin
	return w, h
}

func (rd *renderer) render(grid [][]cell) *image.RGBA {
	W, H := rd.size()
	img := image.NewRGBA(image.Rect(0, 0, W, H))
	fill(img, img.Bounds(), pageBG)
	// window with rounded corners
	win := image.Rect(rd.margin, rd.margin, W-rd.margin, H-rd.margin)
	rad := 10 * rd.scale
	for y := win.Min.Y; y < win.Max.Y; y++ {
		for x := win.Min.X; x < win.Max.X; x++ {
			dx := max(win.Min.X+rad-x, x-(win.Max.X-rad-1), 0)
			dy := max(win.Min.Y+rad-y, y-(win.Max.Y-rad-1), 0)
			if dx*dx+dy*dy <= rad*rad {
				img.SetRGBA(x, y, defBG)
			}
		}
	}
	// title bar
	for i, c := range []color.RGBA{rgb(255, 95, 86), rgb(255, 189, 46), rgb(39, 201, 63)} {
		cx, cy, r := win.Min.X+rd.pad+i*20*rd.scale+6*rd.scale, win.Min.Y+rd.bar/2+2*rd.scale, 6*rd.scale
		for y := -r; y <= r; y++ {
			for x := -r; x <= r; x++ {
				if x*x+y*y <= r*r {
					img.SetRGBA(cx+x, cy+y, c)
				}
			}
		}
	}
	d := &font.Drawer{Dst: img, Face: rd.fs.regular[0].face}
	tw := d.MeasureString(rd.title).Round()
	d.Src = image.NewUniform(rgb(0x8C, 0x85, 0x7E))
	d.Dot = fixed.P(win.Min.X+(win.Dx()-tw)/2, win.Min.Y+rd.bar/2+rd.fs.asc/2)
	d.DrawString(rd.title)

	ox, oy := win.Min.X+rd.pad, win.Min.Y+rd.bar+rd.pad/2
	cw, ch := rd.fs.cw, rd.fs.ch
	for row, line := range grid {
		for col, c := range line {
			x, y := ox+col*cw, oy+row*ch
			if c.hasBg {
				fill(img, image.Rect(x, y, x+cw, y+ch), c.bg)
			}
			if c.r == ' ' || c.r == 0 {
				continue
			}
			if drawSpecial(img, x, y, cw, ch, c.r, c.fg, rd.scale) {
				continue
			}
			d := &font.Drawer{Dst: img, Src: image.NewUniform(c.fg), Face: rd.fs.pick(c.r, c.bold)}
			adv, _ := d.Face.GlyphAdvance(c.r)
			// centre narrow/wide glyphs in the cell
			gx := x + (cw-adv.Round())/2
			d.Dot = fixed.P(gx, y+(ch+rd.fs.asc)/2-rd.fs.asc/8)
			d.DrawString(string(c.r))
		}
	}
	return img
}

func savePNG(path string, img image.Image) {
	f, err := os.Create(path)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		log.Fatal(err)
	}
}

func main() {
	in := flag.String("in", "frames.json", "captured frames")
	out := flag.String("out", ".", "output directory for demo.gif and screenshots")
	cols := flag.Int("cols", 118, "terminal columns")
	rows := flag.Int("rows", 34, "terminal rows")
	size := flag.Float64("size", 14, "font size")
	scale := flag.Int("scale", 1, "pixel scale (2 for crisp stills)")
	title := flag.String("title", "teveus — ~/demo-app", "window title")
	flag.Parse()

	var frames []frame
	b, err := os.ReadFile(*in)
	if err != nil {
		log.Fatal(err)
	}
	if err := json.Unmarshal(b, &frames); err != nil {
		log.Fatal(err)
	}

	home, _ := os.UserHomeDir()
	fontPath := func(names ...string) []string {
		var out []string
		for _, n := range names {
			for _, dir := range []string{filepath.Join(home, "Library/Fonts"), "/Library/Fonts", "/System/Library/Fonts", "/System/Library/Fonts/Supplemental"} {
				if _, err := os.Stat(filepath.Join(dir, n)); err == nil {
					out = append(out, filepath.Join(dir, n))
					break
				}
			}
		}
		return out
	}
	load := func(paths []string) []face {
		var fl []face
		for _, p := range paths {
			f, err := loadFace(p, *size*float64(*scale))
			if err != nil {
				log.Printf("font %s: %v", p, err)
				continue
			}
			fl = append(fl, f)
		}
		if len(fl) == 0 {
			log.Fatal("no usable fonts")
		}
		return fl
	}
	fallbacks := []string{"Menlo.ttc", "Apple Symbols.ttf", "Arial Unicode.ttf"}
	fs := &fonts{
		regular: load(fontPath(append([]string{"JetBrainsMono-Regular.ttf"}, fallbacks...)...)),
		bold:    load(fontPath(append([]string{"JetBrainsMono-Bold.ttf", "JetBrainsMono-Regular.ttf"}, fallbacks...)...)),
	}
	adv, _ := fs.regular[0].face.GlyphAdvance('M')
	m := fs.regular[0].face.Metrics()
	fs.cw, fs.asc = adv.Round(), m.Ascent.Round()
	fs.ch = int(math.Round(float64((m.Ascent + m.Descent).Round()) * 1.18))

	rd := &renderer{fs: fs, cols: *cols, rows: *rows, scale: *scale, pad: 14 * *scale, bar: 34 * *scale, margin: 18 * *scale, title: *title}

	tmp, err := os.MkdirTemp("", "screencast")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	// Collapse bursts: a frame shown for <70ms is replaced by the next one,
	// which keeps typing visible but drops spinner noise.
	var list strings.Builder
	n := 0
	for i, f := range frames {
		img := rd.render(parse(f.S, *cols, *rows))
		if f.Mark != "" {
			savePNG(filepath.Join(*out, f.Mark+".png"), img)
		}
		dur := 1.5
		if i+1 < len(frames) {
			dur = float64(frames[i+1].T-f.T) / 1000
		}
		if dur < 0.07 && i+1 < len(frames) && frames[i+1].Mark == "" && f.Mark == "" {
			continue
		}
		name := filepath.Join(tmp, fmt.Sprintf("f%05d.png", n))
		savePNG(name, img)
		fmt.Fprintf(&list, "file '%s'\nduration %.3f\n", name, max(dur, 0.04))
		n++
	}
	// the concat demuxer needs the last file repeated to honour its duration
	fmt.Fprintf(&list, "file '%s'\n", filepath.Join(tmp, fmt.Sprintf("f%05d.png", n-1)))
	listPath := filepath.Join(tmp, "list.txt")
	os.WriteFile(listPath, []byte(list.String()), 0o644)

	gif := filepath.Join(*out, "demo.gif")
	cmd := exec.Command("ffmpeg", "-y", "-loglevel", "error", "-f", "concat", "-safe", "0", "-i", listPath,
		"-vf", "split[a][b];[a]palettegen=max_colors=128:stats_mode=diff[p];[b][p]paletteuse=dither=none:diff_mode=rectangle",
		"-loop", "0", gif)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatal("ffmpeg: ", err)
	}
	st, _ := os.Stat(gif)
	fmt.Printf("%d frames → %s (%.1f MB)\n", n, gif, float64(st.Size())/1e6)
}
