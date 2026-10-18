package reports

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"superset/internal/config"
	"superset/internal/models"
)

// rescanInterval is how often the scheduler reloads report_schedule.
const rescanInterval = 60 * time.Second

// RunScheduler blocks while scheduling every active alert/report. It returns
// when ctx is cancelled.
func RunScheduler(ctx context.Context, cfg *config.Config, log *zap.Logger, db *gorm.DB) error {
	if db == nil {
		return errors.New("reports: scheduler requires a database")
	}
	if log == nil {
		log = zap.NewNop()
	}
	ex := NewExecutor(cfg, log, db)
	return runScheduler(ctx, ex, log, rescanInterval)
}

// runScheduler is RunScheduler with an injectable executor and rescan period.
func runScheduler(ctx context.Context, ex *Executor, log *zap.Logger, interval time.Duration) error {
	if interval <= 0 {
		interval = rescanInterval
	}
	s := &scheduler{
		executor: ex,
		log:      log,
		crons:    map[string]*cron.Cron{},
		entries:  map[int]scheduledEntry{},
		ctx:      ctx,
	}
	if err := s.sync(ctx); err != nil {
		log.Error("initial schedule load failed", zap.Error(err))
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.stop()
			log.Info("report scheduler stopped")
			return nil
		case <-ticker.C:
			if err := s.sync(ctx); err != nil {
				log.Warn("schedule rescan failed", zap.Error(err))
			}
		}
	}
}

// scheduledEntry is one registered cron entry.
type scheduledEntry struct {
	id       int
	crontab  string
	timezone string
	zone     string
	entryID  cron.EntryID
}

// scheduler keeps one cron runner per timezone so each schedule can use its own
// location.
type scheduler struct {
	executor *Executor
	log      *zap.Logger
	ctx      context.Context

	mu      sync.Mutex
	crons   map[string]*cron.Cron
	entries map[int]scheduledEntry

	running sync.Map // schedule id -> start time, guards re-entrancy
}

// sync reconciles the registered jobs with the database.
func (s *scheduler) sync(ctx context.Context) error {
	var schedules []models.ReportSchedule
	if err := s.executor.db.WithContext(ctx).
		Where("active = ? AND deleted = ?", true, false).
		Order("id asc").Find(&schedules).Error; err != nil {
		return fmt.Errorf("reports: load schedules: %w", err)
	}

	seen := make(map[int]struct{}, len(schedules))
	for i := range schedules {
		sc := schedules[i]
		seen[sc.ID] = struct{}{}
		if err := s.reconcile(sc); err != nil {
			s.log.Warn("schedule not registered",
				zap.Int("schedule_id", sc.ID),
				zap.String("crontab", sc.Crontab),
				zap.Error(err))
		}
	}

	s.mu.Lock()
	stale := make([]int, 0)
	for id := range s.entries {
		if _, ok := seen[id]; !ok {
			stale = append(stale, id)
		}
	}
	s.mu.Unlock()
	for _, id := range stale {
		s.remove(id)
	}
	return nil
}

// reconcile adds, updates or keeps the cron entry for one schedule.
func (s *scheduler) reconcile(sc models.ReportSchedule) error {
	spec := normalizeCrontab(sc.Crontab)
	if spec == "" {
		return errors.New("crontab is empty")
	}
	if _, err := cron.ParseStandard(spec); err != nil {
		return fmt.Errorf("invalid crontab %q: %w", sc.Crontab, err)
	}
	loc := loadLocation(sc.Timezone)
	zone := loc.String()

	s.mu.Lock()
	cur, ok := s.entries[sc.ID]
	s.mu.Unlock()
	if ok && cur.crontab == spec && cur.zone == zone {
		return nil
	}

	runner := s.runnerFor(zone, loc)
	id := sc.ID
	entryID, err := runner.AddFunc(spec, func() { s.runJob(id) })
	if err != nil {
		return fmt.Errorf("register crontab %q: %w", spec, err)
	}

	s.mu.Lock()
	s.entries[sc.ID] = scheduledEntry{
		id: id, crontab: spec, timezone: sc.Timezone, zone: zone, entryID: entryID,
	}
	s.mu.Unlock()

	if ok {
		s.log.Info("schedule rescheduled",
			zap.Int("schedule_id", id), zap.String("crontab", spec), zap.String("timezone", zone))
	} else {
		s.log.Info("schedule registered",
			zap.Int("schedule_id", id), zap.String("crontab", spec), zap.String("timezone", zone))
	}
	return nil
}

// remove drops the cron entry for a schedule.
func (s *scheduler) remove(scheduleID int) {
	s.mu.Lock()
	entry, ok := s.entries[scheduleID]
	delete(s.entries, scheduleID)
	s.mu.Unlock()
	if !ok {
		return
	}
	if runner, ok := s.crons[entry.zone]; ok {
		runner.Remove(entry.entryID)
	}
	s.log.Info("schedule unregistered", zap.Int("schedule_id", scheduleID))
}

// runnerFor returns the cron runner for a timezone, starting it on first use.
func (s *scheduler) runnerFor(zone string, loc *time.Location) *cron.Cron {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.crons[zone]; ok {
		return c
	}
	c := cron.New(
		cron.WithLocation(loc),
		cron.WithChain(cron.Recover(cron.DefaultLogger)),
	)
	c.Start()
	s.crons[zone] = c
	return c
}

// runJob executes one schedule, skipping when the previous run is still busy.
func (s *scheduler) runJob(scheduleID int) {
	if started, loaded := s.running.LoadOrStore(scheduleID, time.Now()); loaded {
		s.log.Warn("schedule already running, skipping tick",
			zap.Int("schedule_id", scheduleID),
			zap.Time("started", started.(time.Time)))
		if _, err := s.executor.RecordSkipped(s.ctx, scheduleID, "already running"); err != nil {
			s.log.Warn("record skipped run", zap.Int("schedule_id", scheduleID), zap.Error(err))
		}
		return
	}
	defer s.running.Delete(scheduleID)

	if _, err := s.executor.Execute(s.ctx, scheduleID, TriggerScheduler); err != nil {
		s.log.Warn("scheduled execution failed",
			zap.Int("schedule_id", scheduleID), zap.Error(err))
	}
}

// stop halts every cron runner and waits for running jobs.
func (s *scheduler) stop() {
	s.mu.Lock()
	runners := make([]*cron.Cron, 0, len(s.crons))
	for _, c := range s.crons {
		runners = append(runners, c)
	}
	s.crons = map[string]*cron.Cron{}
	s.entries = map[int]scheduledEntry{}
	s.mu.Unlock()

	for _, c := range runners {
		<-c.Stop().Done()
	}
}

// normalizeCrontab trims a crontab and strips the "@" macros' trailing spaces.
func normalizeCrontab(raw string) string {
	out := make([]rune, 0, len(raw))
	lastSpace := false
	for _, r := range raw {
		if r == ' ' || r == '\t' {
			if !lastSpace {
				out = append(out, ' ')
			}
			lastSpace = true
			continue
		}
		out = append(out, r)
		lastSpace = false
	}
	return trimSpaces(string(out))
}

// trimSpaces removes leading and trailing spaces.
func trimSpaces(s string) string {
	start, end := 0, len(s)
	for start < end && s[start] == ' ' {
		start++
	}
	for end > start && s[end-1] == ' ' {
		end--
	}
	return s[start:end]
}

// loadLocation resolves a schedule timezone, falling back to UTC.
func loadLocation(name string) *time.Location {
	name = trimSpaces(name)
	if name == "" {
		return time.UTC
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.UTC
	}
	return loc
}
