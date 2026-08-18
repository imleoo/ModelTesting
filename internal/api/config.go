// Package api 是 P4 Web 控制台的 Gin API 层：把此前 P1-P4 独立的 Go CLI
// 工具（testbed-cli 的用例引擎、benchmark-cli 的压测引擎、report-cli 的报告
// 生成）编排成设计方案 09 节执行时序描述的完整任务流，通过 HTTP 暴露给
// horizon-next 前端。首版按 10.1 节"单一进程内直接执行用例/压测任务，不
// 引入任务队列"的选型：任务在请求触发的一个后台 goroutine 里跑完，状态写
// 回 SQLite（internal/store），前端轮询查询进度。
package api

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/leoobai/modeltestbed/internal/benchmark"
	"github.com/leoobai/modeltestbed/internal/store"
)

// Config 是 API 服务的运行时配置 + 依赖，同时承载编排状态（互斥锁）。
type Config struct {
	Store *store.Store

	// SuitesRoot 是套件定义根目录；TestRun.SuiteID 是相对这个目录的路径
	// （如 "kimi-k3/suite.v1.json"），不是一个独立的套件注册表——首版只有
	// 手动登记的本地套件文件，见 07 节 SOP。
	SuitesRoot string
	// ReportsRoot 是功能测试结果/压测结果/报告 HTML 的落盘根目录，按
	// model_key 分子目录，沿用 P1-P4 CLI 阶段已有的文件命名习惯。
	ReportsRoot string
	// RepoRoot 供 suitedef.LoadMaterialsManifestForSuite 解析素材清单的
	// 相对路径。
	RepoRoot         string
	MaterialsBaseURL string
	RequestTimeout   time.Duration
	// DefaultTotalSessions 是发起测试任务时不传 total_sessions 的默认压测
	// 规模。
	DefaultTotalSessions int

	// NewBenchmarkParams 默认是 benchmark.DefaultParams，测试时可以替换成
	// 一个更快的合成采样器（同 internal/benchmark 自己的端到端测试），避免
	// 集成测试因为真实 6.1 节参数（轮次间隔均值 18.6s 等）跑到分钟级。
	NewBenchmarkParams func(totalSessions int) (*benchmark.Params, error)

	// orchestrateMu 保证同一时刻只有一个测试任务在执行（功能测试+压测合并
	// 算一个任务）——对应 09 节执行时序本身就是串行的一条流水线，也呼应
	// 6.3 节"压测同一时刻只能有一个"的互斥要求；10.1 节明确首版不引入任务
	// 队列，所以这里选择"撞上就直接拒绝"而不是排队等待。
	orchestrateMu sync.Mutex
}

func (cfg *Config) benchmarkParams(totalSessions int) (*benchmark.Params, error) {
	if cfg.NewBenchmarkParams != nil {
		return cfg.NewBenchmarkParams(totalSessions)
	}
	return benchmark.DefaultParams(totalSessions)
}

func writeJSONFile(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func nowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339)
}
