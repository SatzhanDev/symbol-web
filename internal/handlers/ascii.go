package handlers

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"

	"symbol-web/internal/ascii"
)

// maxFormBytes caps the size of a POSTed form. 1000 characters of text
// plus the banner name fit easily; anything bigger is rejected.
const maxFormBytes = 64 << 10 // 64 KB

// pageData is what index.html (and its result.html block) receives.
type pageData struct {
	Text    string
	Banner  string
	Banners []string
	Art     string
	Error   string
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

	h.renderPage(w, http.StatusOK, pageData{Banner: ascii.DefaultBanner})
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
			Banner: ascii.DefaultBanner,
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
				data.Banner = ascii.DefaultBanner
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

// classifyGenerateError maps an error from the ascii package to an HTTP
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

// renderPage executes index.html (with its result.html block) with data.
// The output goes into a buffer first, so a failure halfway through never
// sends a half-written page with status 200.
func (h *Handler) renderPage(w http.ResponseWriter, status int, data pageData) {
	data.Banners = ascii.Banners()

	tmpl, err := h.templates.get("index.html", "result.html")
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
