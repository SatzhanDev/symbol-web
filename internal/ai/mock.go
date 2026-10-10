package ai

import (
	"context"
	"strings"

	"symbol-web/internal/ascii"
)

// Mock is used when no LLM backend is configured. It returns
// deterministic canned answers built from the input, without any network
// request or API key, so the project runs and can be tested anywhere.
// It has the same methods as Client.
type Mock struct{}

// NewMock returns a Mock.
func NewMock() *Mock {
	return &Mock{}
}

// Mode reports "mock", for logs and the X-LLM-Mode response header.
func (m *Mock) Mode() string {
	return "mock"
}

// GetSuggestions returns canned completions of text.
func (m *Mock) GetSuggestions(_ context.Context, text string) ([]string, error) {
	base := mockBase(text)
	return []string{
		base + "!",
		base + " World",
		"~ " + base + " ~",
		strings.ToUpper(base),
	}, nil
}

// GetVariations returns canned variations of text, one per style the
// real prompt asks for.
func (m *Mock) GetVariations(_ context.Context, text string) ([]Variation, error) {
	base := mockBase(text)
	return []Variation{
		{Text: titleCase(base) + ".", Description: "Professional, formal style", SuggestedBanner: ascii.Standard},
		{Text: strings.ToUpper(base) + "!", Description: "Bold, attention-grabbing", SuggestedBanner: ascii.Shadow},
		{Text: strings.ToLower(base) + " :)", Description: "Friendly, casual style", SuggestedBanner: ascii.Standard},
		{Text: "~ " + base + " ~", Description: "Friendly, decorated style", SuggestedBanner: ascii.Thinkertoy},
	}, nil
}

// mockBase shortens text so that every canned answer stays within
// maxSuggestionLen characters.
func mockBase(text string) string {
	const room = maxSuggestionLen - len(" World") // the longest decoration above
	s := strings.TrimSpace(strings.ReplaceAll(text, "\n", " "))
	if len(s) > room {
		s = strings.TrimSpace(s[:room])
	}
	return s
}

// titleCase upper-cases the first letter of every word: "hello world" -> "Hello World".
func titleCase(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		words[i] = strings.ToUpper(w[:1]) + strings.ToLower(w[1:])
	}
	return strings.Join(words, " ")
}
