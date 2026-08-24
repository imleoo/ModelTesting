# Kimi-K3 功能自测结果对比（we2ai 中转 vs 官方 api.moonshot.cn）

## 一、环境信息

| 项 | we2ai 中转 | 官方 api.moonshot.cn |
|---|---|---|
| 模型 ID | `kimi-k3` | `kimi-k3` |
| API 端点 | `https://api.we2ai.com/v1/chat/completions` | `https://api.moonshot.cn/v1/chat/completions` |
| 测试日期 | 2026-08-24 | 2026-08-24 |
| 套件版本 | `suites/kimi-k3/suite.v1.json`（suite_version 1.1.0，19 项基础 + 6 项附加，共 28 条用例） | 同左，base_path 均为默认 `/v1/chat/completions`，未做协议改写 |
| 功能测试留痕 | `reports/kimi-k3/run-2026-08-24-we2ai.json` | `reports/kimi-k3/run-2026-08-24-official.json` |
| 压测留痕 | `reports/kimi-k3/benchmark-2026-08-24-we2ai.json`（+`.log`） | `reports/kimi-k3/benchmark-2026-08-24-official.json`（+`.log`） |
| 汇总报告 | `reports/kimi-k3/report-2026-08-24-we2ai.html` | `reports/kimi-k3/report-2026-08-24-official.html` |

两渠道使用**完全相同的套件定义、能力声明、请求体**，仅 base URL / API Key 不同，压测参数也一致（`-total-sessions 3 -seed 1 -request-timeout 120s`），结果差异可直接归因于渠道本身。

## 二、能力声明（`capability_kimi-k3.real.json`）

`tool_call` 支持，`tool_choice_function` 未声明；四种思考开关方式声明（`enable_thinking`/`thinking_type`/`chat_template_kwargs_enable_thinking`/`chat_template_kwargs_thinking`），`default_thinking_behavior=thinks_by_default`，`reasoning_effort` 支持；多模态仅声明 `image_base64`/`video_base64`（`image_url`/`video_url` 未声明）。

## 三、功能测试结果对比表（19 项计入基础分母 + 6 项附加不计入）

| 用例 | we2ai | 官方 API | 平均延迟 we2ai / 官方 | 说明 |
|---|---|---|---|---|
| connectivity.basic | ✅ PASS | ✅ PASS | 5.5s / 8.4s | |
| protocol.usage_nonstream | ✅ PASS | ✅ PASS | 3.1s / 9.0s | |
| protocol.usage_stream | ✅ PASS | ❌ FAIL | 4.3s / 11.3s | **渠道差异，见「四」1** |
| protocol.stream_integrity | ✅ PASS | ✅ PASS | 5.5s / 12.6s | |
| multimodal.image_url | ⚪ NOT_DECLARED | ⚪ NOT_DECLARED | - | 能力未声明，跳过 |
| multimodal.image_base64 | ✅ PASS | ✅ PASS | 9.7s / 4.7s | |
| multimodal.video_url | ⚪ NOT_DECLARED | ⚪ NOT_DECLARED | - | 能力未声明，跳过 |
| multimodal.video_base64 | ✅ PASS | ✅ PASS | 4.8s / 4.0s | |
| tool.call_capability | ✅ PASS | ✅ PASS | 4.3s / 4.4s | |
| tool.choice_default | ✅ PASS | ✅ PASS | 4.7s / 6.3s | |
| tool.choice_auto | ✅ PASS | ✅ PASS | 5.2s / 5.1s | |
| tool.choice_required | ✅ PASS | ✅ PASS | 4.9s / 3.8s | |
| tool.choice_none | ✅ PASS | ✅ PASS | 7.1s / 3.8s | |
| tool.choice_function | ⚪ NOT_DECLARED | ⚪ NOT_DECLARED | - | 能力未声明，跳过 |
| tool.choice_allowed_tools | ✅ PASS | ✅ PASS | 7.1s / 4.0s | |
| thinking.enable_thinking | ❌ FAIL | ❌ FAIL | 4.6s / 7.2s | 两渠道一致，见「四」2 |
| thinking.thinking_type | ✅ PASS | ❌ FAIL | 3.4s / 11.5s | **渠道差异，见「四」3** |
| thinking.chat_template_kwargs_enable_thinking | ❌ FAIL | ❌ FAIL | 9.0s / 10.2s | 官方为 429 过载，见「四」3 |
| thinking.chat_template_kwargs_thinking | ❌ FAIL | ❌ FAIL | 11.0s / 8.0s | 两渠道一致，见「四」2 |
| thinking.default_behavior | ✅ PASS | ❌ FAIL | 6.8s / 9.7s | **官方 429 过载，见「四」3** |
| structured.json_schema | ✅ PASS | ✅ PASS | 4.5s / 7.4s | |
| structured.json_object | ❌ FAIL | ✅ PASS | 10.0s / 7.8s | **渠道差异，见「四」4** |
| reasoning_effort.scaling（附加） | ❌ FAIL | ❌ FAIL | 25.4s / 44.2s | 见「四」5，均为聚合/采样问题，非渠道差异 |
| input_validation.max_tokens_negative（附加） | ✅ PASS | ✅ PASS | 0.3s / 0.1s | |
| input_validation.tool_result_content_object（附加） | ✅ PASS | ✅ PASS | 1.0s / 0.1s | |
| input_validation.duplicate_content_parts（附加） | ❌ FAIL | ❌ FAIL | 10.2s / 10.4s | 两渠道一致，见「四」6 |
| input_validation.invalid_role（附加） | ❌ FAIL | ✅ PASS | 16.9s / 0.1s | **渠道差异，见「四」6** |
| input_validation.corrupted_image_base64（附加） | ✅ PASS | ✅ PASS | 13.9s / 0.1s | |

