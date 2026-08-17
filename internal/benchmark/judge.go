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

// judgeLowerIsBetter 用于"越低越好"的指标（TTFT、TPOT）：
// 达到或优于基线直接 OK；劣化时看 actual/baseline 是否在 MaxDegradeRatio 内。
func judgeLowerIsBetter(actual, baseline float64) model.BaselineVerdict {
	if actual <= baseline {
		return model.BaselineOK
	}
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
