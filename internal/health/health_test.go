package health

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func get(t *testing.T, h http.Handler, path string) (int, response) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	var body response
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decoding %s: %v", path, err)
	}
	return rec.Code, body
}

func TestLive(t *testing.T) {
	r := NewRegistry(time.Second)
	r.Add("broker", func(context.Context) error { return errors.New("down") })

	code, body := get(t, r.Handler(), "/health/live")

	if code != http.StatusOK || body.Status != "ok" {
		t.Fatalf("live = %d %+v; liveness must not depend on dependencies", code, body)
	}
}

func TestReady(t *testing.T) {
	r := NewRegistry(time.Second)
	r.Add("storage", func(context.Context) error { return nil })

	code, body := get(t, r.Handler(), "/health/ready")
	if code != http.StatusOK || body.Checks["storage"] != "ok" {
		t.Fatalf("ready = %d %+v", code, body)
	}

	r.Add("broker", func(context.Context) error { return errors.New("connection refused") })
	code, body = get(t, r.Handler(), "/health/ready")
	if code != http.StatusServiceUnavailable || body.Status != "unavailable" ||
		body.Checks["broker"] != "connection refused" || body.Checks["storage"] != "ok" {
		t.Fatalf("ready = %d %+v", code, body)
	}
}

func TestReady_ChecksShareTimeout(t *testing.T) {
	r := NewRegistry(20 * time.Millisecond)
	r.Add("slow", func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})

	code, _ := get(t, r.Handler(), "/health/ready")

	if code != http.StatusServiceUnavailable {
		t.Fatalf("ready = %d, want 503 after timeout", code)
	}
}
