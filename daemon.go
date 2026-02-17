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
	vultrBaseURL                     = "https://api.vultr.com/v2"
	labelPrefix                      = "paropal-"
	listenAddr                       = ":8080"
	requestTimeout                   = 10 * time.Second
	shutdownTimeout                  = 15 * time.Second
	shutdownTokenEnv                 = "SHUTDOWN_BEARER_TOKEN"
	cleanupTimeZone                  = "Asia/Seoul"
	cleanupHourKST                   = 0
	cleanupMinuteKST                 = 10
	cleanupWindowStartHourKST        = 0
	cleanupWindowStartMinuteKST      = 0
	cleanupWindowEndHourKST          = 7
	cleanupWindowEndMinuteKST        = 0
	defaultCleanupSettleDelay        = 20 * time.Second
	defaultCleanupBackoffMin         = 15 * time.Second
	defaultCleanupBackoffMax         = 5 * time.Minute
	defaultCleanupPassDeleteInterval = 2 * time.Second
)

var errInstanceNotFound = errors.New("no instance found with matching label prefix")

type app struct {
	vultr                     *vultrClient
	logger                    *slog.Logger
	server                    *http.Server
	shutdownToken             string
	stopBackground            context.CancelFunc
	cleanupLoc                *time.Location
	cleanupSettleDelay        time.Duration
	cleanupBackoffMin         time.Duration
	cleanupBackoffMax         time.Duration
	cleanupPassDeleteInterval time.Duration
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
	ID     string `json:"id"`
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

	cleanupLoc, err := time.LoadLocation(cleanupTimeZone)
	if err != nil {
		logger.Error("failed to load cleanup timezone", "timezone", cleanupTimeZone, "error", err)
		os.Exit(1)
	}

	backgroundCtx, stopBackground := context.WithCancel(context.Background())

	a := &app{
		vultr:                     client,
		logger:                    logger,
		shutdownToken:             shutdownToken,
		stopBackground:            stopBackground,
		cleanupLoc:                cleanupLoc,
		cleanupSettleDelay:        defaultCleanupSettleDelay,
		cleanupBackoffMin:         defaultCleanupBackoffMin,
		cleanupBackoffMax:         defaultCleanupBackoffMax,
		cleanupPassDeleteInterval: defaultCleanupPassDeleteInterval,
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

	go a.runDailyCleanup(backgroundCtx)

	logger.Info("starting daemon", "addr", listenAddr)
	err = server.ListenAndServe()
	stopBackground()
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

func (a *app) runDailyCleanup(ctx context.Context) {
	now := time.Now()
	next := firstCleanupRunTimeKST(now, a.cleanupLoc)
	a.logger.Info("daily instance cleanup scheduler started",
		"timezone", cleanupTimeZone,
		"startup_kst", now.In(a.cleanupLoc).Format(time.RFC3339),
		"next_run_kst", next.In(a.cleanupLoc).Format(time.RFC3339),
	)

	for {
		wait := time.Until(next)
		if wait < 0 {
			wait = 0
		}

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			a.logger.Info("daily instance cleanup scheduler stopped")
			return
		case <-timer.C:
			now := time.Now()
			windowStart, windowEnd := cleanupWindowBounds(now, a.cleanupLoc)
			if !isWithinCleanupWindow(now, a.cleanupLoc) {
				a.logger.Warn("skipping cleanup outside allowed window",
					"window_start_kst", windowStart.In(a.cleanupLoc).Format(time.RFC3339),
					"window_end_kst", windowEnd.In(a.cleanupLoc).Format(time.RFC3339),
					"current_kst", now.In(a.cleanupLoc).Format(time.RFC3339),
				)
				next = nextCleanupTimeKST(now, a.cleanupLoc)
				continue
			}

			a.logger.Warn("starting scheduled instance cleanup run",
				"scheduled_kst", next.In(a.cleanupLoc).Format(time.RFC3339),
				"started_kst", now.In(a.cleanupLoc).Format(time.RFC3339),
				"window_end_kst", windowEnd.In(a.cleanupLoc).Format(time.RFC3339),
			)
			a.reconcileDestroyAllInstances(ctx, windowEnd)
			next = nextCleanupTimeKST(time.Now(), a.cleanupLoc)
		}
	}
}

func nextCleanupTimeKST(now time.Time, loc *time.Location) time.Time {
	localNow := now.In(loc)
	scheduled := time.Date(
		localNow.Year(),
		localNow.Month(),
		localNow.Day(),
		cleanupHourKST,
		cleanupMinuteKST,
		0,
		0,
		loc,
	)

	if !localNow.Before(scheduled) {
		scheduled = scheduled.Add(24 * time.Hour)
	}

	return scheduled
}

func firstCleanupRunTimeKST(now time.Time, loc *time.Location) time.Time {
	if !isWithinCleanupWindow(now, loc) {
		return nextCleanupTimeKST(now, loc)
	}

	localNow := now.In(loc)
	scheduledToday := time.Date(
		localNow.Year(),
		localNow.Month(),
		localNow.Day(),
		cleanupHourKST,
		cleanupMinuteKST,
		0,
		0,
		loc,
	)
	if !localNow.Before(scheduledToday) {
		return now
	}

	return scheduledToday
}

func cleanupWindowBounds(now time.Time, loc *time.Location) (time.Time, time.Time) {
	localNow := now.In(loc)
	windowStart := time.Date(
		localNow.Year(),
		localNow.Month(),
		localNow.Day(),
		cleanupWindowStartHourKST,
		cleanupWindowStartMinuteKST,
		0,
		0,
		loc,
	)
	windowEnd := time.Date(
		localNow.Year(),
		localNow.Month(),
		localNow.Day(),
		cleanupWindowEndHourKST,
		cleanupWindowEndMinuteKST,
		0,
		0,
		loc,
	)
	return windowStart, windowEnd
}

func isWithinCleanupWindow(now time.Time, loc *time.Location) bool {
	windowStart, windowEnd := cleanupWindowBounds(now, loc)
	localNow := now.In(loc)
	if localNow.Before(windowStart) {
		return false
	}
	return localNow.Before(windowEnd)
}

func (a *app) reconcileDestroyAllInstances(ctx context.Context, cutoff time.Time) {
	backoff := a.cleanupBackoffMin

	for {
		if err := ctx.Err(); err != nil {
			return
		}
		if !time.Now().Before(cutoff) {
			a.logger.Warn("cleanup reconciliation stopped at window cutoff",
				"cutoff_kst", cutoff.In(a.cleanupLoc).Format(time.RFC3339),
			)
			return
		}

		instances, err := a.vultr.listAllInstances(ctx)
		if err != nil {
			a.logger.Error("cleanup reconciliation failed to list instances", "error", err, "retry_in", backoff.String())
			if !sleepWithContextUntil(ctx, backoff, cutoff) {
				return
			}
			backoff = nextBackoff(backoff, a.cleanupBackoffMax)
			continue
		}

		if len(instances) == 0 {
			a.logger.Info("cleanup reconciliation complete", "remaining_instances", 0)
			return
		}

		a.logger.Warn("cleanup reconciliation deleting instances", "count", len(instances))

		deleteFailures := 0
		for _, instance := range instances {
			if !time.Now().Before(cutoff) {
				a.logger.Warn("cleanup reconciliation reached window cutoff during delete pass",
					"cutoff_kst", cutoff.In(a.cleanupLoc).Format(time.RFC3339),
				)
				return
			}

			if instance.ID == "" {
				deleteFailures++
				a.logger.Error("cleanup reconciliation found instance without id", "label", instance.Label, "ip", instance.MainIP)
				continue
			}

			err := a.vultr.deleteInstance(ctx, instance.ID)
			if err != nil {
				deleteFailures++
				a.logger.Error("cleanup reconciliation failed to delete instance",
					"instance_id", instance.ID,
					"label", instance.Label,
					"error", err,
				)
				continue
			}

			a.logger.Info("cleanup reconciliation delete requested", "instance_id", instance.ID, "label", instance.Label)

			// Keep a short gap between delete calls to reduce burst rate against the API.
			if !sleepWithContextUntil(ctx, a.cleanupPassDeleteInterval, cutoff) {
				return
			}
		}

		if deleteFailures > 0 {
			a.logger.Warn("cleanup reconciliation pass incomplete", "delete_failures", deleteFailures, "retry_in", backoff.String())
			if !sleepWithContextUntil(ctx, backoff, cutoff) {
				return
			}
			backoff = nextBackoff(backoff, a.cleanupBackoffMax)
			continue
		}

		// Deletions are asynchronous upstream; allow state to settle before verifying again.
		if !sleepWithContextUntil(ctx, a.cleanupSettleDelay, cutoff) {
			return
		}
		backoff = a.cleanupBackoffMin
	}
}

func sleepWithContext(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func sleepWithContextUntil(ctx context.Context, d time.Duration, cutoff time.Time) bool {
	if cutoff.IsZero() {
		return sleepWithContext(ctx, d)
	}

	remaining := time.Until(cutoff)
	if remaining <= 0 {
		return false
	}

	wait := d
	if wait > remaining {
		wait = remaining
	}

	timer := time.NewTimer(wait)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return time.Now().Before(cutoff)
	}
}

func nextBackoff(current, max time.Duration) time.Duration {
	next := current * 2
	if next > max {
		return max
	}
	return next
}

func (c *vultrClient) pendingCharges(ctx context.Context) (float64, error) {
	var response accountResponse
	if err := c.do(ctx, http.MethodGet, "/account", &response); err != nil {
		return 0, err
	}

	return response.Account.PendingCharges, nil
}

func (c *vultrClient) firstInstanceWithLabelPrefix(ctx context.Context, prefix string) (*vultrInstance, error) {
	instances, err := c.listAllInstances(ctx)
	if err != nil {
		return nil, err
	}

	for _, instance := range instances {
		if strings.HasPrefix(instance.Label, prefix) {
			match := instance
			return &match, nil
		}
	}

	return nil, errInstanceNotFound
}

func (c *vultrClient) listAllInstances(ctx context.Context) ([]vultrInstance, error) {
	cursor := ""
	instances := make([]vultrInstance, 0, 16)

	for {
		params := url.Values{}
		params.Set("per_page", "100")
		if cursor != "" {
			params.Set("cursor", cursor)
		}

		path := "/instances?" + params.Encode()
		var response listInstancesResponse
		if err := c.do(ctx, http.MethodGet, path, &response); err != nil {
			return nil, err
		}

		instances = append(instances, response.Instances...)

		nextCursor, err := extractCursor(response.Meta.Links.Next)
		if err != nil {
			return nil, err
		}
		if nextCursor == "" {
			break
		}
		cursor = nextCursor
	}

	return instances, nil
}

func (c *vultrClient) deleteInstance(ctx context.Context, instanceID string) error {
	if strings.TrimSpace(instanceID) == "" {
		return errors.New("instance id cannot be empty")
	}

	path := "/instances/" + url.PathEscape(instanceID)
	return c.do(ctx, http.MethodDelete, path, nil)
}

func (c *vultrClient) do(ctx context.Context, method, path string, dest any) error {
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

	if dest == nil {
		io.Copy(io.Discard, resp.Body)
		return nil
	}

	if err := json.NewDecoder(resp.Body).Decode(dest); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
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
