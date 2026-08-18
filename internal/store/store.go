// Package store 是 P4 Web 控制台的 SQLite 持久化层，按设计方案 10.1 节
// 「SQLite + 本地文件系统」的首版方案分工：SQLite 只存协调元数据（供应商/
// 模型登记、TEST_RUN 生命周期、REPORT 指针），请求/响应体、压测原始日志等
// 大体积产物继续走本地文件系统（沿用 P1-P3 已有的 JSON 文件约定），
// TestRun.CaseResultsPath/BenchmarkResultPath 只是指向这些文件的引用，
// 不在数据库里重复存一份，避免同一份数据出现两个真相来源。
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"

	"github.com/leoobai/modeltestbed/internal/model"
)

// modelKeyPattern 限制 ModelKey 只能是字母/数字开头，其余允许字母数字和
// . _ : -——internal/api 的编排逻辑会直接拿 ModelKey 当文件系统目录名用
// （ReportsRoot/<model_key>/...），不加这道白名单校验的话，注册一个形如
// "../../etc" 的 model_key 就能让后续所有该模型的测试结果文件写到
// ReportsRoot 之外的任意路径（路径穿越），不是防御性的过度设计。
var modelKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]*$`)

const schema = `
CREATE TABLE IF NOT EXISTS providers (
	id      TEXT PRIMARY KEY,
	name    TEXT NOT NULL,
	contact TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS models (
	id                       TEXT PRIMARY KEY,
	provider_id              TEXT NOT NULL REFERENCES providers(id),
	model_key                TEXT NOT NULL,
	endpoint_via_tokenpanel  TEXT NOT NULL,
	capability_json          TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS test_runs (
	id                     TEXT PRIMARY KEY,
	model_id               TEXT NOT NULL REFERENCES models(id),
	suite_id               TEXT NOT NULL,
	status                 TEXT NOT NULL,
	started_at             TEXT NOT NULL,
	finished_at            TEXT NOT NULL DEFAULT '',
	case_results_path      TEXT NOT NULL DEFAULT '',
	benchmark_result_path  TEXT NOT NULL DEFAULT '',
	error_message          TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS reports (
	id            TEXT PRIMARY KEY,
	test_run_id   TEXT NOT NULL REFERENCES test_runs(id),
	generated_at  TEXT NOT NULL,
	verdict       TEXT NOT NULL,
	html_ref      TEXT NOT NULL
);
`

// Store 包装一个 SQLite 连接，提供 03 节协调实体（PROVIDER/MODEL/TEST_RUN/
// REPORT）的 CRUD。方法均以 struct 值传入/传出，不对外暴露 *sql.DB，调用方
// 不需要写 SQL。
type Store struct {
	db *sql.DB
}

// Open 打开（必要时创建）path 处的 SQLite 数据库文件，并确保表结构存在。
// path 传 ":memory:" 可用于测试。
//
// foreign_keys/busy_timeout 通过 DSN 查询参数设置，而不是 Open 之后单独
// db.Exec 一次 PRAGMA——modernc.org/sqlite（本项目用的纯 Go、无 CGO 驱动）
// 这两个 PRAGMA 是逐连接（per-connection）生效的，写进 DSN 才能保证
// database/sql 在连接池里创建任何新连接时都自动重新应用，不会出现"连接
// 失效被重建后 PRAGMA 悄悄失效"的边界情况。
func Open(path string) (*Store, error) {
	dsn := path + "?_foreign_keys=on&_busy_timeout=5000"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite db: %w", err)
	}
	// SQLite 对并发写入的支持有限，本系统首版本来就是单进程内串行执行任务
	// （见设计方案 6.3 节压测互斥锁、10.1 节"不引入任务队列"），限制单连接
	// 让本进程内的读写天然串行，降低（而非杜绝）"database is locked"
	// 出现的概率——这只覆盖本进程，不能防止另一个进程同时打开同一个数据库
	// 文件写入；busy_timeout 让确实撞上锁时等待重试而不是立刻报错，作为
	// 额外一层保护。
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("create schema: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func newID(prefix string) string {
	return fmt.Sprintf("%s-%s", prefix, uuid.NewString())
}

// --- Provider ---

func (s *Store) CreateProvider(p model.Provider) (model.Provider, error) {
	if p.Name == "" {
		return model.Provider{}, fmt.Errorf("provider name 不能为空")
	}
	p.ID = newID("provider")
	_, err := s.db.Exec(`INSERT INTO providers (id, name, contact) VALUES (?, ?, ?)`, p.ID, p.Name, p.Contact)
	if err != nil {
		return model.Provider{}, fmt.Errorf("insert provider: %w", err)
	}
	return p, nil
}

func (s *Store) ListProviders() ([]model.Provider, error) {
	rows, err := s.db.Query(`SELECT id, name, contact FROM providers ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list providers: %w", err)
	}
	defer rows.Close()

	var out []model.Provider
	for rows.Next() {
		var p model.Provider
		if err := rows.Scan(&p.ID, &p.Name, &p.Contact); err != nil {
			return nil, fmt.Errorf("scan provider: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) GetProvider(id string) (model.Provider, error) {
	var p model.Provider
	err := s.db.QueryRow(`SELECT id, name, contact FROM providers WHERE id = ?`, id).Scan(&p.ID, &p.Name, &p.Contact)
	if err == sql.ErrNoRows {
		return model.Provider{}, ErrNotFound
	}
	if err != nil {
		return model.Provider{}, fmt.Errorf("get provider: %w", err)
	}
	return p, nil
}

// --- Model ---

// CreateModel 校验 CapabilityProfile 是否合法（不接受一份声明本身就不合法的
// 能力，见 model.ValidateCapabilityProfile），并要求 provider_id 指向一个
// 已存在的 Provider（外键约束的应用层前置校验，给出比裸 SQLite 错误更明确
// 的错误信息）。
func (s *Store) CreateModel(m model.Model) (model.Model, error) {
	if m.ModelKey == "" || m.EndpointViaTokenpanel == "" || m.ProviderID == "" {
		return model.Model{}, fmt.Errorf("provider_id/model_key/endpoint_via_tokenpanel 均不能为空")
	}
	if !modelKeyPattern.MatchString(m.ModelKey) {
		return model.Model{}, fmt.Errorf("model_key %q 不合法：只能以字母/数字开头，其余字符限于字母、数字、'.'、'_'、':'、'-'", m.ModelKey)
	}
	if _, err := s.GetProvider(m.ProviderID); err != nil {
		return model.Model{}, fmt.Errorf("provider_id %q 不存在: %w", m.ProviderID, err)
	}
	if err := model.ValidateCapabilityProfile(m.Capability); err != nil {
		return model.Model{}, fmt.Errorf("能力声明不合法: %w", err)
	}
	m.ID = newID("model")
	m.Capability.ModelID = m.ID // 回填 ModelID，保证落盘的声明自带正确的归属
	capJSON, err := json.Marshal(m.Capability)
	if err != nil {
		return model.Model{}, fmt.Errorf("marshal capability: %w", err)
	}
	_, err = s.db.Exec(`INSERT INTO models (id, provider_id, model_key, endpoint_via_tokenpanel, capability_json) VALUES (?, ?, ?, ?, ?)`,
		m.ID, m.ProviderID, m.ModelKey, m.EndpointViaTokenpanel, string(capJSON))
	if err != nil {
		return model.Model{}, fmt.Errorf("insert model: %w", err)
	}
	return m, nil
}

func scanModel(row interface{ Scan(...any) error }) (model.Model, error) {
	var m model.Model
	var capJSON string
	if err := row.Scan(&m.ID, &m.ProviderID, &m.ModelKey, &m.EndpointViaTokenpanel, &capJSON); err != nil {
		return model.Model{}, err
	}
	if err := json.Unmarshal([]byte(capJSON), &m.Capability); err != nil {
		return model.Model{}, fmt.Errorf("unmarshal capability_json for model %s: %w", m.ID, err)
	}
	return m, nil
}

func (s *Store) ListModels() ([]model.Model, error) {
	rows, err := s.db.Query(`SELECT id, provider_id, model_key, endpoint_via_tokenpanel, capability_json FROM models ORDER BY model_key`)
	if err != nil {
		return nil, fmt.Errorf("list models: %w", err)
	}
	defer rows.Close()

	var out []model.Model
	for rows.Next() {
		m, err := scanModel(rows)
		if err != nil {
			return nil, fmt.Errorf("scan model: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) GetModel(id string) (model.Model, error) {
	row := s.db.QueryRow(`SELECT id, provider_id, model_key, endpoint_via_tokenpanel, capability_json FROM models WHERE id = ?`, id)
	m, err := scanModel(row)
	if err == sql.ErrNoRows {
		return model.Model{}, ErrNotFound
	}
	if err != nil {
		return model.Model{}, fmt.Errorf("get model: %w", err)
	}
	return m, nil
}

// --- TestRun ---

func (s *Store) CreateTestRun(t model.TestRun) (model.TestRun, error) {
	if t.ModelID == "" || t.SuiteID == "" {
		return model.TestRun{}, fmt.Errorf("model_id/suite_id 不能为空")
	}
	if _, err := s.GetModel(t.ModelID); err != nil {
		return model.TestRun{}, fmt.Errorf("model_id %q 不存在: %w", t.ModelID, err)
	}
	if t.Status == "" {
		t.Status = model.RunPending
	}
	if err := model.ValidateTestRunStatus(t.Status); err != nil {
		return model.TestRun{}, err
	}
	t.ID = newID("run")
	_, err := s.db.Exec(`INSERT INTO test_runs (id, model_id, suite_id, status, started_at) VALUES (?, ?, ?, ?, ?)`,
		t.ID, t.ModelID, t.SuiteID, string(t.Status), t.StartedAt)
	if err != nil {
		return model.TestRun{}, fmt.Errorf("insert test_run: %w", err)
	}
	return t, nil
}

// UpdateTestRun 用整个 TestRun 覆盖更新（状态机推进、文件路径回填、报错信息
// 记录都走这一个方法，避免多个"只改一个字段"的方法各自拼 SQL 时漏改字段）。
func (s *Store) UpdateTestRun(t model.TestRun) error {
	if err := model.ValidateTestRunStatus(t.Status); err != nil {
		return err
	}
	res, err := s.db.Exec(`UPDATE test_runs SET status=?, finished_at=?, case_results_path=?, benchmark_result_path=?, error_message=? WHERE id=?`,
		string(t.Status), t.FinishedAt, t.CaseResultsPath, t.BenchmarkResultPath, t.ErrorMessage, t.ID)
	if err != nil {
		return fmt.Errorf("update test_run: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update test_run rows affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func scanTestRun(row interface{ Scan(...any) error }) (model.TestRun, error) {
	var t model.TestRun
	var status string
	if err := row.Scan(&t.ID, &t.ModelID, &t.SuiteID, &status, &t.StartedAt, &t.FinishedAt, &t.CaseResultsPath, &t.BenchmarkResultPath, &t.ErrorMessage); err != nil {
		return model.TestRun{}, err
	}
	t.Status = model.TestRunStatus(status)
	return t, nil
}

func (s *Store) GetTestRun(id string) (model.TestRun, error) {
	row := s.db.QueryRow(`SELECT id, model_id, suite_id, status, started_at, finished_at, case_results_path, benchmark_result_path, error_message FROM test_runs WHERE id = ?`, id)
	t, err := scanTestRun(row)
	if err == sql.ErrNoRows {
		return model.TestRun{}, ErrNotFound
	}
	if err != nil {
		return model.TestRun{}, fmt.Errorf("get test_run: %w", err)
	}
	return t, nil
}

func (s *Store) ListTestRunsForModel(modelID string) ([]model.TestRun, error) {
	rows, err := s.db.Query(`SELECT id, model_id, suite_id, status, started_at, finished_at, case_results_path, benchmark_result_path, error_message FROM test_runs WHERE model_id = ? ORDER BY started_at DESC`, modelID)
	if err != nil {
		return nil, fmt.Errorf("list test_runs: %w", err)
	}
	defer rows.Close()

	var out []model.TestRun
	for rows.Next() {
		t, err := scanTestRun(rows)
		if err != nil {
			return nil, fmt.Errorf("scan test_run: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// --- Report ---

func (s *Store) CreateReport(r model.Report) (model.Report, error) {
	if r.TestRunID == "" || r.HTMLRef == "" {
		return model.Report{}, fmt.Errorf("test_run_id/html_ref 不能为空")
	}
	if _, err := s.GetTestRun(r.TestRunID); err != nil {
		return model.Report{}, fmt.Errorf("test_run_id %q 不存在: %w", r.TestRunID, err)
	}
	r.ID = newID("report")
	_, err := s.db.Exec(`INSERT INTO reports (id, test_run_id, generated_at, verdict, html_ref) VALUES (?, ?, ?, ?, ?)`,
		r.ID, r.TestRunID, r.GeneratedAt, r.Verdict, r.HTMLRef)
	if err != nil {
		return model.Report{}, fmt.Errorf("insert report: %w", err)
	}
	return r, nil
}

// GetReportForTestRun 返回某个 TestRun 最新生成的一条 Report（同一 TestRun
// 理论上只会生成一次报告，但按"最新优先"取，容错允许未来支持重新生成）。
func (s *Store) GetReportForTestRun(testRunID string) (model.Report, error) {
	var r model.Report
	err := s.db.QueryRow(`SELECT id, test_run_id, generated_at, verdict, html_ref FROM reports WHERE test_run_id = ? ORDER BY generated_at DESC LIMIT 1`, testRunID).
		Scan(&r.ID, &r.TestRunID, &r.GeneratedAt, &r.Verdict, &r.HTMLRef)
	if err == sql.ErrNoRows {
		return model.Report{}, ErrNotFound
	}
	if err != nil {
		return model.Report{}, fmt.Errorf("get report: %w", err)
	}
	return r, nil
}

// ErrNotFound 是查询/更新目标不存在时的哨兵错误，调用方（如 API handler）
// 用 errors.Is 判断，映射成 HTTP 404，不需要解析错误字符串。
var ErrNotFound = fmt.Errorf("not found")
