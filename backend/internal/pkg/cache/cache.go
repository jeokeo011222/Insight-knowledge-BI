// Package cache provides the Redis-backed cache abstraction used for query
// results, thumbnails, filter state and metadata caching.
package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Client wraps the redis client with superset-style helpers.
type Client struct {
	rdb *redis.Client
}

// New connects to Redis at the given URL (redis://host:port/db).
func New(url string) (*Client, error) {
	opt, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	rdb := redis.NewClient(opt)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("ping redis: %w", err)
	}
	return &Client{rdb: rdb}, nil
}

// Redis exposes the underlying client.
func (c *Client) Redis() *redis.Client { return c.rdb }

// Get unmarshals a JSON value stored at key. Returns false when missing.
func (c *Client) Get(ctx context.Context, key string, out any) (bool, error) {
	raw, err := c.rdb.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return false, err
	}
	return true, nil
}

// Set marshals v as JSON and stores it with a TTL.
func (c *Client) Set(ctx context.Context, key string, v any, ttl time.Duration) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.rdb.Set(ctx, key, raw, ttl).Err()
}

// Delete removes keys.
func (c *Client) Delete(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	return c.rdb.Del(ctx, keys...).Err()
}

// Exists reports whether a key exists.
func (c *Client) Exists(ctx context.Context, key string) (bool, error) {
	n, err := c.rdb.Exists(ctx, key).Result()
	return n > 0, err
}

// DeletePrefix removes every key starting with prefix and returns how many were
// deleted. It walks the keyspace with SCAN (never KEYS) so a large cache does
// not block Redis, deleting in small batches.
func (c *Client) DeletePrefix(ctx context.Context, prefix string) (int, error) {
	if prefix == "" {
		return 0, fmt.Errorf("cache: refusing to delete with an empty prefix")
	}
	var cursor uint64
	deleted := 0
	for {
		keys, next, err := c.rdb.Scan(ctx, cursor, prefix+"*", 200).Result()
		if err != nil {
			return deleted, err
		}
		if len(keys) > 0 {
			n, delErr := c.rdb.Del(ctx, keys...).Result()
			if delErr != nil {
				return deleted, delErr
			}
			deleted += int(n)
		}
		cursor = next
		if cursor == 0 {
			return deleted, nil
		}
	}
}

// Close closes the redis connection.
func (c *Client) Close() error { return c.rdb.Close() }
