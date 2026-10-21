package consumer

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/piliotov/linkpulse/services/analytics/internal/stats"
)

type Applier interface {
	Apply(ctx context.Context, clicks []stats.Click) error
}

type Config struct {
	Stream   string
	Group    string
	Consumer string
	Batch    int64
	Block    time.Duration
}

type Consumer struct {
	rdb       *redis.Client
	applier   Applier
	cfg       Config
	log       *slog.Logger
	processed func(n int)
}

func New(rdb *redis.Client, a Applier, cfg Config, log *slog.Logger) *Consumer {
	if cfg.Batch == 0 {
		cfg.Batch = 100
	}
	if cfg.Block == 0 {
		cfg.Block = 5 * time.Second
	}
	return &Consumer{rdb: rdb, applier: a, cfg: cfg, log: log, processed: func(int) {}}
}

func (c *Consumer) OnProcessed(fn func(n int)) {
	c.processed = fn
}

func (c *Consumer) EnsureGroup(ctx context.Context) error {
	err := c.rdb.XGroupCreateMkStream(ctx, c.cfg.Stream, c.cfg.Group, "0").Err()
	if err != nil && !strings.HasPrefix(err.Error(), "BUSYGROUP") {
		return err
	}
	return nil
}

func (c *Consumer) Run(ctx context.Context) error {
	if err := c.EnsureGroup(ctx); err != nil {
		return err
	}
	if _, err := c.Poll(ctx, "0"); err != nil && ctx.Err() == nil {
		c.log.Warn("drain pending", "err", err)
	}
	for ctx.Err() == nil {
		if _, err := c.Poll(ctx, ">"); err != nil && ctx.Err() == nil {
			c.log.Error("poll", "err", err)
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
			}
		}
	}
	return nil
}

func (c *Consumer) Poll(ctx context.Context, start string) (int, error) {
	block := c.cfg.Block
	if start != ">" {
		block = -1
	}
	res, err := c.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    c.cfg.Group,
		Consumer: c.cfg.Consumer,
		Streams:  []string{c.cfg.Stream, start},
		Count:    c.cfg.Batch,
		Block:    block,
	}).Result()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}

	var ids []string
	var clicks []stats.Click
	for _, s := range res {
		for _, msg := range s.Messages {
			ids = append(ids, msg.ID)
			click, ok := parse(msg.Values)
			if !ok {
				c.log.Warn("skipping malformed event", "id", msg.ID)
				continue
			}
			clicks = append(clicks, click)
		}
	}
	if len(ids) == 0 {
		return 0, nil
	}
	if err := c.applier.Apply(ctx, clicks); err != nil {
		return 0, err
	}
	if err := c.rdb.XAck(ctx, c.cfg.Stream, c.cfg.Group, ids...).Err(); err != nil {
		return 0, err
	}
	c.processed(len(clicks))
	return len(clicks), nil
}

func parse(v map[string]any) (stats.Click, bool) {
	code, _ := v["code"].(string)
	if code == "" {
		return stats.Click{}, false
	}
	ref, _ := v["referrer"].(string)
	if ref == "" {
		ref = "direct"
	}
	tsRaw, _ := v["ts"].(string)
	ms, err := strconv.ParseInt(tsRaw, 10, 64)
	if err != nil {
		return stats.Click{}, false
	}
	return stats.Click{Code: code, Referrer: ref, At: time.UnixMilli(ms)}, true
}
