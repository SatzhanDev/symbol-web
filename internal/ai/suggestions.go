package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"symbol-web/internal/ascii"
)

// Limits for what the LLM may return.
const (
	MinSuggestInput   = 3  // /api/suggest needs at least this many characters
	minResults        = 3  // the task asks for 3-5 suggestions / variations...
	maxResults        = 5  // ...so between minResults and maxResults are returned
	maxSuggestionLen  = 50 // "Each completion should be under 50 characters"
	maxDescriptionLen = 40 // the prompt asks for under 30; allow a little slack
)

// Variation is one creative rewrite of the text, for /api/variations.
type Variation struct {
	Text            string `json:"text"`
	Description     string `json:"description"`
	SuggestedBanner string `json:"suggested_banner"`
}

// suggestPrompt and variationsPrompt are the prompts from the task. The
// input is inserted with %q, which adds the quotes and escapes any quote
// inside the text, so user input cannot "close" the quotes and smuggle in
// its own instructions.
const suggestPrompt = `Complete this text creatively for ASCII art display:
Input: %q
Provide 3-5 short, creative completions suitable for ASCII art.
Each completion should be under 50 characters.
Return only the completions, one per line.`

const variationsPrompt = `Generate creative variations of this text for ASCII art:
Input: %q

Create 4 variations:
1. Professional/formal style
2. Bold/emphatic style
3. Friendly/casual style
4. Artistic/decorative style

For each variation, provide:
- The modified text
- Brief description (under 30 chars)
- Recommended banner (shadow/standard/thinkertoy)

Format as JSON array.
Use only English letters, digits and ASCII symbols.
Each item must look like: {"text": "...", "description": "...", "suggested_banner": "standard"}
Return only the JSON array, nothing else.`

// GetSuggestions asks the LLM for 3-5 completions of text. Successful
// answers are cached for a few minutes.
func (c *Client) GetSuggestions(ctx context.Context, text string) ([]string, error) {
	key := "suggest:" + text
	if v, ok := c.cache.get(key); ok {
		return v.([]string), nil
	}

	answer, err := c.complete(ctx, fmt.Sprintf(suggestPrompt, text))
	if err != nil {
		return nil, err
	}
	suggestions, err := parseSuggestions(answer)
	if err != nil {
		return nil, err
	}

	c.cache.set(key, suggestions)
	return suggestions, nil
}

// GetVariations asks the LLM for 3-5 creative variations of text.
// Successful answers are cached for a few minutes.
func (c *Client) GetVariations(ctx context.Context, text string) ([]Variation, error) {
	key := "variations:" + text
	if v, ok := c.cache.get(key); ok {
		return v.([]Variation), nil
	}

	answer, err := c.complete(ctx, fmt.Sprintf(variationsPrompt, text))
	if err != nil {
		return nil, err
	}
	variations, err := parseVariations(answer)
	if err != nil {
		return nil, err
	}

	c.cache.set(key, variations)
	return variations, nil
}

// ---------------------------------------------------------------------
// Parsing and validating LLM answers
// ---------------------------------------------------------------------

// parseSuggestions turns "one completion per line" into a clean list.
// LLMs often add numbering, bullets, quotes or a chatty intro line, so
// those are removed, and lines that could not be drawn as ASCII art or
// are too long are skipped.
func parseSuggestions(answer string) ([]string, error) {
	var out []string
	seen := map[string]bool{}

	for _, line := range strings.Split(answer, "\n") {
		s := cleanLine(line)
		if s == "" || strings.HasSuffix(s, ":") { // "Here are some ideas:"
			continue
		}
		if len(s) > maxSuggestionLen || !printableASCII(s) || seen[strings.ToLower(s)] {
			continue
		}
		seen[strings.ToLower(s)] = true
		out = append(out, s)
		if len(out) == maxResults {
			break
		}
	}

	if len(out) < minResults {
		return nil, fmt.Errorf("%w: only %d usable suggestions in %q", ErrInvalidResponse, len(out), snippet([]byte(answer)))
	}
	return out, nil
}

// parseVariations extracts the JSON array from the answer (LLMs like to
// wrap it in ```json fences or add a sentence around it) and keeps only
// the valid variations.
func parseVariations(answer string) ([]Variation, error) {
	start := strings.Index(answer, "[")
	end := strings.LastIndex(answer, "]")
	if start == -1 || end < start {
		return nil, fmt.Errorf("%w: no JSON array in %q", ErrInvalidResponse, snippet([]byte(answer)))
	}

	var raw []Variation
	if err := json.Unmarshal([]byte(answer[start:end+1]), &raw); err != nil {
		return nil, fmt.Errorf("%w: bad JSON array: %v", ErrInvalidResponse, err)
	}

	var out []Variation
	for _, v := range raw {
		v.Text = strings.TrimSpace(v.Text)
		v.Description = truncate(strings.TrimSpace(v.Description), maxDescriptionLen)
		v.SuggestedBanner = strings.ToLower(strings.TrimSpace(v.SuggestedBanner))

		if v.Text == "" || len(v.Text) > maxSuggestionLen || !printableASCII(v.Text) {
			continue
		}
		if !ascii.IsValidBanner(v.SuggestedBanner) {
			v.SuggestedBanner = ascii.DefaultBanner
		}
		out = append(out, v)
		if len(out) == maxResults {
			break
		}
	}

	if len(out) < minResults {
		return nil, fmt.Errorf("%w: only %d usable variations", ErrInvalidResponse, len(out))
	}
	return out, nil
}

// cleanLine strips list markers and quotes: `1. "Hello!"` -> `Hello!`.
func cleanLine(line string) string {
	s := strings.TrimSpace(line)
	s = strings.TrimLeft(s, "-*•# ")
	// Numbering such as "1." or "2)".
	if i := strings.IndexAny(s, ".)"); i > 0 && i <= 2 && isDigits(s[:i]) {
		s = strings.TrimSpace(s[i+1:])
	}
	s = strings.Trim(s, `"'`+"`")
	return strings.TrimSpace(s)
}

func isDigits(s string) bool {
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return s != ""
}

// printableASCII reports whether every character can be drawn by the
// banners (space through '~'), so a suggestion never breaks /symbol-art.
func printableASCII(s string) bool {
	for _, r := range s {
		if r < ascii.FirstRune || r > ascii.LastRune {
			return false
		}
	}
	return true
}

// truncate shortens s to at most max characters, ending with "...". It
// counts runes, not bytes, so a multi-byte character is never cut in half.
func truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return strings.TrimSpace(string(runes[:max-3])) + "..."
}
