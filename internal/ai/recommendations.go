// Package ai contains the "smart" features of symbol-web: a rule-based
// banner recommender (this file) and LLM-backed text suggestions.
package ai

import (
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"

	"symbol-web/internal/ascii"
)

// Banner names, kept in sync with ascii.Banners.
const (
	Shadow     = "shadow"
	Standard   = "standard"
	Thinkertoy = "thinkertoy"
)

// Banner styles, classified from the average character width with the
// table from the symbol-fs task:
//
//	below 6    -> compact
//	6 to 8.5   -> standard
//	above 8.5  -> wide
const (
	StyleCompact  = "compact"
	StyleStandard = "standard"
	StyleWide     = "wide"

	compactBelow = 6.0 // average widths below this are compact
	wideAbove    = 8.5 // average widths above this are wide
)

// Thresholds used by the recommendation rules.
const (
	shortTextMax     = 6   // up to this many characters counts as "short"
	longTextMin      = 13  // from this many characters counts as "long"
	symbolHeavyRatio = 0.3 // share of special characters that makes text "playful"
	symbolHeavyMin   = 2   // ...but at least this many, so "Hi!" is not playful
	screenWidth      = 80  // columns of art that fit without scrolling
)

// ---------------------------------------------------------------------
// Banner profiling (the idea from symbol-fs)
// ---------------------------------------------------------------------

// BannerProfile describes how wide a banner draws its characters.
type BannerProfile struct {
	Name     string
	AvgWidth float64 // average width of the 95 characters, rounded to 2 decimals
	Style    string  // compact, standard or wide
}

// ProfileBanner measures the average character width of font: the width
// of each of the 95 characters (the length of one of its lines), averaged
// and rounded to 2 decimal places, then classified into a style.
func ProfileBanner(name string, font ascii.Font) BannerProfile {
	total, count := 0, 0
	for r := rune(ascii.FirstRune); r <= ascii.LastRune; r++ {
		if glyph, ok := font[r]; ok {
			total += len(glyph[0]) // every line of a glyph has the same width
			count++
		}
	}

	avg := 0.0
	if count > 0 {
		avg = round2(float64(total) / float64(count))
	}
	return BannerProfile{Name: name, AvgWidth: avg, Style: classifyStyle(avg)}
}

// ProfileBanners loads and profiles every banner in dir. A banner that
// cannot be loaded is skipped: the profiles of the others are still
// returned, together with an error that names every failure.
func ProfileBanners(dir string) ([]BannerProfile, error) {
	var profiles []BannerProfile
	var errs []error
	for _, name := range ascii.Banners {
		font, err := ascii.LoadFont(filepath.Join(dir, name+".txt"))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		profiles = append(profiles, ProfileBanner(name, font))
	}
	return profiles, errors.Join(errs...)
}

func classifyStyle(avgWidth float64) string {
	switch {
	case avgWidth < compactBelow:
		return StyleCompact
	case avgWidth <= wideAbove:
		return StyleStandard
	default:
		return StyleWide
	}
}

// ---------------------------------------------------------------------
// Text profiling (the character classification from symbol-art)
// ---------------------------------------------------------------------

// TextProfile describes the characteristics the rules look at.
type TextProfile struct {
	Length    int // characters, without surrounding whitespace and newlines
	Lines     int // number of lines
	Longest   int // characters in the longest line: decides the art width
	Uppercase int
	Lowercase int
	Digits    int
	Special   int
	Spaces    int
}

// Profile classifies every character of text.
func Profile(text string) TextProfile {
	trimmed := strings.TrimSpace(text)
	var p TextProfile

	for _, line := range strings.Split(trimmed, "\n") {
		p.Lines++
		if n := len([]rune(line)); n > p.Longest {
			p.Longest = n
		}
		for _, r := range line {
			switch {
			case r >= 'A' && r <= 'Z':
				p.Uppercase++
			case r >= 'a' && r <= 'z':
				p.Lowercase++
			case r >= '0' && r <= '9':
				p.Digits++
			case r == ' ':
				p.Spaces++
			default:
				p.Special++
			}
			p.Length++
		}
	}
	return p
}

