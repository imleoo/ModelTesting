# z-ai 套件说明（suite.v1.json 增量）

通用字段含义见 [`../kimi-k3/SCHEMA.md`](../kimi-k3/SCHEMA.md)，本文只写 z-ai 套件相对它的差异。

## 用例来源映射

底稿 = z.ai 实测结果表（14 项）。基线补充 = 按 07 节 SOP 从 kimi-k3 套件并入的正交协议项。

| 底稿序号 | 底稿测试项 | 底稿结果 | 本套件用例 ID | 断言类型 | 判定 |
|---|---|---|---|---|---|
| 1 | 非流式对话 | 未通过（usage 字段不稳定） | `protocol.usage_nonstream` | `usage_fields_nonstream` ×5 | 必过 |
| 2 | 流式对话 | 未通过（没有 usage 结构） | `protocol.usage_stream` | `usage_fields_stream` | 必过 |
| 3 | max_tokens 强制截断 | 通过 | `output_control.max_tokens_truncation` | `max_tokens_truncation` ×3 | 必过 |
| 4 | max_tokens 非法 | 未通过（应 400 实返 200） | `input_validation.max_tokens_over_limit` | `rejects_invalid_request` | 必过（附加） |
| 5 | 关闭思考块 | 未通过（禁用后仍输出思考） | `thinking.thinking_type`、`thinking.reasoning_effort_off` | `thinking_toggle_pair` | 视声明 |
| 6 | 思考等级 | 通过 | `reasoning_effort.scaling` | `reasoning_effort_scaling` ×3 | 视声明（附加） |
| 7 | temperature=0 | 通过 | `determinism.temperature_zero` | `deterministic_repeat` ×3 | 必过 |
| 8 | 工具调用 | 通过 | `tool.call_capability` | `tool_call_required` | 必过 |
| 9 | 指定工具调用 | 通过 | `tool.choice_function` | `tool_call_named` | 视声明 |
| 10 | 结构化输出 | 通过 | `structured.json_schema` | `json_schema_valid` | 必过 |
| 11 | 结构化输出 json_object | 通过 | `structured.json_object` | `json_parseable` | 必过 |
| 12 | stop 语义 | 通过 | `output_control.stop_semantics` | `stop_sequence_respected` | 必过 |
| 13 | 1M 上下文 | 通过 | `context.long_context_recall` | `long_context_recall` | 视声明（附加） |
| 14 | 缓存命中率 | 100.00%（指标） | `cache.prompt_cache_hit_rate` | `prompt_cache_hit_rate` | 视声明（附加） |
| — | 基线补充 | — | `protocol.stream_integrity` | `stream_integrity` | 必过 |
| — | 基线补充 | — | `tool.choice_default`/`_auto`/`_required`/`_none` | `text_or_tool_call`、`tool_call_required`、`tool_call_forbidden` | 必过 |
| — | 基线补充 | — | `thinking.default_behavior` | `default_thinking_matches_declaration` | 必过 |
| — | 基线补充 | — | `connectivity.basic` | `content_nonempty` | 必过 |

分母：18 项基础（`counts_in_base22=true`）+ 4 项附加。`counts_in_base22` 是历史字段名，语义是「计入基础分母」，不代表这里有 22 项。

底稿第 5 项拆成两条用例：z.ai 的 GLM-5.x 有两条关闭思考路径（`thinking.type=disabled` 与 `reasoning_effort=off`），只有一条失效时结论是「某个参数没接住」而不是「不支持关闭思考」，必须分开归因。

## 新增断言类型

| 断言类型 | 判定逻辑（全部条件与，任一不成立即 FAIL） | 依赖 `assertion_params` |
|---|---|---|
| `max_tokens_truncation` | `finish_reason=="length"` + `usage.completion_tokens ∈ (0, max_tokens]` + `content` 非空。usage 缺失直接 FAIL，不宽松放行 | — |
| `stop_sequence_respected` | `content` 含 `must_contain` + 不含任何 stop 序列 + `finish_reason=="stop"` | `must_contain` |
| `deterministic_repeat` | `repeat_attempts` 次输出逐字节相同（不 trim）。`repeat_attempts` 必须 ≥2 | — |
| `long_context_recall` | `usage.prompt_tokens ≥ min_prompt_tokens` + 回答中唯一数字串 == `needle` | `needle`、`min_prompt_tokens` |
| `prompt_cache_hit_rate` | 预热 `warmup_requests` 次后再发一次，对后者判 `cached_tokens / prompt_tokens ≥ min_hit_rate` | `warmup_requests`、`min_hit_rate` |

判定口径上的三个刻意选择：

- `stop_sequence_respected` 必须给 `must_contain`。只查「不含 stop 串」的话，一个空响应也能通过。
- `long_context_recall` 的 `min_prompt_tokens` 不能省。只看答案对不对，无法区分「读完全文找到了」和「输入被截断后恰好保留了暗号那一段」。
- `prompt_cache_hit_rate` 只对预热之后那次判定。首次请求必然 0 命中，拿它判定等于写了一条恒假断言。`usage` 里既无 `prompt_tokens_details.cached_tokens` 也无平铺的 `cached_tokens` 时判 FAIL 并注明「网关未回传命中数」——那是能力缺失，不是命中 0 个。

