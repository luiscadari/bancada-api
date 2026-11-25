// Command api runs the Bancada API.
//
//	api serve              start the HTTP server (default)
//	api migrate up|down|status
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata" // the distroless image has no zoneinfo; branches use IANA time zones

	"github.com/luiscadari/bancada-api/internal/app"
	"github.com/luiscadari/bancada-api/internal/platform/config"
	"github.com/luiscadari/bancada-api/internal/platform/database"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	setupLogger(cfg.LogLevel)

	cmd := "serve"
	if len(args) > 0 {
		cmd = args[0]
	}
	switch cmd {
	case "serve":
		return serve(cfg)
	case "migrate":
		if err := cfg.ValidateMigrate(); err != nil {
			return err
		}
		sub := "up"
		if len(args) > 1 {
			sub = args[1]
		}
		results, err := database.Migrate(context.Background(), cfg.MigrationDatabaseURL, sub)
		for _, r := range results {
			if r != nil {
				slog.Info("migration", "version", r.Source.Version, "path", r.Source.Path, "duration", r.Duration)
			}
		}
		return err
	default:
		return fmt.Errorf("unknown command %q (use serve or migrate)", cmd)
	}
}

func serve(cfg config.Config) error {
	if err := cfg.ValidateServe(); err != nil {
		return err
	}
	a, err := app.New(cfg)
	if err != nil {
		return err
	}
	defer a.Close()

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           a.Router,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", cfg.HTTPAddr)
		errCh <- srv.ListenAndServe()
	}()
	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		slog.Info("shutting down")
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

func setupLogger(level string) {
	var l slog.Level
	if err := l.UnmarshalText([]byte(level)); err != nil {
		l = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: l})))
}
