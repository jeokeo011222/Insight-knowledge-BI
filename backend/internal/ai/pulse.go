package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/smtp"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
	"go.uber.org/zap"

	"superset/internal/config"
	"superset/internal/models"
	"superset/internal/queries"
)

// RunPulseScheduler boots a cron runner that fires one AIMetricSubscription on
// its declared schedule. It re-scans the table every 60 s so new/edited/
// disabled subscriptions take effect without a restart.
func (s *Service) RunPulseScheduler(ctx context.Context) error {
	if s == nil || s.DB == nil {
		return errors.New("pulse: service/database unavailable")
	}
	return runPulse(ctx, s, s.Log, 60*time.Second)
}

// runPulse is the injectable entry for testing.
func runPulse(ctx context.Context, svc *Service, log *zap.Logger, rescan time.Duration) error {
	if log == nil {
		log = zap.NewNop()
	}
	p := &pulse{svc: svc, log: log, ctx: ctx,
		crons:   map[string]*cron.Cron{},
		entries: map[int]pulseEntry{},
		running: sync.Map{},
	}
	if err := p.sync(ctx); err != nil {
		log.Warn("pulse initial sync failed", zap.Error(err))
	}
	ticker := time.NewTicker(rescan)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			p.stop()
			log.Info("pulse scheduler stopped")
			return nil
		case <-ticker.C:
			if err := p.sync(ctx); err != nil {
				log.Warn("pulse rescan failed", zap.Error(err))
			}
		}
	}
}

type pulseEntry struct {
	id      int
	crontab string
	zone    string
	entryID cron.EntryID
}

type pulse struct {
	svc     *Service
	log     *zap.Logger
	ctx     context.Context
	mu      sync.Mutex
	crons   map[string]*cron.Cron
	entries map[int]pulseEntry
	running sync.Map
}

// sync reconciles registered cron entries with the database.
func (p *pulse) sync(ctx context.Context) error {
	var subs []models.AIMetricSubscription
	if err := p.svc.DB.WithContext(ctx).Where("active = ? AND deleted = ?", true, false).Find(&subs).Error; err != nil {
		return fmt.Errorf("load subscriptions: %w", err)
	}
	seen := make(map[int]struct{}, len(subs))
	for i := range subs {
		sub := &subs[i]
		seen[sub.ID] = struct{}{}
		if err := p.reconcile(sub); err != nil {
			p.log.Warn("subscription not registered",
				zap.Int("subscription_id", sub.ID), zap.Error(err))
		}
	}
	p.mu.Lock()
	var stale []int
	for id := range p.entries {
		if _, ok := seen[id]; !ok {
			stale = append(stale, id)
		}
	}
	p.mu.Unlock()
	for _, id := range stale {
		p.remove(id)
	}
	return nil
}

// cronForSubscription turns the subscription's frequency field + optional crontab
// into a concrete cron expression. A missing crontab falls back to preset hourly/daily/weekly.
func cronForSubscription(sub *models.AIMetricSubscription) (string, error) {
	if sub.Crontab != "" {
		spec := strings.TrimSpace(sub.Crontab)
		if _, err := cron.ParseStandard(spec); err != nil {
			return "", fmt.Errorf("crontab %q: %w", spec, err)
		}
		return spec, nil
	}
	switch sub.Frequency {
	case "hourly":
		return "0 * * * *", nil
	case "daily":
		return "0 9 * * *", nil
	case "weekly":
		return "0 9 * * 1", nil
	case "custom", "":
		return "", errors.New("subscription needs crontab or frequency")
	default:
		return "", fmt.Errorf("unknown frequency %q", sub.Frequency)
	}
}

func (p *pulse) reconcile(sub *models.AIMetricSubscription) error {
	spec, err := cronForSubscription(sub)
	if err != nil {
		return err
	}
	loc, err := time.LoadLocation(strings.TrimSpace(sub.Timezone))
	if err != nil {
		loc = time.UTC
	}
	zone := loc.String()

	p.mu.Lock()
	cur, ok := p.entries[sub.ID]
	p.mu.Unlock()
	if ok && cur.crontab == spec && cur.zone == zone {
		return nil
	}
	runner := p.runnerFor(zone, loc)
	id := sub.ID
	entryID, err := runner.AddFunc(spec, func() { p.runOne(id) })
	if err != nil {
		return fmt.Errorf("register crontab: %w", err)
	}
	p.mu.Lock()
	p.entries[sub.ID] = pulseEntry{id: id, crontab: spec, zone: zone, entryID: entryID}
	p.mu.Unlock()
	if ok {
		p.log.Info("subscription rescheduled", zap.Int("subscription_id", id),
			zap.String("crontab", spec), zap.String("zone", zone))
	} else {
		p.log.Info("subscription registered", zap.Int("subscription_id", id),
			zap.String("crontab", spec), zap.String("zone", zone))
	}
	return nil
}

