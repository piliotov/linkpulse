package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/piliotov/linkpulse/services/shortener/internal/codegen"
	"github.com/piliotov/linkpulse/services/shortener/internal/store"
)

type Store interface {
	Create(ctx context.Context, l store.Link) error
	Get(ctx context.Context, code string) (store.Link, error)
	RecordClick(ctx context.Context, c store.Click) error
	Ping(ctx context.Context) error
}

type Config struct {
	BaseURL    string
	CodeLength int
}

type Server struct {
	store   Store
	cfg     Config
	metrics *Metrics
	log     *slog.Logger
	now     func() time.Time
}

func NewServer(s Store, cfg Config, m *Metrics, log *slog.Logger) *Server {
	if cfg.CodeLength == 0 {
		cfg.CodeLength = 7
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	return &Server{store: s, cfg: cfg, metrics: m, log: log, now: time.Now}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/links", s.createLink)
	mux.HandleFunc("GET /api/links/{code}", s.getLink)
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /readyz", s.readyz)
	mux.Handle("GET /metrics", s.metrics.handler)
	mux.HandleFunc("GET /{code}", s.redirect)
	return s.metrics.instrument(mux)
}

type createRequest struct {
	URL  string `json:"url"`
	Code string `json:"code,omitempty"`
}

type linkResponse struct {
	store.Link
	ShortURL string `json:"short_url"`
}

func (s *Server) createLink(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if !validURL(req.URL) {
		writeError(w, http.StatusBadRequest, "url must be an absolute http or https url")
		return
	}
	if req.Code != "" && (!codegen.Valid(req.Code) || reserved(req.Code)) {
		writeError(w, http.StatusBadRequest, "code must be 3-32 characters of letters, digits, - or _")
		return
	}

	link := store.Link{Code: req.Code, URL: req.URL, CreatedAt: s.now().UTC()}
	if err := s.save(r.Context(), &link, req.Code != ""); err != nil {
		if errors.Is(err, store.ErrExists) {
			writeError(w, http.StatusConflict, "code already taken")
			return
		}
		s.log.Error("create link", "err", err)
		writeError(w, http.StatusInternalServerError, "could not create link")
		return
	}

	s.metrics.linksCreated.Inc()
	writeJSON(w, http.StatusCreated, s.response(link))
}

func (s *Server) save(ctx context.Context, link *store.Link, custom bool) error {
	if custom {
		return s.store.Create(ctx, *link)
	}
	for range 5 {
		code, err := codegen.New(s.cfg.CodeLength)
		if err != nil {
			return err
		}
		link.Code = code
		err = s.store.Create(ctx, *link)
		if !errors.Is(err, store.ErrExists) {
			return err
		}
	}
	return errors.New("could not generate a free code")
}

func (s *Server) getLink(w http.ResponseWriter, r *http.Request) {
	link, err := s.store.Get(r.Context(), r.PathValue("code"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "link not found")
		return
	}
	if err != nil {
		s.log.Error("get link", "err", err)
		writeError(w, http.StatusInternalServerError, "lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, s.response(link))
}

func (s *Server) redirect(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	link, err := s.store.Get(r.Context(), code)
	if errors.Is(err, store.ErrNotFound) {
		s.metrics.redirects.WithLabelValues("not_found").Inc()
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.metrics.redirects.WithLabelValues("error").Inc()
		s.log.Error("redirect lookup", "code", code, "err", err)
		http.Error(w, "lookup failed", http.StatusInternalServerError)
		return
	}

	click := store.Click{Code: code, Referrer: referrerHost(r.Referer()), At: s.now()}
	if err := s.store.RecordClick(r.Context(), click); err != nil {
		s.log.Warn("record click", "code", code, "err", err)
	}
	s.metrics.redirects.WithLabelValues("found").Inc()
	http.Redirect(w, r, link.URL, http.StatusFound)
}

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.store.Ping(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, "redis unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) response(l store.Link) linkResponse {
	return linkResponse{Link: l, ShortURL: s.cfg.BaseURL + "/" + l.Code}
}

func validURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func reserved(code string) bool {
	switch strings.ToLower(code) {
	case "api", "healthz", "readyz", "metrics":
		return true
	}
	return false
}

func referrerHost(ref string) string {
	if ref == "" {
		return "direct"
	}
	u, err := url.Parse(ref)
	if err != nil || u.Host == "" {
		return "unknown"
	}
	return strings.TrimPrefix(u.Hostname(), "www.")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
