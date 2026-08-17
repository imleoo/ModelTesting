package benchmark

import "strconv"

// RequestOutcome 是单次请求（会话内某一轮）压测完成后提炼出的指标样本，
// 对应设计方案 6.2 节要回传的各项指标的"一个样本点"。
type RequestOutcome struct {
	SessionID      int
	RoundIndex     int
	Success        bool
	HTTPStatus     int
	Err            string
	TTFTSeconds    float64
	LatencySeconds float64
	// TPOTMillis 用 usage.completion_tokens 做分母（首 token 之后的耗时 /
	// (completion_tokens-1)），是标准 TPOT 定义（time per output token）的
	// 直接实现，不是用 SSE 分片数近似 token 数——分片数 ≠ token 数（一个分片
	// 可能携带多个 token 的文本，或反过来一个 token 分几个分片下发）。
	// 缺少 usage.completion_tokens 时 TPOTMillis 为 0（不产出误导性数字）。
	TPOTMillis float64
	// ITLMillis 是相邻 SSE 分片的到达间隔（毫秒），作为 token 间延迟的近似——
	// 受限于没有真正的逐 token 时间戳，这是"分片级"而非"token 级"的测量，
	// 6.2 节里 ITL 本身也无 PDF 基线、只记录不做硬判定，精度要求相应更低。
	ITLMillis    []float64
	OutputTokens int
	PromptTokens int
	// CachedTokens 为 nil 表示该次响应从未观测到
	// usage.prompt_tokens_details.cached_tokens 字段（不可观测，需要和"明确
	// 命中 0 个 token"区分开，否则会把"没测到"误判成"命中率 0%"进而误判 FAIL）。
	CachedTokens *int
	RoundNumber  int // 会话内第几轮（0 起），用于分轮次统计缓存命中率
}

// DeriveOutcome 从一次流式调用结果计算出 RequestOutcome。
func DeriveOutcome(sessionID, roundNumber int, r StreamCallResult) RequestOutcome {
	o := RequestOutcome{
		SessionID:   sessionID,
		RoundIndex:  roundNumber,
		RoundNumber: roundNumber,
		HTTPStatus:  r.HTTPStatus,
	}
	if r.Err != nil {
		o.Err = r.Err.Error()
		return o
	}
	if r.HTTPStatus != 200 {
		o.Err = "http status " + strconv.Itoa(r.HTTPStatus)
		return o
	}
	o.Success = true
	if !r.FirstContentAt.IsZero() {
		o.TTFTSeconds = r.FirstContentAt.Sub(r.SentAt).Seconds()
	}
	o.LatencySeconds = r.DoneAt.Sub(r.SentAt).Seconds()
	o.PromptTokens = r.PromptTokens
	o.OutputTokens = r.OutputTokens
	o.CachedTokens = r.CachedTokens

	if !r.FirstContentAt.IsZero() && o.OutputTokens > 1 {
		afterFirstMs := r.DoneAt.Sub(r.FirstContentAt).Seconds() * 1000
		o.TPOTMillis = afterFirstMs / float64(o.OutputTokens-1)
	}

	var itlTimes []float64
	var lastAt *float64
	for _, ch := range r.Chunks {
		if ch.Content == "" {
			continue
		}
		t := ch.At.Sub(r.SentAt).Seconds() * 1000
		if lastAt != nil {
			itlTimes = append(itlTimes, t-*lastAt)
		}
		tCopy := t
		lastAt = &tCopy
	}
	o.ITLMillis = itlTimes

	return o
}
