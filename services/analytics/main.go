package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/errgroup"

	"github.com/piliotov/linkpulse/services/analytics/internal/api"
	"github.com/piliotov/linkpulse/services/analytics/internal/consumer"
	"github.com/piliotov/linkpulse/services/analytics/internal/stats"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("analytics stopped", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	addr := env("LISTEN_ADDR", ":8081")
	hostname, _ := os.Hostname()

	rdb := redis.NewClient(&redis.Options{
		Addr:     env("REDIS_ADDR", "localhost:6379"),
		Password: os.Getenv("REDIS_PASSWORD"),
	})
	defer func() { _ = rdb.Close() }()

	repo := stats.NewRepo(rdb)
	srv := api.NewServer(repo, log)
	cons := consumer.New(rdb, repo, consumer.Config{
		Stream:   env("CLICK_STREAM", "clicks"),
		Group:    env("CONSUMER_GROUP", "analytics"),
		Consumer: env("CONSUMER_NAME", hostname),
	}, log)
	cons.OnProcessed(func(n int) { srv.Clicks.Add(float64(n)) })

	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		log.Info("listening", "addr", addr)
		if err := httpSrv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	})
	g.Go(func() error {
		return cons.Run(ctx)
	})
	g.Go(func() error {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return httpSrv.Shutdown(shutdownCtx)
	})
	return g.Wait()
}

func env(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
