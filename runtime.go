package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	configFileName  = "config.json"
	stateFileName   = "state.json"
	historyFileName = "history.json"
	maxHistory      = 200
)

var ErrRunInProgress = errors.New("a probe run is already in progress")

type Host interface {
	ListAuthFiles(context.Context) ([]AuthFile, error)
	GetAuth(context.Context, string) (json.RawMessage, error)
	HTTPDo(context.Context, HostHTTPRequest) (HostHTTPResponse, error)
	ExecuteModel(context.Context, HostModelExecutionRequest) (HostModelExecutionResponse, error)
	Log(context.Context, string, string, map[string]any)
}

type realHost struct{}

type AuthFile struct {
	ID            string `json:"id,omitempty"`
	AuthIndex     string `json:"auth_index,omitempty"`
	Name          string `json:"name"`
	Type          string `json:"type,omitempty"`
	Provider      string `json:"provider,omitempty"`
	Label         string `json:"label,omitempty"`
	Status        string `json:"status,omitempty"`
	StatusMessage string `json:"status_message,omitempty"`
	Disabled      bool   `json:"disabled,omitempty"`
	Unavailable   bool   `json:"unavailable,omitempty"`
	Email         string `json:"email,omitempty"`
}

type HostHTTPRequest struct {
	Method  string              `json:"method"`
	URL     string              `json:"url"`
	Headers map[string][]string `json:"headers,omitempty"`
	Body    []byte              `json:"body,omitempty"`
}

type HostHTTPResponse struct {
	StatusCode int
	Headers    map[string][]string
	Body       []byte
}

type HostModelExecutionRequest struct {
	EntryProtocol string              `json:"entry_protocol"`
	ExitProtocol  string              `json:"exit_protocol"`
	Model         string              `json:"model"`
	Stream        bool                `json:"stream"`
	Body          []byte              `json:"body"`
	Headers       map[string][]string `json:"headers,omitempty"`
}

type HostModelExecutionResponse struct {
	StatusCode int
	Headers    map[string][]string
	Body       []byte
}

func (r *HostModelExecutionResponse) UnmarshalJSON(data []byte) error {
	var v struct {
		StatusCode  int                 `json:"StatusCode"`
		StatusCode2 int                 `json:"status_code"`
		Headers     map[string][]string `json:"Headers"`
		Headers2    map[string][]string `json:"headers"`
		Body        []byte              `json:"Body"`
		Body2       []byte              `json:"body"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	r.StatusCode = v.StatusCode
	if r.StatusCode == 0 {
		r.StatusCode = v.StatusCode2
	}
	r.Headers = v.Headers
	if r.Headers == nil {
		r.Headers = v.Headers2
	}
	r.Body = v.Body
	if r.Body == nil {
		r.Body = v.Body2
	}
	return nil
}

func (r *HostHTTPResponse) UnmarshalJSON(data []byte) error {
	var v struct {
		StatusCode  int                 `json:"StatusCode"`
		StatusCode2 int                 `json:"status_code"`
		Headers     map[string][]string `json:"Headers"`
		Headers2    map[string][]string `json:"headers"`
		Body        []byte              `json:"Body"`
		Body2       []byte              `json:"body"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	r.StatusCode = v.StatusCode
	if r.StatusCode == 0 {
		r.StatusCode = v.StatusCode2
	}
	r.Headers = v.Headers
	if r.Headers == nil {
		r.Headers = v.Headers2
	}
	r.Body = v.Body
	if r.Body == nil {
		r.Body = v.Body2
	}
	return nil
}

func (realHost) ListAuthFiles(ctx context.Context) ([]AuthFile, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var v struct {
		Files []AuthFile `json:"files"`
	}
	if err := callHost("host.auth.list", map[string]any{}, &v); err != nil {
		return nil, err
	}
	return v.Files, nil
}
func (realHost) GetAuth(ctx context.Context, index string) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var v struct {
		JSON json.RawMessage `json:"json"`
		Auth json.RawMessage `json:"auth"`
		Data json.RawMessage `json:"data"`
	}
	if err := callHost("host.auth.get", map[string]any{"auth_index": index}, &v); err != nil {
		return nil, err
	}
	if len(v.JSON) > 0 {
		return v.JSON, nil
	}
	if len(v.Auth) > 0 {
		return v.Auth, nil
	}
	if len(v.Data) > 0 {
		return v.Data, nil
	}
	return nil, errors.New("credential document is empty")
}
func (realHost) HTTPDo(ctx context.Context, req HostHTTPRequest) (HostHTTPResponse, error) {
	if err := ctx.Err(); err != nil {
		return HostHTTPResponse{}, err
	}
	var v HostHTTPResponse
	if err := callHost("host.http.do", req, &v); err != nil {
		return v, err
	}
	return v, nil
}
func (realHost) ExecuteModel(ctx context.Context, req HostModelExecutionRequest) (HostModelExecutionResponse, error) {
	if err := ctx.Err(); err != nil {
		return HostModelExecutionResponse{}, err
	}
	var v HostModelExecutionResponse
	if err := callHost("host.model.execute", req, &v); err != nil {
		return v, err
	}
	return v, nil
}
func (realHost) Log(ctx context.Context, level, message string, fields map[string]any) {
	if ctx.Err() != nil {
		return
	}
	_ = callHost("host.log", map[string]any{"level": level, "message": message, "fields": fields}, nil)
}

