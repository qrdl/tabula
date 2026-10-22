package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// The panel is 800×480 in its native (landscape) orientation, 4 bits per pixel.
const (
	PanelWidth  = 800
	PanelHeight = 480
	PanelBytes  = PanelWidth * PanelHeight / 2
)

// Palette indices used in rendered images.
const (
	colBlack uint8 = iota
	colWhite
	colYellow
	colRed
	colBlue
	colGreen
)

// palette approximates how the panel's colours look, for the PNG preview only;
// the device gets panel codes, not these RGB values.
var palette = color.Palette{
	colBlack:  color.RGBA{0x19, 0x1e, 0x21, 0xff},
	colWhite:  color.RGBA{0xe8, 0xe8, 0xe8, 0xff},
	colYellow: color.RGBA{0xef, 0xde, 0x44, 0xff},
	colRed:    color.RGBA{0xb2, 0x13, 0x18, 0xff},
	colBlue:   color.RGBA{0x21, 0x57, 0xba, 0xff},
	colGreen:  color.RGBA{0x12, 0x5f, 0x20, 0xff},
}

// panelCode maps palette indices to the panel's colour codes
// (from Waveshare's driver: black 0, white 1, yellow 2, red 3, blue 5, green 6).
var panelCode = [...]byte{
	colBlack:  0x0,
	colWhite:  0x1,
	colYellow: 0x2,
	colRed:    0x3,
	colBlue:   0x5,
	colGreen:  0x6,
}

// separatorPx is the thickness of the line between tasks of different priorities.
const separatorPx = 2

var priorityColor = map[Priority]uint8{
	PriorityHigh:   colRed,
	PriorityMedium: colBlue,
	PriorityLow:    colGreen,
}

type Margin struct {
	Top    int `json:"top"`
	Right  int `json:"right"`
	Bottom int `json:"bottom"`
	Left   int `json:"left"`
}

type ImageConfig struct {
	Margin      Margin  `json:"margin"`
	Font        string  `json:"font"`         // path to a .ttf/.otf file; "" = built-in Go Regular
	FontSize    float64 `json:"font_size"`    // px
	LineSpacing float64 `json:"line_spacing"` // multiple of the font's line height
	TaskSpacing int     `json:"task_spacing"` // extra px between tasks
	DateFormat  string  `json:"date_format"`  // Go time layout
}

func defaultImageConfig() ImageConfig {
	return ImageConfig{
		Margin:      Margin{Top: 20, Right: 24, Bottom: 20, Left: 24},
		FontSize:    28,
		LineSpacing: 1.2,
		TaskSpacing: 12,
		DateFormat:  "Jan 2",
	}
}

// ImageTask is what the renderer needs to know about one open task.
type ImageTask struct {
	Text     string
	Priority Priority
	Due      *string // YYYY-MM-DD
	Icon     rune    // Material Icons character; 0 = none
}

// Frame is a rendered task list.
type Frame struct {
	Image    *image.Paletted // as it looks when mounted
	Rotation int             // degrees clockwise: 0, 90, 180 or 270
	Shown    int             // tasks drawn
	Total    int             // tasks given
}

type Renderer struct {
	mu         sync.Mutex // font faces are not safe for concurrent use
	cfg        ImageConfig
	text, icon font.Face
}

// ValidRotation reports whether r is a rotation the renderer supports.
func ValidRotation(r int) bool { return r == 0 || r == 90 || r == 180 || r == 270 }

// mountedSize returns the image size for a rotation: portrait for 90 and 270.
func mountedSize(rotation int) (w, h int) {
	if rotation == 90 || rotation == 270 {
		return PanelHeight, PanelWidth
	}
	return PanelWidth, PanelHeight
}

func NewRenderer(cfg ImageConfig) (*Renderer, error) {
	r := &Renderer{cfg: cfg}
	m := cfg.Margin
	// The narrow side is PanelHeight in either orientation.
	switch {
	case cfg.FontSize <= 0:
		return nil, errors.New("font_size must be positive")
	case cfg.LineSpacing <= 0:
		return nil, errors.New("line_spacing must be positive")
	case m.Top < 0 || m.Right < 0 || m.Bottom < 0 || m.Left < 0 || cfg.TaskSpacing < 0:
		return nil, errors.New("margins and task_spacing must not be negative")
	case PanelHeight-m.Left-m.Right < 4*int(cfg.FontSize) || PanelHeight-m.Top-m.Bottom < int(cfg.FontSize):
		return nil, errors.New("margins leave too little room for the font size")
	}

	ttf := goregular.TTF
	if cfg.Font != "" {
		var err error
		if ttf, err = os.ReadFile(cfg.Font); err != nil {
			return nil, fmt.Errorf("font: %w", err)
		}
	}
	var err error
	if r.text, err = newFace(ttf, cfg.FontSize); err != nil {
		return nil, fmt.Errorf("font %q: %w", cfg.Font, err)
	}
	if r.icon, err = newFace(materialIconsTTF, cfg.FontSize); err != nil {
		return nil, fmt.Errorf("icon font: %w", err)
	}
	return r, nil
}