**19 项基础用例通过数：we2ai 15/19，官方 API 13/19**（`counts_in_base22=false` 的 6 项附加用例不计入分母）。

**验收结论（按设计方案 08 节规则 1）**：两渠道基础用例通过率均未达 100% → 均为 **总体 FAIL**。7 项 FAIL 中有 5 项两渠道结果不同（`protocol.usage_stream`、`thinking.thinking_type`、`thinking.chat_template_kwargs_enable_thinking`、`thinking.default_behavior`、`structured.json_object`、`input_validation.invalid_role`，共 6 项差异，超过 z-ai 那轮对比的 1 项），说明 kimi-k3 在两个渠道上的行为一致性明显更差。

## 四、关键发现（均可通过 `run-2026-08-24-*.json` 的 `case_attempts` 追溯完整请求/响应体）

### 1. `protocol.usage_stream`：官方直连缺少 `usage` 字段，we2ai 正常

请求体两渠道完全一致（`stream:true`，未显式传 `stream_options.include_usage`）。we2ai 返回的流式响应最终 chunk 含 `usage` 字段，PASS；官方直连返回的流未包含 `usage` 字段，`fail_reason` 为「缺少 usage 字段」。属于协议层真实差异——we2ai 中转可能默认注入了 `include_usage` 或对响应做了补全，官方原生行为是不传该参数就不返回 usage。

### 2. `thinking.enable_thinking` / `thinking.chat_template_kwargs_thinking`：两渠道一致 FAIL，非渠道问题

两渠道对 `off` 变体的请求，模型仍返回了 `reasoning_content`（或等价字段），即通过 `enable_thinking=false`/`chat_template_kwargs.thinking=false` 关闭思考开关未生效。两渠道现象一致，说明是 kimi-k3 模型本身对这两种关闭思考的取值不敏感，与走哪个渠道无关。

### 3. 官方直连在思考相关用例上多次触发 `429 engine_overloaded_error`

`thinking.thinking_type`、`thinking.chat_template_kwargs_enable_thinking`、`thinking.default_behavior` 三个用例，官方直连在 `off`/默认变体的请求上均返回：

```json
{"error":{"message":"The engine is currently overloaded, please try again later","type":"engine_overloaded_error"}}
```

HTTP 状态码 429。we2ai 中转对同样的请求全部 200 正常返回。压测阶段（见「五」）官方直连同样出现 1 次 429，we2ai 压测零失败。**本次测试窗口内官方 API 出现多次过载/限流，we2ai 中转没有复现**——这可能反映官方直连侧容量/限流策略更紧，也可能是 we2ai 后端有更宽松的排队或多节点分流；仅凭本次单次运行不能断定是系统性差异还是偶发窗口拥堵，建议官方直连侧择时重跑一次交叉验证。

### 4. `structured.json_object` 是本次除「四.3」外唯一的真实渠道行为差异

请求体两渠道逐字节相同（`response_format:{type:"json_object"}`）。we2ai 返回的 `content` 带 Markdown 代码块围栏（`` ```json ... ``` ``），断言 `content 不是合法 JSON: invalid character '`' looking for beginning of value` FAIL；官方直连返回干净 JSON，PASS。与 z-ai 那轮结论方向相反——那轮是官方在 `json_schema` 严格模式下加了围栏，这轮是 we2ai 在 `json_object` 宽松模式下加了围栏。说明"是否额外包裹 Markdown 围栏"这件事两个渠道都不稳定，且没有固定的"谁更规范"的结论，依赖 JSON 结构化输出的场景无论走哪个渠道都需要做一层围栏剥离兜底。