type TargetState struct {
	TargetID              string    `json:"target_id"`
	Name                  string    `json:"name"`
	CheckType             string    `json:"check_type,omitempty"`
	Status                string    `json:"status"`
	NotifiedStatus        string    `json:"notified_status,omitempty"`
	StreakStatus          string    `json:"streak_status,omitempty"`
	StreakCount           int       `json:"streak_count"`
	LastCheckedAt         time.Time `json:"last_checked_at,omitempty"`
	LastChangedAt         time.Time `json:"last_changed_at,omitempty"`
	LastLatencyMS         int64     `json:"last_latency_ms"`
	LastHTTPStatus        int       `json:"last_http_status"`
	LastErrorCode         string    `json:"last_error_code,omitempty"`
	LastError             string    `json:"last_error,omitempty"`
	LastNotificationAt    time.Time `json:"last_notification_at,omitempty"`
	LastNotificationError string    `json:"last_notification_error,omitempty"`
}

type ProbeResult struct {
	TargetID   string    `json:"target_id"`
	Name       string    `json:"name"`
	CheckType  string    `json:"check_type,omitempty"`
	Model      string    `json:"model"`
	Healthy    bool      `json:"healthy"`
	Status     string    `json:"status"`
	HTTPStatus int       `json:"http_status"`
	LatencyMS  int64     `json:"latency_ms"`
	ErrorCode  string    `json:"error_code,omitempty"`
	Error      string    `json:"error,omitempty"`
	CheckedAt  time.Time `json:"checked_at"`
}

type RunRecord struct {
	StartedAt  time.Time     `json:"started_at"`
	FinishedAt time.Time     `json:"finished_at"`
	Trigger    string        `json:"trigger"`
	Total      int           `json:"total"`
	Healthy    int           `json:"healthy"`
	Unhealthy  int           `json:"unhealthy"`
	Results    []ProbeResult `json:"results"`
}

type persistedState struct {
	Version   int                     `json:"version"`
	Targets   map[string]*TargetState `json:"targets"`
	LastRunAt time.Time               `json:"last_run_at,omitempty"`
	NextRunAt time.Time               `json:"next_run_at,omitempty"`
}

type Runtime struct {
	host            Host
	dataDir         string
	mu              sync.RWMutex
	persistMu       sync.Mutex
	runWG           sync.WaitGroup
	config          Config
	state           persistedState
	history         []RunRecord
	running         bool
	runCancel       context.CancelFunc
	runDone         chan struct{}
	stopping        bool
	schedulerCancel context.CancelFunc
	schedulerDone   chan struct{}
}

