package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"time"

	"superset/internal/ai"
	"superset/internal/config"
	"superset/internal/models"
	"superset/internal/server"
)

// System overview (系统运行总览).
//
// 系统管理 lands on a console-style page: one screen that answers "is every
// module working, are the data sources reachable, what does the UI look
// like". Everything here is read-only and cheap — no probe is triggered, we
// only report what the last health pass recorded, so opening the page never
// hammers the engines.

// processStartedAt approximates the server start time. Package init runs when
// the process boots, which is close enough for an uptime readout.
var processStartedAt = time.Now()

// moduleStatus is the health vocabulary shared with the frontend.
const (
	moduleOK   = "ok"
	moduleWarn = "warn"
	moduleDown = "down"
	moduleOff  = "off"
)

// SystemModule is one row of the 模块运行情况 card.
type SystemModule struct {
	Key       string `json:"key"`
	Name      string `json:"name"`
	Category  string `json:"category"`
	Status    string `json:"status"`
	Detail    string `json:"detail"`
	LatencyMs int    `json:"latency_ms"`
	// Metric is a short headline number rendered next to the status dot,
	// e.g. "3" with Unit "个待执行".
	Metric int    `json:"metric"`
	Unit   string `json:"unit"`
}

// SystemDatabaseItem is one engine row of the 数据库连接情况 card.
type SystemDatabaseItem struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Engine    string `json:"engine"`
	Status    string `json:"status"`
	LatencyMs int    `json:"latency_ms"`
	CheckedAt string `json:"checked_at"`
	Error     string `json:"error"`
}

// SystemDatabaseSummary aggregates the probes.
type SystemDatabaseSummary struct {
	Total   int                  `json:"total"`
	OK      int                  `json:"ok"`
	Failing int                  `json:"failing"`
	Unknown int                  `json:"unknown"`
	Items   []SystemDatabaseItem `json:"items"`
}

// SystemThemeInfo summarises the appearance slot without shipping theme JSON.
type SystemThemeInfo struct {
	Total        int    `json:"total"`
	SystemThemes int    `json:"system_themes"`
	DefaultName  string `json:"default_name"`
	DarkName     string `json:"dark_name"`
}

// SystemCounts is the asset census shown under the KPI strip.
type SystemCounts struct {
	Users      int64 `json:"users"`
	Roles      int64 `json:"roles"`
	Dashboards int64 `json:"dashboards"`
	Charts     int64 `json:"charts"`
	Datasets   int64 `json:"datasets"`
	Screens    int64 `json:"screens"`
	Queries24h int64 `json:"queries_24h"`
}

// SystemAppInfo describes the running binary.
type SystemAppInfo struct {
	Version    string `json:"version"`
	GoVersion  string `json:"go_version"`
	StartedAt  string `json:"started_at"`
	UptimeSec  int64  `json:"uptime_sec"`
	PID        int    `json:"pid"`
	Hostname   string `json:"hostname"`
	ConfigPath string `json:"config_path"`
}

// SystemOverview is the payload of GET /system/overview.
type SystemOverview struct {
	App       SystemAppInfo         `json:"app"`
	Modules   []SystemModule        `json:"modules"`
	Databases SystemDatabaseSummary `json:"databases"`
	Theme     SystemThemeInfo       `json:"theme"`
	Counts    SystemCounts          `json:"counts"`
	// Warnings are operator-facing sentences that need action (e.g. cache
	// disabled, failing engine). The page renders them as an alert stack.
	Warnings []string `json:"warnings"`
}

// overview handles GET /system/overview.
func (a *systemAPI) overview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	out := SystemOverview{
		App:     a.appInfo(),
		Modules: a.modules(ctx),
		Theme:   a.themeInfo(ctx),
		Counts:  a.counts(ctx),
	}
	out.Databases = a.databaseHealth(ctx)
	out.Warnings = a.warnings(ctx, out)
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": out})
}

