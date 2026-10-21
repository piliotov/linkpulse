package consumer

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/piliotov/linkpulse/services/analytics/internal/stats"
)

type recorder struct {
	clicks []stats.Click
	err    error
}

func (r *recorder) Apply(_ context.Context, c []stats.Click) error {
	if r.err != nil {
		return r.err
	}
	r.clicks = append(r.clicks, c...)
	return nil
}

func setup(t *testing.T) (*redis.Client, *recorder, *Consumer) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	rec := &recorder{}
	c := New(rdb, rec, Config{Stream: "clicks", Group: "analytics", Consumer: "test", Block: 10 * time.Millisecond}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := c.EnsureGroup(context.Background()); err != nil {
		t.Fatal(err)
	}
	return rdb, rec, c
}

func add(t *testing.T, rdb *redis.Client, values map[string]any) {
	t.Helper()
	if err := rdb.XAdd(context.Background(), &redis.XAddArgs{Stream: "clicks", Values: values}).Err(); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureGroupIdempotent(t *testing.T) {
	_, _, c := setup(t)
	if err := c.EnsureGroup(context.Background()); err != nil {
		t.Fatalf("second EnsureGroup: %v", err)
	}
}

func TestPollAppliesAndAcks(t *testing.T) {
	rdb, rec, c := setup(t)
	ctx := context.Background()
	add(t, rdb, map[string]any{"code": "abc", "referrer": "google.com", "ts": "1700000000000"})
	add(t, rdb, map[string]any{"code": "abc", "referrer": "", "ts": "1700000001000"})
	add(t, rdb, map[string]any{"referrer": "broken"})

	n, err := c.Poll(ctx, ">")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 || len(rec.clicks) != 2 {
		t.Fatalf("n = %d, clicks = %+v", n, rec.clicks)
	}
	if rec.clicks[1].Referrer != "direct" {
		t.Errorf("empty referrer should become direct, got %q", rec.clicks[1].Referrer)
	}
	if !rec.clicks[0].At.Equal(time.UnixMilli(1700000000000)) {
		t.Errorf("at = %v", rec.clicks[0].At)
	}

	pending, err := rdb.XPending(ctx, "clicks", "analytics").Result()
	if err != nil {
		t.Fatal(err)
	}
	if pending.Count != 0 {
		t.Fatalf("pending = %d, want 0", pending.Count)
	}
}

func TestPollKeepsPendingOnApplyError(t *testing.T) {
	rdb, rec, c := setup(t)
	ctx := context.Background()
	add(t, rdb, map[string]any{"code": "abc", "referrer": "x", "ts": "1"})
	rec.err = errors.New("redis down")

	if _, err := c.Poll(ctx, ">"); err == nil {
		t.Fatal("expected error")
	}
	rec.err = nil
	n, err := c.Poll(ctx, "0")
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("redelivered = %d, want 1", n)
	}
}

func TestPollEmpty(t *testing.T) {
	_, _, c := setup(t)
	n, err := c.Poll(context.Background(), ">")
	if err != nil || n != 0 {
		t.Fatalf("n = %d, err = %v", n, err)
	}
}

func TestRunStopsOnCancel(t *testing.T) {
	rdb, rec, c := setup(t)
	add(t, rdb, map[string]any{"code": "abc", "referrer": "x", "ts": "1"})

	got := make(chan int, 1)
	c.OnProcessed(func(n int) { got <- n })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()

	select {
	case <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("event not processed")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop")
	}
	if len(rec.clicks) != 1 {
		t.Fatalf("clicks = %d", len(rec.clicks))
	}
}