func newFace(ttf []byte, size float64) (font.Face, error) {
	f, err := opentype.Parse(ttf)
	if err != nil {
		return nil, err
	}
	return opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
}

// taskLayout is one task broken into lines.
type taskLayout struct {
	lines []string
	due   string
}

// Render draws the tasks for a panel mounted with the given rotation (an invalid one counts as 0).
func (r *Renderer) Render(tasks []ImageTask, rotation int) *Frame {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !ValidRotation(rotation) {
		rotation = 0
	}
	width, height := mountedSize(rotation)
	c := newCanvas(width, height)
	f := &Frame{Image: c.img, Rotation: rotation, Total: len(tasks)}

	m := r.text.Metrics()
	ascent, descent := m.Ascent.Ceil(), m.Descent.Ceil()
	lineH := float64(m.Height) / 64 * r.cfg.LineSpacing
	gap := int(math.Round(r.cfg.FontSize * 0.4))
	iconW := int(math.Round(r.cfg.FontSize))
	left, right := r.cfg.Margin.Left, width-r.cfg.Margin.Right
	top, bottom := float64(r.cfg.Margin.Top), float64(height-r.cfg.Margin.Bottom)
	textX := left + iconW + gap
	spacing := float64(r.cfg.TaskSpacing)

	if len(tasks) == 0 {
		msg := "Tabula rasa"
		x := (width - advance(r.text, msg)) / 2
		c.text(r.text, x, (height+ascent-descent)/2, msg, colBlack)
		return f
	}

	layouts := make([]taskLayout, len(tasks))
	for i, t := range tasks {
		textW := right - textX
		if t.Due != nil {
			if d, err := time.Parse(time.DateOnly, *t.Due); err == nil {
				layouts[i].due = d.Format(r.cfg.DateFormat)
				textW -= advance(r.text, layouts[i].due) + gap
			}
		}
		layouts[i].lines = wrap(r.text, t.Text, textW)
	}

	// starts[i] is the top of task i; starts[n] is where a line after the last task would go.
	n := len(tasks)
	starts := make([]float64, n+1)
	y := top
	for i, l := range layouts {
		starts[i] = y
		y += float64(len(l.lines))*lineH + spacing
	}
	starts[n] = y
	// end is where the text of task i stops: its last line needs only the glyph height, not the line spacing.
	textH := float64(ascent + descent)
	end := func(i int) float64 { return starts[i] + float64(len(layouts[i].lines)-1)*lineH + textH }
	// The gap between tasks is the same everywhere: from end(i) to starts[i+1].
	halfGap := (lineH - textH + spacing) / 2

	// Show as many whole tasks as fit. If some don't, keep room for the "+N more" line.
	if end(n-1) <= bottom {
		f.Shown = n
	} else {
		for f.Shown < n && end(f.Shown) <= bottom && starts[f.Shown+1]+textH <= bottom {
			f.Shown++
		}
	}

	for i := range f.Shown {
		t, l := tasks[i], layouts[i]
		col := priorityColor[t.Priority]
		if i%2 == 1 {
			// Even tasks (2nd, 4th, …) sit on a light grey band reaching halfway into the gaps around them.
			bandTop := max(int(math.Round(starts[i]-halfGap)), int(top))
			bandBottom := min(int(math.Round(starts[i+1]-halfGap)), int(bottom))
			c.shade(image.Rect(left, bandTop, right, bandBottom))
		}
		if i > 0 && t.Priority != tasks[i-1].Priority {
			// A line in the middle of the gap separates priorities.
			mid := int(math.Round((end(i-1) + starts[i]) / 2))
			c.hline(left, right, mid-separatorPx/2, separatorPx, colBlack)
		}
		firstBaseline := int(math.Round(starts[i])) + ascent
		if t.Icon != 0 {
			c.iconCentered(r.icon, left, iconW, firstBaseline-ascent, ascent+descent, t.Icon, col)
		}
		for j, line := range l.lines {
			c.text(r.text, textX, int(math.Round(starts[i]+float64(j)*lineH))+ascent, line, col)
		}
		if l.due != "" {
			c.text(r.text, right-advance(r.text, l.due), firstBaseline, l.due, colBlack)
		}
	}
	if hidden := n - f.Shown; hidden > 0 {
		c.text(r.text, textX, int(math.Round(starts[f.Shown]))+ascent, "+"+strconv.Itoa(hidden)+" more", colBlack)
	}
	return f
}

