// Package handlers contains the HTTP handlers of symbol-web: the HTML
// pages (this file) and the JSON AI endpoints.
package handlers

import (
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"symbol-web/internal/ai"
	"symbol-web/internal/ascii"
)

// maxFormBytes caps the size of a POSTed form. 1000 characters of text
// plus the banner name fit easily; anything bigger is rejected.
const maxFormBytes = 64 << 10 // 64 KB

const defaultBanner = "standard"

// Config holds everything the handlers depend on.
type Config struct {
	Generator   *ascii.Generator
	Recommender *ai.Recommender
	TemplateDir string
	StaticDir   string
}

// Handler serves every route of the application.
type Handler struct {
	cfg Config
}

// New returns a Handler built from cfg.
func New(cfg Config) *Handler {
	return &Handler{cfg: cfg}
}

// pageData is what index.html (and its result.html block) receives.
type pageData struct {
	Text    string
	Banner  string
	Banners []string
	Art     string
	Error   string
}

// errorData is what error.html receives.
type errorData struct {
	Status     int
	StatusText string
	Message    string
}

// Routes registers every route and wraps them with the middleware.
func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", h.home)
	mux.HandleFunc("/symbol-art", h.symbolArt)
	mux.HandleFunc("/api/recommend-banner", h.recommendBanner)
	mux.HandleFunc("/api/", h.apiNotFound)

	static := http.StripPrefix("/static/", http.FileServer(http.Dir(h.cfg.StaticDir)))
	mux.Handle("/static/", noDirListing(static))

	return h.logRequests(h.recoverPanics(mux))
}

// home serves GET / : the main page with an empty form.
func (h *Handler) home(w http.ResponseWriter, r *http.Request) {
	// "/" matches every path no other route claimed, so reject the rest.
	if r.URL.Path != "/" {
		h.renderError(w, http.StatusNotFound, "The page you are looking for does not exist.")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		h.methodNotAllowed(w, "GET, HEAD")
		return
	}

	h.renderPage(w, http.StatusOK, pageData{Banner: defaultBanner})
}

// symbolArt serves POST /symbol-art : it reads the form, generates the
// ASCII art and shows it on the main page below the form.
func (h *Handler) symbolArt(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.methodNotAllowed(w, "POST")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		h.renderPage(w, http.StatusBadRequest, pageData{
			Banner: defaultBanner,
			Error:  "The form could not be read (is the text too large?).",
		})
		return
	}

	data := pageData{
		Text:   r.PostFormValue("text"),
		Banner: r.PostFormValue("banner"),
	}

	art, err := h.cfg.Generator.Generate(data.Text, data.Banner)
	if err != nil {
		status, msg := classifyGenerateError(err)
		if status == http.StatusBadRequest {
			// The user can fix this: show the message next to their input.
			if !ascii.IsValidBanner(data.Banner) {
				data.Banner = defaultBanner
			}
			data.Error = msg
			h.renderPage(w, status, data)
			return
		}
		log.Printf("symbol-art: %v", err)
		h.renderError(w, status, msg)
		return
	}

	data.Art = art
	h.renderPage(w, http.StatusOK, data)
}

// classifyGenerateError maps an error from ascii.Generate to an HTTP
// status code and a message that is safe to show to the user.
func classifyGenerateError(err error) (int, string) {
	switch {
	case errors.Is(err, ascii.ErrEmptyText):
		return http.StatusBadRequest, "Please enter some text."
	case errors.Is(err, ascii.ErrTextTooLong):
		return http.StatusBadRequest, fmt.Sprintf("Text is too long: the limit is %d characters.", ascii.MaxTextLength)
	case errors.Is(err, ascii.ErrInvalidChar):
		return http.StatusBadRequest, "Only English letters, digits, spaces and standard symbols are supported (" + err.Error() + ")."
	case errors.Is(err, ascii.ErrInvalidBanner):
		return http.StatusBadRequest, "Please choose one of the available banners."
	case errors.Is(err, ascii.ErrBannerNotFound):
		return http.StatusNotFound, "The selected banner file is missing on the server."
	default:
		return http.StatusInternalServerError, "Something went wrong while generating the ASCII art."
	}
}

// renderPage executes index.html (+ result.html) with data.
//
// Templates are parsed on every request: slightly slower, but a missing
// or edited template file is noticed immediately (404 / 500) without a
// server restart. The output goes into a buffer first, so a failure
// halfway through never sends a half-written page with status 200.
func (h *Handler) renderPage(w http.ResponseWriter, status int, data pageData) {
	data.Banners = ascii.Banners

	tmpl, err := template.ParseFiles(
		filepath.Join(h.cfg.TemplateDir, "index.html"),
		filepath.Join(h.cfg.TemplateDir, "result.html"),
	)
	if err != nil {
		log.Printf("template: %v", err)
		if errors.Is(err, fs.ErrNotExist) {
			h.renderError(w, http.StatusNotFound, "The page template was not found.")
		} else {
			h.renderError(w, http.StatusInternalServerError, "The page template is broken.")
		}
		return
	}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "index.html", data); err != nil {
		log.Printf("template: %v", err)
		h.renderError(w, http.StatusInternalServerError, "The page could not be displayed.")
		return
	}

	writeHTML(w, status, &buf)
}

// renderError shows error.html. If that template itself is missing or
// broken, it falls back to a plain-text error so the user always gets an
// answer with the right status code.
func (h *Handler) renderError(w http.ResponseWriter, status int, message string) {
	data := errorData{Status: status, StatusText: http.StatusText(status), Message: message}

	var buf bytes.Buffer
	tmpl, err := template.ParseFiles(filepath.Join(h.cfg.TemplateDir, "error.html"))
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
	buf.WriteTo(w)
}

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

// statusRecorder remembers the status code a handler wrote, for logging.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// logRequests is middleware that logs method, path, status and duration.
func (h *Handler) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		log.Printf("%s %s %d %s", r.Method, r.URL.Path, rec.status, time.Since(start).Round(time.Microsecond))
	})
}

// recoverPanics is middleware that turns a panic in any handler into a
// 500 response instead of a dropped connection.
func (h *Handler) recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				log.Printf("panic: %v", v)
				h.renderError(w, http.StatusInternalServerError, "Unexpected server error.")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
