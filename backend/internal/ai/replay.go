package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"superset/internal/ai/registry"
	"superset/internal/models"
)

// Answer replay (回答记忆).
//
// A BI Agent is asked the same question more than once — a refresh, a second
// look a minute later, a colleague reading over a shoulder. Re-deriving it costs
// tokens, seconds, and a fresh chance to answer differently. This file stores
// the statement a question resolved to, together with the answer it produced,
// and reuses them while the underlying data has not moved.
//
// Three properties keep the shortcut honest:
//
//   - it is per user. RLS predicates and column masking differ per person, so an
//     answer must never be replayed across users;
//   - it is conditional on the data. The rows the recorded SQL returns are
//     hashed and compared; if the numbers moved, the record is discarded and the
//     question is answered from scratch. A replay can never show a stale figure;
//   - it is bounded. Records expire (AI_REPLAY_TTL_SEC, 5 minutes by default),
//     so "same question" never quietly means "last month's answer".
//
// The SQL itself is re-executed rather than cached as rows: the replay is only
// the decision not to ask the model again, never a decision not to ask the
// warehouse. RBAC, RLS, masking and the row cap therefore apply exactly as in a
// normal run.

// replayTTL is the lifetime of a replay record; 0 disables the feature.
func (s *Service) replayTTL() time.Duration {
	if s.Config == nil || s.Config.AIReplyReplayTTLSec <= 0 {
		return 0
	}
	return time.Duration(s.Config.AIReplyReplayTTLSec) * time.Second
}

// lookupReplay finds a live record for this exact question.
func (s *Service) lookupReplay(ctx context.Context, userID, agentID, datasetID int, question string) *models.AIRunReplay {
	if s.DB == nil || datasetID <= 0 {
		return nil
	}
	var row models.AIRunReplay
	err := s.DB.WithContext(ctx).
		Where(`user_id = ? AND agent_id = ? AND dataset_id = ? AND question_hash = ? AND prompt_version = ?
			AND expires_on > ?`, userID, agentID, datasetID, questionFingerprint(question), agentPromptVersion, time.Now()).
		First(&row).Error
	if err != nil {
		if s.Log != nil {
			s.Log.Debug("replay lookup missed",
				zap.Int("user_id", userID), zap.Int("agent_id", agentID),
				zap.Int("dataset_id", datasetID), zap.Error(err))
		}
		return nil
	}
	return &row
}

// resultFingerprint hashes a result set so a replay can tell "same numbers" from
// "numbers moved".
//
// Row *order* is deliberately normalised away: PostgreSQL does not promise an
// order without ORDER BY, and even with one, ties can come back in either
// order. Hashing the raw array made the replay miss on queries whose data had
// not changed at all — a false negative that quietly turned a deterministic
// answer back into a model call. The hash therefore covers the row count plus
// the sorted multiset of the first rows, which is what "the numbers are the
// same" actually means.
func resultFingerprint(rows any) string {
	if rows == nil {
		return ""
	}
	b, err := json.Marshal(rows)
	if err != nil {
		return ""
	}
	var decoded []any
	if err := json.Unmarshal(b, &decoded); err != nil {
		// Not a row array (a caller handed us something else): hash as-is.
		return hashText(string(b))
	}
	const maxRows = 500
	head := decoded
	if len(head) > maxRows {
		head = head[:maxRows]
	}
	lines := make([]string, 0, len(head))
	for _, row := range head {
		encoded, mErr := json.Marshal(row) // map keys are sorted by encoding/json
		if mErr != nil {
			continue
		}
		lines = append(lines, string(encoded))
	}
	sort.Strings(lines)
	return hashText(fmt.Sprintf("%d|%s", len(decoded), strings.Join(lines, "\n")))
}

