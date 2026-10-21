package stats

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

type Click struct {
	Code     string
	Referrer string
	At       time.Time
}

type Referrer struct {
	Host   string `json:"host"`
	Clicks int64  `json:"clicks"`
}

type Summary struct {
	Code         string           `json:"code"`
	Total        int64            `json:"total"`
	Daily        map[string]int64 `json:"daily"`
	TopReferrers []Referrer       `json:"top_referrers"`
	LastClickAt  *time.Time       `json:"last_click_at,omitempty"`
}

type Repo struct {
	rdb *redis.Client
}

func NewRepo(rdb *redis.Client) *Repo {
	return &Repo{rdb: rdb}
}

func key(code, part string) string {
	return "stats:" + code + ":" + part
}

func (r *Repo) Apply(ctx context.Context, clicks []Click) error {
	if len(clicks) == 0 {
		return nil
	}
	_, err := r.rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
		for _, c := range clicks {
			day := c.At.UTC().Format(time.DateOnly)
			p.Incr(ctx, key(c.Code, "total"))
			p.HIncrBy(ctx, key(c.Code, "daily"), day, 1)
			p.ZIncrBy(ctx, key(c.Code, "referrers"), 1, c.Referrer)
			p.Set(ctx, key(c.Code, "last"), c.At.UnixMilli(), 0)
		}
		return nil
	})
	return err
}

func (r *Repo) Summary(ctx context.Context, code string, topN int64) (Summary, error) {
	s := Summary{Code: code, Daily: map[string]int64{}, TopReferrers: []Referrer{}}

	var total, last *redis.StringCmd
	var daily *redis.MapStringStringCmd
	var refs *redis.ZSliceCmd
	_, err := r.rdb.Pipelined(ctx, func(p redis.Pipeliner) error {
		total = p.Get(ctx, key(code, "total"))
		daily = p.HGetAll(ctx, key(code, "daily"))
		refs = p.ZRevRangeWithScores(ctx, key(code, "referrers"), 0, topN-1)
		last = p.Get(ctx, key(code, "last"))
		return nil
	})
	if err != nil && !errors.Is(err, redis.Nil) {
		return s, err
	}

	s.Total, _ = total.Int64()
	for day, v := range daily.Val() {
		n, _ := strconv.ParseInt(v, 10, 64)
		s.Daily[day] = n
	}
	for _, z := range refs.Val() {
		s.TopReferrers = append(s.TopReferrers, Referrer{Host: z.Member.(string), Clicks: int64(z.Score)})
	}
	sort.SliceStable(s.TopReferrers, func(i, j int) bool {
		if s.TopReferrers[i].Clicks != s.TopReferrers[j].Clicks {
			return s.TopReferrers[i].Clicks > s.TopReferrers[j].Clicks
		}
		return s.TopReferrers[i].Host < s.TopReferrers[j].Host
	})
	if ms, err := last.Int64(); err == nil {
		t := time.UnixMilli(ms).UTC()
		s.LastClickAt = &t
	}
	return s, nil
}

func (r *Repo) Ping(ctx context.Context) error {
	return r.rdb.Ping(ctx).Err()
}
