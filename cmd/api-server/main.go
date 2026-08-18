// api-server 是 P4「Web 控制台」的后端入口：把 testbed-cli/benchmark-cli/
// report-cli 三个 P1-P4 阶段的独立 CLI 工具编排成设计方案 09 节执行时序描述
// 的完整任务流，通过 Gin HTTP API 暴露给 horizon-next 前端。
package main

import (
	"encoding/json"
	"flag"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/leoobai/modeltestbed/internal/api"
	"github.com/leoobai/modeltestbed/internal/model"
	"github.com/leoobai/modeltestbed/internal/store"
)

func main() {
	addr := flag.String("addr", ":8090", "监听地址")
	dbPath := flag.String("db", "testbed.db", "SQLite 数据库文件路径")
	suitesRoot := flag.String("suites-root", "suites", "套件定义根目录（TestRun.suite_id 是相对这个目录的路径）")
	reportsRoot := flag.String("reports-root", "reports", "结果/报告文件落盘根目录")
	materialsBaseURL := flag.String("materials-base-url", "http://127.0.0.1:8080", "素材托管服务 base URL")
	repoRoot := flag.String("repo-root", ".", "仓库根目录（用于解析 materials_manifest 相对路径）")
	requestTimeout := flag.Duration("request-timeout", 120*time.Second, "单请求超时")
	defaultTotalSessions := flag.Int("default-total-sessions", 20, "发起任务时不传 total_sessions 的默认压测规模")
	authToken := flag.String("auth-token", "", "固定 Token 鉴权（设计方案 10.1 节首版要求）；留空则不鉴权，仅限本地开发/测试，生产部署必须设置")
	flag.Parse()

	if *authToken == "" {
		log.Printf("警告：未设置 -auth-token，API 对任何能访问到 %s 的人完全开放，仅建议在完全受信的本地/内网环境这样运行", *addr)
	}

	s, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("打开数据库失败: %v", err)
	}
	defer s.Close()

	seedKimiK3Defaults(s, *suitesRoot)

	cfg := &api.Config{
		Store:                s,
		SuitesRoot:           *suitesRoot,
		ReportsRoot:          *reportsRoot,
		MaterialsBaseURL:     *materialsBaseURL,
		RepoRoot:             *repoRoot,
		RequestTimeout:       *requestTimeout,
		DefaultTotalSessions: *defaultTotalSessions,
		AuthToken:            *authToken,
	}

	r := api.NewRouter(cfg)
	log.Printf("api-server 监听 %s（数据库: %s，套件根目录: %s，结果根目录: %s）", *addr, *dbPath, *suitesRoot, *reportsRoot)
	if err := r.Run(*addr); err != nil {
		log.Fatalf("服务启动失败: %v", err)
	}
}

// seedKimiK3Defaults 在数据库还没有任何供应商时，把首个套件模板 Kimi-K3
// 内置为默认供应商/模型（对应真实测试已跑过的两条通道：官方 Moonshot API
// 与 we2ai 中转），免得每次新建数据库都要在 Web 控制台手动录入一遍。
// 只在 providers 表为空时执行一次，已有数据时直接跳过，不会覆盖用户的
// 手动登记。不含 API Key——按 12 节"应用层加密存储"的首版简化处理，Key
// 始终随发起测试任务的请求传入，不落盘。
func seedKimiK3Defaults(s *store.Store, suitesRoot string) {
	providers, err := s.ListProviders()
	if err != nil {
		log.Printf("检查默认数据失败，跳过内置 Kimi-K3 供应商/模型: %v", err)
		return
	}
	if len(providers) > 0 {
		return
	}

	capPath := filepath.Join(suitesRoot, "kimi-k3", "capability_kimi-k3.real.json")
	capBytes, err := os.ReadFile(capPath)
	if err != nil {
		log.Printf("读取 %s 失败，跳过内置 Kimi-K3 供应商/模型: %v", capPath, err)
		return
	}
	var capability model.CapabilityProfile
	if err := json.Unmarshal(capBytes, &capability); err != nil {
		log.Printf("解析 %s 失败，跳过内置 Kimi-K3 供应商/模型: %v", capPath, err)
		return
	}

	// 两条真实跑过的通道，见 reports/kimi-k3/P2-功能测试结果表-2026-08-18-*.md。
	// model_key 必须是上游认识的真实模型名——套件模板用 {{model_key}} 原样
	// 替换进请求体的 "model" 字段发给上游，两条通道都固定是 "kimi-k3"，靠
	// 挂在不同 Provider 下 + 不同 endpoint 区分，不能靠改 model_key 本身
	// 区分（那样上游会因为不认识这个模型名而 404）。endpoint 只填 base
	// URL，不含 path——internal/client.Call 会自动拼上套件 protocol.base_path
	// （/v1/chat/completions），带了 path 会拼出 .../v1/chat/completions/v1/chat/completions。
	defaultProviders := []struct {
		providerName string
		endpoint     string
	}{
		{"MoonshotAI（官方）", "https://api.moonshot.cn"},
		{"we2ai（k3 中转）", "https://api.we2ai.com"},
	}
	for _, dp := range defaultProviders {
		provider, err := s.CreateProvider(model.Provider{Name: dp.providerName})
		if err != nil {
			log.Printf("内置供应商 %s 创建失败: %v", dp.providerName, err)
			continue
		}
		if _, err := s.CreateModel(model.Model{
			ProviderID:            provider.ID,
			ModelKey:              "kimi-k3",
			EndpointViaTokenpanel: dp.endpoint,
			Capability:            capability,
		}); err != nil {
			log.Printf("内置模型 kimi-k3（供应商 %s）创建失败: %v", dp.providerName, err)
		}
	}
	log.Printf("已内置默认供应商/模型：MoonshotAI（官方）+ we2ai（k3 中转），model_key 均为 kimi-k3")
}
