// Package ascii renders text as ASCII art using banner files (text fonts).
// It is the core of symbol-art, adapted for use from a web server.
package ascii

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Height is the fixed number of lines used to draw a single character.
const Height = 8

// FirstRune and LastRune bound the printable ASCII range a banner file
// must describe, in order: space (32) through '~' (126).
const (
	FirstRune = 32
	LastRune  = 126
)

// MaxTextLength is the maximum number of characters accepted as input.
const MaxTextLength = 1000

// Banners lists every banner name the generator supports.
var Banners = []string{"standard", "shadow", "thinkertoy"}

// Sentinel errors let callers (the HTTP handlers) pick the right status
// code with errors.Is instead of comparing error strings.
var (
	ErrEmptyText      = errors.New("text is empty")
	ErrTextTooLong    = fmt.Errorf("text is longer than %d characters", MaxTextLength)
	ErrInvalidChar    = errors.New("text contains an unsupported character")
	ErrInvalidBanner  = errors.New("unknown banner")
	ErrBannerNotFound = errors.New("banner file not found")
)

// Font maps a printable ASCII rune to its Height-line ASCII-art glyph.
type Font map[rune][Height]string

// Generator renders text with banner files stored in a directory.
type Generator struct {
	dir string
}

// NewGenerator returns a Generator that reads banner files from dir.
func NewGenerator(dir string) *Generator {
	return &Generator{dir: dir}
}

// Generate validates text and banner, loads the banner file and returns
// the rendered ASCII art as a single string with "\n" line separators.
func (g *Generator) Generate(text, banner string) (string, error) {
	text = NormalizeNewlines(text)
	if err := ValidateText(text); err != nil {
		return "", err
	}
	if !IsValidBanner(banner) {
		return "", fmt.Errorf("%w: %q", ErrInvalidBanner, banner)
	}

	font, err := LoadFont(filepath.Join(g.dir, banner+".txt"))
	if err != nil {
		return "", err
	}

	lines, err := Render(font, text)
	if err != nil {
		return "", err
	}
	return strings.Join(lines, "\n"), nil
}

// NormalizeNewlines converts Windows-style "\r\n" (which browsers send
// for a <textarea>) into plain "\n".
func NormalizeNewlines(text string) string {
	return strings.ReplaceAll(text, "\r\n", "\n")
}

// ValidateText checks that text is non-blank, not too long and made only
// of printable ASCII characters and newlines.
func ValidateText(text string) error {
	if strings.TrimSpace(text) == "" {
		return ErrEmptyText
	}
	if len([]rune(text)) > MaxTextLength {
		return ErrTextTooLong
	}
	for _, r := range text {
		if r != '\n' && (r < FirstRune || r > LastRune) {
			return fmt.Errorf("%w: %q", ErrInvalidChar, r)
		}
	}
	return nil
}

// IsValidBanner reports whether name is one of the supported Banners.
func IsValidBanner(name string) bool {
	for _, b := range Banners {
		if b == name {
			return true
		}
	}
	return false
}

// LoadFont reads a banner file and builds a Font from it.
//
// A banner file lists the printable ASCII characters in order, starting
// at FirstRune. Each character occupies a block of Height+1 lines: one
// blank separator line followed by exactly Height lines of ASCII art.
// A missing file is reported as ErrBannerNotFound.
func LoadFont(path string) (Font, error) {
	file, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s", ErrBannerNotFound, path)
	}
	if err != nil {
		return nil, fmt.Errorf("ascii: open %s: %w", path, err)
	}
	defer file.Close()

	font := Font{}
	block := make([]string, 0, Height+1)
	current := rune(FirstRune)

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		block = append(block, line)

		if len(block) == Height+1 {
			var glyph [Height]string
			copy(glyph[:], block[1:])
			font[current] = glyph
			current++
			block = block[:0]
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("ascii: read %s: %w", path, err)
	}
	if len(block) != 0 {
		return nil, fmt.Errorf("ascii: %s: incomplete character block (%d leftover lines)", path, len(block))
	}
	if current != LastRune+1 {
		return nil, fmt.Errorf("ascii: %s: expected %d characters, got %d", path, LastRune-FirstRune+1, current-FirstRune)
	}

	return font, nil
}

// Render converts input into the list of output lines that make up the
// ASCII-art rendering.
//
// Rules:
//   - "" (the empty string) produces no output lines at all.
//   - input is split on "\n"; each non-empty segment becomes a
//     Height-line block of ASCII art.
//   - Every run of one or more consecutive empty segments collapses into
//     exactly one blank output line.
func Render(font Font, input string) ([]string, error) {
	if input == "" {
		return nil, nil
	}

	segments := strings.Split(input, "\n")
	var lines []string
	prevEmpty := false

	for _, seg := range segments {
		if seg == "" {
			if !prevEmpty { // first empty segment in a run: emit one blank line
				lines = append(lines, "")
			}
			prevEmpty = true
			continue
		}
		prevEmpty = false

		block, err := renderSegment(font, seg)
		if err != nil {
			return nil, err
		}
		lines = append(lines, block...)
	}

	return lines, nil
}

// renderSegment renders a single, newline-free segment into Height rows.
func renderSegment(font Font, segment string) ([]string, error) {
	rows := make([]strings.Builder, Height)
	for _, r := range segment {
		glyph, ok := font[r]
		if !ok {
			return nil, fmt.Errorf("%w: %q", ErrInvalidChar, r)
		}
		for row := 0; row < Height; row++ {
			rows[row].WriteString(glyph[row])
		}
	}

	out := make([]string, Height)
	for row := range rows {
		out[row] = rows[row].String()
	}
	return out, nil
}