func (a *systemAPI) appInfo() SystemAppInfo {
	host, _ := os.Hostname()
	return SystemAppInfo{
		Version:    server.Version,
		GoVersion:  runtime.Version(),
		StartedAt:  processStartedAt.Format(time.RFC3339),
		UptimeSec:  int64(time.Since(processStartedAt).Seconds()),
		PID:        os.Getpid(),
		Hostname:   host,
		ConfigPath: config.ConfigFilePath(),
	}
}

// modules assembles the per-module status rows.
func (a *systemAPI) modules(ctx context.Context) []SystemModule {
	var out []SystemModule

	// --- metadata database -------------------------------------------------
	dbMod := SystemModule{
		Key: "metadata_db", Name: "元数据库", Category: "infra", Status: moduleOK,
	}
	start := time.Now()
	sqlDB, err := a.deps.DB.DB()
	if err != nil {
		dbMod.Status = moduleDown
		dbMod.Detail = err.Error()
	} else if err := sqlDB.PingContext(ctx); err != nil {
		dbMod.Status = moduleDown
		dbMod.Detail = err.Error()
	} else {
		stats := sqlDB.Stats()
		dbMod.LatencyMs = int(time.Since(start).Milliseconds())
		dbMod.Detail = fmt.Sprintf("PostgreSQL · %d/%d 连接在用 · 等待 %d",
			stats.InUse, stats.OpenConnections, stats.WaitCount)
		dbMod.Metric = stats.InUse
		dbMod.Unit = "连接在用"
	}
	out = append(out, dbMod)

	// --- cache -------------------------------------------------------------
	cacheMod := SystemModule{Key: "cache", Name: "查询缓存", Category: "infra", Status: moduleOff,
		Detail: "未配置 Redis，缓存与异步队列不可用"}
	if a.deps.Cache != nil && a.deps.Cache.Redis() != nil {
		start := time.Now()
		if err := a.deps.Cache.Redis().Ping(ctx).Err(); err != nil {
			cacheMod.Status = moduleDown
			cacheMod.Detail = "Redis 不可达: " + truncate(err.Error(), 160)
		} else {
			cacheMod.Status = moduleOK
			cacheMod.LatencyMs = int(time.Since(start).Milliseconds())
			keys, _ := a.deps.Cache.Redis().DBSize(ctx).Result()
			cacheMod.Detail = fmt.Sprintf("Redis 正常 · %d 个键", keys)
			cacheMod.Metric = int(keys)
			cacheMod.Unit = "缓存键"
		}
	}
	out = append(out, cacheMod)

	// --- async queue (asynq via Redis) ------------------------------------
	queueMod := SystemModule{Key: "queue", Name: "异步任务队列", Category: "runtime", Status: moduleOff,
		Detail: "未启用（SQL 异步查询将降级为进程内协程）"}
	if a.deps.Queue != nil && a.deps.Queue.Redis() != nil {
		start := time.Now()
		if err := a.deps.Queue.Redis().Ping(ctx).Err(); err != nil {
			queueMod.Status = moduleDown
			queueMod.Detail = "队列后端不可达: " + truncate(err.Error(), 160)
		} else {
			queueMod.Status = moduleOK
			queueMod.LatencyMs = int(time.Since(start).Milliseconds())
			pending, running := countQueries(ctx, a, "pending", "running")
			queueMod.Detail = fmt.Sprintf("asynq 就绪 · 待执行 %d / 执行中 %d", pending, running)
			queueMod.Metric = pending + running
			queueMod.Unit = "在途查询"
		}
	}
	out = append(out, queueMod)

	// --- query engine ------------------------------------------------------
	qMod := SystemModule{Key: "query", Name: "查询执行", Category: "runtime", Status: moduleOK}
	var failed24h int64
	a.deps.DB.WithContext(ctx).Model(&models.Query{}).
		Where("status = ? AND created_on >= ?", "failed", time.Now().Add(-24*time.Hour)).
		Count(&failed24h)
	var total24h int64
	a.deps.DB.WithContext(ctx).Model(&models.Query{}).
		Where("created_on >= ?", time.Now().Add(-24*time.Hour)).Count(&total24h)
	qMod.Detail = fmt.Sprintf("近 24 小时 %d 次查询 · 失败 %d", total24h, failed24h)
	qMod.Metric = int(total24h)
	qMod.Unit = "次/24h"
	if failed24h > 0 && total24h > 0 {
		rate := float64(failed24h) / float64(total24h)
		if rate >= 0.2 {
			qMod.Status = moduleWarn
		}
	}
	out = append(out, qMod)

	// --- schedules (reports + alerts) --------------------------------------
	sMod := SystemModule{Key: "schedules", Name: "报告与告警", Category: "runtime", Status: moduleOK}
	var active, failedSched int64
	a.deps.DB.WithContext(ctx).Model(&models.ReportSchedule{}).
		Where("active = ?", true).Count(&active)
	a.deps.DB.WithContext(ctx).Model(&models.ReportSchedule{}).
		Where("active = ? AND last_state IN ?", true, []string{"error", "failed"}).Count(&failedSched)
	if active == 0 {
		sMod.Status = moduleOff
		sMod.Detail = "暂无启用的调度任务"
	} else {
		sMod.Detail = fmt.Sprintf("%d 个启用 · %d 个最近一次失败", active, failedSched)
		sMod.Metric = int(active)
		sMod.Unit = "启用中"
		if failedSched > 0 {
			sMod.Status = moduleWarn
		}
	}
	out = append(out, sMod)

	// --- AI plane ----------------------------------------------------------
	aiMod := SystemModule{Key: "ai", Name: "AI 智能体", Category: "runtime", Status: moduleOff}
	aiEnabled := false
	if a.deps.Config != nil {
		aiEnabled = ai.EnabledForDB(ctx, a.deps.DB, a.deps.Config.FeatureEnabled("AI_AGENT"))
	}
	if aiEnabled {
		var providers, agents int64
		a.deps.DB.WithContext(ctx).Model(&models.AIProvider{}).
			Where("enabled = ?", true).Count(&providers)
		a.deps.DB.WithContext(ctx).Model(&models.AIAgent{}).
			Where("enabled = ?", true).Count(&agents)
		aiMod.Metric = int(agents)
		aiMod.Unit = "个智能体"
		if providers == 0 {
			aiMod.Status = moduleWarn
			aiMod.Detail = "已开启但没有可用的模型供应商"
		} else {
			aiMod.Status = moduleOK
			aiMod.Detail = fmt.Sprintf("%d 个供应商 · %d 个智能体", providers, agents)
		}
	} else {
		aiMod.Detail = "AI 能力未开启（AI_AGENT=off）"
	}
	out = append(out, aiMod)

	// --- screenshot renderer ----------------------------------------------
	shot := SystemModule{Key: "thumbnails", Name: "截图渲染", Category: "runtime", Status: moduleOff,
		Detail: "未配置 WEBDRIVER_BASEURL，图表/看板导出图片不可用"}
	if a.deps.Config != nil && a.deps.Config.WebdriverBaseURL != "" {
		shot.Status = moduleOK
		shot.Detail = "无头浏览器已配置 · " + a.deps.Config.WebdriverBaseURL
	}
	out = append(out, shot)

	return out
}