func defaultDataDir() string {
	if v := strings.TrimSpace(os.Getenv("CPA_MODEL_MONITOR_DATA_DIR")); v != "" {
		return v
	}
	if info, err := os.Stat("/CLIProxyAPI/plugins"); err == nil && info.IsDir() {
		return "/CLIProxyAPI/plugins/cpa-model-health-monitor"
	}
	return filepath.Join("plugins", "cpa-model-health-monitor")
}

func NewRuntime(host Host, dataDir string) *Runtime {
	r := &Runtime{host: host, dataDir: dataDir, config: defaultConfig(), state: persistedState{Version: 1, Targets: map[string]*TargetState{}}}
	r.load()
	return r
}

func (r *Runtime) Configure(raw string) error {
	r.mu.Lock()
	cfg := r.config
	r.mu.Unlock()
	cfg = parseBootstrap(raw, cfg)
	normalized, err := normalizeConfig(cfg)
	if err != nil {
		return fmt.Errorf("invalid plugin configuration: %w", err)
	}
	r.mu.Lock()
	r.config = normalized
	r.mu.Unlock()
	r.restartScheduler()
	return nil
}

func (r *Runtime) UpdateConfig(next Config) error {
	r.mu.RLock()
	old := r.config
	r.mu.RUnlock()
	next = mergeSecrets(next, old)
	normalized, err := normalizeConfig(next)
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.config = normalized
	active := map[string]bool{}
	for _, t := range normalized.Targets {
		active[t.ID] = true
	}
	for id := range r.state.Targets {
		if !active[id] {
			delete(r.state.Targets, id)
		}
	}
	r.mu.Unlock()
	if err := r.persistConfig(); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	r.persistState()
	r.restartScheduler()
	return nil
}

func (r *Runtime) PublicConfig() PublicConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return publicConfig(r.config)
}

func (r *Runtime) AuthFiles() ([]AuthFile, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	files, err := r.host.ListAuthFiles(ctx)
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].AuthIndex < files[j].AuthIndex })
	return files, nil
}

func (r *Runtime) restartScheduler() {
	r.mu.Lock()
	cancel := r.schedulerCancel
	done := r.schedulerDone
	if cancel != nil {
		cancel()
	}
	cfg := r.config
	ctx, newCancel := context.WithCancel(context.Background())
	newDone := make(chan struct{})
	r.schedulerCancel = newCancel
	r.schedulerDone = newDone
	r.mu.Unlock()
	if done != nil {
		<-done
	}
	go r.schedulerLoop(ctx, newDone, cfg)
}

func (r *Runtime) schedulerLoop(ctx context.Context, done chan struct{}, cfg Config) {
	defer close(done)
	if !cfg.Enabled {
		return
	}
	interval := time.Duration(cfg.IntervalMin) * time.Minute
	timer := time.NewTimer(interval)
	defer timer.Stop()
	r.mu.Lock()
	r.state.NextRunAt = time.Now().Add(interval).UTC()
	r.mu.Unlock()
	r.persistState()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			doneRun, err := r.StartRun("schedule")
			if err == nil {
				select {
				case <-doneRun:
				case <-ctx.Done():
					return
				}
			}
			r.mu.Lock()
			latest := r.config
			next := time.Duration(latest.IntervalMin) * time.Minute
			r.state.NextRunAt = time.Now().Add(next).UTC()
			r.mu.Unlock()
			r.persistState()
			timer.Reset(next)
		}
	}
}

