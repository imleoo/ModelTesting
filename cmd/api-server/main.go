// api-server 是 P4「Web 控制台」的后端入口：把 testbed-cli/benchmark-cli/
// report-cli 三个 P1-P4 阶段的独立 CLI 工具编排成设计方案 09 节执行时序描述
// 的完整任务流，通过 Gin HTTP API 暴露给 horizon-next 前端。
package main

import (
	"flag"
	"log"
	"time"

	"github.com/leoobai/modeltestbed/internal/api"
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
