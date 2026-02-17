package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNextCleanupTimeKST(t *testing.T) {
	loc, err := time.LoadLocation(cleanupTimeZone)
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	tests := []struct {
		name string
		now  time.Time
		want time.Time
	}{
		{
			name: "before cutoff schedules same day",
			now:  time.Date(2026, time.February, 17, 0, 9, 59, 0, loc),
			want: time.Date(2026, time.February, 17, 0, 10, 0, 0, loc),
		},
		{
			name: "exact cutoff schedules next day",
			now:  time.Date(2026, time.February, 17, 0, 10, 0, 0, loc),
			want: time.Date(2026, time.February, 18, 0, 10, 0, 0, loc),
		},
		{
			name: "after cutoff schedules next day",
			now:  time.Date(2026, time.February, 17, 15, 0, 0, 0, loc),
			want: time.Date(2026, time.February, 18, 0, 10, 0, 0, loc),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := nextCleanupTimeKST(tt.now, loc)
			if !got.Equal(tt.want) {
				t.Fatalf("nextCleanupTimeKST() = %s, want %s", got.Format(time.RFC3339), tt.want.Format(time.RFC3339))
			}
		})
	}
}

func TestFirstCleanupRunTimeKST(t *testing.T) {
	loc, err := time.LoadLocation(cleanupTimeZone)
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	tests := []struct {
		name string
		now  time.Time
		want time.Time
	}{
		{
			name: "before scheduled time in window waits for 00:10",
			now:  time.Date(2026, time.February, 17, 0, 5, 0, 0, loc),
			want: time.Date(2026, time.February, 17, 0, 10, 0, 0, loc),
		},
		{
			name: "after scheduled time in window runs immediately",
			now:  time.Date(2026, time.February, 17, 0, 11, 0, 0, loc),
			want: time.Date(2026, time.February, 17, 0, 11, 0, 0, loc),
		},
		{
			name: "late in window runs immediately",
			now:  time.Date(2026, time.February, 17, 6, 59, 0, 0, loc),
			want: time.Date(2026, time.February, 17, 6, 59, 0, 0, loc),
		},
		{
			name: "outside window schedules next day",
			now:  time.Date(2026, time.February, 17, 7, 1, 0, 0, loc),
			want: time.Date(2026, time.February, 18, 0, 10, 0, 0, loc),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := firstCleanupRunTimeKST(tt.now, loc)
			if !got.Equal(tt.want) {
				t.Fatalf("firstCleanupRunTimeKST() = %s, want %s", got.Format(time.RFC3339), tt.want.Format(time.RFC3339))
			}
		})
	}
}

func TestNextProvisionTimeKST(t *testing.T) {
	loc, err := time.LoadLocation(cleanupTimeZone)
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	tests := []struct {
		name string
		now  time.Time
		want time.Time
	}{
		{
			name: "before scheduled time schedules same day",
			now:  time.Date(2026, time.February, 17, 7, 9, 59, 0, loc),
			want: time.Date(2026, time.February, 17, 7, 10, 0, 0, loc),
		},
		{
			name: "exact scheduled time schedules next day",
			now:  time.Date(2026, time.February, 17, 7, 10, 0, 0, loc),
			want: time.Date(2026, time.February, 18, 7, 10, 0, 0, loc),
		},
		{
			name: "after scheduled time schedules next day",
			now:  time.Date(2026, time.February, 17, 15, 0, 0, 0, loc),
			want: time.Date(2026, time.February, 18, 7, 10, 0, 0, loc),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := nextProvisionTimeKST(tt.now, loc)
			if !got.Equal(tt.want) {
				t.Fatalf("nextProvisionTimeKST() = %s, want %s", got.Format(time.RFC3339), tt.want.Format(time.RFC3339))
			}
		})
	}
}

func TestFirstProvisionRunTimeKST(t *testing.T) {
	loc, err := time.LoadLocation(cleanupTimeZone)
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	tests := []struct {
		name string
		now  time.Time
		want time.Time
	}{
		{
			name: "before scheduled time waits for 07:10",
			now:  time.Date(2026, time.February, 17, 7, 0, 0, 0, loc),
			want: time.Date(2026, time.February, 17, 7, 10, 0, 0, loc),
		},
		{
			name: "after scheduled time runs immediately",
			now:  time.Date(2026, time.February, 17, 7, 11, 0, 0, loc),
			want: time.Date(2026, time.February, 17, 7, 11, 0, 0, loc),
		},
		{
			name: "late in day runs immediately",
			now:  time.Date(2026, time.February, 17, 23, 59, 0, 0, loc),
			want: time.Date(2026, time.February, 17, 23, 59, 0, 0, loc),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := firstProvisionRunTimeKST(tt.now, loc)
			if !got.Equal(tt.want) {
				t.Fatalf("firstProvisionRunTimeKST() = %s, want %s", got.Format(time.RFC3339), tt.want.Format(time.RFC3339))
			}
		})
	}
}