func (r *Runtime) StartRun(trigger string) (<-chan struct{}, error) {
	r.mu.Lock()
	if r.running || r.stopping {
		r.mu.Unlock()
		return nil, ErrRunInProgress
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.running = true
	r.runCancel = cancel
	done := make(chan struct{})
	r.runDone = done
	r.runWG.Add(1)
	r.mu.Unlock()
	go func() {
		defer r.runWG.Done()
		record := r.executeRun(ctx, trigger)
		r.mu.Lock()
		r.running = false
		r.runCancel = nil
		r.runDone = nil
		r.state.LastRunAt = record.FinishedAt
		r.history = append([]RunRecord{record}, r.history...)
		if len(r.history) > maxHistory {
			r.history = r.history[:maxHistory]
		}
		r.mu.Unlock()
		r.persistAll()
		close(done)
	}()
	return done, nil
}

func (r *Runtime) executeRun(ctx context.Context, trigger string) RunRecord {
	started := time.Now().UTC()
	r.mu.RLock()
	cfg := r.config
	targets := append([]Target(nil), cfg.Targets...)
	r.mu.RUnlock()
	enabled := targets[:0]
	for _, t := range targets {
		if t.Enabled {
			enabled = append(enabled, t)
		}
	}
	results := make([]ProbeResult, len(enabled))
	sem := make(chan struct{}, cfg.MaxConcurrency)
	var wg sync.WaitGroup
	for i, target := range enabled {
		wg.Add(1)
		go func(i int, t Target) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				results[i] = cancelledResult(t)
				return
			}
			defer func() { <-sem }()
			results[i] = r.probeTarget(ctx, t, cfg.TimeoutSec)
		}(i, target)
	}
	wg.Wait()
	sort.Slice(results, func(i, j int) bool { return results[i].TargetID < results[j].TargetID })
	for _, result := range results {
		r.applyResult(cfg, result)
	}
	record := RunRecord{StartedAt: started, FinishedAt: time.Now().UTC(), Trigger: trigger, Total: len(results), Results: results}
	for _, x := range results {
		if x.Healthy {
			record.Healthy++
		} else {
			record.Unhealthy++
		}
	}
	return record
}

func cancelledResult(t Target) ProbeResult {
	return ProbeResult{TargetID: t.ID, Name: t.Name, CheckType: t.CheckType, Model: t.Model, Status: "cancelled", ErrorCode: "cancelled", Error: "probe cancelled", CheckedAt: time.Now().UTC()}
}

func (r *Runtime) applyResult(cfg Config, result ProbeResult) {
	rawStatus := "down"
	if result.Healthy {
		rawStatus = "up"
	}
	r.mu.Lock()
	s := r.state.Targets[result.TargetID]
	first := s == nil
	if first {
		s = &TargetState{TargetID: result.TargetID, Name: result.Name, Status: "unknown"}
		r.state.Targets[result.TargetID] = s
	}
	s.Name = result.Name
	s.CheckType = result.CheckType
	s.LastCheckedAt = result.CheckedAt
	s.LastLatencyMS = result.LatencyMS
	s.LastHTTPStatus = result.HTTPStatus
	s.LastErrorCode = result.ErrorCode
	s.LastError = result.Error
	if s.StreakStatus == rawStatus {
		s.StreakCount++
	} else {
		s.StreakStatus = rawStatus
		s.StreakCount = 1
	}
	threshold := cfg.FailureThreshold
	if rawStatus == "up" {
		threshold = cfg.RecoveryThreshold
	}
	changed := false
	if first || s.Status == "unknown" {
		s.Status = rawStatus
		s.LastChangedAt = result.CheckedAt
		changed = cfg.NotifyInitial
		if !cfg.NotifyInitial {
			s.NotifiedStatus = rawStatus
		}
	} else if s.Status != rawStatus && s.StreakCount >= threshold {
		s.Status = rawStatus
		s.StreakCount = 0
		s.LastChangedAt = result.CheckedAt
		changed = true
	}
	shouldNotify := cfg.SMTP.Enabled && ((changed && s.NotifiedStatus != s.Status) || (!changed && s.NotifiedStatus != "" && s.NotifiedStatus != s.Status))
	snapshot := *s
	r.mu.Unlock()
	if !shouldNotify {
		return
	}
	err := sendStatusEmail(cfg.SMTP, result, snapshot)
	r.mu.Lock()
	if current := r.state.Targets[result.TargetID]; current != nil && current.Status == snapshot.Status {
		if err == nil {
			current.NotifiedStatus = current.Status
			current.LastNotificationAt = time.Now().UTC()
			current.LastNotificationError = ""
		} else {
			current.LastNotificationError = redactError(err.Error())
		}
	}
	r.mu.Unlock()
	r.persistState()
}