// AllUppercase reports whether the text has letters and all are uppercase.
func (p TextProfile) AllUppercase() bool {
	return p.Uppercase > 0 && p.Lowercase == 0
}

// SymbolHeavy reports whether special characters make up a large share
// of the text, e.g. "~*~ yay ~*~".
func (p TextProfile) SymbolHeavy() bool {
	if p.Length == 0 || p.Special < symbolHeavyMin {
		return false
	}
	return float64(p.Special)/float64(p.Length) >= symbolHeavyRatio
}

// ---------------------------------------------------------------------
// Recommendation
// ---------------------------------------------------------------------

// Alternative is a banner that was not picked, with its score and reason.
type Alternative struct {
	Banner string  `json:"banner"`
	Score  float64 `json:"score"`
	Reason string  `json:"reason"`
}

// Recommendation is the response of /api/recommend-banner.
type Recommendation struct {
	Recommended  string        `json:"recommended"`
	Reasoning    string        `json:"reasoning"`
	Alternatives []Alternative `json:"alternatives"`
}

// styleFit is how well each banner's look suits one kind of text. Its
// fields are named after the JSON fields they end up in.
type styleFit struct {
	reasoning string             // -> "reasoning": why this text suits the best banner
	fit       map[string]float64 // style fit of every banner, 0..1
	reasons   map[string]string  // -> alternatives[].reason: one short line per banner
}

// Recommender scores banners using the text profile and the measured
// banner profiles. It never calls an LLM or the network.
type Recommender struct {
	profiles map[string]BannerProfile
}

// NewRecommender returns a Recommender that uses the given banner profiles.
func NewRecommender(profiles []BannerProfile) *Recommender {
	r := &Recommender{profiles: make(map[string]BannerProfile, len(profiles))}
	for _, p := range profiles {
		r.profiles[p.Name] = p
	}
	return r
}

// Recommend picks the most suitable banner for text.
//
// Every banner gets a score = style fit × width fit:
//   - style fit (0..1) comes from fixed rules on the text profile
//     (case, length, special characters), see classifyText;
//   - width fit is 1 if the art fits in 80 columns, otherwise
//     80 / estimated width, where the estimated width is the longest line
//     times the banner's measured average character width. If any banner
//     has no profile (its file could not be read), width is ignored and
//     every banner is judged on style alone.
//
// The banner with the highest score is recommended; the others are
// returned as alternatives, best first. The result is deterministic.
func (rc *Recommender) Recommend(text string) Recommendation {
	p := Profile(text)
	sf := classifyText(p)

	type candidate struct {
		banner string
		score  float64
		fit    float64
		width  int
	}
	// Comparing widths is only fair when every banner was measured;
	// otherwise all banners are judged on style alone.
	useWidth := rc.profiledAll()

	var cands []candidate
	for _, name := range []string{Shadow, Standard, Thinkertoy} {
		width := 0
		if useWidth {
			width = rc.estimateWidth(name, p)
		}
		cands = append(cands, candidate{
			banner: name,
			score:  round2(sf.fit[name] * widthFit(width)),
			fit:    sf.fit[name],
			width:  width,
		})
	}
	// Highest score first; on a tie, the better style fit wins.
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].score != cands[j].score {
			return cands[i].score > cands[j].score
		}
		return cands[i].fit > cands[j].fit
	})

	best := cands[0]
	rec := Recommendation{Recommended: best.banner, Reasoning: sf.reasoning}
	if sf.fit[best.banner] < 1 {
		// The width, not the style, decided: say so.
		rec.Reasoning = fmt.Sprintf("%s keeps the art narrower (about %d columns), so it is easier to read.",
			best.banner, best.width)
	}
	if best.width > screenWidth {
		rec.Reasoning += fmt.Sprintf(" Note: the art will be about %d columns wide, more than %d.", best.width, screenWidth)
	}

	for _, c := range cands[1:] {
		reason := sf.reasons[c.banner]
		if c.width > screenWidth {
			reason += fmt.Sprintf("; %s (%.2f columns per character) makes it about %d columns wide",
				rc.profiles[c.banner].Style, rc.profiles[c.banner].AvgWidth, c.width)
		}
		rec.Alternatives = append(rec.Alternatives, Alternative{Banner: c.banner, Score: c.score, Reason: reason})
	}
	return rec
}