// PanelData returns the frame in the panel's native orientation and format:
// 800×480, rows top to bottom, two pixels per byte, left pixel in the high 4 bits.
func (f *Frame) PanelData() []byte {
	out := make([]byte, PanelBytes)
	src := f.Image
	for py := range PanelHeight {
		for px := range PanelWidth {
			lx, ly := px, py
			switch f.Rotation {
			case 90:
				lx, ly = py, PanelWidth-1-px
			case 180:
				lx, ly = PanelWidth-1-px, PanelHeight-1-py
			case 270:
				lx, ly = PanelHeight-1-py, px
			}
			code := panelCode[src.Pix[ly*src.Stride+lx]]
			i := py*PanelWidth/2 + px/2
			if px%2 == 0 {
				out[i] |= code << 4
			} else {
				out[i] |= code
			}
		}
	}
	return out
}

// ETag returns a quoted HTTP entity tag for panel data.
func ETag(data []byte) string {
	sum := sha256.Sum256(data)
	return `"` + hex.EncodeToString(sum[:8]) + `"`
}

func advance(face font.Face, s string) int { return font.MeasureString(face, s).Ceil() }

// wrap breaks s into lines no wider than width, splitting words that are too long on their own.
func wrap(face font.Face, s string, width int) []string {
	var lines []string
	line := ""
	for _, w := range strings.Fields(s) {
		for advance(face, w) > width {
			if line != "" {
				lines = append(lines, line)
				line = ""
			}
			n := fitPrefix(face, w, width)
			lines = append(lines, w[:n])
			w = w[n:]
		}
		switch {
		case w == "":
		case line == "":
			line = w
		case advance(face, line+" "+w) <= width:
			line += " " + w
		default:
			lines = append(lines, line)
			line = w
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}

// fitPrefix returns the byte length of the longest proper prefix of s that fits in width,
// but at least one character.
func fitPrefix(face font.Face, s string, width int) int {
	_, n := utf8.DecodeRuneInString(s)
	for i := range s {
		if i <= n {
			continue
		}
		if advance(face, s[:i]) > width {
			break
		}
		n = i
	}
	return n
}

// canvas draws text in single palette colours without anti-aliasing:
// glyphs are rendered to an alpha mask and each pixel at least half covered gets the colour.
type canvas struct {
	img  *image.Paletted
	mask *image.Alpha
}

func newCanvas(w, h int) *canvas {
	c := &canvas{
		img:  image.NewPaletted(image.Rect(0, 0, w, h), palette),
		mask: image.NewAlpha(image.Rect(0, 0, w, h)),
	}
	for i := range c.img.Pix {
		c.img.Pix[i] = colWhite
	}
	return c
}

func (c *canvas) text(face font.Face, x, baseline int, s string, col uint8) {
	d := font.Drawer{Dst: c.mask, Src: image.Opaque, Face: face, Dot: fixed.P(x, baseline)}
	b, _ := d.BoundString(s)
	d.DrawString(s)
	c.apply(image.Rect(b.Min.X.Floor(), b.Min.Y.Floor(), b.Max.X.Ceil(), b.Max.Y.Ceil()), col)
}

// iconCentered draws an icon centred in the box with the given left edge, width, top and height.
func (c *canvas) iconCentered(face font.Face, left, width, top, height int, icon rune, col uint8) {
	s := string(icon)
	b, _ := font.BoundString(face, s)
	w, h := (b.Max.X - b.Min.X).Ceil(), (b.Max.Y - b.Min.Y).Ceil()
	x := left + (width-w)/2 - b.Min.X.Floor()
	y := top + (height-h)/2 - b.Min.Y.Floor()
	c.text(face, x, y, s, col)
}

// hline fills rows top..top+thickness-1 from x0 up to (not including) x1.
func (c *canvas) hline(x0, x1, top, thickness int, col uint8) {
	r := image.Rect(x0, top, x1, top+thickness).Intersect(c.img.Rect)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			c.img.Pix[c.img.PixOffset(x, y)] = col
		}
	}
}

// shade makes r light grey. The panel has no grey, so it's black dots on white:
// one pixel in 8, on a diagonal grid tied to the image so neighbouring bands line up.
func (c *canvas) shade(r image.Rectangle) {
	r = r.Intersect(c.img.Rect)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			if x%2 == 0 && x%4 == y%4 {
				c.img.Pix[c.img.PixOffset(x, y)] = colBlack
			}
		}
	}
}

func (c *canvas) apply(r image.Rectangle, col uint8) {
	r = r.Intersect(c.img.Rect)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			i := c.mask.PixOffset(x, y)
			if c.mask.Pix[i] >= 0x80 {
				c.img.Pix[c.img.PixOffset(x, y)] = col
			}
			c.mask.Pix[i] = 0
		}
	}
}
