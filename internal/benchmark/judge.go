package benchmark

import "github.com/leoobai/modeltestbed/internal/model"

// MaxDegradeRatio 对应设计方案 6.2 节的 MAX_DEGRADE_RATIO（默认 2×，可配置）：
// 劣于基线但在这个倍数以内记"同一数量级"，超出才判 FAIL。
const MaxDegradeRatio = 2.0

// judgeHigherIsBetter 用于"越高越好"的指标（吞吐、缓存命中率）：
// 达到或优于基线直接 OK；劣化时看 baseline/actual 是否在 MaxDegradeRatio 内。
func judgeHigherIsBetter(actual, baseline float64) model.BaselineVerdict {
	if actual >= baseline {
		return model.BaselineOK
	}
	if actual <= 0 {
		return model.BaselineFail
	}
	if baseline/actual <= MaxDegradeRatio {
		return model.BaselineSameOrder
	}
	return model.BaselineFail
}

// judgeLowerIsBetter 用于"越低越好"、基线是"≤"关系的指标（TTFT P50 ≤ 15s）：
// 达到或优于基线直接 OK；劣化时看 actual/baseline 是否在 MaxDegradeRatio 内。
func judgeLowerIsBetter(actual, baseline float64) model.BaselineVerdict {
	if actual <= baseline {
		return model.BaselineOK
	}
	return degradeVerdictLower(actual, baseline)
}

// judgeLowerIsBetterStrict 用于基线是"<"严格小于关系的指标（TPOT P50 <35ms，
// PDF 原文明确写的是严格小于，等于基线本身不算达标，只是劣化很轻微，
// 仍归入 SAME_ORDER 而不是直接 OK——不能和 judgeLowerIsBetter 共用同一个
// "<="判定，否则恰好等于 35ms 会被误判 OK）。
func judgeLowerIsBetterStrict(actual, baseline float64) model.BaselineVerdict {
	if actual < baseline {
		return model.BaselineOK
	}
	return degradeVerdictLower(actual, baseline)
}

func degradeVerdictLower(actual, baseline float64) model.BaselineVerdict {
	if baseline <= 0 {
		return model.BaselineFail
	}
	if actual/baseline <= MaxDegradeRatio {
		return model.BaselineSameOrder
	}
	return model.BaselineFail
}

// 6.2 节 PDF 参考基线。
const (
	baselineThroughputReqS = 0.6
	baselineTTFTP50Seconds = 15.0
	baselineTPOTP50Millis  = 35.0
	baselineCacheHitRate   = 0.60
)
