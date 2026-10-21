package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/piliotov/linkpulse/services/analytics/internal/stats"
)

var validCode = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)

type Repo interface {
	Summary(ctx context.Context, code string, topN int64) (stats.Summary, error)
	Ping(ctx context.Context) error
}

type Server struct {
	repo     Repo
	log      *slog.Logger
	metrics  http.Handler
	Clicks   prometheus.Counter
	requests *prometheus.CounterVec
}

func NewServer(repo Repo, log *slog.Logger) *Server {
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	s := &Server{
		repo:    repo,
		log:     log,
		metrics: promhttp.HandlerFor(reg, promhttp.HandlerOpts{}),
		Clicks: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "analytics_clicks_processed_total",
			Help: "Click events consumed from the stream.",
		}),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "HTTP requests by route and status.",
		}, []string{"route", "method", "status"}),
	}
	reg.MustRegister(s.Clicks, s.requests)
	return s
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/stats/{code}", s.stats)
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /readyz", s.readyz)
	mux.Handle("GET /metrics", s.metrics)
	return s.instrument(mux)
}

func (s *Server) stats(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	if !validCode.MatchString(code) {
		writeError(w, http.StatusBadRequest, "invalid code")
		return
	}
	top := int64(10)
	if v := r.URL.Query().Get("top"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 || n > 100 {
			writeError(w, http.StatusBadRequest, "top must be between 1 and 100")
			return
		}
		top = n
	}
	sum, err := s.repo.Summary(r.Context(), code, top)
	if err != nil {
		s.log.Error("summary", "code", code, "err", err)
		writeError(w, http.StatusInternalServerError, "could not load stats")
		return
	}
	writeJSON(w, http.StatusOK, sum)
}

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.repo.Ping(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, "redis unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (s *Server) instrument(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		route := r.Pattern
		if route == "" {
			route = "unmatched"
		}
		s.requests.WithLabelValues(route, r.Method, strconv.Itoa(rec.status)).Inc()
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
