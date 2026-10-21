package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newTestStore(t *testing.T) (*Store, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return New(rdb), mr
}

func TestCreateAndGet(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	in := Link{Code: "abc", URL: "https://example.com", CreatedAt: time.Unix(1700000000, 0).UTC()}

	if err := s.Create(ctx, in); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, "abc")
	if err != nil {
		t.Fatal(err)
	}
	if got != in {
		t.Fatalf("got %+v, want %+v", got, in)
	}
}

func TestCreateDuplicate(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	l := Link{Code: "dup", URL: "https://a.com"}
	if err := s.Create(ctx, l); err != nil {
		t.Fatal(err)
	}
	if err := s.Create(ctx, l); !errors.Is(err, ErrExists) {
		t.Fatalf("err = %v, want ErrExists", err)
	}
}

func TestGetMissing(t *testing.T) {
	s, _ := newTestStore(t)
	if _, err := s.Get(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestRecordClick(t *testing.T) {
	s, mr := newTestStore(t)
	at := time.UnixMilli(1700000000123)
	if err := s.RecordClick(context.Background(), Click{Code: "abc", Referrer: "news.ycombinator.com", At: at}); err != nil {
		t.Fatal(err)
	}
	entries, err := mr.Stream(ClickStream)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	want := []string{"code", "abc", "referrer", "news.ycombinator.com", "ts", "1700000000123"}
	got := entries[0].Values
	if len(got) != len(want) {
		t.Fatalf("values = %v", got)
	}
	fields := map[string]string{}
	for i := 0; i < len(got); i += 2 {
		fields[got[i]] = got[i+1]
	}
	for i := 0; i < len(want); i += 2 {
		if fields[want[i]] != want[i+1] {
			t.Errorf("%s = %q, want %q", want[i], fields[want[i]], want[i+1])
		}
	}
}

func TestPing(t *testing.T) {
	s, mr := newTestStore(t)
	if err := s.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	mr.Close()
	if err := s.Ping(context.Background()); err == nil {
		t.Fatal("expected error after redis shutdown")
	}
}
