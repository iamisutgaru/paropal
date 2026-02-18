package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"time"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	client, err := newVultrClientFromEnv()
	if err != nil {
		logger.Error("failed to initialize vultr client", "error", err)
		os.Exit(1)
	}

	shutdownToken, err := shutdownTokenFromEnv()
	if err != nil {
		logger.Error("failed to initialize shutdown auth", "error", err)
		os.Exit(1)
	}

	cleanupLoc, err := time.LoadLocation(cleanupTimeZone)
	if err != nil {
		logger.Error("failed to load cleanup timezone", "timezone", cleanupTimeZone, "error", err)
		os.Exit(1)
	}

	labelLoc, err := time.LoadLocation(labelTimeZone)
	if err != nil {
		logger.Error("failed to load label timezone", "timezone", labelTimeZone, "error", err)
		os.Exit(1)
	}

	backgroundCtx, stopBackground := context.WithCancel(context.Background())

	a := &app{
		vultr:                     client,
		logger:                    logger,
		shutdownToken:             shutdownToken,
		stopBackground:            stopBackground,
		cleanupLoc:                cleanupLoc,
		labelLoc:                  labelLoc,
		cleanupSettleDelay:        defaultCleanupSettleDelay,
		cleanupBackoffMin:         defaultCleanupBackoffMin,
		cleanupBackoffMax:         defaultCleanupBackoffMax,
		cleanupPassDeleteInterval: defaultCleanupPassDeleteInterval,
		provisionBackoffMin:       defaultProvisionBackoffMin,
		provisionBackoffMax:       defaultProvisionBackoffMax,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", a.handleRoot)
	mux.HandleFunc("GET /static/sjb.tar.gz", a.handleSjbTar)
	mux.HandleFunc("GET /api/charges", a.handleCharges)
	mux.HandleFunc("GET /api/instance", a.handleInstance)
	mux.HandleFunc("POST /api/shutdown", a.handleShutdown)

	server := &http.Server{
		Addr:              listenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	a.server = server

	go a.runDailyCleanup(backgroundCtx)
	go a.runDailyProvision(backgroundCtx)

	logger.Info("starting daemon", "addr", listenAddr)
	err = server.ListenAndServe()
	stopBackground()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("server stopped with error", "error", err)
		os.Exit(1)
	}
}
