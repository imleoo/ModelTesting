package benchmark

import (
	"math/rand"
	"strings"
)

// fillerSentences 是用于拼出目标长度请求正文的中性句子池（不含真实用户数据，
// 纯粹用于制造指定长度的负载压力，语义内容对压测本身不重要——压测关心的是
// 网关/模型在给定输入长度下的处理耗时，不是内容本身）。
var fillerSentences = []string{
	"今天天气不错，适合出门散步。",
	"请帮我总结一下这段内容的要点。",
	"这个项目的进度比预期要慢一些。",
	"我们需要重新评估这个方案的可行性。",
	"数据分析显示用户活跃度有所提升。",
	"请注意检查代码里的边界条件处理。",
	"这次会议讨论了下个季度的规划。",
	"系统在高并发场景下表现基本稳定。",
	"文档里提到的几个假设需要进一步验证。",
	"团队正在推进这个功能的联调测试。",
}

// approxCharsPerToken 是中文场景下字符数到 token 数的粗略换算比例（未接入
// 真实分词器时的近似估计，压测请求体留痕里会注明这是近似值，不是精确 token
// 计数——真实 token 数以网关回传的 usage.prompt_tokens 为准）。
const approxCharsPerToken = 1.6

// GenerateText 生成大致对应 targetTokens 个 token 的中文填充文本。
func GenerateText(rng *rand.Rand, targetTokens int) string {
	targetChars := max(int(float64(targetTokens)*approxCharsPerToken), 4)
	var sb strings.Builder
	for sb.Len() < targetChars {
		sb.WriteString(fillerSentences[rng.Intn(len(fillerSentences))])
	}
	s := sb.String()
	// 按 rune 截断，避免切断多字节字符。
	runes := []rune(s)
	if len(runes) > targetChars {
		runes = runes[:targetChars]
	}
	return string(runes)
}
