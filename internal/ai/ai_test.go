package ai

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"symbol-web/internal/ascii"
)

// referenceProfiles are the values the symbol-fs task gives "to check
// your work". The real code measures the banner files instead.
var referenceProfiles = []BannerProfile{
	{Name: Thinkertoy, AvgWidth: 5.06, Style: StyleCompact},
	{Name: Standard, AvgWidth: 7.59, Style: StyleStandard},
	{Name: Shadow, AvgWidth: 8.69, Style: StyleWide},
}

func TestProfileBannersMatchesReference(t *testing.T) {
	got, err := ProfileBanners("../..")
	if err != nil {
		t.Fatalf("ProfileBanners: %v", err)
	}

	want := map[string]BannerProfile{}
	for _, p := range referenceProfiles {
		want[p.Name] = p
	}
	if len(got) != len(want) {
		t.Fatalf("got %d profiles, want %d", len(got), len(want))
	}
	for _, p := range got {
		if p != want[p.Name] {
			t.Errorf("profile of %s = %+v, want %+v", p.Name, p, want[p.Name])
		}
	}
}

func TestProfileBannerAverage(t *testing.T) {
	// Two glyphs of width 2 and 5 -> average 3.5 -> compact.
	font := ascii.Font{
		'A': {"aa", "aa", "aa", "aa", "aa", "aa", "aa", "aa"},
		'B': {"bbbbb", "bbbbb", "bbbbb", "bbbbb", "bbbbb", "bbbbb", "bbbbb", "bbbbb"},
	}
	got := ProfileBanner("tiny", font)
	want := BannerProfile{Name: "tiny", AvgWidth: 3.5, Style: StyleCompact}
	if got != want {
		t.Errorf("ProfileBanner = %+v, want %+v", got, want)
	}
}

func TestClassifyStyle(t *testing.T) {
	tests := []struct {
		width float64
		want  string
	}{
		{5.06, StyleCompact},
		{5.99, StyleCompact},
		{6, StyleStandard},
		{8.5, StyleStandard},
		{8.51, StyleWide},
		{8.69, StyleWide},
	}
	for _, tt := range tests {
		if got := classifyStyle(tt.width); got != tt.want {
			t.Errorf("classifyStyle(%v) = %q, want %q", tt.width, got, tt.want)
		}
	}
}

func TestProfile(t *testing.T) {
	got := Profile("  Hi 42!\nYo  ")
	want := TextProfile{Length: 8, Lines: 2, Longest: 6, Uppercase: 2, Lowercase: 2, Digits: 2, Special: 1, Spaces: 1}
	if got != want {
		t.Errorf("Profile = %+v, want %+v", got, want)
	}
}

func newTestRecommender() *Recommender {
	return NewRecommender(referenceProfiles)
}

func TestProfileBannersSkipsMissingFile(t *testing.T) {
	dir := t.TempDir()
	data, err := os.ReadFile("../../standard.txt")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "standard.txt"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	// Only standard.txt exists: its profile is returned, the others fail.
	got, err := ProfileBanners(dir)
	if err == nil {
		t.Error("expected an error for the missing shadow and thinkertoy files")
	}
	if len(got) != 1 || got[0].Name != Standard {
		t.Errorf("got %+v, want only the standard profile", got)
	}
}

// Without banner profiles the recommendation still works, on style alone.
func TestRecommendWithoutProfiles(t *testing.T) {
	rc := NewRecommender(nil)

	rec := rc.Recommend("Hello wonderful world of ASCII")
	if rec.Recommended != Standard {
		t.Errorf("recommended %q, want %q", rec.Recommended, Standard)
	}
	// No width penalty: the scores are the plain style fit values.
	if got := alternative(t, rec, Shadow).Score; got != 0.4 {
		t.Errorf("shadow score = %v, want 0.4 (style fit only)", got)
	}
	if strings.Contains(rec.Reasoning, "columns") {
		t.Errorf("reasoning should not mention widths without profiles: %q", rec.Reasoning)
	}
}

// If only some banners were measured, comparing widths would favour the
// unmeasured one, so width is ignored for all of them.
func TestRecommendWithPartialProfiles(t *testing.T) {
	var withoutShadow []BannerProfile
	for _, p := range referenceProfiles {
		if p.Name != Shadow {
			withoutShadow = append(withoutShadow, p)
		}
	}

	text := "Hello wonderful world of ASCII"
	got := NewRecommender(withoutShadow).Recommend(text)
	want := NewRecommender(nil).Recommend(text)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("partial profiles should behave like no profiles:\n got  %+v\n want %+v", got, want)
	}
}

// Every rule must give a reason for every banner, so any banner can be
// shown as an alternative with a sensible explanation.
func TestEveryRuleExplainsEveryBanner(t *testing.T) {
	texts := []string{"~*~ yay ~*~", "WELCOME", "hello", "Hello wonderful world", "Hello there"}
	for _, text := range texts {
		sf := classifyText(Profile(text))
		for _, b := range []string{Shadow, Standard, Thinkertoy} {
			if sf.reasons[b] == "" {
				t.Errorf("%q: rule has no reason for %s", text, b)
			}
			if _, ok := sf.fit[b]; !ok {
				t.Errorf("%q: rule has no style fit for %s", text, b)
			}
		}
	}
}

