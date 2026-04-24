package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim"
)

var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func main() {
	showVersion := flag.Bool("version", false, "print version information and exit")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	slog.SetDefault(logger)

	if *showVersion {
		logger.Info("promshim", "version", version, "commit", commit, "date", date)
		return
	}

	opts, err := promshim.LoadOptionsFromEnv()
	if err != nil {
		logger.Error("load configuration", "err", err)
		os.Exit(1)
	}

	handler, err := promshim.NewHandler(opts)
	if err != nil {
		logger.Error("create handler", "err", err)
		os.Exit(1)
	}

	addr := ":9090"
	if value := os.Getenv("PROM_SHIM_LISTEN_ADDR"); value != "" {
		addr = value
	}

	serverHandler := handler
	if os.Getenv("PROM_SHIM_ENABLE_PPROF") == "1" || os.Getenv("PROM_SHIM_ENABLE_PPROF") == "true" {
		mux := http.NewServeMux()
		mux.Handle("/debug/pprof/", http.DefaultServeMux)
		mux.Handle("/debug/pprof/cmdline", http.DefaultServeMux)
		mux.Handle("/debug/pprof/profile", http.DefaultServeMux)
		mux.Handle("/debug/pprof/symbol", http.DefaultServeMux)
		mux.Handle("/debug/pprof/trace", http.DefaultServeMux)
		mux.Handle("/", handler)
		serverHandler = mux
		logger.Warn("pprof endpoints enabled", "addr", addr)
	}

	server := &http.Server{
		Addr:              addr,
		Handler:           serverHandler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      opts.RequestTimeout + 10*time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("starting promshim", "addr", addr, "version", version, "commit", commit)
		serverErrors <- server.ListenAndServe()
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	select {
	case err := <-serverErrors:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("serve HTTP", "err", err)
			os.Exit(1)
		}
	case <-ctx.Done():
		logger.Info("shutting down promshim")
		stop()

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Error("graceful shutdown failed", "err", err)
			if closeErr := server.Close(); closeErr != nil {
				logger.Error("force close failed", "err", closeErr)
			}
			os.Exit(1)
		}
	}

	logger.Info("promshim stopped")
}