## `assertion_params`

用例级判定参数，放在用例数据里而不是写死在引擎里，让同一断言类型能被不同供应商用不同阈值复用。

| 参数 | 适用断言 | 默认值 | 说明 |
|---|---|---|---|
| `must_contain` | `stop_sequence_respected` | 空 | 输出必须含有的前置标记 |
| `needle` | `long_context_recall` | 无（缺失即 FAIL） | 埋入长文本中央的数字暗号 |
| `min_prompt_tokens` | `long_context_recall` | 无（缺失即 FAIL） | 实测输入规模下限 |
| `warmup_requests` | `prompt_cache_hit_rate` | 1 | 预热请求次数 |
| `min_hit_rate` | `prompt_cache_hit_rate` | 0.5 | 命中率门槛 |
| `required_distinct_pairs` | `reasoning_effort_scaling` | `[["low","high"]]` | 必须有可观测差异的档位对 |
| `trace_body_max_chars` | 任意 | 0（不截断） | 请求/响应体留痕的字符数上限 |

`required_distinct_pairs` 存在的原因：z.ai 在 GLM-5.2 上把 `low` 与 `high` 映射到同一档思考强度，沿用 kimi-k3 默认的 low/high 比较会恒失败——那是套件口径没对齐，不是模型不合格。本套件声明 `[["low","max"]]`，`high` 档仍采样留痕供人工观察映射关系。

`trace_body_max_chars` 默认关闭，保持设计方案 04 节「强制留痕完整请求体&响应体」的现状。只有长上下文这类单请求体上百万字符的用例才显式打开，否则一条用例就能把结果 JSON 撑到几百 MB。

## 新增请求模板占位符

`{{filler:<字符数>}}` → 生成确定性填充文本，用于长上下文与缓存用例。

三条硬约束（改动 `internal/render` 里的 `fillerSentence` 前先读）：

1. **不含阿拉伯数字** —— 长上下文用例靠「回答里唯一的数字串」判定，填充语料出现数字就会污染判定。
2. **完全确定、不随机** —— 缓存命中率用例的两次请求前缀必须逐字节一致，否则缓存必然不命中。
3. **语义上声明「本段不含答案」** —— 避免模型把填充语料当成需要总结的正文。

参数是**字符数不是 token 数**：token 数取决于供应商分词器，测试台无法本地精确计算。「输入确实达到了目标规模」由响应体的 `usage.prompt_tokens` 来验证（`min_prompt_tokens`），占位符只负责生成一份可复现的、足够大的输入。

## CAPABILITY_PROFILE 新增字段

| 字段 | z-ai 取值 | 用途 |
|---|---|---|
| `context_window_tokens` | 1048576 | >0 视为声明了 `long_context` 能力；也是校准 `min_prompt_tokens` 的依据 |
| `max_output_tokens` | 131072 | 输入校验用例构造「刚好越界」的 max_tokens 的依据 |
| `prompt_cache` | true | 声明为 false 时 `cache.prompt_cache_hit_rate` 记 NOT_DECLARED |

`image_base64` / `video_base64` 在本文件里是 `false`。这不是对 PDF「最少支持项」的豁免声明，而是本套件根本不含多模态用例（被测模型 `glm-5.2` 是纯文本模型）。要测多模态请克隆本套件、换成 `glm-5v-turbo`、补 `materials/` 与四条多模态用例，并把这两项声明改回 `true`。

## 运行

```bash
go run ./cmd/testbed-cli \
  -suite suites/z-ai/suite.v1.json \
  -base-url https://<tokenpanel 网关> \
  -api-key <key> \
  -model-key <tokenpanel 里配置的 z.ai 模型 ID> \
  -capability suites/z-ai/capability_z-ai.real.json \
  -out reports/z-ai/run-$(date +%F).json
```

## 首次运行必须校准的三项

| 项 | 位置 | 校准方法 |
|---|---|---|
| `default_thinking_behavior` | `capability_z-ai.real.json` | 现填 `thinks_by_default`。若 `thinking.default_behavior` 失败，先核对不传开关时的实际行为再改声明——这类失败通常是登记环节填错，不是模型缺陷 |
| `{{filler:750000}}` 字符数 | `context.long_context_recall` | 按「1 token ≈ 1.5 汉字」估的。跑一次后用实测 `usage.prompt_tokens` 反推，使其达到 `min_prompt_tokens` |
| `max_tokens: 131073` | `input_validation.max_tokens_over_limit` | = `max_output_tokens + 1`。换模型时必须手工同步，引擎不会从能力声明推导请求体取值 |

## 成本提示

`context.long_context_recall` 单次请求体约 4.5MB、输入约 100 万 token，耗时与费用都显著高于其他用例，不建议进每日回归，按需手动跑。类别超时已设为 900s。