func TestRecommendRules(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{"uppercase word", "WELCOME", Shadow},
		{"long uppercase", "HAPPY BIRTHDAY TEAM", Shadow},
		{"short lowercase", "hello", Shadow},
		{"short with one punctuation", "Hi!", Shadow},
		{"exactly 6 chars", "Hello!", Shadow},
		{"long mixed text", "Hello wonderful world", Standard},
		{"exactly 13 chars", "Hello, world!", Standard},
		{"very long mixed text", strings.Repeat("Hello world ", 8), Standard},
		{"multiline", "Hello\nthere", Standard},
		{"medium mixed text", "Hello there", Thinkertoy},
		{"symbol heavy", "~*~ yay ~*~", Thinkertoy},
		{"symbol heavy beats uppercase", "**WOW**", Thinkertoy},
	}

	rc := newTestRecommender()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rc.Recommend(tt.text)
			if got.Recommended != tt.want {
				t.Errorf("Recommend(%q) = %q (%s), want %q", tt.text, got.Recommended, got.Reasoning, tt.want)
			}
		})
	}
}

// For "WELCOME" (fits on screen in every banner) the answer is exactly
// the example from the task.
func TestRecommendMatchesTaskExample(t *testing.T) {
	want := Recommendation{
		Recommended: Shadow,
		Reasoning:   "Bold, uppercase text works best with shadow for maximum impact and readability.",
		Alternatives: []Alternative{
			{Banner: Standard, Score: 0.7, Reason: "Good alternative, slightly less impactful"},
			{Banner: Thinkertoy, Score: 0.4, Reason: "Too decorative for uppercase text"},
		},
	}
	if got := newTestRecommender().Recommend("WELCOME"); !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
}

// Scores depend on the measured banner widths: once the art no longer
// fits in 80 columns, the wide banners lose points.
func TestScoresDependOnBannerWidth(t *testing.T) {
	rc := newTestRecommender()

	// Both texts are "long" (standard wins); only their width differs.
	// shadow: 13 chars * 8.69 = 113 columns -> 0.4 * 80/113 = 0.28
	// shadow: 30 chars * 8.69 = 261 columns -> 0.4 * 80/261 = 0.12
	medium := alternative(t, rc.Recommend("Hello, world!"), Shadow)
	long := alternative(t, rc.Recommend("Hello wonderful world of ASCII"), Shadow)

	if medium.Score != 0.28 {
		t.Errorf("shadow score for 13 chars = %v, want 0.28", medium.Score)
	}
	if long.Score != 0.12 {
		t.Errorf("shadow score for 30 chars = %v, want 0.12", long.Score)
	}
	if !strings.Contains(long.Reason, "columns wide") {
		t.Errorf("an alternative that overflows the screen should explain its width, got %q", long.Reason)
	}
}

// alternative returns the alternative for banner, failing the test if absent.
func alternative(t *testing.T, rec Recommendation, banner string) Alternative {
	t.Helper()
	for _, a := range rec.Alternatives {
		if a.Banner == banner {
			return a
		}
	}
	t.Fatalf("banner %q is not among the alternatives of %+v", banner, rec)
	return Alternative{}
}

// Every recommendation must be complete and consistent, whatever the text.
func TestRecommendShape(t *testing.T) {
	inputs := []string{
		"WELCOME", "hello", "Hello wonderful world", "Hello\nthere", "Hello there",
		"~*~ yay ~*~", "HAPPY BIRTHDAY TEAM", strings.Repeat("Hello world ", 8),
	}

	rc := newTestRecommender()
	for _, text := range inputs {
		rec := rc.Recommend(text)

		if strings.TrimSpace(rec.Reasoning) == "" {
			t.Errorf("%q: empty reasoning", text)
		}
		if len(rec.Alternatives) != 2 {
			t.Fatalf("%q: got %d alternatives, want 2", text, len(rec.Alternatives))
		}

		// The recommended banner plus the alternatives cover all 3 banners once.
		seen := map[string]bool{rec.Recommended: true}
		for i, alt := range rec.Alternatives {
			if seen[alt.Banner] {
				t.Errorf("%q: banner %q appears twice", text, alt.Banner)
			}
			seen[alt.Banner] = true

			if alt.Score <= 0 || alt.Score >= 1 {
				t.Errorf("%q: score %v of %q is outside (0, 1)", text, alt.Score, alt.Banner)
			}
			if alt.Reason == "" {
				t.Errorf("%q: alternative %q has no reason", text, alt.Banner)
			}
			if i > 0 && alt.Score > rec.Alternatives[i-1].Score {
				t.Errorf("%q: alternatives are not sorted by score", text)
			}
		}
		for _, b := range []string{Shadow, Standard, Thinkertoy} {
			if !seen[b] {
				t.Errorf("%q: banner %q is missing", text, b)
			}
		}
	}
}

func TestRecommendIsDeterministic(t *testing.T) {
	rc := newTestRecommender()
	first := rc.Recommend("Hello there")
	for i := 0; i < 5; i++ {
		if !reflect.DeepEqual(rc.Recommend("Hello there"), first) {
			t.Fatal("the same text must always give the same recommendation")
		}
	}
}