// storeReplay records a finished run for later replay. It deliberately refuses
// runs that touched anything but read: replaying a write would be a side effect
// the user did not ask for.
func (s *Service) storeReplay(ctx context.Context, run *models.AIAgentRun, plan PlanEvent, sql, answer, resultHash string, rowCount int, artifacts map[string]any) {
	ttl := s.replayTTL()
	if ttl <= 0 || s.DB == nil || run == nil || run.DatasetID == nil || *run.DatasetID <= 0 {
		if s.Log != nil {
			s.Log.Debug("replay not recorded: disabled or no dataset attached",
				zap.Duration("ttl", ttl), zap.Int("dataset_id", datasetIDOfRun(run)))
		}
		return
	}
	if strings.TrimSpace(sql) == "" || strings.TrimSpace(answer) == "" {
		if s.Log != nil {
			s.Log.Debug("replay not recorded: run produced no SQL or no answer")
		}
		return
	}
	// Without a result fingerprint there is nothing to compare against on the
	// way back in, so the record could never be used. Storing it would only
	// inflate the table with rows that are guaranteed to miss.
	if strings.TrimSpace(resultHash) == "" {
		if s.Log != nil {
			s.Log.Debug("replay not recorded: no result fingerprint to verify a replay against")
		}
		return
	}
	var writes int64
	if err := s.DB.WithContext(ctx).Model(&models.AIToolCallLog{}).
		Where("run_id = ? AND side_effect <> ?", run.ID, "read").Count(&writes).Error; err == nil && writes > 0 {
		if s.Log != nil {
			s.Log.Debug("replay not recorded: run performed a write", zap.Int64("writes", writes))
		}
		return
	}
	rawArtifacts, _ := json.Marshal(artifacts)
	rawPlan, _ := json.Marshal(plan)
	now := time.Now()
	sql = unwrapExecutorSQL(sql)
	row := &models.AIRunReplay{
		UserID:        run.UserID,
		AgentID:       agentIDOfRun(ctx),
		DatasetID:     *run.DatasetID,
		QuestionHash:  questionFingerprint(run.Question),
		PromptVersion: agentPromptVersion,
		Question:      run.Question,
		RunID:         run.ID,
		SQLQuery:      sql,
		SQLHash:       hashText(sql),
		Answer:        answer,
		Artifacts:     string(rawArtifacts),
		PlanJSON:      string(rawPlan),
		ResultHash:    resultHash,
		RowCount:      rowCount,
		TokenUsage:    run.TokenUsage,
		LatencyMS:     run.LatencyMS,
		ExpiresOn:     now.Add(ttl),
	}
	// Upsert on the replay key: asking the same thing again refreshes the record
	// instead of accumulating rows that compete to answer it.
	err := s.DB.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "user_id"}, {Name: "agent_id"}, {Name: "dataset_id"},
			{Name: "question_hash"}, {Name: "prompt_version"},
		},
		DoUpdates: clause.AssignmentColumns([]string{
			"question", "run_id", "sql_query", "sql_hash", "answer", "artifacts",
			"plan_json", "result_hash", "row_count", "token_usage", "latency_ms",
			"expires_on", "changed_on",
		}),
	}).Create(row).Error
	if err != nil && s.Log != nil {
		// Warn, not Debug: a replay layer that silently records nothing looks
		// exactly like one that works until someone measures the hit rate.
		s.Log.Warn("replay record not stored", zap.Error(err))
		return
	}
	// Opportunistic cleanup so an expired record never accumulates: the delete
	// is indexed on expires_on and bounded.
	s.DB.WithContext(ctx).Where("expires_on < ?", now.Add(-24*time.Hour)).Delete(&models.AIRunReplay{})
}

// hitReplay bumps the reuse counter and extends the window, in one statement.
//
// The increment is done in SQL (`hits + 1`) rather than by writing back the
// value that was read during the lookup: two people asking the same question at
// the same moment would otherwise both write `hits+1` and count as one hit.
func (s *Service) hitReplay(ctx context.Context, rec *models.AIRunReplay) {
	if s.DB == nil || rec == nil || rec.ID == 0 {
		return
	}
	updates := map[string]any{"hits": gorm.Expr("hits + 1")}
	// A question asked twice in a row is likely to be asked once more, and the
	// result hash guard still protects correctness.
	if ttl := s.replayTTL(); ttl > 0 {
		updates["expires_on"] = time.Now().Add(ttl)
	}
	s.DB.WithContext(ctx).Model(&models.AIRunReplay{}).Where("id = ?", rec.ID).Updates(updates)
}

// agentIDOfRun pulls the agent id out of the run scope carried by ctx.
func agentIDOfRun(ctx context.Context) int {
	if scope := runScopeFrom(ctx); scope != nil {
		return scope.AgentID
	}
	return 0
}

