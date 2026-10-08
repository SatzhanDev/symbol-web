package handlers

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

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
			writeJSONError(w, http.StatusRequestEntityTooLarge, "Request body is too large.")
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
		http.Error(w, `{"error":"Internal server error."}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	w.Write(body)
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, errorResponse{Error: message})
}
