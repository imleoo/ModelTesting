// Package benchmark 实现设计方案 06 节压测引擎：按 6.1 节固化参数生成
// ShareGPT 风格多轮会话负载、按爬坡到达率发起并发请求、按 6.2 节规则
// 判定回传指标。
//
// PDF/设计方案只给出了各参数的分位数摘要（avg/p50/p75/p90/p95），没有给出
// 原始 ShareGPT 数据集本身，因此本包用 PercentileSampler 把已知分位点重建成
// 一个可采样的分布（分段对数线性插值），而不是凭空捏造或要求外部数据集——
// 这是"固化参数字面复现"与"可执行"之间能做到的最接近实现，重建方法本身
// 在 BenchmarkRun.raw_params_json 里完整留痕，可审计、可复现。
//
// 重要限制：本采样器只精确复现已知的分位点（p50/p75/p90/p95）本身，不对
// 采样得到的经验均值做单独校准。PDF 给出的 avg 通常明显高于 p50（右偏长尾
// 分布），而尾部外推又被 tailCapMultiplier 硬性限幅（见下方常量注释）——
// 这两个因素叠加，会让本采样器的经验均值系统性低于 PDF 的 avg（实测偏差可
// 达 20% 量级）。这是"可执行、不失控"与"逐项数值精确复现"之间的明确取舍，
// 不是需要修复的 bug；如果未来需要经验均值也贴合 PDF，需要改成更复杂的
// 分布拟合方法（如样条+矩匹配），当前版本未做这个投入。
package benchmark

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
)

// PercentilePoint 是分布的一个已知分位点：P 是分位（0.0-1.0），Value 是该分位对应的值。
type PercentilePoint struct {
	P     float64
	Value float64
}

// PercentileSampler 用分段对数线性插值在给定分位点之间采样，精确复现
// PDF 6.1 节给出的 p50/p75/p90/p95 分位点；不单独校准经验均值，与 PDF
// 给出的 avg 可能有系统性偏差，见包注释。
type PercentileSampler struct {
	points []PercentilePoint // 按 P 升序排列，floor 与 tail 已补齐
	name   string
}

// NewPercentileSampler 从 PDF 给出的分位点（如 p50/p75/p90/p95，PDF 里部分
// 参数只给了 p50/p95 两个点）构造采样器，自动补上 P=0 下限与 P=0.999 长尾外推点。
// floor 是 P=0 处的下限值（必须 > 0，用于支持对数插值）；
// tailP999 是外推的 P=0.999 处的值（用于承载长尾，避免采样值被最大分位硬截断）。
// known 必须按 P 升序传入，且不含 P=0 或 P=0.999。
func NewPercentileSampler(name string, floor float64, known []PercentilePoint, tailP999 float64) (*PercentileSampler, error) {
	if floor <= 0 {
		return nil, fmt.Errorf("sampler %q: floor 必须为正数，实际为 %v", name, floor)
	}
	if math.IsNaN(tailP999) || math.IsInf(tailP999, 0) {
		return nil, fmt.Errorf("sampler %q: tailP999 必须是有限值，实际为 %v", name, tailP999)
	}
	points := make([]PercentilePoint, 0, len(known)+2)
	points = append(points, PercentilePoint{P: 0.0, Value: floor})
	points = append(points, known...)
	points = append(points, PercentilePoint{P: 0.999, Value: tailP999})
	for i := 1; i < len(points); i++ {
		if points[i].P <= points[i-1].P {
			return nil, fmt.Errorf("sampler %q: 分位 P 必须严格递增，points[%d].P=%v <= points[%d].P=%v",
				name, i, points[i].P, i-1, points[i-1].P)
		}
		if points[i].P > 1 || points[i].P < 0 {
			return nil, fmt.Errorf("sampler %q: 分位 P 必须落在 [0,1] 区间，points[%d].P=%v", name, i, points[i].P)
		}
		if points[i].Value < points[i-1].Value {
			return nil, fmt.Errorf("sampler %q: 分位点必须单调不减，points[%d]=%v < points[%d]=%v",
				name, i, points[i].Value, i-1, points[i-1].Value)
		}
		if points[i].Value <= 0 {
			return nil, fmt.Errorf("sampler %q: 分位点必须为正数以支持对数插值，points[%d]=%v", name, i, points[i].Value)
		}
	}
	return &PercentileSampler{points: points, name: name}, nil
}