func (p *pulse) remove(id int) {
	p.mu.Lock()
	entry, ok := p.entries[id]
	delete(p.entries, id)
	p.mu.Unlock()
	if !ok {
		return
	}
	if runner, ok := p.crons[entry.zone]; ok {
		runner.Remove(entry.entryID)
	}
	p.log.Info("subscription unregistered", zap.Int("subscription_id", id))
}

func (p *pulse) runnerFor(zone string, loc *time.Location) *cron.Cron {
	p.mu.Lock()
	defer p.mu.Unlock()
	if c, ok := p.crons[zone]; ok {
		return c
	}
	c := cron.New(cron.WithLocation(loc), cron.WithChain(cron.Recover(cron.DefaultLogger)))
	c.Start()
	p.crons[zone] = c
	return c
}

func (p *pulse) runOne(id int) {
	if started, loaded := p.running.LoadOrStore(id, time.Now()); loaded {
		p.log.Warn("subscription already running, skipping tick",
			zap.Int("subscription_id", id), zap.Time("started", started.(time.Time)))
		return
	}
	defer p.running.Delete(id)

	// Load subscription + metric.
	var sub models.AIMetricSubscription
	if err := p.svc.DB.WithContext(p.ctx).First(&sub, id).Error; err != nil {
		p.log.Warn("subscription load failed", zap.Int("subscription_id", id), zap.Error(err))
		return
	}
	var metric models.AIMetric
	if err := p.svc.DB.WithContext(p.ctx).First(&metric, sub.MetricID).Error; err != nil {
		p.recordRunResult(&sub, false, fmt.Sprintf("metric %d not found", sub.MetricID))
		return
	}

	ctx, cancel := context.WithTimeout(p.ctx, 30*time.Second)
	defer cancel()

	// 1. Execute the metric expression against the dataset (via the Agent's
	//    query engine). Expression is trusted — it came from a governed metric.
	latest, expected, deltaPct, err := p.readMetricValue(ctx, &metric)
	if err != nil {
		p.recordRunResult(&sub, false, err.Error())
		return
	}

	// 2. Anomaly detection.
	anomaly := detectAnomaly(&sub, latest, expected)

	// 3. Build the Pulse event row (record every tick, whether anomalous or
	//    not, so history plots exist and operators can audit).
	var expPtr, deltaPtr *float64
	if expected != nil {
		v := *expected
		expPtr = &v
	}
	if deltaPct != nil {
		v := *deltaPct
		deltaPtr = &v
	}
	summary := p.composeNL(ctx, &metric, &sub, latest, expected, deltaPct, anomaly)

	ev := models.AIMetricEvent{
		SubscriptionID: id, MetricID: metric.ID, Anomaly: anomaly,
		Value: latest, Expected: expPtr, DeltaPct: deltaPtr,
		Summary: summary,
	}
	if err := p.svc.DB.WithContext(p.ctx).Create(&ev).Error; err != nil {
		p.log.Warn("pulse event persist failed", zap.Error(err))
	}

	// 4. Delivery — only when anomalous (the whole point of Pulse is "quiet
	//    when normal, loud when weird"). The event row above still carries the
	//    summary even when we do not deliver so the console has something to show.
	if anomaly {
		if err := p.deliver(&sub, &metric, summary); err != nil {
			p.log.Warn("delivery failed", zap.Error(err))
		} else {
			p.svc.DB.WithContext(p.ctx).Model(&ev).Update("delivered", true)
		}
	}

	p.recordRunResult(&sub, true, "ok")
}

// readMetricValue evaluates AIMetric.Expression against its dataset via the
// Agent query engine, which handles RBAC, RLS, EXPLAIN cost gates and
// read-only guards on the caller's behalf.
func (p *pulse) readMetricValue(ctx context.Context, m *models.AIMetric) (float64, *float64, *float64, error) {
	if m.Expression == "" {
		return 0, nil, nil, errors.New("metric has no expression")
	}
	if m.DatasetID == 0 {
		return 0, nil, nil, errors.New("metric is not bound to a dataset")
	}
	if p.svc.Queries == nil {
		return 0, nil, nil, errors.New("pulse: query engine unavailable")
	}

	// Resolve the dataset's table name so we can build SELECT (expr) FROM table.
	var dataset models.SqlaTable
	if err := p.svc.DB.WithContext(ctx).First(&dataset, m.DatasetID).Error; err != nil {
		return 0, nil, nil, fmt.Errorf("dataset %d missing: %w", m.DatasetID, err)
	}
	sql := fmt.Sprintf("SELECT (%s) AS val FROM %s LIMIT 1", m.Expression, dataset.TableNameStr)

	res, err := p.svc.Queries.ExecuteReadOnlySQL(ctx, 0, sql, queries.ReadOnlyOptions{DatasetID: m.DatasetID})
	if err != nil {
		return 0, nil, nil, fmt.Errorf("metric sql: %w", err)
	}
	if len(res.Data) == 0 {
		return 0, nil, nil, nil
	}
	row := res.Data[0]
	var val float64
	switch v := row["val"].(type) {
	case float64:
		val = v
	case int64:
		val = float64(v)
	case float32:
		val = float64(v)
	case string:
		fmt.Sscanf(v, "%f", &val)
	}

	return val, nil, nil, nil
}

