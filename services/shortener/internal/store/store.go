package store

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

const ClickStream = "clicks"

var (
	ErrNotFound = errors.New("link not found")
	ErrExists   = errors.New("code already taken")
)

type Link struct {
	Code      string    `json:"code"`
	URL       string    `json:"url"`
	CreatedAt time.Time `json:"created_at"`
}

type Click struct {
	Code     string
	Referrer string
	At       time.Time
}

type Store struct {
	rdb       *redis.Client
	streamLen int64
}

func New(rdb *redis.Client) *Store {
	return &Store{rdb: rdb, streamLen: 100000}
}

func linkKey(code string) string {
	return "link:" + code
}

func (s *Store) Create(ctx context.Context, l Link) error {
	data, err := json.Marshal(l)
	if err != nil {
		return err
	}
	ok, err := s.rdb.SetNX(ctx, linkKey(l.Code), data, 0).Result()
	if err != nil {
		return err
	}
	if !ok {
		return ErrExists
	}
	return nil
}

func (s *Store) Get(ctx context.Context, code string) (Link, error) {
	var l Link
	data, err := s.rdb.Get(ctx, linkKey(code)).Bytes()
	if errors.Is(err, redis.Nil) {
		return l, ErrNotFound
	}
	if err != nil {
		return l, err
	}
	err = json.Unmarshal(data, &l)
	return l, err
}

func (s *Store) RecordClick(ctx context.Context, c Click) error {
	return s.rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: ClickStream,
		MaxLen: s.streamLen,
		Approx: true,
		Values: map[string]any{
			"code":     c.Code,
			"referrer": c.Referrer,
			"ts":       strconv.FormatInt(c.At.UnixMilli(), 10),
		},
	}).Err()
}

func (s *Store) Ping(ctx context.Context) error {
	return s.rdb.Ping(ctx).Err()
}
