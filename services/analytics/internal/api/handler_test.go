package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/piliotov/linkpulse/services/analytics/internal/stats"
)

type fakeRepo struct {
	summary stats.Summary
	err     error
	pingErr error
	lastTop int64
}

func (f *fakeRepo) Summary(_ context.Context, code string, top int64) (stats.Summary, error) {
	f.lastTop = top
	s := f.summary
	s.Code = code
	return s, f.err
}

func (f *fakeRepo) Ping(context.Context) error { return f.pingErr }

func get(h http.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func newHandler(repo *fakeRepo) http.Handler {
	return NewServer(repo, slog.New(slog.NewTextHandler(io.Discard, nil))).Routes()
}

func TestStats(t *testing.T) {
	repo := &fakeRepo{summary: stats.Summary{Total: 4, Daily: map[string]int64{"2026-01-01": 4}}}
	h := newHandler(repo)

	rec := get(h, "/api/stats/abc")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got stats.Summary
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Code != "abc" || got.Total != 4 {
		t.Fatalf("got %+v", got)
	}
	if repo.lastTop != 10 {
		t.Fatalf("default top = %d", repo.lastTop)
	}
}

func TestStatsTopParam(t *testing.T) {
	repo := &fakeRepo{}
	h := newHandler(repo)
	if rec := get(h, "/api/stats/abc?top=3"); rec.Code != http.StatusOK || repo.lastTop != 3 {
		t.Fatalf("status = %d, top = %d", rec.Code, repo.lastTop)
	}
	for _, q := range []string{"0", "101", "x"} {
		if rec := get(h, "/api/stats/abc?top="+q); rec.Code != http.StatusBadRequest {
			t.Errorf("top=%s status = %d", q, rec.Code)
		}
	}
}

func TestStatsErrors(t *testing.T) {
	repo := &fakeRepo{err: errors.New("boom")}
	h := newHandler(repo)
	if rec := get(h, "/api/stats/abc"); rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec := get(h, "/api/stats/"+strings.Repeat("a", 40)); rec.Code != http.StatusBadRequest {
		t.Fatalf("long code status = %d", rec.Code)
	}
}

func TestProbes(t *testing.T) {
	repo := &fakeRepo{}
	h := newHandler(repo)
	if rec := get(h, "/healthz"); rec.Code != http.StatusOK {
		t.Fatalf("healthz = %d", rec.Code)
	}
	if rec := get(h, "/readyz"); rec.Code != http.StatusOK {
		t.Fatalf("readyz = %d", rec.Code)
	}
	repo.pingErr = errors.New("down")
	if rec := get(h, "/readyz"); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("readyz down = %d", rec.Code)
	}
}

func TestMetrics(t *testing.T) {
	s := NewServer(&fakeRepo{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.Clicks.Add(3)
	h := s.Routes()
	get(h, "/api/stats/abc")

	body := get(h, "/metrics").Body.String()
	for _, want := range []string{"analytics_clicks_processed_total 3", `route="GET /api/stats/{code}"`} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics missing %q", want)
		}
	}
}