### 5. `reasoning_effort.scaling`：两渠道均 FAIL，但根因不同，均非渠道问题

- **we2ai**：`max` 档第 2 次采样 HTTP 503，导致该档采样不完整，无法可靠聚合 `reasoning_tokens`。
- **官方 API**：全部采样请求都成功（9/9），但 `low` 档 3 次采样的 `reasoning_tokens` 取值分散，未形成多数结果。

we2ai 是传输层偶发失败，官方是模型输出本身在低 effort 档位下方差较大，两者都不是渠道路由逻辑导致，且与 z-ai 那轮报告中"引擎聚合规则问题"性质类似，建议对该用例的聚合容忍度做校准。

### 6. `input_validation`：`duplicate_content_parts` 两渠道一致，`invalid_role` 渠道不同

- `duplicate_content_parts`：两渠道均返回 HTTP 200，未对非法输入（重复 content parts）做校验，直接当合法请求处理，现象一致。
- `invalid_role`：we2ai 返回 HTTP 200（未校验非法 role 直接处理），FAIL；官方直连正确拒绝/校验了非法 role，PASS。这是本次唯一体现"官方直连输入校验更严格"的用例。

## 五、压测（benchmark-cli，`-total-sessions 3 -seed 1 -request-timeout 120s`）对比

| 指标 | we2ai | 官方 API | 说明 |
|---|---|---|---|
| 总请求数 | 91 | 88（含 1 次 429 失败） | we2ai 压测阶段零失败 |
| 总耗时 | 814.5s | 902.3s | |
| 吞吐 req/s | 0.112 | 0.096 | 两者均判 `FAIL`（低于设计方案基线，两渠道同病） |
| 吞吐 input tok/s | 2275.97 | 1963.52 | |
| 吞吐 output tok/s | 4.39 | 5.47 | 官方单请求输出 token 吞吐反而更高 |
| TTFT（首字延迟）P50 | 3.08s | 6.54s | **we2ai 明显更快** |
| TTFT P90 / P99 | 12.91s / 24.23s | 14.81s / 21.56s | 尾部延迟两渠道接近 |
| TPOT（逐 token）P50 | 1.47ms | 0.003ms | 官方样本中大量极小值拉低中位数，P90/P99（8.10ms/18.89ms vs 5.85ms/11.02ms）更能反映真实差距——we2ai 尾部更高 |
| 端到端延迟 P50 | 3.34s | 7.18s | **we2ai P50 延迟约为官方的一半** |
| 端到端延迟 P90 / P99 | 15.53s / 39.84s | 11.64s / 28.69s | we2ai 尾部（P99）比官方更长，说明 we2ai 中位数快但尾部抖动更大 |
| 失败请求 | 0 | 1（HTTP 429） | 与「四.3」的过载现象吻合 |

**结论**：吞吐两渠道同为 `FAIL`（均低于设计方案基线，样本量小 `total-sessions=3` 波动也大，仅供参考，不建议据此下渠道容量结论）。延迟层面 we2ai 的 P50 首字/端到端延迟明显更低，但 P99 尾部延迟比官方更长，波动更大；官方直连本次出现 1 次真实 429 失败，与功能测试阶段的过载现象相互印证，提示官方直连在本次测试窗口内存在容量/限流压力。

## 六、总体结论

1. **验收结论**：两渠道 19 项基础用例通过率均未达 100%（we2ai 15/19，官方 13/19），按规则均为总体 FAIL。
2. **真实渠道行为差异（4 项）**：`protocol.usage_stream`（we2ai 有 usage，官方无）、`structured.json_object`（围栏包裹方向不同）、`input_validation.invalid_role`（官方校验更严格）、以及官方直连在思考类用例上的 429 过载（`thinking.thinking_type`/`thinking.chat_template_kwargs_enable_thinking`/`thinking.default_behavior`）。
3. **模型本身问题，非渠道差异（3 项）**：`thinking.enable_thinking`/`chat_template_kwargs_thinking` 关闭思考开关不生效、`reasoning_effort.scaling` 聚合/采样问题、`input_validation.duplicate_content_parts` 均未做输入校验。
4. **压测层面**：we2ai 中位数延迟更低，官方本次出现真实过载失败；吞吐两渠道均不达基线，样本量小（3 会话）建议后续加大 `-total-sessions` 重跑以提高置信度。
5. **待复核项**：官方直连的多次 429 过载建议择时重跑排除偶发拥堵；`suites/kimi-k3/suite.v1.json` 中 `reasoning_effort.scaling` 用例的采样聚合容忍度建议参照 z-ai 那轮的处理方式做校准。
