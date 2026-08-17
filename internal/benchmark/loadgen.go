package benchmark

import (
	"context"
	"math/rand"
	"net/http"
	"sync"
	"time"
)

// LoadGenConfig 是一次压测执行所需的运行时配置。
type LoadGenConfig struct {
	Params         *Params
	BaseURL        string
	APIKey         string
	ModelKey       string
	HTTPClient     *http.Client
	RequestTimeout time.Duration
}

// scheduleArrivals 用 thinning（舍选）算法模拟非齐次泊松过程，按 6.1 节
// "TPM 匀速爬坡至目标稳态后持续压测"生成 len(sessions) 个会话的启动时刻：
// [0, RampDurationSeconds] 内到达率从 ArrivalRateStart 线性爬坡到
// ArrivalRateEnd，之后维持在 ArrivalRateEnd（稳态持续压测）。
// 返回值与 sessions 一一对应，是每个会话相对压测起点的启动偏移（秒）。
func scheduleArrivals(p *Params, n int, rng *rand.Rand) []float64 {
	maxRate := max(p.ArrivalRateStart, p.ArrivalRateEnd)
	rateAt := func(t float64) float64 {
		if t >= p.RampDurationSeconds {
			return p.ArrivalRateEnd
		}
		frac := t / p.RampDurationSeconds
		return p.ArrivalRateStart + frac*(p.ArrivalRateEnd-p.ArrivalRateStart)
	}

	offsets := make([]float64, 0, n)
	t := 0.0
	for len(offsets) < n {
		// 齐次泊松提议过程（速率 maxRate），再按 rateAt(t)/maxRate 接受概率舍选。
		t += rng.ExpFloat64() / maxRate
		if rng.Float64() <= rateAt(t)/maxRate {
			offsets = append(offsets, t)
		}
	}
	return offsets
}

// RunLoadTest 按到达计划并发执行全部会话，每完成一次请求就通过 onOutcome
// 回调上报（供调用方实时收集指标/打印进度），返回压测整体起止时刻。
func RunLoadTest(ctx context.Context, cfg LoadGenConfig, sessions []Session, rng *rand.Rand, onOutcome func(RequestOutcome)) (startedAt, endedAt time.Time) {
	offsets := scheduleArrivals(cfg.Params, len(sessions), rng)
	startedAt = time.Now()

	var wg sync.WaitGroup
	var mu sync.Mutex // 保护 onOutcome 的并发调用（onOutcome 本身不假定线程安全）

	for i, sess := range sessions {
		wg.Add(1)
		go func(sess Session, delay float64) {
			defer wg.Done()
			select {
			case <-time.After(time.Duration(delay * float64(time.Second))):
			case <-ctx.Done():
				return
			}
			runSession(ctx, cfg, sess, func(o RequestOutcome) {
				mu.Lock()
				onOutcome(o)
				mu.Unlock()
			})
		}(sess, offsets[i])
	}
	wg.Wait()
	endedAt = time.Now()
	return startedAt, endedAt
}

func runSession(ctx context.Context, cfg LoadGenConfig, sess Session, onOutcome func(RequestOutcome)) {
	rng := rand.New(rand.NewSource(int64(sess.ID) + 1))
	history := make([]map[string]any, 0, len(sess.Rounds)*2+1)
	history = append(history, map[string]any{
		"role":    "user",
		"content": GenerateText(rng, sess.InitPromptLengthTok),
	})

	for _, round := range sess.Rounds {
		if round.Index > 0 {
			select {
			case <-time.After(time.Duration(round.TurnIntervalSeconds * float64(time.Second))):
			case <-ctx.Done():
				return
			}
			history = append(history, map[string]any{
				"role":    "user",
				"content": GenerateText(rng, round.InputLengthTok),
			})
		}

		body := map[string]any{
			"model":      cfg.ModelKey,
			"messages":   append([]map[string]any{}, history...),
			"max_tokens": max(round.OutputLengthTok, 1),
		}
		result := StreamCall(ctx, cfg.HTTPClient, cfg.BaseURL, cfg.APIKey, body, cfg.RequestTimeout)
		onOutcome(DeriveOutcome(sess.ID, round.Index, result))

		if result.Err == nil && result.HTTPStatus == 200 {
			content := result.ConcatenatedContent()
			if content == "" {
				// 极端情况下模型可能真的没输出文本（比如立刻触发某种截断）；
				// 用占位符而不是空字符串，规避部分网关拒绝空 content 的历史消息。
				content = "(empty response)"
			}
			history = append(history, map[string]any{
				"role":    "assistant",
				"content": content,
			})
		} else {
			return // 本轮失败，会话提前终止（真实场景里客户端通常也会中断该会话）
		}
	}
}