// Sample 用给定随机源采样一个值。
func (s *PercentileSampler) Sample(rng *rand.Rand) float64 {
	u := rng.Float64()
	for i := 1; i < len(s.points); i++ {
		if u <= s.points[i].P {
			lo, hi := s.points[i-1], s.points[i]
			if hi.P == lo.P {
				return hi.Value
			}
			frac := (u - lo.P) / (hi.P - lo.P)
			// 对数空间线性插值：适合这类右偏长尾分布（如 input-length p50=493
			// 到 p95=4740，近 10 倍跨度），比线性插值更贴近真实分布形状。
			logVal := math.Log(lo.Value) + frac*(math.Log(hi.Value)-math.Log(lo.Value))
			return math.Exp(logVal)
		}
	}
	return s.points[len(s.points)-1].Value
}

// MarshalJSON 把采样器完整暴露为 JSON（分位点 + 名称），供 6.3 节
// "完整参数 JSON" 留痕使用——PercentileSampler 的字段本身未导出（不希望
// 调用方绕过 NewPercentileSampler 直接构造），但留痕要求参数必须完整可读，
// 所以单独实现 MarshalJSON 而不是导出字段本身。
func (s *PercentileSampler) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Name   string            `json:"name"`
		Points []PercentilePoint `json:"points"`
	}{Name: s.name, Points: s.points})
}

// SampleInt 采样并四舍五入为非负整数（用于 token 数、轮数等计数型参数）。
func (s *PercentileSampler) SampleInt(rng *rand.Rand) int {
	v := int(math.Round(s.Sample(rng)))
	return max(v, 0)
}

// tailCapMultiplier 是尾部外推的硬上限倍数：外推结果不允许超过最后一个已知
// 分位点（通常是 p95）的这个倍数。
//
// 早期实现直接把帕累托幂律外推到 P=0.999，在 turn-interval 这类"尾部很陡"
// 的参数上（p90=35s 到 p95=86s，仅 5 个百分点就翻了 2.46 倍）会指数级发散，
// 实测外推出约 13741 秒（近 3.8 小时）的会话轮次间隔——这种值会直接拖垮一次
// 压测的可执行性，也早已偏离"长尾但合理"的范畴。用仅两个相邻经验分位点去
// 外推一个离它们很远的分位（0.999）本身就是数值不稳定的，因此改为"有上限的
// 长尾"：仍用帕累托公式估计形状，但结果裁剪到不超过 p95 的 tailCapMultiplier
// 倍——牺牲了尾部的理论精确性，换取一个不会让压测失控的可执行近似，这个
// 取舍在此明确记录，不是精确复现原始分布。
const tailCapMultiplier = 2.0

// extrapolateParetoTail 用最后两个已知分位点做帕累托尾部外推，估计 targetP
// （如 0.999）处的值，再裁剪到不超过 vB*tailCapMultiplier（见上方注释）。
func extrapolateParetoTail(pA, vA, pB, vB, targetP float64) float64 {
	capValue := vB * tailCapMultiplier
	if vA <= 0 || vB <= vA || pB <= pA || pB >= 1 {
		return capValue // 输入不满足外推前提时的保守兜底：直接用上限
	}
	// (1-pA)/(1-pB) = (vB/vA)^alpha  =>  alpha = ln((1-pA)/(1-pB)) / ln(vB/vA)
	alpha := math.Log((1-pA)/(1-pB)) / math.Log(vB/vA)
	if alpha <= 0 || math.IsInf(alpha, 0) || math.IsNaN(alpha) {
		return capValue
	}
	// vTarget/vB = ((1-pB)/(1-targetP))^(1/alpha)
	ratio := math.Pow((1-pB)/(1-targetP), 1/alpha)
	raw := vB * ratio
	if raw > capValue || math.IsInf(raw, 0) || math.IsNaN(raw) {
		return capValue
	}
	return raw
}
