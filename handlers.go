package main

import (
	"context"
	"errors"
	"net/http"
)

func (a *app) handleCharges(w http.ResponseWriter, r *http.Request) {
	charges, err := a.vultr.pendingCharges(r.Context())
	if err != nil {
		a.logger.Error("failed to fetch pending charges", "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": "failed to fetch pending charges from Vultr",
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]float64{
		"pending_charges": charges,
	})
}

func (a *app) handleInstance(w http.ResponseWriter, r *http.Request) {
	instance, err := a.vultr.firstInstanceWithLabelPrefix(r.Context(), labelPrefix)
	if err != nil {
		if errors.Is(err, errInstanceNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{
				"error": "no instance found with label prefix paropal-",
			})
			return
		}

		a.logger.Error("failed to fetch instance", "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": "failed to fetch instances from Vultr",
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status": instance.Status,
		"ip":     instance.MainIP,
		"label":  instance.Label,
	})
}

func (a *app) handleShutdown(w http.ResponseWriter, r *http.Request) {
	if !authorizedBearerToken(r.Header.Get("Authorization"), a.shutdownToken) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="daemon-shutdown"`)
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"error": "unauthorized",
		})
		return
	}

	if a.server == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "server is not initialized",
		})
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]string{
		"status": "shutting down",
	})

	go func() {
		if a.stopBackground != nil {
			a.stopBackground()
		}

		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()

		if err := a.server.Shutdown(ctx); err != nil {
			a.logger.Error("graceful shutdown failed", "error", err)
		} else {
			a.logger.Info("graceful shutdown complete")
		}
	}()
}
