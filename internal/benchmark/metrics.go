package benchmark

import "sort"

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

// percentileOf 用最近秩（nearest-rank）法从已排序样本里取分位数。
func percentileOf(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(p * float64(len(sorted)))
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}
