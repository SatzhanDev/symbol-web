// Command server starts the symbol-web HTTP server: a web GUI for
// symbol-art. Run it from the project root so it can find the banner
// files, templates/ and static/.
package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"symbol-web/internal/ai"
	"symbol-web/internal/ascii"
	"symbol-web/internal/handlers"
)

const (
	defaultPort = "8080"
	// shutdownTimeout is how long in-flight requests get to finish after
	// Ctrl+C. It is longer than the 10-second LLM timeout, so a pending AI
	// request can still complete.
	shutdownTimeout = 15 * time.Second
)

func main() {
	if err := loadDotEnv(".env"); err != nil {
		log.Fatalf(".env: %v", err)
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = defaultPort
	}

	// One generator for the whole server: its font cache is shared by the
	// ASCII art endpoint and the banner recommender.
	generator := ascii.NewGenerator(".")
	logBannerProfiles(generator)

	assistant, err := newAssistant(ai.ConfigFromEnv())
	if err != nil {
		log.Fatalf("LLM config: %v", err)
	}

	h := handlers.New(handlers.Config{
		Generator:   generator,
		Recommender: ai.NewRecommender(generator),
		Assistant:   assistant,
		TemplateDir: "templates",
		StaticDir:   "static",
	})

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           h.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
		// Must stay above the 10-second LLM timeout used by the AI endpoints.
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// ctx is cancelled when the process receives Ctrl+C (SIGINT) or SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Take the port first, so "Server running" is printed only when the
	// server can really accept connections (and a busy port fails here).
	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		log.Fatalf("server: %v", err)
	}
	log.Printf("Server running on http://localhost:%s", port)

	// Serve blocks, so it runs in its own goroutine and reports back
	// through serverErr if it stops on its own.
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- srv.Serve(ln)
	}()

	select {
	case err := <-serverErr:
		// The server stopped without us asking it to.
		log.Fatalf("server: %v", err)
	case <-ctx.Done():
	}

	stop() // a second Ctrl+C now kills the process immediately
	log.Println("Shutting down, waiting for in-flight requests...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: %v", err)
		return
	}
	log.Println("Server stopped")
}

// logBannerProfiles prints the measured banner profiles (symbol-fs) at
// startup. A missing banner is only a warning: the other endpoints still
// work, and /symbol-art answers 404 for that banner.
func logBannerProfiles(fonts ai.FontSource) {
	profiles, err := ai.ProfileBanners(fonts)
	if err != nil {
		log.Printf("warning: banner profiling: %v", err)
	}
	for _, p := range profiles {
		log.Printf("banner %-10s average width %.2f (%s)", p.Name, p.AvgWidth, p.Style)
	}
}

// newAssistant picks the LLM implementation: without LLM_BASE_URL the
// server runs in mock mode with canned answers and no network.
func newAssistant(cfg ai.Config) (handlers.Assistant, error) {
	if cfg.MockMode() {
		log.Printf("LLM mode: mock (LLM_BASE_URL is not set, canned answers)")
		return ai.NewMock(), nil
	}
	client, err := ai.NewClient(cfg)
	if err != nil {
		return nil, err
	}
	log.Printf("LLM mode: live (model %q)", client.Model())
	return client, nil
}

// loadDotEnv reads KEY=VALUE lines from path into the environment, so the
// settings from .env work without exporting them by hand. Variables that
// are already set win over the file, and a missing file is not an error.
func loadDotEnv(path string) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}

	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(strings.TrimPrefix(line, "export "), "=")
		if !ok {
			return fmt.Errorf("line %d: expected KEY=VALUE", i+1)
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if _, exists := os.LookupEnv(key); !exists {
			os.Setenv(key, value)
		}
	}
	return nil
}
