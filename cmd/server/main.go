// Command server starts the symbol-web HTTP server: a web GUI for
// symbol-art. Run it from the project root so it can find the banner
// files, templates/ and static/.
package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
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
	port := os.Getenv("PORT")
	if port == "" {
		port = defaultPort
	}

	// Measure the banners once at startup (the symbol-fs banner profile).
	// A banner that cannot be read is skipped: the recommendation still
	// works and judges that banner on style alone.
	profiles, err := ai.ProfileBanners(".")
	if err != nil {
		log.Printf("warning: banner profiling: %v", err)
	}
	for _, p := range profiles {
		log.Printf("banner %-10s average width %.2f (%s)", p.Name, p.AvgWidth, p.Style)
	}

	h := handlers.New(handlers.Config{
		Generator:   ascii.NewGenerator("."),
		Recommender: ai.NewRecommender(profiles),
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
