package handlers

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"symbol-web/internal/ascii"
)

// Paths to the real project files, relative to this package.
const (
	projectRoot = "../.."
	templateDir = "../../templates"
	staticDir   = "../../static"
)

func TestMain(m *testing.M) {
	log.SetOutput(io.Discard) // keep test output readable
	os.Exit(m.Run())
}

// newTestServer builds the full router with the given directories.
func newTestServer(bannerDir, tmplDir string) http.Handler {
	return New(Config{
		Generator:   ascii.NewGenerator(bannerDir),
		TemplateDir: tmplDir,
		StaticDir:   staticDir,
	}).Routes()
}

func do(t *testing.T, h http.Handler, req *http.Request) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	body, _ := io.ReadAll(rec.Body)
	return rec.Code, string(body)
}

func postForm(text, banner string) *http.Request {
	form := url.Values{"text": {text}, "banner": {banner}}
	req := httptest.NewRequest(http.MethodPost, "/symbol-art", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

func TestHomePage(t *testing.T) {
	code, body := do(t, newTestServer(projectRoot, templateDir), httptest.NewRequest(http.MethodGet, "/", nil))
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	for _, want := range []string{`action="/symbol-art"`, `id="text-input"`, `value="shadow"`, `value="standard"`, `value="thinkertoy"`} {
		if !strings.Contains(body, want) {
			t.Errorf("home page is missing %q", want)
		}
	}
}

func TestGenerateArt(t *testing.T) {
	code, body := do(t, newTestServer(projectRoot, templateDir), postForm("Hi", "standard"))
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	// Second row of "Hi" in the standard banner.
	if !strings.Contains(body, "| |  | | (_) ") {
		t.Error("response does not contain the rendered ASCII art")
	}
	// The form keeps the chosen banner selected.
	if !strings.Contains(body, `value="standard" checked`) {
		t.Error("chosen banner is not kept selected")
	}
}

func TestUserInputIsEscaped(t *testing.T) {
	_, body := do(t, newTestServer(projectRoot, templateDir), postForm("<script>", "standard"))
	if strings.Contains(body, "<script>") {
		t.Error("user text must be HTML-escaped, found a raw <script> tag")
	}
}

func TestStatusCodes(t *testing.T) {
	missingTemplates := t.TempDir()

	brokenTemplates := t.TempDir()
	writeFile(t, brokenTemplates, "index.html", `{{.NoSuchField}}`)
	writeFile(t, brokenTemplates, "result.html", `{{define "result"}}{{end}}`)

	tests := []struct {
		name      string
		bannerDir string
		tmplDir   string
		req       *http.Request
		want      int
	}{
		{"empty text", projectRoot, templateDir, postForm("", "standard"), http.StatusBadRequest},
		{"too long text", projectRoot, templateDir, postForm(strings.Repeat("a", 1001), "standard"), http.StatusBadRequest},
		{"invalid character", projectRoot, templateDir, postForm("Привет", "standard"), http.StatusBadRequest},
		{"invalid banner", projectRoot, templateDir, postForm("hi", "comic"), http.StatusBadRequest},
		{"banner file missing", t.TempDir(), templateDir, postForm("hi", "standard"), http.StatusNotFound},
		{"template missing", projectRoot, missingTemplates, httptest.NewRequest(http.MethodGet, "/", nil), http.StatusNotFound},
		{"template execution fails", projectRoot, brokenTemplates, httptest.NewRequest(http.MethodGet, "/", nil), http.StatusInternalServerError},
		{"unknown path", projectRoot, templateDir, httptest.NewRequest(http.MethodGet, "/nope", nil), http.StatusNotFound},
		{"GET /symbol-art", projectRoot, templateDir, httptest.NewRequest(http.MethodGet, "/symbol-art", nil), http.StatusMethodNotAllowed},
		{"POST /", projectRoot, templateDir, httptest.NewRequest(http.MethodPost, "/", nil), http.StatusMethodNotAllowed},
		{"static css", projectRoot, templateDir, httptest.NewRequest(http.MethodGet, "/static/css/style.css", nil), http.StatusOK},
		{"static dir listing", projectRoot, templateDir, httptest.NewRequest(http.MethodGet, "/static/css/", nil), http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, _ := do(t, newTestServer(tt.bannerDir, tt.tmplDir), tt.req)
			if code != tt.want {
				t.Errorf("status = %d, want %d", code, tt.want)
			}
		})
	}
}

func TestPanicBecomes500(t *testing.T) {
	h := New(Config{TemplateDir: templateDir})
	panicky := h.recoverPanics(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))
	code, _ := do(t, panicky, httptest.NewRequest(http.MethodGet, "/", nil))
	if code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", code)
	}
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
