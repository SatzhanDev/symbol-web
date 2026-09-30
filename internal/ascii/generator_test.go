package ascii

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// bannerDir is where the real banner files live, relative to this package.
const bannerDir = "../.."

// tinyFont is a small hand-built font used to test Render in isolation
// from the real banner files.
func tinyFont() Font {
	return Font{
		'A': {"A0", "A1", "A2", "A3", "A4", "A5", "A6", "A7"},
		'B': {"B0", "B1", "B2", "B3", "B4", "B5", "B6", "B7"},
	}
}

func TestRender(t *testing.T) {
	a := []string{"A0", "A1", "A2", "A3", "A4", "A5", "A6", "A7"}
	b := []string{"B0", "B1", "B2", "B3", "B4", "B5", "B6", "B7"}
	join := func(parts ...[]string) []string {
		var out []string
		for _, p := range parts {
			out = append(out, p...)
		}
		return out
	}

	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{"empty", "", nil},
		{"single newline", "\n", []string{""}},
		{"word", "AB", []string{"A0B0", "A1B1", "A2B2", "A3B3", "A4B4", "A5B5", "A6B6", "A7B7"}},
		{"trailing newline", "A\n", join(a, []string{""})},
		{"one newline between", "A\nB", join(a, b)},
		{"blank line between", "A\n\nB", join(a, []string{""}, b)},
		{"run of newlines collapses", "\n\n\n", []string{""}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Render(tinyFont(), tt.input)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRenderUnsupportedCharacter(t *testing.T) {
	_, err := Render(tinyFont(), "C")
	if !errors.Is(err, ErrInvalidChar) {
		t.Fatalf("got %v, want ErrInvalidChar", err)
	}
}

func TestLoadFontAllBanners(t *testing.T) {
	for _, name := range Banners {
		t.Run(name, func(t *testing.T) {
			font, err := LoadFont(filepath.Join(bannerDir, name+".txt"))
			if err != nil {
				t.Fatalf("LoadFont: %v", err)
			}
			if want := LastRune - FirstRune + 1; len(font) != want {
				t.Errorf("got %d glyphs, want %d", len(font), want)
			}
		})
	}
}

func TestLoadFontIncompleteFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "broken.txt")
	if err := os.WriteFile(path, []byte("\nline1\nline2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFont(path); err == nil {
		t.Fatal("expected an error for an incomplete banner file, got nil")
	}
}

func TestGenerateStandard(t *testing.T) {
	// Reference output produced by symbol-art for "Hi!".
	want := strings.Join([]string{
		` _    _   _   _  `,
		`| |  | | (_) | | `,
		`| |__| |  _  | | `,
		`|  __  | | | | | `,
		`| |  | | | | |_| `,
		`|_|  |_| |_| (_) `,
		`                 `,
		`                 `,
	}, "\n")

	got, err := NewGenerator(bannerDir).Generate("Hi!", "standard")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestGenerateAllBannersSameWidthRows(t *testing.T) {
	g := NewGenerator(bannerDir)
	for _, name := range Banners {
		t.Run(name, func(t *testing.T) {
			art, err := g.Generate("Hello", name)
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			rows := strings.Split(art, "\n")
			if len(rows) != Height {
				t.Fatalf("got %d rows, want %d", len(rows), Height)
			}
			for i, r := range rows {
				if len(r) != len(rows[0]) {
					t.Errorf("row %d has width %d, want %d", i, len(r), len(rows[0]))
				}
			}
		})
	}
}

func TestGenerateWindowsNewlines(t *testing.T) {
	g := NewGenerator(bannerDir)
	unix, err := g.Generate("A\nB", "standard")
	if err != nil {
		t.Fatal(err)
	}
	windows, err := g.Generate("A\r\nB", "standard")
	if err != nil {
		t.Fatal(err)
	}
	if unix != windows {
		t.Error(`"\r\n" and "\n" should render identically`)
	}
}

func TestGenerateErrors(t *testing.T) {
	tests := []struct {
		name   string
		dir    string
		text   string
		banner string
		want   error
	}{
		{"empty text", bannerDir, "", "standard", ErrEmptyText},
		{"only spaces", bannerDir, "   ", "standard", ErrEmptyText},
		{"too long", bannerDir, strings.Repeat("a", MaxTextLength+1), "standard", ErrTextTooLong},
		{"non-ASCII", bannerDir, "Привет", "standard", ErrInvalidChar},
		{"tab", bannerDir, "a\tb", "standard", ErrInvalidChar},
		{"unknown banner", bannerDir, "hi", "comic", ErrInvalidBanner},
		{"path traversal banner", bannerDir, "hi", "../standard", ErrInvalidBanner},
		{"missing banner file", t.TempDir(), "hi", "standard", ErrBannerNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewGenerator(tt.dir).Generate(tt.text, tt.banner)
			if !errors.Is(err, tt.want) {
				t.Fatalf("got %v, want %v", err, tt.want)
			}
		})
	}
}

func TestGenerateMaxLengthAllowed(t *testing.T) {
	if _, err := NewGenerator(bannerDir).Generate(strings.Repeat("a", MaxTextLength), "standard"); err != nil {
		t.Fatalf("text of exactly %d characters should be accepted: %v", MaxTextLength, err)
	}
}
