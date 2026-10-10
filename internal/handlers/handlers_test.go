package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"symbol-web/internal/ai"
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

// newTestServer builds the full router with the given directories and
// the LLM assistant in mock mode.
func newTestServer(bannerDir, tmplDir string) http.Handler {
	return newServerWithAssistant(bannerDir, tmplDir, ai.NewMock())
}

func newServerWithAssistant(bannerDir, tmplDir string, assistant Assistant) http.Handler {
	generator := ascii.NewGenerator(bannerDir)
	return New(Config{
		Generator:   generator,
		Recommender: ai.NewRecommender(generator),
		Assistant:   assistant,
		TemplateDir: tmplDir,
		StaticDir:   staticDir,
	}).Routes()
}

// fakeAssistant is an Assistant that always fails with err, to test how
// the handlers react to LLM problems without any network.
type fakeAssistant struct{ err error }

func (f fakeAssistant) GetSuggestions(context.Context, string) ([]string, error) {
	return nil, f.err
}

func (f fakeAssistant) GetVariations(context.Context, string) ([]ai.Variation, error) {
	return nil, f.err
}

func (f fakeAssistant) Mode() string { return "fake" }

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

// A panic after the response has started cannot turn it into a 500: the
// partial response is kept and no second error page is appended.
func TestPanicAfterWriteKeepsResponse(t *testing.T) {
	h := New(Config{TemplateDir: templateDir})
	panicky := h.recoverPanics(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "partial")
		panic("boom")
	}))
	code, body := do(t, panicky, httptest.NewRequest(http.MethodGet, "/", nil))
	if code != http.StatusOK || body != "partial" {
		t.Errorf("got %d %q, want 200 \"partial\"", code, body)
	}
}

// http.ErrAbortHandler is net/http's way to abort a response on purpose;
// it must reach the server instead of becoming a 500 page.
func TestAbortHandlerPanicIsNotRecovered(t *testing.T) {
	h := New(Config{TemplateDir: templateDir})
	aborting := h.recoverPanics(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}))
	defer func() {
		if v := recover(); v != http.ErrAbortHandler {
			t.Errorf("recovered %v, want http.ErrAbortHandler to be re-panicked", v)
		}
	}()
	aborting.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
}

