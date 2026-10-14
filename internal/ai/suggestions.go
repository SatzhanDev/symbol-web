package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"symbol-web/internal/ascii"
)

// Limits for what the LLM may return.
const (
	maxAttempts = 2 // an unusable answer is asked once more

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
//
// The last two lines of suggestPrompt are ours: without them models tend
// to return only the continuation ("World" for "Hello w"), while the task
// expects the whole text ("Happy Birthday!" for "Happy Birth").
const suggestPrompt = `Complete this text creatively for ASCII art display:
Input: %q
Provide 3-5 short, creative completions suitable for ASCII art.
Each completion should be under 50 characters.
Return only the completions, one per line.
Each line must be the whole text: the input followed by its continuation.
For example, for the input "Happy Birth" a line could be: Happy Birthday!`

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

	suggestions, err := askLLM(ctx, c, fmt.Sprintf(suggestPrompt, text), func(answer string) ([]string, error) {
		return parseSuggestions(text, answer)
	})
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

	variations, err := askLLM(ctx, c, fmt.Sprintf(variationsPrompt, text), parseVariations)
	if err != nil {
		return nil, err
	}

	c.cache.set(key, variations)
	return variations, nil
}

// askLLM sends prompt to the LLM and parses the answer with parse.
//
// LLM answers vary from call to call, so when the model answers but not in
// a usable form (ErrInvalidResponse), it is asked once more. All attempts
// share one timeout (10 seconds by default), so a retry never makes the
// user wait longer than the task allows. Network errors are not retried:
// a server that is down will most likely still be down.
func askLLM[T any](ctx context.Context, c *Client, prompt string, parse func(answer string) (T, error)) (T, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	var result T
	var err error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		var answer string
		answer, err = c.complete(ctx, prompt)
		if err != nil {
			return result, err
		}
		result, err = parse(answer)
		if !errors.Is(err, ErrInvalidResponse) {
			break
		}
	}
	return result, err
}

// ---------------------------------------------------------------------
// Parsing and validating LLM answers
// ---------------------------------------------------------------------

// parseSuggestions turns the LLM's "one completion per line" answer into
// a clean list of full texts for input. LLMs often add numbering, bullets,
// quotes or a chatty intro line, so those are removed. Every line is then
// joined with the input (see joinCompletion); lines that do not continue
// the input, add more than maxSuggestionLen characters or could not be
// drawn as ASCII art are skipped.
func parseSuggestions(input, answer string) ([]string, error) {
	var out []string
	seen := map[string]bool{}

	for _, line := range strings.Split(answer, "\n") {
		s := cleanLine(line)
		if s == "" || strings.HasSuffix(s, ":") || !printableASCII(s) { // ":" -> "Here are some ideas:"
			continue
		}
		full, ok := joinCompletion(input, s)
		if !ok || ascii.ValidateText(full) != nil || seen[strings.ToLower(full)] {
			continue
		}
		seen[strings.ToLower(full)] = true
		out = append(out, full)
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

// joinCompletion turns one LLM line into the full suggested text for
// input, and reports false if the line does not continue the input.
// The characters the user typed are always kept as typed.
//
//	input "Happy Birth", line "Happy Birthday!" -> "Happy Birthday!" (whole text)
//	input "Hello w",     line "World"           -> "Hello world"     (finishes the last word)
//	input "Hello ",      line "Kitty"           -> "Hello Kitty"     (continues after a space)
//	input "Hello w",     line "Smiling Face"    -> false             (not a continuation)
//
// completion must be ASCII (the caller checks), so byte indexes are safe.
func joinCompletion(input, completion string) (string, bool) {
	typed := strings.TrimRight(input, " ")
	var full string

	switch lastWord := typed[strings.LastIndexAny(typed, " \n")+1:]; {
	case hasPrefixFold(completion, typed):
		full = typed + completion[len(typed):]
	case lastWord != "" && typed == input && hasPrefixFold(completion, lastWord):
		full = typed + completion[len(lastWord):]
	case typed != input: // the input ends with a space: the line is the next words
		full = input + completion
	default:
		return "", false
	}

	added := len(full) - len(typed)
	if added <= 0 || added > maxSuggestionLen {
		return "", false // nothing new, or more than the task's 50 characters
	}
	return full, true
}

func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
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
