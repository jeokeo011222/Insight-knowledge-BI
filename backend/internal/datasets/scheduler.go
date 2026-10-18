package datasets

import (
	"context"
	"errors"
	"time"

	"go.uber.org/zap"

	"superset/internal/models"
)

// RunScheduler blocks until ctx is cancelled, refreshing every dataset whose
// interval has elapsed.
//
// It is intended for the `beat` process. The loop is deliberately simple
// (poll + dispatch) instead of a per-dataset cron: refresh intervals are
// arbitrary seconds, and a single scan keeps the reconciliation logic in one
// place.
func (s *Service) RunScheduler(ctx context.Context, scan time.Duration) error {
	if s.db == nil {
		return errors.New("datasets: scheduler requires a database")
	}
	if scan <= 0 {
		scan = DefaultScanInterval
	}

	s.log.Info("dataset refresh scheduler started", zap.Duration("scan_interval", scan))
	s.dispatchDue(ctx)

	ticker := time.NewTicker(scan)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.log.Info("dataset refresh scheduler stopped")
			return nil
		case <-ticker.C:
			s.dispatchDue(ctx)
		}
	}
}

// dispatchDue refreshes every dataset that is due, at most
// maxConcurrentRefreshes at a time. Each refresh runs asynchronously so one slow
// source cannot delay the whole scan.
func (s *Service) dispatchDue(ctx context.Context) {
	var rows []models.SqlaTable
	err := s.db.WithContext(ctx).
		Where("refresh_interval_sec > 0 AND deleted_at IS NULL").
		Find(&rows).Error
	if err != nil {
		s.log.Warn("could not load datasets with auto refresh", zap.Error(err))
		return
	}

	sem := make(chan struct{}, maxConcurrentRefreshes)
	now := time.Now()
	for i := range rows {
		dataset := rows[i]
		if !isDue(&dataset, now) {
			continue
		}
		// Re-entrancy guard: the previous run may still be in flight when the
		// next scan fires.
		if _, busy := s.running.LoadOrStore(dataset.ID, struct{}{}); busy {
			continue
		}
		// Claim the slot in the database too, so a second beat instance cannot
		// pick the same dataset. Marking it "running" also makes the UI honest
		// while the refresh is in progress.
		if !s.claim(ctx, &dataset, now) {
			s.running.Delete(dataset.ID)
			continue
		}

		sem <- struct{}{}
		go func() {
			defer func() {
				<-sem
				s.running.Delete(dataset.ID)
			}()
			if _, err := s.Refresh(ctx, dataset.ID, TriggerSchedule); err != nil {
				if errors.Is(err, ErrNotFound) {
					return
				}
				s.log.Warn("scheduled dataset refresh failed",
					zap.Int("dataset_id", dataset.ID),
					zap.String("table", dataset.TableNameStr),
					zap.Error(err))
			}
		}()
	}
}

// isDue reports whether the dataset's interval has elapsed since its last run.
func isDue(t *models.SqlaTable, now time.Time) bool {
	if t.RefreshIntervalSec <= 0 {
		return false
	}
	if t.LastRefreshedAt == nil {
		return true
	}
	return !now.Before(t.LastRefreshedAt.Add(time.Duration(t.RefreshIntervalSec) * time.Second))
}

// claim atomically reserves the dataset for this scheduler. It returns false
// when another worker (or a manual refresh) already took it.
func (s *Service) claim(ctx context.Context, t *models.SqlaTable, now time.Time) bool {
	cutoff := now.Add(-time.Duration(t.RefreshIntervalSec) * time.Second)
	res := s.db.WithContext(ctx).Model(&models.SqlaTable{}).
		Where("id = ? AND (last_refreshed_at IS NULL OR last_refreshed_at <= ?)", t.ID, cutoff).
		Updates(map[string]any{
			"last_refresh_status":  StatusRunning,
			"last_refresh_trigger": TriggerSchedule,
			"last_refreshed_at":    now.UTC(),
		})
	if res.Error != nil {
		s.log.Warn("could not claim dataset for refresh",
			zap.Int("dataset_id", t.ID), zap.Error(res.Error))
		return false
	}
	return res.RowsAffected > 0
}
