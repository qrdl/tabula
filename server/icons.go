package main

import (
	_ "embed"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/image/font/sfnt"
)

//go:embed assets/MaterialIcons-Regular.ttf
var materialIconsTTF []byte

//go:embed assets/MaterialIcons-Regular.codepoints
var materialIconsCodepoints string

// Icons maps Material icon names to their characters in the Material Icons font.
type Icons struct {
	byName map[string]rune
	names  []string
}

func LoadIcons() (*Icons, error) {
	f, err := sfnt.Parse(materialIconsTTF)
	if err != nil {
		return nil, fmt.Errorf("parse Material Icons font: %w", err)
	}
	var buf sfnt.Buffer
	ic := &Icons{byName: map[string]rune{}}
	for line := range strings.Lines(materialIconsCodepoints) {
		name, hex, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		cp, err := strconv.ParseUint(hex, 16, 32)
		if err != nil {
			return nil, fmt.Errorf("codepoints line %q: %w", line, err)
		}
		// Skip names the font has no glyph for, so every listed icon can be drawn.
		if gi, err := f.GlyphIndex(&buf, rune(cp)); err != nil || gi == 0 {
			continue
		}
		ic.byName[name] = rune(cp)
		ic.names = append(ic.names, name)
	}
	sort.Strings(ic.names)
	return ic, nil
}

func (ic *Icons) Rune(name string) (rune, bool) {
	r, ok := ic.byName[name]
	return r, ok
}

func (ic *Icons) Names() []string { return ic.names }
