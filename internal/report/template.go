package report

const reportTemplate = `<title>自测报告 · {{.Env.ModelID}}</title>
<style>
  body { font-family: -apple-system, "PingFang SC", "Microsoft YaHei", sans-serif; margin: 2rem auto; max-width: 1100px; color: #1a1a1a; line-height: 1.5; }
  h1 { font-size: 1.5rem; border-bottom: 2px solid #333; padding-bottom: .5rem; }
  h2 { font-size: 1.2rem; margin-top: 2rem; border-left: 4px solid #444; padding-left: .5rem; }
  table { border-collapse: collapse; width: 100%; margin: .75rem 0 1.5rem; font-size: .9rem; }
  th, td { border: 1px solid #ccc; padding: .4rem .6rem; text-align: left; vertical-align: top; }
  th { background: #f2f2f2; }
  .badge { display: inline-block; padding: .1rem .5rem; border-radius: 3px; font-weight: 600; font-size: .8rem; }
  .badge-PASS, .badge-OK { background: #d4edda; color: #155724; }
  .badge-FAIL { background: #f8d7da; color: #721c24; }
  .badge-NOT_DECLARED, .badge-NOT_APPLICABLE { background: #e2e3e5; color: #383d41; }
  .badge-MANUAL_REVIEW, .badge-PENDING, .badge-PENDING_MANUAL_REVIEW, .badge-SAME_ORDER { background: #fff3cd; color: #856404; }
  .verdict-box { border: 2px solid #333; padding: 1rem; border-radius: 6px; margin: 1rem 0; }
  .reasons { font-size: .85rem; color: #555; margin: .25rem 0 0 1rem; }
  details { margin: .25rem 0; }
  summary { cursor: pointer; color: #333; font-size: .85rem; }
  pre { white-space: pre-wrap; word-break: break-all; background: #f8f8f8; border: 1px solid #ddd; padding: .5rem; font-size: .8rem; max-height: 400px; overflow: auto; }
  .meta { font-size: .9rem; color: #444; }
  @media print { body { max-width: 100%; } details { display: block; } summary { display: none; } }
</style>

<h1>模型自测报告 · {{.Env.ModelID}}</h1>

<h2>一、环境信息</h2>
<table>
  <tr><th>模型 ID</th><td>{{.Env.ModelID}}</td></tr>
  <tr><th>API 端点</th><td>{{.Env.Endpoint}}</td></tr>
  <tr><th>测试套件</th><td>{{.Env.SuiteID}} · {{.Env.SuiteVersion}}</td></tr>
  <tr><th>测试日期</th><td>{{.Env.TestDate}}</td></tr>
  <tr><th>报告生成时间</th><td>{{.Env.GeneratedAt}}</td></tr>
</table>

<h2>二、功能测试结果表</h2>
<p class="meta">基础用例：{{.Base22Pass}} / {{.Base22Total}} 通过</p>
<table>
  <tr><th>用例 ID</th><th>名称</th><th>类别</th><th>状态</th><th>通过次数</th><th>失败原因</th><th>请求/响应明细</th></tr>
  {{range .Base22}}
  <tr>
    <td>{{.CaseID}}</td>
    <td>{{.Name}}</td>
    <td>{{.Category}}</td>
    <td><span class="badge badge-{{.Status}}">{{statusLabel .Status}}</span></td>
    <td>{{.PassedAttempts}}/{{.Attempts}}</td>
    <td>{{.FailReason}}</td>
    <td>{{template "attempts" .CaseAttempts}}</td>
  </tr>
  {{end}}
</table>

{{if .Additional}}
<h3>附加能力用例结果（不计入基础分母，如 reasoning_effort、长上下文、缓存命中率）</h3>
<table>
  <tr><th>用例 ID</th><th>名称</th><th>状态</th><th>通过次数</th><th>失败原因</th><th>各次采样明细</th></tr>
  {{range .Additional}}
  <tr>
    <td>{{.CaseID}}</td>
    <td>{{.Name}}</td>
    <td><span class="badge badge-{{.Status}}">{{statusLabel .Status}}</span></td>
    <td>{{.PassedAttempts}}/{{.Attempts}}</td>
    <td>{{.FailReason}}</td>
    <td>
      {{range .CaseAttempts}}{{.VariantLabel}}: {{if .ReasoningTokens}}{{.ReasoningTokens}} tokens{{end}}{{range $k, $v := .Metrics}}{{$k}}={{$v}} {{end}}（第 {{.AttemptIndex}} 次，{{if .Passed}}✓{{else}}✗{{end}}）<br>{{end}}
    </td>
  </tr>
  {{end}}
</table>
{{end}}

<h3>能力声明与豁免说明</h3>
<table>
  <tr><th>能力</th><th>声明值</th></tr>
  <tr><td>image_url</td><td>{{.Capability.ImageURL}}</td></tr>
  <tr><td>image_base64</td><td>{{.Capability.ImageBase64}}（固定必过，不可豁免）</td></tr>
  <tr><td>video_url</td><td>{{.Capability.VideoURL}}</td></tr>
  <tr><td>video_base64</td><td>{{.Capability.VideoBase64}}（固定必过，不可豁免）</td></tr>
  <tr><td>tool_call</td><td>{{.Capability.ToolCall}}</td></tr>
  <tr><td>tool_choice_function</td><td>{{.Capability.ToolChoiceFunction}}</td></tr>
  <tr><td>thinking_toggle_methods</td><td>{{range .Capability.ThinkingToggleMethods}}{{.}} {{end}}</td></tr>
  <tr><td>default_thinking_behavior</td><td>{{.Capability.DefaultThinkingBehavior}}</td></tr>
  <tr><td>reasoning_effort</td><td>{{.Capability.ReasoningEffort}}</td></tr>
  {{if .Capability.ContextWindowTokens}}<tr><td>context_window_tokens</td><td>{{.Capability.ContextWindowTokens}}</td></tr>{{end}}
  {{if .Capability.MaxOutputTokens}}<tr><td>max_output_tokens</td><td>{{.Capability.MaxOutputTokens}}</td></tr>{{end}}
  {{if .Capability.PromptCache}}<tr><td>prompt_cache</td><td>{{.Capability.PromptCache}}</td></tr>{{end}}
</table>
{{if .NotDeclared}}
<p class="meta">以下用例因能力未声明标记为 NOT_DECLARED（不计入基础分母）：</p>
<ul>{{range .NotDeclared}}<li>{{.CaseID}}（{{.Name}}）</li>{{end}}</ul>
{{else}}
<p class="meta">无 NOT_DECLARED 用例（全部能力均已声明或为固定必过项）。</p>
{{end}}

<h2>三、性能测试结果</h2>
{{if .HasBenchmarkData}}
<table>
  <tr><th>Run ID</th><td>{{.BenchmarkRun.ID}}</td></tr>
  <tr><th>原始命令</th><td><code>{{.BenchmarkRun.RawCommand}}</code></td></tr>
  <tr><th>工具版本</th><td>{{.BenchmarkRun.ToolVersion}}</td></tr>
  <tr><th>数据集版本</th><td>{{.BenchmarkRun.DatasetVersion}}</td></tr>
  <tr><th>全量日志引用</th><td>{{.BenchmarkRun.RawStdoutRef}}</td></tr>
  <tr><th>总请求数</th><td>{{.BenchmarkRun.TotalRequests}}</td></tr>
  <tr><th>压测时长</th><td>{{printf "%.1f" .BenchmarkRun.DurationS}}s</td></tr>
</table>

<table>
  <tr><th>指标</th><th>avg</th><th>p50</th><th>p75</th><th>p90</th><th>p95</th><th>p99</th><th>单位</th><th>判定</th><th>说明</th></tr>
  {{range .OverallMetrics}}
  <tr>
    <td>{{.Name}}</td>
    <td>{{printf "%.3f" .Avg}}</td>
    <td>{{printf "%.3f" .P50}}</td>
    <td>{{printf "%.3f" .P75}}</td>
    <td>{{printf "%.3f" .P90}}</td>
    <td>{{printf "%.3f" .P95}}</td>
    <td>{{printf "%.3f" .P99}}</td>
    <td>{{.Unit}}</td>
    <td><span class="badge badge-{{.BaselineVerdict}}">{{.BaselineVerdict}}</span></td>
    <td>{{.Note}}</td>
  </tr>
  {{end}}
</table>

{{if .PerRoundMetrics}}
<h3>缓存命中率 · 分轮次</h3>
<table>
  <tr><th>轮次</th><th>命中率</th><th>说明</th></tr>
  {{range .PerRoundMetrics}}
  <tr><td>{{.RoundIndex}}</td><td>{{pct .Avg}}</td><td>{{.Note}}</td></tr>
  {{end}}
</table>
{{end}}
{{else}}
<p class="meta">压测尚未执行，本报告仅包含功能测试结果；验收结论规则 3（性能指标）暂无法判定。</p>
{{end}}

<h2>四、验收结论</h2>
<div class="verdict-box">
  <p><strong>总体结论：<span class="badge badge-{{.Summary.Verdict}}">{{.VerdictLabel}}</span></strong></p>
  <p>规则 1（基础用例 100% 通过）：<span class="badge badge-{{.Summary.Rule1.State}}">{{.Summary.Rule1.State}}</span></p>
  {{if .Summary.Rule1.Reasons}}<ul class="reasons">{{range .Summary.Rule1.Reasons}}<li>{{.}}</li>{{end}}</ul>{{end}}
  <p>规则 2（已声明且不计入基础分母的附加能力用例 100% 通过）：<span class="badge badge-{{.Summary.Rule2.State}}">{{.Summary.Rule2.State}}</span></p>
  {{if .Summary.Rule2.Reasons}}<ul class="reasons">{{range .Summary.Rule2.Reasons}}<li>{{.}}</li>{{end}}</ul>{{end}}
  <p>规则 3（有 PDF 基线且可观测的性能指标满足判定）：<span class="badge badge-{{.Summary.Rule3.State}}">{{.Summary.Rule3.State}}</span></p>
  {{if .Summary.Rule3.Reasons}}<ul class="reasons">{{range .Summary.Rule3.Reasons}}<li>{{.}}</li>{{end}}</ul>{{end}}
  <p class="meta">规则 1、2、3 任一为 FAIL 即总体 FAIL；无 FAIL 但存在 PENDING（含未清空的 MANUAL_REVIEW 或缺失数据）则总体锁定为「待人工确认」，直至人工复核改写。</p>
</div>

{{define "attempts"}}
{{if .}}<details><summary>{{len .}} 次请求明细</summary>
{{range .}}
<p class="meta">第 {{.AttemptIndex}} 次（{{.VariantLabel}}）· HTTP {{.HTTPStatus}} · {{.LatencyMS}}ms · {{if .Passed}}✓ 通过{{else}}✗ 未通过：{{.FailReason}}{{end}}{{range $k, $v := .Metrics}} · {{$k}}={{$v}}{{end}}</p>
<pre>请求：{{.RequestBody}}</pre>
<pre>响应：{{.ResponseBody}}</pre>
{{end}}
</details>{{end}}
{{end}}
`
