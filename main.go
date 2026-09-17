// Command media-sequencer serves the multi-window media sequencer backend.
//
// It has no third-party dependencies: the standard library covers routing
// (Go 1.22 patterns), persistence (JSON + atomic file writes) and live updates
// (server-sent events). That keeps the deployable artifact a single static
// binary with no CGO and no driver to install.
package main

import (
	"context"
	"embed"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"media-sequencer/internal/api"
	"media-sequencer/internal/store"
)

// The seeded images ship inside the binary so a fresh deployment shows real
// media without depending on any external host.
//
//go:embed all:assets/media
var assets embed.FS

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("[sequencer] ")

	cfg := loadConfig()

	st, err := store.NewFileStore(store.Config{
		Path:          cfg.dataFile,
		CycleMs:       cfg.cycle.Milliseconds(),
		DefaultSyncMs: cfg.syncDuration.Milliseconds(),
	})
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer st.Close()

	mediaFS, err := fs.Sub(assets, "assets/media")
	if err != nil {
		log.Fatalf("assets: %v", err)
	}

	handler := api.New(st, api.Config{
		SyncLeadMs:    cfg.syncLead.Milliseconds(),
		DefaultSyncMs: cfg.syncDuration.Milliseconds(),
		AllowedOrigin: cfg.corsOrigin,
		StaticDir:     cfg.staticDir,
		MediaFS:       mediaFS,
	})

	srv := &http.Server{
		Addr:              ":" + cfg.port,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		// No write timeout: the SSE stream is intentionally long-lived.
		IdleTimeout: 120 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		snap := st.Snapshot()
		log.Printf("listening on :%s", cfg.port)
		log.Printf("data file %s | cycle %s | sync %s (lead %s)", cfg.dataFile, cfg.cycle, cfg.syncDuration, cfg.syncLead)
		log.Printf("seeded %d windows, %d media items", len(snap.Windows), len(snap.Media))
		if cfg.staticDir != "" {
			log.Printf("serving frontend from %s", cfg.staticDir)
		}
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen: %v", err)
		}
	}()

	<-ctx.Done()
	log.Print("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}

type config struct {
	port         string
	dataFile     string
	cycle        time.Duration
	syncDuration time.Duration
	syncLead     time.Duration
	corsOrigin   string
	staticDir    string
}

// loadConfig reads the environment, falling back to values that work for a
// local run with no setup at all.
func loadConfig() config {
	cfg := config{
		port:         envString("PORT", "8080"),
		dataFile:     envString("DATA_FILE", "./data/state.json"),
		cycle:        envDuration("CYCLE_DURATION", 5*time.Hour),
		syncDuration: envDuration("SYNC_DURATION", 15*time.Second),
		syncLead:     envDuration("SYNC_LEAD", 750*time.Millisecond),
		corsOrigin:   envString("CORS_ORIGIN", "*"),
		staticDir:    envString("STATIC_DIR", ""),
	}

	// A port given as "8080" or as ":8080" should both work; hosting providers
	// are inconsistent about this.
	if len(cfg.port) > 0 && cfg.port[0] == ':' {
		cfg.port = cfg.port[1:]
	}
	if _, err := strconv.Atoi(cfg.port); err != nil {
		log.Fatalf("PORT must be a number, got %q", cfg.port)
	}
	if cfg.cycle <= 0 {
		log.Fatal("CYCLE_DURATION must be positive")
	}
	if cfg.syncDuration <= 0 {
		log.Fatal("SYNC_DURATION must be positive")
	}
	return cfg
}

func envString(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

// envDuration accepts Go duration strings such as "5h", "90s" or "750ms".
func envDuration(key string, fallback time.Duration) time.Duration {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return fallback
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		log.Fatalf("%s must be a duration like 5h or 90s, got %q", key, raw)
	}
	return d
}
