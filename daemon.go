package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	vultrBaseURL     = "https://api.vultr.com/v2"
	labelPrefix      = "paropal-"
	listenAddr       = ":8080"
	requestTimeout   = 10 * time.Second
	shutdownTimeout  = 15 * time.Second
	shutdownTokenEnv = "SHUTDOWN_BEARER_TOKEN"
)

var errInstanceNotFound = errors.New("no instance found with matching label prefix")

type app struct {
	vultr         *vultrClient
	logger        *slog.Logger
	server        *http.Server
	shutdownToken string
}

type vultrClient struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

type accountResponse struct {
	Account struct {
		PendingCharges float64 `json:"pending_charges"`
	} `json:"account"`
}

type vultrInstance struct {
	Status string `json:"status"`
	MainIP string `json:"main_ip"`
	Label  string `json:"label"`
}

type listInstancesResponse struct {
	Instances []vultrInstance `json:"instances"`
	Meta      struct {
		Links struct {
			Next string `json:"next"`
		} `json:"links"`
	} `json:"meta"`
}

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

	a := &app{
		vultr:         client,
		logger:        logger,
		shutdownToken: shutdownToken,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /charges", a.handleCharges)
	mux.HandleFunc("GET /instance", a.handleInstance)
	mux.HandleFunc("POST /shutdown", a.handleShutdown)

	server := &http.Server{
		Addr:              listenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	a.server = server

	logger.Info("starting daemon", "addr", listenAddr)
	err = server.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("server stopped with error", "error", err)
		os.Exit(1)
	}
}

func newVultrClientFromEnv() (*vultrClient, error) {
	apiKey := strings.TrimSpace(os.Getenv("VULTR_API_KEY"))
	if apiKey == "" {
		return nil, errors.New("VULTR_API_KEY environment variable is required")
	}

	return &vultrClient{
		apiKey:  apiKey,
		baseURL: vultrBaseURL,
		httpClient: &http.Client{
			Timeout: requestTimeout,
		},
	}, nil
}

func shutdownTokenFromEnv() (string, error) {
	token := strings.TrimSpace(os.Getenv(shutdownTokenEnv))
	if token == "" {
		return "", fmt.Errorf("%s environment variable is required", shutdownTokenEnv)
	}

	return token, nil
}

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
		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()

		if err := a.server.Shutdown(ctx); err != nil {
			a.logger.Error("graceful shutdown failed", "error", err)
		} else {
			a.logger.Info("graceful shutdown complete")
		}
	}()
}

func (c *vultrClient) pendingCharges(ctx context.Context) (float64, error) {
	var response accountResponse
	if err := c.doJSON(ctx, http.MethodGet, "/account", &response); err != nil {
		return 0, err
	}

	return response.Account.PendingCharges, nil
}

func (c *vultrClient) firstInstanceWithLabelPrefix(ctx context.Context, prefix string) (*vultrInstance, error) {
	cursor := ""

	for {
		params := url.Values{}
		params.Set("per_page", "100")
		if cursor != "" {
			params.Set("cursor", cursor)
		}

		path := "/instances?" + params.Encode()
		var response listInstancesResponse
		if err := c.doJSON(ctx, http.MethodGet, path, &response); err != nil {
			return nil, err
		}

		for _, instance := range response.Instances {
			if strings.HasPrefix(instance.Label, prefix) {
				match := instance
				return &match, nil
			}
		}

		nextCursor, err := extractCursor(response.Meta.Links.Next)
		if err != nil {
			return nil, err
		}
		if nextCursor == "" {
			break
		}
		cursor = nextCursor
	}

	return nil, errInstanceNotFound
}

func (c *vultrClient) doJSON(ctx context.Context, method, path string, dest any) error {
	endpoint := c.baseURL + path

	req, err := http.NewRequestWithContext(ctx, method, endpoint, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request %s failed: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("vultr %s returned %s: %s", path, resp.Status, strings.TrimSpace(string(body)))
	}

	if err := json.NewDecoder(resp.Body).Decode(dest); err != nil {
		return fmt.Errorf("decode %s response: %w", path, err)
	}

	return nil
}

func extractCursor(nextLink string) (string, error) {
	nextLink = strings.TrimSpace(nextLink)
	if nextLink == "" {
		return "", nil
	}

	parsed, err := url.Parse(nextLink)
	if err != nil {
		return "", fmt.Errorf("parse pagination link %q: %w", nextLink, err)
	}

	return parsed.Query().Get("cursor"), nil
}

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
