package main

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
)

func authorizedBearerToken(authHeader, expectedToken string) bool {
	parts := strings.Fields(strings.TrimSpace(authHeader))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return false
	}

	presentedToken := parts[1]
	if len(presentedToken) != len(expectedToken) {
		return false
	}

	return subtle.ConstantTimeCompare([]byte(presentedToken), []byte(expectedToken)) == 1
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(payload); err != nil {
		http.Error(w, "failed to encode response", http.StatusInternalServerError)
	}
}
