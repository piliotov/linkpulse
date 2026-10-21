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
	"sync"
	"testing"
	"time"

	"github.com/piliotov/linkpulse/services/shortener/internal/store"
)

type fakeStore struct {
	mu       sync.Mutex
	links    map[string]store.Link
	clicks   []store.Click
	failGet  bool
	pingErr  error
	clickErr error
}

func newFakeStore() *fakeStore {
	return &fakeStore{links: map[string]store.Link{}}
}

func (f *fakeStore) Create(_ context.Context, l store.Link) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.links[l.Code]; ok {
		return store.ErrExists
	}
	f.links[l.Code] = l
	return nil
}

func (f *fakeStore) Get(_ context.Context, code string) (store.Link, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failGet {
		return store.Link{}, errors.New("boom")
	}
	l, ok := f.links[code]
	if !ok {
		return store.Link{}, store.ErrNotFound
	}
	return l, nil
}

func (f *fakeStore) RecordClick(_ context.Context, c store.Click) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.clickErr != nil {
		return f.clickErr
	}
	f.clicks = append(f.clicks, c)
	return nil
}

func (f *fakeStore) Ping(context.Context) error { return f.pingErr }

func newTestServer(fs *fakeStore) http.Handler {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := NewServer(fs, Config{BaseURL: "http://lp.test/"}, NewMetrics(), log)
	s.now = func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }
	return s.Routes()
}

func do(h http.Handler, method, path, body string, headers ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return out
}

func TestCreateGeneratedCode(t *testing.T) {
	fs := newFakeStore()
	h := newTestServer(fs)

	rec := do(h, http.MethodPost, "/api/links", `{"url":"https://example.com/a?b=c"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	body := decode(t, rec)
	code, _ := body["code"].(string)
	if len(code) != 7 {
		t.Fatalf("code = %q", code)
	}
	if body["short_url"] != "http://lp.test/"+code {
		t.Fatalf("short_url = %v", body["short_url"])
	}
	if _, ok := fs.links[code]; !ok {
		t.Fatal("link not stored")
	}
}

func TestCreateCustomCode(t *testing.T) {
	fs := newFakeStore()
	h := newTestServer(fs)

	rec := do(h, http.MethodPost, "/api/links", `{"url":"https://go.dev","code":"golang"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d", rec.Code)
	}
	rec = do(h, http.MethodPost, "/api/links", `{"url":"https://other.dev","code":"golang"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate status = %d", rec.Code)
	}
}

func TestCreateValidation(t *testing.T) {
	h := newTestServer(newFakeStore())
	cases := []struct {
		name string
		body string
	}{
		{"bad json", `{`},
		{"missing url", `{}`},
		{"relative url", `{"url":"/foo"}`},
		{"ftp scheme", `{"url":"ftp://files.example.com"}`},
		{"bad code", `{"url":"https://a.com","code":"a b"}`},
		{"reserved code", `{"url":"https://a.com","code":"metrics"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(h, http.MethodPost, "/api/links", tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d", rec.Code)
			}
		})
	}
}

func TestGetLink(t *testing.T) {
	fs := newFakeStore()
	fs.links["abc"] = store.Link{Code: "abc", URL: "https://example.com"}
	h := newTestServer(fs)

	rec := do(h, http.MethodGet, "/api/links/abc", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if decode(t, rec)["url"] != "https://example.com" {
		t.Fatalf("body = %s", rec.Body)
	}
	if rec := do(h, http.MethodGet, "/api/links/zzz", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("missing status = %d", rec.Code)
	}
}

func TestRedirectRecordsClick(t *testing.T) {
	fs := newFakeStore()
	fs.links["abc"] = store.Link{Code: "abc", URL: "https://example.com"}
	h := newTestServer(fs)

	rec := do(h, http.MethodGet, "/abc", "", "Referer", "https://www.reddit.com/r/golang")
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "https://example.com" {
		t.Fatalf("location = %q", loc)
	}
	if len(fs.clicks) != 1 || fs.clicks[0].Referrer != "reddit.com" || fs.clicks[0].Code != "abc" {
		t.Fatalf("clicks = %+v", fs.clicks)
	}
}

func TestRedirectSurvivesClickFailure(t *testing.T) {
	fs := newFakeStore()
	fs.links["abc"] = store.Link{Code: "abc", URL: "https://example.com"}
	fs.clickErr = errors.New("stream down")
	h := newTestServer(fs)

	if rec := do(h, http.MethodGet, "/abc", ""); rec.Code != http.StatusFound {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestRedirectErrors(t *testing.T) {
	fs := newFakeStore()
	h := newTestServer(fs)
	if rec := do(h, http.MethodGet, "/missing", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
	fs.failGet = true
	if rec := do(h, http.MethodGet, "/abc", ""); rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestHealthAndReadiness(t *testing.T) {
	fs := newFakeStore()
	h := newTestServer(fs)
	if rec := do(h, http.MethodGet, "/healthz", ""); rec.Code != http.StatusOK {
		t.Fatalf("healthz = %d", rec.Code)
	}
	if rec := do(h, http.MethodGet, "/readyz", ""); rec.Code != http.StatusOK {
		t.Fatalf("readyz = %d", rec.Code)
	}
	fs.pingErr = errors.New("down")
	if rec := do(h, http.MethodGet, "/readyz", ""); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("readyz when down = %d", rec.Code)
	}
}

func TestMetricsEndpoint(t *testing.T) {
	fs := newFakeStore()
	h := newTestServer(fs)
	do(h, http.MethodPost, "/api/links", `{"url":"https://example.com"}`)

	rec := do(h, http.MethodGet, "/metrics", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	for _, want := range []string{"shortener_links_created_total 1", `route="POST /api/links"`} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("metrics missing %q", want)
		}
	}
}

func TestReferrerHost(t *testing.T) {
	cases := map[string]string{
		"":                            "direct",
		"https://www.google.com/q":    "google.com",
		"http://news.ycombinator.com": "news.ycombinator.com",
		"not a url":                   "unknown",
	}
	for in, want := range cases {
		if got := referrerHost(in); got != want {
			t.Errorf("referrerHost(%q) = %q, want %q", in, got, want)
		}
	}
}