// Templates are cached, but an edited or deleted template is noticed on
// the next request without restarting the server.
func TestTemplateCacheFollowsFileChanges(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"index.html", "result.html", "error.html"} {
		data, err := os.ReadFile(filepath.Join(templateDir, name))
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, dir, name, string(data))
	}
	srv := newTestServer(projectRoot, dir)
	get := func() (int, string) { return do(t, srv, httptest.NewRequest(http.MethodGet, "/", nil)) }

	if code, _ := get(); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}

	writeFile(t, dir, "index.html", `<p>edited</p>{{template "result" .}}`)
	later := time.Now().Add(time.Hour) // a clearly newer modification time
	os.Chtimes(filepath.Join(dir, "index.html"), later, later)
	if code, body := get(); code != http.StatusOK || !strings.Contains(body, "edited") {
		t.Errorf("edited template not picked up: %d %q", code, body)
	}

	os.Remove(filepath.Join(dir, "index.html"))
	if code, _ := get(); code != http.StatusNotFound {
		t.Errorf("deleted template: status = %d, want 404", code)
	}
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func postJSON(path, body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestRecommendBannerEndpoint(t *testing.T) {
	h := newTestServer(projectRoot, templateDir)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, postJSON("/api/recommend-banner", `{"text": "WELCOME"}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var got ai.Recommendation
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if got.Recommended != ascii.Shadow || got.Reasoning == "" || len(got.Alternatives) != 2 {
		t.Errorf("unexpected recommendation: %+v", got)
	}
}

func TestAPIErrors(t *testing.T) {
	tests := []struct {
		name string
		req  *http.Request
		want int
	}{
		{"empty text", postJSON("/api/recommend-banner", `{"text": ""}`), http.StatusBadRequest},
		{"missing text field", postJSON("/api/recommend-banner", `{}`), http.StatusBadRequest},
		{"too long text", postJSON("/api/recommend-banner", `{"text": "`+strings.Repeat("a", 1001)+`"}`), http.StatusBadRequest},
		{"invalid character", postJSON("/api/recommend-banner", `{"text": "Привет"}`), http.StatusBadRequest},
		{"malformed JSON", postJSON("/api/recommend-banner", `{"text": `), http.StatusBadRequest},
		{"body too large", postJSON("/api/recommend-banner", `{"text": "`+strings.Repeat("a", 70<<10)+`"}`), http.StatusBadRequest},
		{"wrong method", httptest.NewRequest(http.MethodGet, "/api/recommend-banner", nil), http.StatusMethodNotAllowed},
		{"unknown endpoint", postJSON("/api/nope", `{"text": "hi"}`), http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			newTestServer(projectRoot, templateDir).ServeHTTP(rec, tt.req)

			if rec.Code != tt.want {
				t.Errorf("status = %d, want %d", rec.Code, tt.want)
			}
			// API errors are JSON with a non-empty "error" message, never HTML.
			var body struct {
				Error string `json:"error"`
			}
			if err := json.NewDecoder(rec.Body).Decode(&body); err != nil || body.Error == "" {
				t.Errorf("want JSON {\"error\": ...}, decode err = %v, body = %+v", err, body)
			}
		})
	}
}

func TestSuggestMockMode(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestServer(projectRoot, templateDir).ServeHTTP(rec, postJSON("/api/suggest", `{"text": "Happy Birth"}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if mode := rec.Header().Get("X-LLM-Mode"); mode != "mock" {
		t.Errorf("X-LLM-Mode = %q, want mock", mode)
	}
	var body struct {
		Suggestions []string `json:"suggestions"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if n := len(body.Suggestions); n < 3 || n > 5 {
		t.Errorf("got %d suggestions, want 3-5: %q", n, body.Suggestions)
	}
}

func TestVariationsMockMode(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestServer(projectRoot, templateDir).ServeHTTP(rec, postJSON("/api/variations", `{"text": "hello"}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		Variations []ai.Variation `json:"variations"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if n := len(body.Variations); n < 3 || n > 5 {
		t.Fatalf("got %d variations, want 3-5", n)
	}
	for _, v := range body.Variations {
		if v.Text == "" || v.Description == "" || !ascii.IsValidBanner(v.SuggestedBanner) {
			t.Errorf("incomplete variation %+v", v)
		}
	}
}

// Failures of a live LLM become JSON errors with the right status code,
// and never crash the server.
func TestAIEndpointErrors(t *testing.T) {
	down := fakeAssistant{fmt.Errorf("%w: connection refused", ai.ErrUnavailable)}
	garbage := fakeAssistant{fmt.Errorf("%w: not JSON", ai.ErrInvalidResponse)}
	mock := ai.NewMock()

	tests := []struct {
		name      string
		assistant Assistant
		req       *http.Request
		want      int
	}{
		{"suggest: LLM down", down, postJSON("/api/suggest", `{"text": "Hello"}`), http.StatusServiceUnavailable},
		{"variations: LLM down", down, postJSON("/api/variations", `{"text": "Hello"}`), http.StatusServiceUnavailable},
		{"suggest: invalid LLM answer", garbage, postJSON("/api/suggest", `{"text": "Hello"}`), http.StatusInternalServerError},
		{"variations: invalid LLM answer", garbage, postJSON("/api/variations", `{"text": "Hello"}`), http.StatusInternalServerError},
		{"suggest: text too short", mock, postJSON("/api/suggest", `{"text": "Hi"}`), http.StatusBadRequest},
		{"suggest: empty text", mock, postJSON("/api/suggest", `{"text": "   "}`), http.StatusBadRequest},
		{"suggest: invalid character", mock, postJSON("/api/suggest", `{"text": "Привет"}`), http.StatusBadRequest},
		{"variations: wrong method", mock, httptest.NewRequest(http.MethodGet, "/api/variations", nil), http.StatusMethodNotAllowed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			newServerWithAssistant(projectRoot, templateDir, tt.assistant).ServeHTTP(rec, tt.req)

			if rec.Code != tt.want {
				t.Errorf("status = %d, want %d", rec.Code, tt.want)
			}
			var body struct {
				Error string `json:"error"`
			}
			if err := json.NewDecoder(rec.Body).Decode(&body); err != nil || body.Error == "" {
				t.Errorf("want a JSON error message, got err=%v body=%+v", err, body)
			}
		})
	}
}
