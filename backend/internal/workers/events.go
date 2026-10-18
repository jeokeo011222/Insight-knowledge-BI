package workers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// Event is a single async-query notification. Every event carries a user_id;
// user_id 0 means "broadcast to every connected client".
type Event map[string]any

// BroadcastUserID is the sentinel user id used for events addressed to all
// subscribers.
const BroadcastUserID = 0

// Bus fan-outs events between the worker and the web processes. With Redis it
// uses the pub/sub channel "superset:events"; without Redis it degrades to an
// in-process broadcast so that a single-process deployment still works.
type Bus struct {
	rdb *redis.Client

	mu     sync.RWMutex
	subs   map[chan Event]struct{}
	closed bool
}

// NewBus builds a bus for the given Redis URL. An unreachable Redis is not an
// error: the bus falls back to in-process delivery (Redis() reports nil).
func NewBus(redisURL string) (*Bus, error) {
	b := &Bus{subs: make(map[chan Event]struct{})}
	redisURL = strings.TrimSpace(redisURL)
	if redisURL == "" {
		return b, nil
	}
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("workers: parse redis url: %w", err)
	}
	rdb := redis.NewClient(opt)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		return b, nil
	}
	b.rdb = rdb
	return b, nil
}

// Redis reports whether the bus is backed by Redis.
func (b *Bus) Redis() bool { return b != nil && b.rdb != nil }

// Publish delivers an event to userID (0 broadcasts). The user_id key is
// always written into the payload so consumers can filter.
func (b *Bus) Publish(ctx context.Context, userID int, event map[string]any) error {
	if b == nil {
		return errors.New("workers: event bus is not configured")
	}
	payload := make(Event, len(event)+1)
	for k, v := range event {
		payload[k] = v
	}
	payload["user_id"] = userID

	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("workers: marshal event: %w", err)
	}
	if b.rdb != nil {
		if err := b.rdb.Publish(ctx, EventChannel, raw).Err(); err != nil {
			return fmt.Errorf("workers: publish event: %w", err)
		}
		return nil
	}
	b.fanout(payload)
	return nil
}

// Subscribe returns a channel of events plus an idempotent cancel function.
// The channel is closed when ctx is cancelled or the cancel function runs.
func (b *Bus) Subscribe(ctx context.Context) (<-chan Event, func(), error) {
	if b == nil {
		return nil, nil, errors.New("workers: event bus is not configured")
	}
	out := make(chan Event, 64)
	var once sync.Once

	if b.rdb != nil {
		sub := b.rdb.Subscribe(ctx, EventChannel)
		if _, err := sub.Receive(ctx); err != nil {
			_ = sub.Close()
			return nil, nil, fmt.Errorf("workers: subscribe: %w", err)
		}
		stop := make(chan struct{})
		cancel := func() {
			once.Do(func() {
				close(stop)
				_ = sub.Close()
			})
		}
		go func() {
			defer close(out)
			msgs := sub.Channel()
			for {
				select {
				case <-stop:
					return
				case <-ctx.Done():
					return
				case msg, ok := <-msgs:
					if !ok {
						return
					}
					var ev Event
					if err := json.Unmarshal([]byte(msg.Payload), &ev); err != nil {
						continue
					}
					select {
					case out <- ev:
					default: // slow consumer: drop rather than block the bus
					}
				}
			}
		}()
		return out, cancel, nil
	}

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil, nil, errors.New("workers: event bus is closed")
	}
	b.subs[out] = struct{}{}
	b.mu.Unlock()

	cancel := func() {
		once.Do(func() {
			b.mu.Lock()
			if _, ok := b.subs[out]; ok {
				delete(b.subs, out)
				close(out)
			}
			b.mu.Unlock()
		})
	}
	go func() {
		<-ctx.Done()
		cancel()
	}()
	return out, cancel, nil
}

// fanout delivers an event to in-process subscribers.
func (b *Bus) fanout(ev Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

// Close releases the Redis connection and closes local subscribers.
func (b *Bus) Close() error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	b.closed = true
	for ch := range b.subs {
		close(ch)
		delete(b.subs, ch)
	}
	b.mu.Unlock()
	if b.rdb != nil {
		return b.rdb.Close()
	}
	return nil
}