func TestIsWithinCleanupWindow(t *testing.T) {
	loc, err := time.LoadLocation(cleanupTimeZone)
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	tests := []struct {
		name string
		now  time.Time
		want bool
	}{
		{
			name: "window open at midnight",
			now:  time.Date(2026, time.February, 17, 0, 0, 0, 0, loc),
			want: true,
		},
		{
			name: "window open before end",
			now:  time.Date(2026, time.February, 17, 6, 59, 59, 0, loc),
			want: true,
		},
		{
			name: "window closed at end",
			now:  time.Date(2026, time.February, 17, 7, 0, 0, 0, loc),
			want: false,
		},
		{
			name: "window closed during day",
			now:  time.Date(2026, time.February, 17, 12, 0, 0, 0, loc),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isWithinCleanupWindow(tt.now, loc)
			if got != tt.want {
				t.Fatalf("isWithinCleanupWindow() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNextBackoff(t *testing.T) {
	tests := []struct {
		name    string
		current time.Duration
		max     time.Duration
		want    time.Duration
	}{
		{
			name:    "doubles under max",
			current: 15 * time.Second,
			max:     5 * time.Minute,
			want:    30 * time.Second,
		},
		{
			name:    "clamps at max",
			current: 4 * time.Minute,
			max:     5 * time.Minute,
			want:    5 * time.Minute,
		},
		{
			name:    "stays at max",
			current: 5 * time.Minute,
			max:     5 * time.Minute,
			want:    5 * time.Minute,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := nextBackoff(tt.current, tt.max)
			if got != tt.want {
				t.Fatalf("nextBackoff() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestAuthorizedBearerToken(t *testing.T) {
	const expected = "s3cret-token"

	tests := []struct {
		name       string
		header     string
		wantAccess bool
	}{
		{
			name:       "exact match",
			header:     "Bearer s3cret-token",
			wantAccess: true,
		},
		{
			name:       "case-insensitive scheme",
			header:     "bearer s3cret-token",
			wantAccess: true,
		},
		{
			name:       "wrong token",
			header:     "Bearer wrong",
			wantAccess: false,
		},
		{
			name:       "missing scheme",
			header:     "s3cret-token",
			wantAccess: false,
		},
		{
			name:       "empty header",
			header:     "",
			wantAccess: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := authorizedBearerToken(tt.header, expected)
			if got != tt.wantAccess {
				t.Fatalf("authorizedBearerToken() = %v, want %v", got, tt.wantAccess)
			}
		})
	}
}

func TestListAllInstancesPagination(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v2/instances" {
			http.NotFound(w, r)
			return
		}

		cursor := r.URL.Query().Get("cursor")
		switch cursor {
		case "":
			resp := listInstancesResponse{
				Instances: []vultrInstance{{ID: "inst-1", Label: "first"}},
			}
			resp.Meta.Links.Next = "https://api.vultr.com/v2/instances?cursor=page-2"
			writeJSON(w, http.StatusOK, resp)
		case "page-2":
			resp := listInstancesResponse{
				Instances: []vultrInstance{{ID: "inst-2", Label: "second"}},
			}
			writeJSON(w, http.StatusOK, resp)
		default:
			t.Fatalf("unexpected cursor %q", cursor)
		}
	}))
	defer server.Close()

	client := newTestVultrClient(server)

	instances, err := client.listAllInstances(context.Background())
	if err != nil {
		t.Fatalf("listAllInstances() error = %v", err)
	}

	if len(instances) != 2 {
		t.Fatalf("listAllInstances() returned %d instances, want 2", len(instances))
	}
	if instances[0].ID != "inst-1" || instances[1].ID != "inst-2" {
		t.Fatalf("unexpected instance order/ids: %+v", instances)
	}
}

func TestReconcileDestroyAllInstances(t *testing.T) {
	t.Parallel()

	type state struct {
		mu          sync.Mutex
		instances   map[string]vultrInstance
		listCalls   int
		deleteCalls int
	}

	st := &state{
		instances: map[string]vultrInstance{
			"inst-a": {ID: "inst-a", Label: "a"},
			"inst-b": {ID: "inst-b", Label: "b"},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/instances":
			st.mu.Lock()
			st.listCalls++
			list := make([]vultrInstance, 0, len(st.instances))
			for _, inst := range st.instances {
				list = append(list, inst)
			}
			st.mu.Unlock()
			sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
			resp := listInstancesResponse{Instances: list}
			writeJSON(w, http.StatusOK, resp)
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/v2/instances/"):
			rawID := strings.TrimPrefix(r.URL.Path, "/v2/instances/")
			id, err := url.PathUnescape(rawID)
			if err != nil {
				t.Fatalf("path unescape: %v", err)
			}

			st.mu.Lock()
			st.deleteCalls++
			delete(st.instances, id)
			st.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	a := &app{
		vultr:                     newTestVultrClient(server),
		logger:                    testLogger(),
		cleanupSettleDelay:        time.Millisecond,
		cleanupBackoffMin:         time.Millisecond,
		cleanupBackoffMax:         5 * time.Millisecond,
		cleanupPassDeleteInterval: time.Millisecond,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	a.reconcileDestroyAllInstances(ctx, time.Now().Add(2*time.Second))

	st.mu.Lock()
	defer st.mu.Unlock()

	if len(st.instances) != 0 {
		t.Fatalf("reconcileDestroyAllInstances() left %d instances; want 0", len(st.instances))
	}
	if st.deleteCalls != 2 {
		t.Fatalf("expected 2 delete calls, got %d", st.deleteCalls)
	}
	if st.listCalls < 2 {
		t.Fatalf("expected at least 2 list calls, got %d", st.listCalls)
	}
}

func TestReconcileRetriesAfterTransientListFailure(t *testing.T) {
	t.Parallel()

	type state struct {
		mu                sync.Mutex
		listCalls         int
		failuresRemaining int
	}

	st := &state{failuresRemaining: 1}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v2/instances" {
			http.NotFound(w, r)
			return
		}

		st.mu.Lock()
		st.listCalls++
		shouldFail := st.failuresRemaining > 0
		if shouldFail {
			st.failuresRemaining--
		}
		st.mu.Unlock()

		if shouldFail {
			http.Error(w, "temporary upstream failure", http.StatusBadGateway)
			return
		}

		resp := listInstancesResponse{Instances: nil}
		writeJSON(w, http.StatusOK, resp)
	}))
	defer server.Close()

	a := &app{
		vultr:                     newTestVultrClient(server),
		logger:                    testLogger(),
		cleanupSettleDelay:        time.Millisecond,
		cleanupBackoffMin:         time.Millisecond,
		cleanupBackoffMax:         5 * time.Millisecond,
		cleanupPassDeleteInterval: time.Millisecond,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	a.reconcileDestroyAllInstances(ctx, time.Now().Add(2*time.Second))

	st.mu.Lock()
	defer st.mu.Unlock()
	if st.listCalls < 2 {
		t.Fatalf("expected retry after transient failure; list calls = %d", st.listCalls)
	}
}

func TestReconcileStopsAtCutoff(t *testing.T) {
	t.Parallel()

	type state struct {
		mu          sync.Mutex
		listCalls   int
		deleteCalls int
	}

	st := &state{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		st.mu.Lock()
		defer st.mu.Unlock()
		if r.Method == http.MethodGet && r.URL.Path == "/v2/instances" {
			st.listCalls++
			resp := listInstancesResponse{
				Instances: []vultrInstance{{ID: "inst-a", Label: "a"}},
			}
			writeJSON(w, http.StatusOK, resp)
			return
		}
		if r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/v2/instances/") {
			st.deleteCalls++
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	a := &app{
		vultr:                     newTestVultrClient(server),
		logger:                    testLogger(),
		cleanupLoc:                time.FixedZone("KST", 9*60*60),
		cleanupSettleDelay:        time.Millisecond,
		cleanupBackoffMin:         time.Millisecond,
		cleanupBackoffMax:         5 * time.Millisecond,
		cleanupPassDeleteInterval: time.Millisecond,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	// Cutoff already passed: no list/delete should be attempted.
	a.reconcileDestroyAllInstances(ctx, time.Now().Add(-time.Second))

	st.mu.Lock()
	defer st.mu.Unlock()
	if st.listCalls != 0 {
		t.Fatalf("expected 0 list calls after cutoff, got %d", st.listCalls)
	}
	if st.deleteCalls != 0 {
		t.Fatalf("expected 0 delete calls after cutoff, got %d", st.deleteCalls)
	}
}

func TestSleepWithContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	ok := sleepWithContext(ctx, time.Second)
	elapsed := time.Since(start)

	if ok {
		t.Fatalf("sleepWithContext() = true, want false")
	}
	if elapsed > 100*time.Millisecond {
		t.Fatalf("sleepWithContext() took %s after cancellation; expected fast return", elapsed)
	}
}

func newTestVultrClient(server *httptest.Server) *vultrClient {
	return &vultrClient{
		apiKey:     "test-key",
		baseURL:    server.URL + "/v2",
		httpClient: server.Client(),
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