// databaseHealth summarises the persisted probe results.
func (a *systemAPI) databaseHealth(ctx context.Context) SystemDatabaseSummary {
	var rows []models.Database
	if err := a.deps.DB.WithContext(ctx).Order("id asc").Find(&rows).Error; err != nil {
		return SystemDatabaseSummary{}
	}
	summary := SystemDatabaseSummary{Total: len(rows), Items: make([]SystemDatabaseItem, 0, len(rows))}
	for i := range rows {
		row := rows[i]
		item := SystemDatabaseItem{
			ID: row.ID, Name: row.DatabaseName, Engine: engineOf(row.SQLAlchemyURI), Status: "unknown",
		}
		if row.LastProbeOK != nil {
			item.LatencyMs = row.LastProbeMs
			if *row.LastProbeOK {
				item.Status = moduleOK
				summary.OK++
			} else {
				item.Status = moduleDown
				item.Error = row.LastProbeError
				summary.Failing++
			}
		} else {
			summary.Unknown++
		}
		if row.LastProbeAt != nil {
			item.CheckedAt = row.LastProbeAt.Format(time.RFC3339)
		}
		summary.Items = append(summary.Items, item)
	}
	return summary
}

func (a *systemAPI) themeInfo(ctx context.Context) SystemThemeInfo {
	var total, systemThemes int64
	a.deps.DB.WithContext(ctx).Model(&models.Theme{}).Count(&total)
	a.deps.DB.WithContext(ctx).Model(&models.Theme{}).Where("is_system = ?", true).Count(&systemThemes)
	info := SystemThemeInfo{Total: int(total), SystemThemes: int(systemThemes)}
	// Report the slots as configured. A deployment may deliberately point the
	// default slot at the dark theme, so the label stays "default" rather than
	// "default light" — the console must not contradict the theme library.
	var def models.Theme
	if err := a.deps.DB.WithContext(ctx).
		Where("is_system_default = ?", true).First(&def).Error; err == nil {
		info.DefaultName = def.ThemeName
	}
	var dark models.Theme
	if err := a.deps.DB.WithContext(ctx).Where("is_system_dark = ?", true).First(&dark).Error; err == nil {
		info.DarkName = dark.ThemeName
	}
	return info
}

