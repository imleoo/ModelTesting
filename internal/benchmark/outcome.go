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
	TPOTMillis     float64 // 首 token 之后，平均每个输出 token 的耗时
	ITLMillis      []float64
	OutputTokens   int
	PromptTokens   int
	CachedTokens   int
	RoundNumber    int // 会话内第几轮（0 起），用于分轮次统计缓存命中率
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
	o.TTFTSeconds = r.FirstByteAt.Sub(r.SentAt).Seconds()
	o.LatencySeconds = r.DoneAt.Sub(r.SentAt).Seconds()
	o.PromptTokens = r.PromptTokens
	o.OutputTokens = r.OutputTokens
	o.CachedTokens = r.CachedTokens

	contentTimes := r.ContentChunkTimestamps()
	if len(contentTimes) >= 2 {
		itl := make([]float64, 0, len(contentTimes)-1)
		for i := 1; i < len(contentTimes); i++ {
			itl = append(itl, contentTimes[i].Sub(contentTimes[i-1]).Seconds()*1000)
		}
		o.ITLMillis = itl
		totalAfterFirst := contentTimes[len(contentTimes)-1].Sub(contentTimes[0]).Seconds() * 1000
		o.TPOTMillis = totalAfterFirst / float64(len(contentTimes)-1)
	}
	return o
}