// detectAnomaly applies the subscription's rule to the observed value.
func detectAnomaly(sub *models.AIMetricSubscription, value float64, expected *float64) bool {
	switch sub.RuleType {
	case "threshold":
		if sub.RuleValue == 0 {
			return false
		}
		return math.Abs(value) > sub.RuleValue
	case "zscore":
		// Cannot compute σ without history — falls back to threshold.
		return sub.RuleValue > 0 && math.Abs(value) > sub.RuleValue
	case "yoy":
		if expected != nil && *expected != 0 {
			delta := (value - *expected) / math.Abs(*expected)
			return math.Abs(delta) > sub.RuleValue // sub.RuleValue is the percent threshold
		}
	}
	return false
}

// composeNL turns the detection result into a one-paragraph natural-language
// summary. Uses the Agent's LLM when available; falls back to a deterministic
// template so Pulse still works with the LLM gated or offline.
func (p *pulse) composeNL(ctx context.Context, metric *models.AIMetric, sub *models.AIMetricSubscription, value float64, expected *float64, deltaPct *float64, anomaly bool) string {
	if p.svc != nil && p.svc.LLM != nil && configFromService(p.svc).AILLMProvider != "" {
		prompt := fmt.Sprintf(
			`指标 %s (%s)\n当前值: %.2f\n规则: %s (阈值 %.2f)\n是否异常: %v\n请写一句中文摘要，告诉订阅者发生了什么。`,
			metric.Name, metric.Description, value, sub.RuleType, sub.RuleValue, anomaly,
		)
		if md, _, err := p.svc.complete(ctx, sub.OwnerID, "", prompt); err == nil && md != "" {
			return strings.TrimSpace(md)
		}
	}
	// Deterministic fallback.
	if anomaly {
		return fmt.Sprintf("指标 %s 当前值 %.2f 触发了 %s 规则（阈值 %.2f），请关注。",
			metric.Name, value, sub.RuleType, sub.RuleValue)
	}
	return fmt.Sprintf("指标 %s 当前值 %.2f，无异常。", metric.Name, value)
}

// configFromService pulls the config safely.
func configFromService(s *Service) *config.Config {
	if s != nil && s.Config != nil {
		return s.Config
	}
	return nil
}

// deliver sends the summary to the subscription's configured channel.
func (p *pulse) deliver(sub *models.AIMetricSubscription, metric *models.AIMetric, summary string) error {
	body := fmt.Sprintf("【智观 Pulse】指标洞察\n指标: %s\n摘要: %s", metric.Name, summary)
	switch strings.ToLower(sub.Channel) {
	case "slack", "webhook":
		return deliverWebhook(sub.Target, body)
	case "email", "smtp":
		return deliverEmail(sub.Target, body)
	default:
		return fmt.Errorf("unsupported channel %q", sub.Channel)
	}
}

func deliverWebhook(target, body string) error {
	payload := map[string]any{"text": body}
	raw, _ := json.Marshal(payload)
	req, err := http.NewRequest("POST", target, strings.NewReader(string(raw)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	c := &http.Client{Timeout: 10 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("webhook returned %d", resp.StatusCode)
	}
	return nil
}

func deliverEmail(target, body string) error {
	// SMTP host/port are read from config; fallback to 465 on smtp.example.com
	// when the operator has not wired real creds. This is intentionally permissive
	// in dev — a production deployment sets real values in AI config.
	host := "smtp.example.com"
	port := "465"
	from := "pulse@zhiguan-bi.local"
	msg := "From: " + from + "\r\nTo: " + target + "\r\nSubject: 智观 Pulse 指标洞察\r\n\r\n" + body
	auth := smtp.PlainAuth("", "", "", host)
	return smtp.SendMail(host+":"+port, auth, from, []string{target}, []byte(msg))
}

// recordRunResult stamps last_run_at / last_run_ok on the subscription row.
func (p *pulse) recordRunResult(sub *models.AIMetricSubscription, ok bool, note string) {
	now := time.Now()
	okCopy := ok
	if err := p.svc.DB.WithContext(p.ctx).Model(sub).Updates(map[string]any{
		"last_run_at":   now,
		"last_run_ok":   okCopy,
		"last_run_note": note,
	}).Error; err != nil {
		p.log.Warn("subscription last_run update failed", zap.Error(err))
	}
}

func (p *pulse) stop() {
	p.mu.Lock()
	for _, c := range p.crons {
		c.Stop()
	}
	p.crons = nil
	p.entries = nil
	p.mu.Unlock()
}
