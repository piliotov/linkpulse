package stats

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newRepo(t *testing.T) *Repo {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return NewRepo(rdb)
}

func TestApplyAndSummary(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	day1 := time.Date(2026, 3, 1, 23, 59, 0, 0, time.UTC)
	day2 := day1.Add(2 * time.Minute)

	err := r.Apply(ctx, []Click{
		{Code: "abc", Referrer: "google.com", At: day1},
		{Code: "abc", Referrer: "direct", At: day1},
		{Code: "abc", Referrer: "google.com", At: day2},
		{Code: "other", Referrer: "direct", At: day2},
	})
	if err != nil {
		t.Fatal(err)
	}

	s, err := r.Summary(ctx, "abc", 10)
	if err != nil {
		t.Fatal(err)
	}
	if s.Total != 3 {
		t.Errorf("total = %d, want 3", s.Total)
	}
	if s.Daily["2026-03-01"] != 2 || s.Daily["2026-03-02"] != 1 {
		t.Errorf("daily = %v", s.Daily)
	}
	if len(s.TopReferrers) != 2 || s.TopReferrers[0] != (Referrer{"google.com", 2}) {
		t.Errorf("referrers = %v", s.TopReferrers)
	}
	if s.LastClickAt == nil || !s.LastClickAt.Equal(day2) {
		t.Errorf("last click = %v", s.LastClickAt)
	}
}

func TestSummaryEmpty(t *testing.T) {
	r := newRepo(t)
	s, err := r.Summary(context.Background(), "none", 5)
	if err != nil {
		t.Fatal(err)
	}
	if s.Total != 0 || len(s.Daily) != 0 || len(s.TopReferrers) != 0 || s.LastClickAt != nil {
		t.Fatalf("summary = %+v", s)
	}
}

func TestSummaryTopN(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	now := time.Now()
	var clicks []Click
	for i, host := range []string{"a.com", "b.com", "c.com"} {
		for range i + 1 {
			clicks = append(clicks, Click{Code: "x", Referrer: host, At: now})
		}
	}
	if err := r.Apply(ctx, clicks); err != nil {
		t.Fatal(err)
	}
	s, err := r.Summary(ctx, "x", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.TopReferrers) != 2 || s.TopReferrers[0].Host != "c.com" || s.TopReferrers[1].Host != "b.com" {
		t.Fatalf("referrers = %v", s.TopReferrers)
	}
}
