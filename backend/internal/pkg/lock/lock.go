// Package lock provides a Redis-backed distributed lock (SETNX) used for
// report/alert single-instance execution and CTAS operations.
package lock

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Lock is a held distributed lock.
type Lock struct {
	rdb  *redis.Client
	key  string
	ttl  time.Duration
	rand string
}

// NewLock creates a lock helper bound to a redis client.
func NewLock(rdb *redis.Client) *Lock { return &Lock{rdb: rdb} }

// Acquire tries to take the lock, blocking up to wait.
func (l *Lock) Acquire(ctx context.Context, key string, ttl, wait time.Duration) (*Lock, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return nil, err
	}
	held := &Lock{rdb: l.rdb, key: key, ttl: ttl, rand: hex.EncodeToString(buf)}
	deadline := time.Now().Add(wait)
	for {
		ok, err := l.rdb.SetNX(ctx, key, held.rand, ttl).Result()
		if err != nil {
			return nil, err
		}
		if ok {
			return held, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("could not acquire lock %s", key)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// Release releases the lock if still held (Lua compare-and-delete).
func (l *Lock) Release(ctx context.Context) error {
	script := redis.NewScript(`
if redis.call("get", KEYS[1]) == ARGV[1] then
  return redis.call("del", KEYS[1])
else
  return 0
end`)
	return script.Run(ctx, l.rdb, []string{l.key}, l.rand).Err()
}

// Extend extends the lock TTL if still held.
func (l *Lock) Extend(ctx context.Context, ttl time.Duration) error {
	script := redis.NewScript(`
if redis.call("get", KEYS[1]) == ARGV[1] then
  return redis.call("pexpire", KEYS[1], ARGV[2])
else
  return 0
end`)
	return script.Run(ctx, l.rdb, []string{l.key}, l.rand, ttl.Milliseconds()).Err()
}