func (r *Runtime) TestEmail() error {
	r.mu.RLock()
	smtpCfg := r.config.SMTP
	r.mu.RUnlock()
	if !smtpCfg.Enabled {
		return errors.New("SMTP is not enabled")
	}
	return sendEmail(smtpCfg, "CPA 模型监控：测试邮件", "SMTP 配置有效，CPA Model Health Monitor 可以发送状态变化通知。")
}

func (r *Runtime) Status() map[string]any {
	r.mu.RLock()
	defer r.mu.RUnlock()
	states := make([]TargetState, 0, len(r.state.Targets))
	for _, v := range r.state.Targets {
		states = append(states, *v)
	}
	sort.Slice(states, func(i, j int) bool { return states[i].TargetID < states[j].TargetID })
	return map[string]any{"version": pluginVersion, "running": r.running, "last_run_at": r.state.LastRunAt, "next_run_at": r.state.NextRunAt, "targets": states, "configured_targets": len(r.config.Targets)}
}
func (r *Runtime) History() []RunRecord {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]RunRecord, len(r.history))
	copy(out, r.history)
	return out
}

func (r *Runtime) Stop() {
	r.mu.Lock()
	r.stopping = true
	schedulerCancel := r.schedulerCancel
	schedulerDone := r.schedulerDone
	runCancel := r.runCancel
	r.schedulerCancel = nil
	r.schedulerDone = nil
	r.mu.Unlock()
	if schedulerCancel != nil {
		schedulerCancel()
	}
	if schedulerDone != nil {
		<-schedulerDone
	}
	if runCancel != nil {
		runCancel()
	}
	r.runWG.Wait()
}

func (r *Runtime) load() {
	if raw, err := os.ReadFile(filepath.Join(r.dataDir, configFileName)); err == nil {
		var cfg Config
		if json.Unmarshal(raw, &cfg) == nil {
			if normalized, e := normalizeConfig(cfg); e == nil {
				r.config = normalized
			}
		}
	}
	if raw, err := os.ReadFile(filepath.Join(r.dataDir, stateFileName)); err == nil {
		var state persistedState
		if json.Unmarshal(raw, &state) == nil {
			if state.Targets == nil {
				state.Targets = map[string]*TargetState{}
			}
			r.state = state
		}
	}
	if raw, err := os.ReadFile(filepath.Join(r.dataDir, historyFileName)); err == nil {
		_ = json.Unmarshal(raw, &r.history)
		if len(r.history) > maxHistory {
			r.history = r.history[:maxHistory]
		}
	}
}
func (r *Runtime) persistAll() {
	r.persistMu.Lock()
	defer r.persistMu.Unlock()
	r.mu.RLock()
	state := r.state
	history := append([]RunRecord(nil), r.history...)
	r.mu.RUnlock()
	r.write(filepath.Join(r.dataDir, stateFileName), state)
	r.write(filepath.Join(r.dataDir, historyFileName), history)
}
func (r *Runtime) persistState() {
	r.persistMu.Lock()
	defer r.persistMu.Unlock()
	r.mu.RLock()
	state := r.state
	r.mu.RUnlock()
	r.write(filepath.Join(r.dataDir, stateFileName), state)
}
func (r *Runtime) persistConfig() error {
	r.persistMu.Lock()
	defer r.persistMu.Unlock()
	r.mu.RLock()
	cfg := r.config
	r.mu.RUnlock()
	return writeJSONAtomic(filepath.Join(r.dataDir, configFileName), cfg)
}
func (r *Runtime) write(path string, v any) {
	if err := writeJSONAtomic(path, v); err != nil {
		r.host.Log(context.Background(), "error", "model health monitor state write failed", map[string]any{"file": filepath.Base(path), "error_code": "state_write_failed"})
	}
}

func writeJSONAtomic(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0o600); err == nil {
		_, err = f.Write(raw)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(name, path)
}

func redactError(s string) string {
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}
