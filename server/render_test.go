package main

import (
	"fmt"
	"image"
	"image/png"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestRenderer(t *testing.T, mod func(*ImageConfig)) *Renderer {
	t.Helper()
	cfg := defaultImageConfig()
	if mod != nil {
		mod(&cfg)
	}
	r, err := NewRenderer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// colours returns the set of palette indices used in img.
func colours(img *image.Paletted) map[uint8]bool {
	used := map[uint8]bool{}
	for _, p := range img.Pix {
		used[p] = true
	}
	return used
}

func TestNewRendererRejectsBadConfig(t *testing.T) {
	for name, mod := range map[string]func(*ImageConfig){
		"zero font size":     func(c *ImageConfig) { c.FontSize = 0 },
		"zero line spacing":  func(c *ImageConfig) { c.LineSpacing = 0 },
		"negative margin":    func(c *ImageConfig) { c.Margin.Left = -1 },
		"margins too wide":   func(c *ImageConfig) { c.Margin.Left, c.Margin.Right = 390, 390 },
		"missing font file":  func(c *ImageConfig) { c.Font = "/nonexistent/font.ttf" },
		"font is not a font": func(c *ImageConfig) { c.Font = "render_test.go" },
	} {
		cfg := defaultImageConfig()
		mod(&cfg)
		if _, err := NewRenderer(cfg); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestRenderSize(t *testing.T) {
	for rot, want := range map[int]image.Point{0: {800, 480}, 90: {480, 800}, 180: {800, 480}, 270: {480, 800}} {
		r := newTestRenderer(t, nil)
		if got := r.Render(nil, rot).Image.Rect.Size(); got != want {
			t.Errorf("rotation %d: size %v, want %v", rot, got, want)
		}
	}
}

func TestRenderColours(t *testing.T) {
	r := newTestRenderer(t, nil)
	f := r.Render([]ImageTask{
		{Text: "high", Priority: PriorityHigh, Due: ptr("2026-10-24"), Icon: 0xe8cc},
		{Text: "medium", Priority: PriorityMedium},
		{Text: "low", Priority: PriorityLow},
	}, 0)
	if f.Shown != 3 || f.Total != 3 {
		t.Errorf("shown %d of %d, want 3 of 3", f.Shown, f.Total)
	}
	used := colours(f.Image)
	for _, c := range []uint8{colWhite, colBlack, colRed, colBlue, colGreen} {
		if !used[c] {
			t.Errorf("colour %d not used", c)
		}
	}
	if used[colYellow] || len(used) != 5 {
		t.Errorf("unexpected colours: %v", used)
	}
}

func TestRenderEmpty(t *testing.T) {
	f := newTestRenderer(t, nil).Render(nil, 0)
	if used := colours(f.Image); !used[colBlack] {
		t.Error(`"Tabula rasa" not drawn`)
	}
}

func TestRenderShowsOnlyWhatFits(t *testing.T) {
	r := newTestRenderer(t, nil)
	var tasks []ImageTask
	for i := range 30 {
		tasks = append(tasks, ImageTask{Text: fmt.Sprintf("task %d", i), Priority: PriorityMedium})
	}
	f := r.Render(tasks, 0)
	if f.Total != 30 || f.Shown == 0 || f.Shown >= 30 {
		t.Fatalf("shown %d of %d", f.Shown, f.Total)
	}

	// The tasks drawn plus the "+N more" line must fit: nothing may be drawn in the bottom margin.
	img := f.Image
	for y := img.Rect.Max.Y - defaultImageConfig().Margin.Bottom; y < img.Rect.Max.Y; y++ {
		for x := range img.Rect.Max.X {
			if img.ColorIndexAt(x, y) != colWhite {
				t.Fatalf("pixel drawn in bottom margin at (%d,%d)", x, y)
			}
		}
	}

	// Fewer tasks either all fit, or the same number is shown as with 30
	// (all equally tall, with room kept for the "+N more" line).
	for n := 1; n <= len(tasks); n++ {
		if g := r.Render(tasks[:n], 0); g.Shown != n && g.Shown != f.Shown {
			t.Errorf("%d tasks: shown %d, want %d or %d", n, g.Shown, n, f.Shown)
		}
	}
}

func TestRenderShadesEvenTasks(t *testing.T) {
	r := newTestRenderer(t, nil)
	m := defaultImageConfig().Margin
	img := r.Render([]ImageTask{
		{Text: "one", Priority: PriorityMedium},
		{Text: "two", Priority: PriorityMedium},
		{Text: "three", Priority: PriorityMedium},
	}, 0).Image

	// A column right of the short texts crosses only the bands, which have a dot every 4 rows
	// in even columns; a longer run of white is an unshaded task.
	x := (PanelWidth - m.Right - 2) &^ 1
	bands, last := 0, -100
	for y := range PanelHeight {
		if img.ColorIndexAt(x, y) != colBlack {
			continue
		}
		if y < m.Top || y >= PanelHeight-m.Bottom {
			t.Errorf("dot in the margin at (%d,%d)", x, y)
		}
		if y-last > 4 {
			bands++
		}
		last = y
	}
	if bands != 1 {
		t.Errorf("%d shaded bands, want 1 (2nd task)", bands)
	}
}

func TestRenderPrioritySeparators(t *testing.T) {
	r := newTestRenderer(t, nil)
	m := defaultImageConfig().Margin
	// fullRows counts rows that are black from the left margin to the right one: only separators are.
	fullRows := func(img *image.Paletted) int {
		n := 0
		for y := range img.Rect.Max.Y {
			full := true
			for x := m.Left; x < img.Rect.Max.X-m.Right && full; x++ {
				full = img.ColorIndexAt(x, y) == colBlack
			}
			if full {
				n++
			}
		}
		return n
	}
	task := func(p Priority) ImageTask { return ImageTask{Text: "Buy milk", Priority: p} }

	tests := []struct {
		name  string
		tasks []ImageTask
		want  int // separators
	}{
		{"one priority", []ImageTask{task(PriorityMedium), task(PriorityMedium)}, 0},
		{"high, medium, low", []ImageTask{task(PriorityHigh), task(PriorityHigh), task(PriorityMedium), task(PriorityLow)}, 2},
		{"medium, low", []ImageTask{task(PriorityMedium), task(PriorityLow), task(PriorityLow)}, 1},
	}
	for _, tt := range tests {
		for _, rot := range []int{0, 90} {
			if got := fullRows(r.Render(tt.tasks, rot).Image); got != tt.want*separatorPx {
				t.Errorf("%s, rotation %d: %d full black rows, want %d", tt.name, rot, got, tt.want*separatorPx)
			}
		}
	}
}

func TestWrap(t *testing.T) {
	r := newTestRenderer(t, nil)
	const width = 200
	for _, s := range []string{
		"Buy milk",
		"Call the plumber about the leaking kitchen tap before the weekend",
		"Supercalifragilisticexpialidocious-and-then-some-more-characters",
		"Купить молоко и хлеб в магазине у дома",
	} {
		lines := wrap(r.text, s, width)
		if len(lines) == 0 {
			t.Errorf("%q: no lines", s)
		}
		for _, l := range lines {
			if advance(r.text, l) > width {
				t.Errorf("%q: line %q is wider than %d", s, l, width)
			}
		}
		if got := strings.ReplaceAll(strings.Join(lines, ""), " ", ""); got != strings.ReplaceAll(s, " ", "") {
			t.Errorf("%q: lines %q lose text", s, lines)
		}
	}
	if lines := wrap(r.text, "Buy milk", width); len(lines) != 1 {
		t.Errorf("short text wrapped: %q", lines)
	}
}

func TestPanelData(t *testing.T) {
	// Mark the top-left pixel of the image as mounted red, its right neighbour blue,
	// and check where they end up in the panel's native orientation.
	// White is 0x1, so an all-white byte is 0x11.
	tests := []struct {
		rotation int
		want     map[int]byte // index → value of every byte that isn't 0x11
	}{
		// red (0,0) in the high bits of byte 0, blue (1,0) in its low bits
		{0, map[int]byte{0: 0x35}},
		// red → top-right corner (799,0), blue → (799,1): low bits of the last byte of rows 0 and 1
		{90, map[int]byte{399: 0x13, 799: 0x15}},
		// red → bottom-right corner (799,479) low bits, blue → (798,479) high bits of the same byte
		{180, map[int]byte{PanelBytes - 1: 0x53}},
		// red → bottom-left corner (0,479), blue → (0,478): high bits of the first byte of those rows
		{270, map[int]byte{479 * 400: 0x31, 478 * 400: 0x51}},
	}
	for _, tt := range tests {
		r := newTestRenderer(t, nil)
		f := r.Render(nil, tt.rotation)
		for i := range f.Image.Pix {
			f.Image.Pix[i] = colWhite
		}
		f.Image.SetColorIndex(0, 0, colRed)
		f.Image.SetColorIndex(1, 0, colBlue)
		data := f.PanelData()
		if len(data) != PanelBytes {
			t.Fatalf("rotation %d: %d bytes, want %d", tt.rotation, len(data), PanelBytes)
		}
		got := map[int]byte{}
		for i, b := range data {
			if b != 0x11 {
				got[i] = b
			}
		}
		if fmt.Sprint(got) != fmt.Sprint(tt.want) {
			t.Errorf("rotation %d: non-white bytes %x, want %x", tt.rotation, got, tt.want)
		}
	}
}

func TestAPIImage(t *testing.T) {
	ta := newTestAPI(t)
	ta.mustDo("POST", "/api/tasks", `{"text":"Buy milk","priority":"high"}`, 201, nil)

	code, body := ta.do("GET", "/api/image.png", ``)
	if code != 200 || !strings.HasPrefix(body, "\x89PNG") {
		t.Fatalf("image.png: status %d", code)
	}

	req := httptest.NewRequest("GET", "/api/image.bin", nil)
	req.RemoteAddr = "192.168.8.50:4321"
	req.Header.Set("X-Device", "photopainter")
	rec := httptest.NewRecorder()
	ta.handler.ServeHTTP(rec, req)
	if rec.Code != 200 || rec.Body.Len() != PanelBytes {
		t.Fatalf("image.bin: status %d, %d bytes", rec.Code, rec.Body.Len())
	}
	etag := rec.Header().Get("ETag")
	if addr, _ := ta.store.DeviceAddr(req.Context()); addr != "192.168.8.50" {
		t.Errorf("device address %q, want 192.168.8.50", addr)
	}

	// Same image: 304. A request without the device header doesn't change the address.
	req = httptest.NewRequest("GET", "/api/image.bin", nil)
	req.RemoteAddr = "192.168.8.181:5555"
	req.Header.Set("If-None-Match", etag)
	rec = httptest.NewRecorder()
	ta.handler.ServeHTTP(rec, req)
	if rec.Code != 304 {
		t.Errorf("unchanged image: status %d, want 304", rec.Code)
	}
	if addr, _ := ta.store.DeviceAddr(req.Context()); addr != "192.168.8.50" {
		t.Errorf("device address changed to %q by a non-device request", addr)
	}

	// The device's rotation is recorded and becomes the default for previews.
	req = httptest.NewRequest("GET", "/api/image.bin?rotation=90", nil)
	req.Header.Set("X-Device", "photopainter")
	rec = httptest.NewRecorder()
	ta.handler.ServeHTTP(rec, req)
	if rec.Code != 200 || rec.Header().Get("ETag") == etag {
		t.Errorf("rotation 90: status %d, etag %s (rotation 0 was %s)", rec.Code, rec.Header().Get("ETag"), etag)
	}
	if rot, _ := ta.store.DeviceRotation(req.Context()); rot != 90 {
		t.Errorf("device rotation %d, want 90", rot)
	}
	code, body = ta.do("GET", "/api/image.png", ``)
	if img, err := png.DecodeConfig(strings.NewReader(body)); code != 200 || err != nil || img.Width != 480 || img.Height != 800 {
		t.Errorf("preview after device rotated to 90: status %d, %v, %dx%d, want 480x800", code, err, img.Width, img.Height)
	}
	if code, _ := ta.do("GET", "/api/image.bin?rotation=45", ``); code != 400 {
		t.Errorf("rotation 45: status %d, want 400", code)
	}

	// Changed image: 200 with a new ETag.
	ta.mustDo("POST", "/api/tasks", `{"text":"Call mum","priority":"low"}`, 201, nil)
	req = httptest.NewRequest("GET", "/api/image.bin", nil)
	req.Header.Set("If-None-Match", etag)
	rec = httptest.NewRecorder()
	ta.handler.ServeHTTP(rec, req)
	if rec.Code != 200 || rec.Header().Get("ETag") == etag {
		t.Errorf("changed image: status %d, etag %s (old %s)", rec.Code, rec.Header().Get("ETag"), etag)
	}
}