func (a *systemAPI) counts(ctx context.Context) SystemCounts {
	count := func(model any, where string, args ...any) int64 {
		var n int64
		q := a.deps.DB.WithContext(ctx).Model(model)
		if where != "" {
			q = q.Where(where, args...)
		}
		q.Count(&n)
		return n
	}
	return SystemCounts{
		Users:      count(&models.User{}, ""),
		Roles:      count(&models.Role{}, ""),
		Dashboards: count(&models.Dashboard{}, ""),
		Charts:     count(&models.Slice{}, ""),
		Datasets:   count(&models.SqlaTable{}, ""),
		Screens:    count(&models.Screen{}, ""),
		Queries24h: count(&models.Query{}, "created_on >= ?", time.Now().Add(-24*time.Hour)),
	}
}

// warnings turns the collected state into actionable sentences.
func (a *systemAPI) warnings(ctx context.Context, o SystemOverview) []string {
	out := make([]string, 0, 4)
	if o.Databases.Failing > 0 {
		out = append(out, fmt.Sprintf("%d 个数据源连接失败，请到「数据库」页体检", o.Databases.Failing))
	}
	if o.Databases.Unknown == o.Databases.Total && o.Databases.Total > 0 {
		out = append(out, "数据源尚未体检，健康状态未知")
	}
	for _, m := range o.Modules {
		if m.Status == moduleDown {
			out = append(out, fmt.Sprintf("%s 不可用：%s", m.Name, m.Detail))
		} else if m.Key == "schedules" && m.Status == moduleWarn {
			out = append(out, "存在最近一次执行失败的调度任务")
		} else if m.Key == "ai" && m.Status == moduleWarn {
			out = append(out, "AI 智能体已开启，但缺少可用的模型供应商")
		}
	}
	return out
}

// countQueries counts queries in the given statuses. Only the last 24 hours
// are considered: a row left in `pending` by a previous deployment (no worker
// process attached) must not be reported as queue backlog forever.
func countQueries(ctx context.Context, a *systemAPI, statuses ...string) (pending, running int) {
	since := time.Now().Add(-24 * time.Hour)
	for _, status := range statuses {
		var n int64
		a.deps.DB.WithContext(ctx).Model(&models.Query{}).
			Where("status = ? AND created_on >= ?", status, since).Count(&n)
		switch status {
		case "pending":
			pending = int(n)
		case "running":
			running = int(n)
		}
	}
	return pending, running
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
