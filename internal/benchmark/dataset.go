package benchmark

import "math/rand"

// Params 是设计方案 6.1 节固化的压测参数（PDF 3.1 原文数值，逐项固化，不是
// "遵循 PDF 分布" 的近似描述）。
type Params struct {
	ArrivalRateStart    float64 // req/s，爬坡起点
	ArrivalRateEnd      float64 // req/s，爬坡终点
	RampDurationSeconds float64
	TotalSessions       int
	NumRounds           *PercentileSampler
	TurnIntervalSeconds *PercentileSampler
	InitPromptLengthTok *PercentileSampler
	InputLengthTok      *PercentileSampler
	OutputLengthTok     *PercentileSampler
}

// DefaultParams 构造设计方案 6.1 节表格里的固化参数。totalSessions 由调用方
// 指定（PDF 原文："按目标并发设定（供应商/tokenpanel 侧约定）"，不是固定值）。
func DefaultParams(totalSessions int) (*Params, error) {
	numRounds, err := NewPercentileSampler("num-rounds", 1,
		[]PercentilePoint{{0.50, 25.0}, {0.75, 34.0}, {0.90, 47.0}, {0.95, 57.0}},
		extrapolateParetoTail(0.90, 47.0, 0.95, 57.0, 0.999))
	if err != nil {
		return nil, err
	}
	turnInterval, err := NewPercentileSampler("turn-interval", 0.1,
		[]PercentilePoint{{0.50, 4.0}, {0.75, 10.0}, {0.90, 35.0}, {0.95, 86.0}},
		extrapolateParetoTail(0.90, 35.0, 0.95, 86.0, 0.999))
	if err != nil {
		return nil, err
	}
	initPromptLength, err := NewPercentileSampler("init-prompt-length", 100,
		[]PercentilePoint{{0.50, 21983.0}, {0.95, 74262.9}},
		extrapolateParetoTail(0.50, 21983.0, 0.95, 74262.9, 0.999))
	if err != nil {
		return nil, err
	}
	inputLength, err := NewPercentileSampler("input-length", 10,
		[]PercentilePoint{{0.50, 493.0}, {0.95, 4740.0}},
		extrapolateParetoTail(0.50, 493.0, 0.95, 4740.0, 0.999))
	if err != nil {
		return nil, err
	}
	outputLength, err := NewPercentileSampler("output-length", 1,
		[]PercentilePoint{{0.50, 68.0}, {0.95, 1514.0}},
		extrapolateParetoTail(0.50, 68.0, 0.95, 1514.0, 0.999))
	if err != nil {
		return nil, err
	}

	return &Params{
		ArrivalRateStart:    0.08,
		ArrivalRateEnd:      1,
		RampDurationSeconds: 600,
		TotalSessions:       totalSessions,
		NumRounds:           numRounds,
		TurnIntervalSeconds: turnInterval,
		InitPromptLengthTok: initPromptLength,
		InputLengthTok:      inputLength,
		OutputLengthTok:     outputLength,
	}, nil
}

// Round 是一轮多轮会话里的一次用户输入。
type Round struct {
	Index               int
	InputLengthTok      int
	OutputLengthTok     int // 作为 max_tokens 软目标下发，不强制模型精确命中
	TurnIntervalSeconds float64
}

// Session 是一次多轮会话的完整计划（生成阶段就固化好，执行阶段按计划回放）。
type Session struct {
	ID                  int
	InitPromptLengthTok int
	Rounds              []Round
}

// GenerateSessions 按 Params 生成 TotalSessions 个会话计划。
func GenerateSessions(p *Params, rng *rand.Rand) []Session {
	sessions := make([]Session, p.TotalSessions)
	for i := range p.TotalSessions {
		numRounds := max(p.NumRounds.SampleInt(rng), 1)
		rounds := make([]Round, numRounds)
		for r := range numRounds {
			interval := 0.0
			if r > 0 {
				interval = p.TurnIntervalSeconds.Sample(rng)
			}
			rounds[r] = Round{
				Index:               r,
				InputLengthTok:      p.InputLengthTok.SampleInt(rng),
				OutputLengthTok:     p.OutputLengthTok.SampleInt(rng),
				TurnIntervalSeconds: interval,
			}
		}
		sessions[i] = Session{
			ID:                  i,
			InitPromptLengthTok: p.InitPromptLengthTok.SampleInt(rng),
			Rounds:              rounds,
		}
	}
	return sessions
}
