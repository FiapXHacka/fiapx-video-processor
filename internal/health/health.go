// Package health exposes the Kubernetes liveness and readiness endpoints.
package health

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"sync"
	"time"
)

// Check returns nil when the dependency is usable.
type Check func(ctx context.Context) error

// Registry holds the readiness checks (broker, storage) registered by the adapters.
type Registry struct {
	mu      sync.RWMutex
	checks  map[string]Check
	timeout time.Duration
}

// NewRegistry creates a registry whose checks share the given timeout.
func NewRegistry(timeout time.Duration) *Registry {
	return &Registry{checks: map[string]Check{}, timeout: timeout}
}

// Add registers (or replaces) a readiness check.
func (r *Registry) Add(name string, c Check) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.checks[name] = c
}

type response struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks,omitempty"`
}

// Handler serves /health/live (process is up) and /health/ready (dependencies are up).
func (r *Registry) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, response{Status: "ok"})
	})
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, req *http.Request) {
		status, body := r.ready(req.Context())
		writeJSON(w, status, body)
	})
	return mux
}

func (r *Registry) ready(ctx context.Context) (int, response) {
	r.mu.RLock()
	names := make([]string, 0, len(r.checks))
	for name := range r.checks {
		names = append(names, name)
	}
	sort.Strings(names)
	checks := make([]Check, len(names))
	for i, name := range names {
		checks[i] = r.checks[name]
	}
	r.mu.RUnlock()

	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	body := response{Status: "ok", Checks: map[string]string{}}
	status := http.StatusOK
	for i, name := range names {
		if err := checks[i](ctx); err != nil {
			body.Checks[name] = err.Error()
			body.Status = "unavailable"
			status = http.StatusServiceUnavailable
			continue
		}
		body.Checks[name] = "ok"
	}
	return status, body
}

func writeJSON(w http.ResponseWriter, status int, body response) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