// tryReplay is the runtime hook: it either returns a complete run result built
// from the recorded SQL, or (false) so the caller proceeds with the model.
func (r *Runtime) tryReplay(runCtx context.Context, userID int, sessionID, question string, actx registry.AIContext, emit func(Event)) (*RunResult, bool) {
	s := r.svc
	datasetID := actxDatasetID(actx)
	if datasetID <= 0 || s.DB == nil || strings.TrimSpace(question) == "" || s.replayTTL() <= 0 {
		return nil, false
	}
	agentID := agentIDOfRun(runCtx)
	// A follow-up question ("那上个月呢？") is meaningless out of its session, and
	// its literal text can collide with a standalone question that means
	// something else. Replay is therefore limited to the first turn of a
	// session; anything with prior context goes to the model.
	if len(s.sessionHistory(runCtx, userID, sessionID)) > 0 {
		return nil, false
	}
	rec := s.lookupReplay(runCtx, userID, agentID, datasetID, question)
	if rec == nil || strings.TrimSpace(rec.SQLQuery) == "" || strings.TrimSpace(rec.Answer) == "" {
		if s.Log != nil {
			s.Log.Debug("replay skip: empty record")
		}
		return nil, false
	}

	args, _ := json.Marshal(map[string]any{"sql": rec.SQLQuery, "dataset_id": datasetID})
	started := time.Now()
	out, err := s.InvokeTool(runCtx, userID, 0, "query_executor", args)
	if err != nil || out == nil {
		if s.Log != nil {
			s.Log.Debug("replay skip: recorded SQL no longer runs", zap.Error(err))
		}
		return nil, false
	}
	result, _ := out.Output.(map[string]any)
	if result == nil {
		if s.Log != nil {
			s.Log.Debug("replay skip: unexpected output type")
		}
		return nil, false
	}
	// The safety catch: replay only while the numbers are the same.
	if rec.ResultHash == "" || resultFingerprint(result["rows"]) != rec.ResultHash {
		if s.Log != nil {
			s.Log.Info("replay skipped: result changed since it was recorded")
		}
		return nil, false
	}

	latency := int(time.Since(started).Milliseconds())
	run := s.Audit.StartRun(runCtx, userID, sessionID, actx, question)
	run.ModelName = "replay"
	plan := PlanEvent{
		RunID: run.ID, SessionID: sessionID, Source: "replay",
		Thought: "命中回答记忆：复用上次问法已验证的 SQL，未重新调用模型",
	}
	if rec.PlanJSON != "" {
		var stored PlanEvent
		if json.Unmarshal([]byte(rec.PlanJSON), &stored) == nil && len(stored.Steps) > 0 {
			plan.Steps = stored.Steps
		}
	}
	emitEvent(emit, EventPlan, plan)
	s.Audit.SavePlan(runCtx, run, plan)

	emitEvent(emit, EventAct, ActEvent{StepNo: 1, Kind: "tool", Name: "query_executor", Args: args})
	s.Audit.LogToolCall(runCtx, run.ID, 1, "tool", "query_executor", "read", args, result, nil, nil, "ok", "", latency)
	emitEvent(emit, EventObserve, ObserveEvent{StepNo: 1, Status: "ok", Result: result, Replayed: true})

	artifacts := map[string]any{}
	if rec.Artifacts != "" {
		_ = json.Unmarshal([]byte(rec.Artifacts), &artifacts)
	}
	if sql, ok := result["sql"].(string); ok && sql != "" {
		artifacts["sql"] = sql
		run.GeneratedSQL = sql
		run.SQLHash = hashText(sql)
	}
	run.TokenUsage = 0
	run.StepCount = 1
	run.ToolCallCount = 1
	run.LatencyMS = latency
	s.Audit.FinishRun(runCtx, run, "done", 0, 1, 1, rec.Answer, "", latency)
	s.hitReplay(runCtx, rec)

	emitEvent(emit, EventAnswer, AnswerEvent{
		RunID: run.ID, SessionID: sessionID, Markdown: rec.Answer, Artifacts: artifacts,
		TokenUsage: 0, StepCount: 1, Replayed: true,
	})
	return &RunResult{
		RunID: run.ID, SessionID: sessionID, Status: "done",
		Markdown: rec.Answer, Artifacts: artifacts, StepCount: 1,
		TokenUsage: 0, SQLHash: run.SQLHash, Replayed: true,
	}, true
}

// datasetIDOfRun is a nil-safe accessor for logging.
func datasetIDOfRun(run *models.AIAgentRun) int {
	if run == nil || run.DatasetID == nil {
		return 0
	}
	return *run.DatasetID
}

// unwrapExecutorSQL strips the row-limit wrapper the query engine adds:
//
//	SELECT * FROM (
//	<statement>
//	) AS __ai_sub
//	LIMIT n
//
// A replay must re-execute the statement the model authored, not the wrapper —
// the wrapper is itself a `SELECT *`, which the SQL guard rejects on the way
// back in. Records written directly from the tool output already carry the
// unwrapped form; this keeps older rows working too.
func unwrapExecutorSQL(sql string) string {
	trimmed := strings.TrimSpace(sql)
	upper := strings.ToUpper(trimmed)
	if !strings.HasPrefix(upper, "SELECT * FROM (") {
		return trimmed
	}
	close := strings.Index(upper, "\n) AS __AI_SUB")
	if close < 0 {
		return trimmed
	}
	inner := trimmed[strings.Index(trimmed, "(")+1 : close]
	return strings.TrimSpace(inner)
}
