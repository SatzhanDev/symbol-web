// Package handlers contains the HTTP layer of symbol-web: routing,
// middleware and error pages (this file), the HTML pages (ascii.go) and
// the JSON AI endpoints (ai.go).
package handlers

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"symbol-web/internal/ai"
)

// The handlers depend on these small interfaces rather than on concrete
// types, so main can plug in any implementation (for example the LLM
// client or its mock) and tests can use simple fakes.

// ArtGenerator renders text as ASCII art. *ascii.Generator implements it.
type ArtGenerator interface {
	Generate(text, banner string) (string, error)
}

// BannerRecommender recommends a banner for a text. *ai.Recommender implements it.
type BannerRecommender interface {
	Recommend(text string) ai.Recommendation
}

// Assistant gives LLM-based ideas for a text. *ai.Client implements it,
// and *ai.Mock does in mock mode.
type Assistant interface {
	GetSuggestions(ctx context.Context, text string) ([]string, error)
	GetVariations(ctx context.Context, text string) ([]ai.Variation, error)
	Mode() string // "live" or "mock"
}

// Config holds everything the handlers depend on.
type Config struct {
	Generator   ArtGenerator
	Recommender BannerRecommender
	Assistant   Assistant
	TemplateDir string
	StaticDir   string
}

// Handler serves every route of the application.
type Handler struct {
	cfg       Config
	templates *templateCache
}

// New returns a Handler built from cfg.
func New(cfg Config) *Handler {
	return &Handler{cfg: cfg, templates: newTemplateCache(cfg.TemplateDir)}
}

// Routes registers every route and wraps them with the middleware.
func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()

	// HTML pages (ascii.go)
	mux.HandleFunc("/", h.home)
	mux.HandleFunc("/symbol-art", h.symbolArt)

	// JSON API (ai.go)
	mux.HandleFunc("/api/recommend-banner", h.recommendBanner)
	mux.HandleFunc("/api/suggest", h.suggest)
	mux.HandleFunc("/api/variations", h.variations)
	mux.HandleFunc("/api/", h.apiNotFound)

	// CSS and JavaScript
	static := http.StripPrefix("/static/", http.FileServer(http.Dir(h.cfg.StaticDir)))
	mux.Handle("/static/", noDirListing(static))

	return h.logRequests(h.recoverPanics(mux))
}

// ---------------------------------------------------------------------
// Error pages
// ---------------------------------------------------------------------

// errorData is what error.html receives.
type errorData struct {
	Status     int
	StatusText string
	Message    string
}

// renderError shows error.html. If that template itself is missing or
// broken, it falls back to a plain-text error so the user always gets an
// answer with the right status code.
func (h *Handler) renderError(w http.ResponseWriter, status int, message string) {
	data := errorData{Status: status, StatusText: http.StatusText(status), Message: message}

	var buf bytes.Buffer
	tmpl, err := h.templates.get("error.html")
	if err == nil {
		err = tmpl.Execute(&buf, data)
	}
	if err != nil {
		log.Printf("error template: %v", err)
		http.Error(w, fmt.Sprintf("%d %s: %s", status, data.StatusText, message), status)
		return
	}

	writeHTML(w, status, &buf)
}

func (h *Handler) methodNotAllowed(w http.ResponseWriter, allowed string) {
	w.Header().Set("Allow", allowed)
	h.renderError(w, http.StatusMethodNotAllowed, "This address does not accept that kind of request.")
}

func writeHTML(w http.ResponseWriter, status int, buf *bytes.Buffer) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	// A write error here means the client has gone away; the status is
	// already sent, so there is nobody left to report it to.
	io.Copy(w, buf)
}

// ---------------------------------------------------------------------
// Middleware
// ---------------------------------------------------------------------

// noDirListing hides the file server's automatic directory listings, so
// /static/css/ answers 404 instead of showing the folder contents.
func noDirListing(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// statusRecorder wraps a ResponseWriter and remembers the status code and
// whether the response has started (headers are sent with the first
// WriteHeader or Write call).
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func newStatusRecorder(w http.ResponseWriter) *statusRecorder {
	return &statusRecorder{ResponseWriter: w, status: http.StatusOK}
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wroteHeader {
		s.status = code
		s.wroteHeader = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	s.wroteHeader = true // an implicit 200 is sent with the first write
	return s.ResponseWriter.Write(b)
}

// logRequests is middleware that logs method, path, status and duration.
func (h *Handler) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := newStatusRecorder(w)
		next.ServeHTTP(rec, r)
		log.Printf("%s %s %d %s", r.Method, r.URL.Path, rec.status, time.Since(start).Round(time.Microsecond))
	})
}

// recoverPanics is middleware that turns a panic in any handler into a
// 500 response instead of a dropped connection.
func (h *Handler) recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := newStatusRecorder(w)
		defer func() {
			v := recover()
			if v == nil {
				return
			}
			if v == http.ErrAbortHandler {
				// net/http's own way to abort a response on purpose:
				// let the server handle it as intended.
				panic(v)
			}
			log.Printf("panic: %v\n%s", v, debug.Stack())
			if rec.wroteHeader {
				return // the response has already started; it cannot become a 500 now
			}
			h.renderError(rec, http.StatusInternalServerError, "Unexpected server error.")
		}()
		next.ServeHTTP(rec, r)
	})
}
