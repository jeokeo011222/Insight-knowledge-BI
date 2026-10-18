package thumbnails

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"superset/internal/config"
	"superset/internal/security"
)

// thumbnailDirEnv overrides the cache directory.
const thumbnailDirEnv = "SUPERSET_THUMBNAIL_DIR"

// digestPattern guards cache lookups against path traversal.
var digestPattern = regexp.MustCompile(`^[a-f0-9]{8,64}$`)

// Service captures and caches thumbnails on disk. Concurrent requests for the
// same key share a single browser run.
type Service struct {
	cacheDir string
	baseURL  string
	secret   string
	log      *zap.Logger

	mu       sync.Mutex
	inflight map[string]*capture
	memory   map[string][]byte
}

// capture is one in-flight screenshot shared by all waiters.
type capture struct {
	wg     sync.WaitGroup
	data   []byte
	err    error
	digest string
}

// NewService builds the thumbnail service from configuration.
func NewService(cfg *config.Config, log *zap.Logger) *Service {
	if log == nil {
		log = zap.NewNop()
	}
	dir := strings.TrimSpace(os.Getenv(thumbnailDirEnv))
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "superset-thumbnails")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Warn("thumbnail cache directory unavailable", zap.String("dir", dir), zap.Error(err))
	}
	s := &Service{
		cacheDir: dir,
		baseURL:  baseURL(cfg),
		log:      log,
		inflight: make(map[string]*capture),
		memory:   make(map[string][]byte),
	}
	if cfg != nil {
		s.secret = cfg.SecretKey
	}
	return s
}

// baseURL resolves the front end origin used for screenshots.
func baseURL(cfg *config.Config) string {
	raw := ""
	port := 8088
	if cfg != nil {
		raw = strings.TrimSpace(cfg.WebdriverBaseURL)
		if cfg.WebserverPort > 0 {
			port = cfg.WebserverPort
		}
	}
	if raw == "" {
		return fmt.Sprintf("http://127.0.0.1:%d", port)
	}
	raw = strings.TrimRight(raw, "/")
	// 0.0.0.0 is a listen address; browsers must dial loopback instead.
	raw = strings.Replace(raw, "//0.0.0.0", "//127.0.0.1", 1)
	return raw
}

// BaseURL returns the configured front end origin (no trailing slash).
func (s *Service) BaseURL() string { return s.baseURL }

// CacheDir returns the on-disk cache directory.
func (s *Service) CacheDir() string { return s.cacheDir }

// Digest returns the 16 hex character cache key for a logical key.
func (s *Service) Digest(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])[:16]
}

// Token issues an access token for a user so the headless browser can boot the
// SPA authenticated.
func (s *Service) Token(userID int, ttl time.Duration) (string, error) {
	if s.secret == "" {
		return "", fmt.Errorf("thumbnails: SUPERSET_SECRET_KEY is not configured")
	}
	if userID <= 0 {
		return "", fmt.Errorf("thumbnails: a user is required to issue a screenshot token")
	}
	seconds := int(ttl.Seconds())
	if seconds <= 0 {
		seconds = 3600
	}
	pair, err := security.IssueTokenPair(s.secret, userID, seconds)
	if err != nil {
		return "", fmt.Errorf("thumbnails: issue token: %w", err)
	}
	return pair.AccessToken, nil
}

// Get returns cached PNG bytes for a digest.
func (s *Service) Get(digest string) ([]byte, bool) {
	if !digestPattern.MatchString(digest) {
		return nil, false
	}
	s.mu.Lock()
	if data, ok := s.memory[digest]; ok {
		s.mu.Unlock()
		return data, true
	}
	s.mu.Unlock()

	data, err := os.ReadFile(s.path(digest))
	if err != nil || len(data) == 0 {
		return nil, false
	}
	s.mu.Lock()
	s.memory[digest] = data
	s.mu.Unlock()
	return data, true
}

// Ensure returns the cached PNG for key, capturing it (once) when missing.
func (s *Service) Ensure(ctx context.Context, key string, target func() Target) (string, []byte, error) {
	digest := s.Digest(key)
	if data, ok := s.Get(digest); ok {
		return digest, data, nil
	}
	return s.captureOnce(ctx, digest, target)
}

// Force discards any cached image for key and captures a fresh one.
func (s *Service) Force(ctx context.Context, key string, target func() Target) (string, []byte, error) {
	digest := s.Digest(key)
	s.mu.Lock()
	delete(s.memory, digest)
	s.mu.Unlock()
	_ = os.Remove(s.path(digest))
	return s.captureOnce(ctx, digest, target)
}

// captureOnce runs one screenshot per digest, sharing the result with every
// concurrent caller.
func (s *Service) captureOnce(ctx context.Context, digest string, target func() Target) (string, []byte, error) {
	s.mu.Lock()
	if c, ok := s.inflight[digest]; ok {
		s.mu.Unlock()
		c.wg.Wait()
		return c.digest, c.data, c.err
	}
	c := &capture{digest: digest}
	c.wg.Add(1)
	s.inflight[digest] = c
	s.mu.Unlock()

	started := time.Now()
	data, err := Capture(ctx, target())
	c.data = data
	c.err = err

	if err == nil {
		if werr := s.store(digest, data); werr != nil {
			s.log.Warn("thumbnail cache write failed", zap.String("digest", digest), zap.Error(werr))
		}
	} else {
		s.log.Warn("thumbnail capture failed", zap.String("digest", digest), zap.Error(err))
	}
	s.log.Debug("thumbnail captured",
		zap.String("digest", digest),
		zap.Int("bytes", len(data)),
		zap.Duration("elapsed", time.Since(started)))

	c.wg.Done()
	s.mu.Lock()
	delete(s.inflight, digest)
	s.mu.Unlock()
	return c.digest, c.data, c.err
}

// store persists the PNG and keeps a memory copy.
func (s *Service) store(digest string, data []byte) error {
	if err := os.MkdirAll(s.cacheDir, 0o755); err != nil {
		return err
	}
	tmp := s.path(digest) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path(digest)); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	s.mu.Lock()
	s.memory[digest] = data
	s.mu.Unlock()
	return nil
}

// path maps a digest onto the cache file.
func (s *Service) path(digest string) string {
	return filepath.Join(s.cacheDir, digest+".png")
}
