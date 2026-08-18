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
	"path/filepath"
	"strings"
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

	// AuthToken 是 10.1 节要求的首版固定 Token 鉴权；空值表示不鉴权，仅供
	// 本地开发/测试使用，见 auth.go。
	AuthToken string

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

// safeJoin 把用户可控的 userPath（如 TestRun.SuiteID，来自 POST 请求体）
// 拼到 root 下，并确认拼接结果没有借助 ".."/绝对路径逃出 root——直接
// filepath.Join(root, userPath) 不会拒绝 "../../etc/passwd" 这类输入，会
// 读到 root 之外任意用户有权限访问的文件，这是一个真实的路径穿越漏洞，
// 不是防御性的过度设计。
func safeJoin(root, userPath string) (string, error) {
	if userPath == "" {
		return "", fmt.Errorf("path 不能为空")
	}
	if filepath.IsAbs(userPath) {
		return "", fmt.Errorf("path 不能是绝对路径: %q", userPath)
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("解析 root 绝对路径失败: %w", err)
	}
	joined := filepath.Join(absRoot, userPath)
	rel, err := filepath.Rel(absRoot, joined)
	if err != nil {
		return "", fmt.Errorf("解析相对路径失败: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q 越出允许的根目录", userPath)
	}
	return joined, nil
}

// resolveWithinRoot 在 safeJoin 的字符串级校验之后再做一层基于真实文件
// 系统的校验：candidate（或者它最近一个已存在的祖先目录）解析符号链接
// 之后，必须仍然落在 root 解析符号链接之后的真实路径之内。字符串级校验
// 只能挡住 ".."/绝对路径这类语法层面的逃逸——如果 root 目录内部本身放了
// 一个指向外部的符号链接（比如运维为了图方便建的一个 alias），字符串
// 校验完全看不出来，必须真正 resolve 文件系统才能发现。
//
// candidate 指向的文件可能还不存在（比如即将要写入的报告文件），所以从
// candidate 本身开始往上找第一个已存在的祖先目录来做 EvalSymlinks——
// 不存在的路径段不可能是符号链接（没法对一个还没创建出来的东西建软链），
// 所以只要"最近的已存在祖先"落在真实 root 内，candidate 剩余的、字符串
// 校验已经通过的部分就是安全的。
func resolveWithinRoot(root, candidate string) error {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("解析 root 真实路径失败: %w", err)
	}

	probe := candidate
	for {
		realProbe, err := filepath.EvalSymlinks(probe)
		if err == nil {
			rel, err := filepath.Rel(realRoot, realProbe)
			if err != nil {
				return fmt.Errorf("解析相对路径失败: %w", err)
			}
			if rel != "." && (rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
				return fmt.Errorf("path 解析符号链接后的真实路径越出允许的根目录")
			}
			return nil
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return fmt.Errorf("无法定位 path 的任何已存在的祖先目录: %w", err)
		}
		probe = parent
	}
}

// safeJoinResolved 是 safeJoin + resolveWithinRoot 的组合：既做字符串级
// 校验，也做符号链接感知的真实路径校验，两层防御互补，不能只依赖其中一层。
func safeJoinResolved(root, userPath string) (string, error) {
	joined, err := safeJoin(root, userPath)
	if err != nil {
		return "", err
	}
	if err := resolveWithinRoot(root, joined); err != nil {
		return "", err
	}
	return joined, nil
}
