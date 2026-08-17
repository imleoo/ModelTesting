// benchmark-cli 是 P3「压测引擎」的命令行入口：按设计方案 6.1 节固化参数
// 生成 ShareGPT 风格多轮会话负载，对被测网关发起爬坡压测，按 6.2 节规则
// 判定回传指标，产出符合 6.3 节完整性要求的留痕（原始命令/参数/日志）。
//
// PDF 提到的 "MTB Benchmark" 工具未给出获取渠道（设计方案 15.2 节已登记为
// 潜在阻塞点），本工具是按 15 节预案实现的"自研等价压测器"。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"

	"github.com/leoobai/modeltestbed/internal/benchmark"
)

const toolVersion = "modeltestbed-benchmark-self-built-v1"

func main() {
	baseURL := flag.String("base-url", "", "被测网关 base URL")
	apiKey := flag.String("api-key", "", "测试用 API Key")
	modelKey := flag.String("model-key", "", "被测模型 model_key")
	totalSessions := flag.Int("total-sessions", 20, "总会话数（6.1 节：按目标并发设定）")
	requestTimeout := flag.Duration("request-timeout", 120*time.Second, "单请求超时")
	seed := flag.Int64("seed", 1, "随机种子（用于可复现的会话生成与到达调度）")
	outPath := flag.String("out", "", "结果 JSON 输出路径")
	logPath := flag.String("log", "", "原始事件日志输出路径（6.3 节 stdout/stderr 全量日志引用；留空则自动生成一个带时间戳的文件名，不允许完全没有落盘引用）")
	flag.Parse()

	if *baseURL == "" || *modelKey == "" {
		log.Fatal("必须指定 -base-url 与 -model-key")
	}

	params, err := benchmark.DefaultParams(*totalSessions)
	if err != nil {
		log.Fatalf("构造压测参数失败: %v", err)
	}

	resolvedLogPath := *logPath
	if resolvedLogPath == "" {
		resolvedLogPath = fmt.Sprintf("benchmark-%d.log", time.Now().Unix())
	}
	logFile, err := os.Create(resolvedLogPath)
	if err != nil {
		log.Fatalf("创建日志文件失败: %v", err)
	}
	defer logFile.Close()
	// 同时打到 stdout，方便实时看进度；文件本身才是 RawStdoutRef 指向的
	// 完整留痕（6.3 节要求"stdout/stderr 全量日志引用"，必须是可复核的落盘产物）。
	logWriter := io.MultiWriter(logFile, os.Stdout)

	rawCommand := strings.Join(os.Args, " ")

	fmt.Printf("开始压测：total_sessions=%d model=%s base_url=%s（详细进度见日志）\n", *totalSessions, *modelKey, *baseURL)

	result, err := benchmark.Run(context.Background(), benchmark.RunConfig{
		Params:         params,
		BaseURL:        *baseURL,
		APIKey:         *apiKey,
		ModelKey:       *modelKey,
		RequestTimeout: *requestTimeout,
		RawCommand:     rawCommand,
		ToolVersion:    toolVersion,
		DatasetVersion: "synthetic-percentile-reconstruction-v1",
		Seed:           *seed,
		LogWriter:      logWriter,
		LogRef:         resolvedLogPath,
	})
	if err != nil {
		log.Fatalf("压测执行失败: %v", err)
	}

	fmt.Printf("\n=== 压测完成 ===\ntotal_requests=%d duration=%.1fs\n\n", result.Run.TotalRequests, result.Run.DurationS)
	for _, m := range result.Metrics {
		if m.Scope != "overall" {
			continue
		}
		fmt.Printf("%-26s avg=%-10.3f p50=%-10.3f p95=%-10.3f unit=%-6s verdict=%s\n",
			m.Name, m.Avg, m.P50, m.P95, m.Unit, m.BaselineVerdict)
		if m.Note != "" {
			fmt.Printf("%-26s note: %s\n", "", m.Note)
		}
	}

	if *outPath != "" {
		out, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			log.Fatalf("序列化结果失败: %v", err)
		}
		if err := os.WriteFile(*outPath, out, 0o644); err != nil {
			log.Fatalf("写入结果文件失败: %v", err)
		}
		fmt.Printf("\n完整结果已写入 %s\n", *outPath)
	}
}
