package assertion

import (
	"strings"
	"testing"
)

// 覆盖 v1.3.0 从 0820 兼容性报告引入的 4 个断言函数：只测纯函数本身的
// PASS/FAIL 判定，不经过 engine/HTTP，跑起来快、也不需要 mock 网关。

func strPtr(s string) *string { return &s }

func TestThinkingDisabledContentClean(t *testing.T) {
	cases := []struct {
		name             string
		reasoningContent *string
		content          string
		wantPass         bool
	}{
		{"干净输出", nil, "37 乘以 48 等于 1776。", true},
		{"仍返回reasoning_content字段", strPtr("让我算一下"), "37 乘以 48 等于 1776。", false},
		{"content里泄漏英文推理独白", nil, "The user is asking me to compute 37*48. 答案是 1776。", false},
		{"content里泄漏Let me organize", nil, "Let me organize my thoughts first. 答案是 1776。", false},
		{"大小写不敏感", nil, "THE USER IS ASKING for the answer, which is 1776.", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := ThinkingDisabledContentClean(tc.reasoningContent, tc.content)
			if v.Passed != tc.wantPass {
				t.Fatalf("Passed=%v, reason=%q; 期望 Passed=%v", v.Passed, v.Reason, tc.wantPass)
			}
		})
	}
}

func TestNoPromptEcho(t *testing.T) {
	prompt := "请你用中文写一段关于信任的三句话短文，直接开始正文。"
	cases := []struct {
		name     string
		content  string
		wantPass bool
	}{
		{"正常作答", "信任是人与人之间关系的基石。它需要时间积累，也可能瞬间崩塌。珍惜身边值得信赖的人。", true},
		{"原样回显prompt开头", prompt + "\n信任是……", false},
		{"prompt前缀完全重复后再作答", "请你用中文写一段关于信任的三句话，跑题了。", false},
		{"空prompt判定失败", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := prompt
			if tc.name == "空prompt判定失败" {
				p = ""
			}
			v := NoPromptEcho(p, tc.content)
			if v.Passed != tc.wantPass {
				t.Fatalf("Passed=%v, reason=%q; 期望 Passed=%v", v.Passed, v.Reason, tc.wantPass)
			}
		})
	}
}

func TestContainsAllSubstrings(t *testing.T) {
	cases := []struct {
		name     string
		content  string
		required []string
		wantPass bool
	}{
		{"两个身份都提到", "我的设定里，我既是猫也是狗。", []string{"猫", "狗"}, true},
		{"只提到一个身份", "我的设定是一只狗。", []string{"猫", "狗"}, false},
		{"required为空判定失败", "无所谓", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := ContainsAllSubstrings(tc.content, tc.required)
			if v.Passed != tc.wantPass {
				t.Fatalf("Passed=%v, reason=%q; 期望 Passed=%v", v.Passed, v.Reason, tc.wantPass)
			}
		})
	}
}

func TestRejectsWithErrorType(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		expected string
		wantPass bool
	}{
		{
			"合规错误体",
			`{"error":{"message":"top_p must be 0.95","type":"invalid_request_error","code":null}}`,
			"invalid_request_error", true,
		},
		{
			"合规错误体_code为字符串",
			`{"error":{"message":"top_p must be 0.95","type":"invalid_request_error","code":"invalid_top_p"}}`,
			"invalid_request_error", true,
		},
		{
			"type被误用成错误文本",
			`{"error":{"message":"top_p must be 0.95, got 0.9","type":"top_p must be 0.95, got 0.9","code":400}}`,
			"invalid_request_error", false,
		},
		{
			"缺少message",
			`{"error":{"type":"invalid_request_error"}}`,
			"invalid_request_error", false,
		},
		{
			"非法JSON",
			`not json`,
			"invalid_request_error", false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := RejectsWithErrorType([]byte(tc.raw), tc.expected)
			if v.Passed != tc.wantPass {
				t.Fatalf("Passed=%v, reason=%q; 期望 Passed=%v", v.Passed, v.Reason, tc.wantPass)
			}
		})
	}
}

func TestNoFabricationVerdict(t *testing.T) {
	cases := []struct {
		name        string
		judgeOutput string
		wantPass    bool
	}{
		{"纯CLARIFICATION", "CLARIFICATION", true},
		{"小写clarification", "clarification", true},
		{"带多余空白", "  CLARIFICATION  \n", true},
		{"纯FABRICATION", "FABRICATION", false},
		{"两个词都出现判失败", "先 CLARIFICATION 又 FABRICATION", false},
		{"都不是判失败", "我不确定", false},
		{"空字符串判失败", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := NoFabricationVerdict(tc.judgeOutput)
			if v.Passed != tc.wantPass {
				t.Fatalf("Passed=%v, reason=%q; 期望 Passed=%v", v.Passed, v.Reason, tc.wantPass)
			}
		})
	}
}

func TestFabricationJudgePrompt(t *testing.T) {
	p := FabricationJudgePrompt("抱歉，我没有查到相关数据，能否提供更多信息？")
	if !strings.Contains(p, "CLARIFICATION") || !strings.Contains(p, "FABRICATION") {
		t.Fatalf("judge prompt 应同时包含两个可选判定词，实际: %s", p)
	}
	if !strings.Contains(p, "抱歉，我没有查到相关数据，能否提供更多信息？") {
		t.Fatalf("judge prompt 应包含原样待判断回复文本，实际: %s", p)
	}
}