// classifyText applies the text rules in order; the first match wins:
//
//  1. symbol-heavy text            -> thinkertoy suits best (playful)
//  2. all uppercase                -> shadow suits best (bold)
//  3. <= 6 characters              -> shadow suits best (stands out)
//  4. >= 13 chars or several lines -> standard suits best (readable)
//  5. anything else                -> thinkertoy suits best (short mixed text)
func classifyText(p TextProfile) styleFit {
	switch {
	case p.SymbolHeavy():
		return styleFit{
			reasoning: fmt.Sprintf("Symbol-heavy text (%d of %d characters are symbols) fits the playful thinkertoy style.", p.Special, p.Length),
			fit:       map[string]float64{Thinkertoy: 1, Standard: 0.6, Shadow: 0.4},
			reasons:   map[string]string{Thinkertoy: "Playful style that suits symbols", Standard: "Readable, but less playful", Shadow: "Heavy letters overpower the symbols"},
		}
	case p.AllUppercase():
		return styleFit{
			reasoning: "Bold, uppercase text works best with shadow for maximum impact and readability.",
			fit:       map[string]float64{Shadow: 1, Standard: 0.7, Thinkertoy: 0.4},
			reasons:   map[string]string{Shadow: "Bold letters give uppercase text impact", Standard: "Good alternative, slightly less impactful", Thinkertoy: "Too decorative for uppercase text"},
		}
	case p.Length <= shortTextMax:
		return styleFit{
			reasoning: fmt.Sprintf("Short text (%d characters) stands out best in bold shadow letters.", p.Length),
			fit:       map[string]float64{Shadow: 1, Standard: 0.7, Thinkertoy: 0.5},
			reasons:   map[string]string{Shadow: "Bold letters make short text stand out", Standard: "Clean, but less eye-catching", Thinkertoy: "Playful, but thin for such short text"},
		}
	case p.Length >= longTextMin || p.Lines > 1:
		reasoning := fmt.Sprintf("Longer text (%d characters) stays readable in the clean standard font.", p.Length)
		if p.Lines > 1 {
			reasoning = fmt.Sprintf("Text with %d lines stays readable in the clean standard font.", p.Lines)
		}
		return styleFit{
			reasoning: reasoning,
			fit:       map[string]float64{Standard: 1, Thinkertoy: 0.6, Shadow: 0.4},
			reasons:   map[string]string{Standard: "Clean font that keeps long text readable", Thinkertoy: "Narrow, but harder to read in long text", Shadow: "Very wide letters make long text hard to read"},
		}
	default:
		return styleFit{
			reasoning: fmt.Sprintf("Short mixed-case text (%d characters) looks playful in thinkertoy.", p.Length),
			fit:       map[string]float64{Thinkertoy: 1, Standard: 0.7, Shadow: 0.5},
			reasons:   map[string]string{Thinkertoy: "Playful style for short mixed text", Standard: "Clean and readable alternative", Shadow: "Bolder, but less playful"},
		}
	}
}

// profiledAll reports whether every banner has a measured profile.
func (rc *Recommender) profiledAll() bool {
	for _, name := range []string{Shadow, Standard, Thinkertoy} {
		if _, ok := rc.profiles[name]; !ok {
			return false
		}
	}
	return true
}

// estimateWidth predicts the art width in columns: the longest line of
// text times the banner's average character width.
func (rc *Recommender) estimateWidth(banner string, p TextProfile) int {
	return int(math.Round(float64(p.Longest) * rc.profiles[banner].AvgWidth))
}

// widthFit is 1 when the art fits on screen and shrinks as it gets wider.
func widthFit(width int) float64 {
	if width <= screenWidth {
		return 1
	}
	return float64(screenWidth) / float64(width)
}

func round2(x float64) float64 {
	return math.Round(x*100) / 100
}
