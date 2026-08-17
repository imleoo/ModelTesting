// benchmark-cli 是 P3「压测引擎」的命令行入口：按设计方案 6.1 节固化参数
// 生成 ShareGPT 风格多轮会话负载，对被测网关发起爬坡压测，按 6.2 节规则
// 判定回传指标，产出符合 6.3 节完整性要求的留痕（原始命令/参数/日志）。
//
// PDF 提到的 "MTB Benchmark" 工具未给出获取渠道（设计方案 15.2 节已登记为
// 潜在阻塞点），本工具是按 15 节预案实现的"自研等价压测器"。
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"github.com/leoobai/modeltestbed/internal/benchmark"
)

const toolVersion = "modeltestbed-benchmark-self-built-v1"

// uniqueRunSuffix 用纳秒时间戳 + 进程 PID 生成默认文件名后缀，避免两个 CLI
// 进程恰好同一秒启动时互相覆盖日志/结果文件（6.3 节要求产物必须可复核，
// 秒级时间戳在自动化重复压测场景下不足以保证唯一）。
func uniqueRunSuffix() string {
	return fmt.Sprintf("%d-%d", time.Now().UnixNano(), os.Getpid())
}

// createExclusive 用 O_EXCL 创建文件：即便 uniqueRunSuffix 理论上仍发生碰撞，
// 也要显式失败而不是静默截断覆盖已存在的审计产物。
func createExclusive(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
}

// writeFileAtomic 把 data 写入一个同目录临时文件、Sync 后再 rename 到 path，
// 避免结果 JSON 在写入过程中被中断（如磁盘满、进程被杀）时留下截断的半成品
// 文件冒充完整的 BENCHMARK_RUN 产物。
func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	f, err := createExclusive(tmp)
	if err != nil {
		return err
	}
	// renamed 之前的任何 return 都必须清理掉 tmp——包括 os.Rename 本身失败
	// 的情况，否则会残留一个占用 tmp 名字的文件，导致下次重试同一路径时
	// 在 createExclusive(tmp) 处永久失败（O_EXCL 拒绝覆盖已存在文件）。
	renamed := false
	defer func() {
		if !renamed {
			os.Remove(tmp)
		}
	}()
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	renamed = true
	return nil
}

func main() {
	// 用独立 FlagSet（而不是默认的 flag.CommandLine）并接管其 Output：
	// 默认 flag.CommandLine 在解析失败或 -h 时会自己把 usage/错误信息直接
	// 写到 os.Stderr 并 os.Exit，完全绕过日志文件——但此时日志文件还没
	// 建好（日志路径本身也是一个待解析的 flag），所以先把这部分输出缓冲
	// 到内存，等日志文件创建好之后再回放进去。
	fs := flag.NewFlagSet("benchmark-cli", flag.ContinueOnError)
	var flagOutput bytes.Buffer
	fs.SetOutput(&flagOutput)

	baseURL := fs.String("base-url", "", "被测网关 base URL")
	apiKey := fs.String("api-key", "", "测试用 API Key")
	modelKey := fs.String("model-key", "", "被测模型 model_key")
	totalSessions := fs.Int("total-sessions", 20, "总会话数（6.1 节：按目标并发设定）")
	requestTimeout := fs.Duration("request-timeout", 120*time.Second, "单请求超时")
	seed := fs.Int64("seed", 1, "随机种子（用于可复现的会话生成与到达调度）")
	outPath := fs.String("out", "", "结果 JSON 输出路径（留空则自动生成一个带时间戳+PID 的文件名；6.3 节要求 BENCHMARK_RUN 必须落盘，不允许默认命令行完全不产出结果文件）")
	logPath := fs.String("log", "", "原始事件日志输出路径（6.3 节 stdout/stderr 全量日志引用；留空则自动生成一个带时间戳+PID 的文件名，不允许完全没有落盘引用）")
	parseErr := fs.Parse(os.Args[1:])

	// resolvedLogPath 在 parseErr != nil 时不信任 *logPath（解析失败，flag
	// 值可能是残缺/默认值），一律退回自动生成的路径，保证无论解析成功与否
	// 都有一个确定的日志落盘目标。
	resolvedLogPath := *logPath
	if parseErr != nil || resolvedLogPath == "" {
		resolvedLogPath = fmt.Sprintf("benchmark-%s.log", uniqueRunSuffix())
	}
	logFile, err := createExclusive(resolvedLogPath)
	if err != nil {
		os.Stderr.Write(flagOutput.Bytes())
		log.Fatalf("创建日志文件失败: %v", err)
	}
	defer logFile.Close()
	// out 同时打到 stdout（方便实时看进度）与日志文件；err 同时打到 stderr
	// 与同一个日志文件。二者合起来才是 RawStdoutRef 指向的"stdout/stderr
	// 全量日志"。
	logWriter := io.MultiWriter(logFile, os.Stdout)
	errWriter := io.MultiWriter(logFile, os.Stderr)
	log.SetOutput(errWriter)

	// 顶层 recover：把 runtime panic 的堆栈也落到同一份日志里，而不是任其
	// 直接写进程 stderr（绕过 RawStdoutRef 引用的文件）后终止。
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(errWriter, "panic: %v\n%s\n", r, debug.Stack())
			logFile.Close()
			os.Exit(1)
		}
	}()

	if flagOutput.Len() > 0 {
		errWriter.Write(flagOutput.Bytes())
	}
	if parseErr != nil {
		if errors.Is(parseErr, flag.ErrHelp) {
			os.Exit(0)
		}
		log.Fatalf("解析命令行参数失败: %v", parseErr)
	}

	if *baseURL == "" || *modelKey == "" {
		log.Fatal("必须指定 -base-url 与 -model-key")
	}

	params, err := benchmark.DefaultParams(*totalSessions)
	if err != nil {
		log.Fatalf("构造压测参数失败: %v", err)
	}

	resolvedOutPath := *outPath
	if resolvedOutPath == "" {
		resolvedOutPath = fmt.Sprintf("benchmark-result-%s.json", uniqueRunSuffix())
	}

	rawCommand := strings.Join(os.Args, " ")

	fmt.Fprintf(logWriter, "开始压测：total_sessions=%d model=%s base_url=%s（详细进度见日志）\n", *totalSessions, *modelKey, *baseURL)

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

	fmt.Fprintf(logWriter, "\n=== 压测完成 ===\ntotal_requests=%d duration=%.1fs\n\n", result.Run.TotalRequests, result.Run.DurationS)
	for _, m := range result.Metrics {
		if m.Scope != "overall" {
			continue
		}
		fmt.Fprintf(logWriter, "%-26s avg=%-10.3f p50=%-10.3f p95=%-10.3f unit=%-6s verdict=%s\n",
			m.Name, m.Avg, m.P50, m.P95, m.Unit, m.BaselineVerdict)
		if m.Note != "" {
			fmt.Fprintf(logWriter, "%-26s note: %s\n", "", m.Note)
		}
	}

	// BENCHMARK_RUN 必须落盘（6.3 节），因此结果 JSON 不再是"传了 -out 才写"
	// 的可选项：未指定时也会写入上面自动生成的 resolvedOutPath；写入用
	// 临时文件+rename 的原子模式，避免中途失败留下截断的半成品文件。
	out, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		log.Fatalf("序列化结果失败: %v", err)
	}
	if err := writeFileAtomic(resolvedOutPath, out); err != nil {
		log.Fatalf("写入结果文件失败: %v", err)
	}
	fmt.Fprintf(logWriter, "\n完整结果已写入 %s\n", resolvedOutPath)
}
