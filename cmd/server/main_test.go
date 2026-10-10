package main

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"testing"

	"symbol-web/internal/ai"
)

func TestMain(m *testing.M) {
	log.SetOutput(io.Discard)
	os.Exit(m.Run())
}

// writeEnv writes content to a temporary .env file and makes sure the
// variables it may set are removed again after the test.
func writeEnv(t *testing.T, content string, keys ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, k := range keys {
		os.Unsetenv(k)
		t.Cleanup(func() { os.Unsetenv(k) })
	}
	return path
}

func TestLoadDotEnv(t *testing.T) {
	path := writeEnv(t, `
# a comment, then a blank line

SW_PLAIN=hello
SW_QUOTED="with spaces"
SW_SINGLE='single'
export SW_EXPORTED=yes
SW_EQUALS=a=b=c
SW_EMPTY=
`, "SW_PLAIN", "SW_QUOTED", "SW_SINGLE", "SW_EXPORTED", "SW_EQUALS", "SW_EMPTY")

	if err := loadDotEnv(path); err != nil {
		t.Fatalf("loadDotEnv: %v", err)
	}

	want := map[string]string{
		"SW_PLAIN":    "hello",
		"SW_QUOTED":   "with spaces",
		"SW_SINGLE":   "single",
		"SW_EXPORTED": "yes",
		"SW_EQUALS":   "a=b=c", // only the first "=" separates key and value
		"SW_EMPTY":    "",
	}
	for k, v := range want {
		got, ok := os.LookupEnv(k)
		if !ok || got != v {
			t.Errorf("%s = %q (set: %v), want %q", k, got, ok, v)
		}
	}
}

// A variable that is already set in the shell wins over the file.
func TestLoadDotEnvKeepsExistingVariables(t *testing.T) {
	path := writeEnv(t, "SW_PORT=9999\n")
	t.Setenv("SW_PORT", "8080")

	if err := loadDotEnv(path); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("SW_PORT"); got != "8080" {
		t.Errorf("SW_PORT = %q, want the shell value 8080", got)
	}
}

func TestLoadDotEnvMissingFileIsFine(t *testing.T) {
	if err := loadDotEnv(filepath.Join(t.TempDir(), "nope.env")); err != nil {
		t.Errorf("a missing .env file should not be an error, got %v", err)
	}
}

func TestLoadDotEnvRejectsBadLine(t *testing.T) {
	path := writeEnv(t, "SW_OK=1\nthis line has no equals sign\n", "SW_OK")
	if err := loadDotEnv(path); err == nil {
		t.Error("expected an error for a line without '='")
	}
}

func TestNewAssistant(t *testing.T) {
	a, err := newAssistant(ai.Config{})
	if err != nil || a.Mode() != "mock" {
		t.Errorf("no base URL: got mode %v, err %v; want mock", a, err)
	}

	a, err = newAssistant(ai.Config{BaseURL: "http://localhost:11434", Model: "llama3.2"})
	if err != nil || a.Mode() != "live" {
		t.Errorf("base URL and model: got %v, err %v; want live", a, err)
	}

	if _, err := newAssistant(ai.Config{BaseURL: "http://localhost:11434"}); err == nil {
		t.Error("a base URL without a model should be rejected")
	}
}
