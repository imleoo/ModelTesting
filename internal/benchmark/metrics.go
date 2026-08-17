package benchmark

import (
	"math"
	"sort"
)

// Percentiles 计算一组样本的 avg/p50/p75/p90/p95/p99。样本为空时全部返回 0。
type Percentiles struct {
	Avg, P50, P75, P90, P95, P99 float64
}

func ComputePercentiles(samples []float64) Percentiles {
	if len(samples) == 0 {
		return Percentiles{}
	}
	sorted := append([]float64(nil), samples...)
	sort.Float64s(sorted)

	sum := 0.0
	for _, v := range sorted {
		sum += v
	}
	return Percentiles{
		Avg: sum / float64(len(sorted)),
		P50: percentileOf(sorted, 0.50),
		P75: percentileOf(sorted, 0.75),
		P90: percentileOf(sorted, 0.90),
		P95: percentileOf(sorted, 0.95),
		P99: percentileOf(sorted, 0.99),
	}
}

// percentileOf 用标准最近秩（nearest-rank）法从已排序样本里取分位数：
// rank = ceil(p*n)，取该秩对应元素（1 起计数，故索引为 rank-1）。
// 此前用 int(p*n) 截断在小样本下会系统性偏移（比如 4 个样本取 P50，
// int(0.5*4)=2 取到第 3 个元素而不是标准定义的第 2 个），足以改变
// TTFT/TPOT 这类参与硬基线判定的指标结论，因此按标准定义重写。
func percentileOf(sorted []float64, p float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	rank := min(max(int(math.Ceil(p*float64(n))), 1), n)
	return sorted[rank-1]
}
