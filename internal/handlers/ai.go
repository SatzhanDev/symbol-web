package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	"symbol-web/internal/ai"
	"symbol-web/internal/ascii"
)

// maxJSONBytes caps the size of a JSON request body.
const maxJSONBytes = 64 << 10 // 64 KB

// textRequest is the JSON body every AI endpoint accepts: {"text": "..."}.
type textRequest struct {
	Text string `json:"text"`
}

// errorResponse is the JSON body of every API error: {"error": "..."}.
type errorResponse struct {
	Error string `json:"error"`
}

// recommendBanner serves POST /api/recommend-banner. It is rule-based:
// no LLM, no API key, no network request, so it always works.
func (h *Handler) recommendBanner(w http.ResponseWriter, r *http.Request) {
	text, ok := readTextRequest(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, h.cfg.Recommender.Recommend(text))
}

// suggestResponse is the body of a successful /api/suggest response.
type suggestResponse struct {
	Suggestions []string `json:"suggestions"`
}

// variationsResponse is the body of a successful /api/variations response.
type variationsResponse struct {
	Variations []ai.Variation `json:"variations"`
}

// suggest serves POST /api/suggest: 3-5 LLM text completions (or canned
// ones in mock mode) for text of at least 3 characters.
func (h *Handler) suggest(w http.ResponseWriter, r *http.Request) {
	text, ok := readTextRequest(w, r)
	if !ok {
		return
	}
	if len(strings.TrimSpace(text)) < ai.MinSuggestInput {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("Type at least %d characters to get suggestions.", ai.MinSuggestInput))
		return
	}

	suggestions, err := h.cfg.Assistant.GetSuggestions(r.Context(), text)
	if err != nil {
		h.writeAIError(w, "suggest", err)
		return
	}
	w.Header().Set("X-LLM-Mode", h.cfg.Assistant.Mode())
	writeJSON(w, http.StatusOK, suggestResponse{Suggestions: suggestions})
}

// variations serves POST /api/variations (bonus): 3-5 creative rewrites
// of the text, each with a description and a suggested banner.
func (h *Handler) variations(w http.ResponseWriter, r *http.Request) {
	text, ok := readTextRequest(w, r)
	if !ok {
		return
	}

	variations, err := h.cfg.Assistant.GetVariations(r.Context(), text)
	if err != nil {
		h.writeAIError(w, "variations", err)
		return
	}
	w.Header().Set("X-LLM-Mode", h.cfg.Assistant.Mode())
	writeJSON(w, http.StatusOK, variationsResponse{Variations: variations})
}

// writeAIError logs the real cause of an LLM failure and sends the user a
// friendly message: 503 if the AI service is unreachable or busy (worth
// retrying), 500 for anything else.
func (h *Handler) writeAIError(w http.ResponseWriter, endpoint string, err error) {
	log.Printf("%s: %v", endpoint, err)
	if errors.Is(err, ai.ErrUnavailable) {
		writeJSONError(w, http.StatusServiceUnavailable, "The AI service is temporarily unavailable. Please try again in a moment.")
		return
	}
	writeJSONError(w, http.StatusInternalServerError, "The AI service returned an unexpected answer. Please try again.")
}

// apiNotFound answers unknown /api/... paths with a JSON 404, so API
// clients never receive an HTML page.
func (h *Handler) apiNotFound(w http.ResponseWriter, r *http.Request) {
	writeJSONError(w, http.StatusNotFound, "Unknown API endpoint.")
}

// readTextRequest checks the method, decodes {"text": "..."} and validates
// the text with the same rules as the ASCII generator. On failure it
// writes the JSON error response itself and returns ok == false.
func readTextRequest(w http.ResponseWriter, r *http.Request) (text string, ok bool) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSONError(w, http.StatusMethodNotAllowed, "Use POST for this endpoint.")
		return "", false
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBytes)
	var req textRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeJSONError(w, http.StatusBadRequest, "Request body is too large.")
		} else {
			writeJSONError(w, http.StatusBadRequest, `Request body must be JSON like {"text": "Hello"}.`)
		}
		return "", false
	}

	text = ascii.NormalizeNewlines(req.Text)
	if err := ascii.ValidateText(text); err != nil {
		status, msg := classifyGenerateError(err)
		writeJSONError(w, status, msg)
		return "", false
	}
	return text, true
}

// writeJSON sends v as a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		log.Printf("json: %v", err)
		status = http.StatusInternalServerError
		body = []byte(`{"error":"Internal server error."}`)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	w.Write(body)
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, errorResponse{Error: message})
}
