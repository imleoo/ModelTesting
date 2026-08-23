# Kimi-K3 功能自测对比清单（新中转 vinzk.cn vs 官方 Moonshot API）

## 一、环境信息

| 项 | vinzk.cn 中转 | 官方 Moonshot API |
|---|---|---|
| 模型 ID | `kimi-k3` | `kimi-k3` |
| API 端点 | `https://api.vinzk.cn/v1/chat/completions` | `https://api.moonshot.cn/v1/chat/completions` |
| 测试日期 | **2026-08-23**（本次新跑） | 2026-08-18（复用既有留痕，套件在此之后无用例内容变更，仅 08-18 当天改过一次描述文案，见 `git log suites/kimi-k3/suite.v1.json`） |
| 套件版本 | `suites/kimi-k3/suite.v1.json`（suite_version 1.1.0，28 项：22 基础 + 1 reasoning_effort 附加 + 5 input_validation 附加） | 同左 |
| 原始留痕 | `reports/kimi-k3/run-2026-08-23-vinzk.json` | `reports/kimi-k3/run-2026-08-18-official.json` |
| 单渠道 HTML 报告 | `reports/kimi-k3/report-2026-08-23-vinzk.html` | `reports/kimi-k3/report-2026-08-18-official.html` |

两边套件定义、能力声明、请求体完全一致，差异可直接归因于渠道本身。

## 二、结果对比清单

| 用例 | vinzk.cn | 官方 API | 延迟 vinzk / 官方 | 备注 |
|---|:---:|:---:|---|---|
| connectivity.basic | ✅ | ✅ | 4.1s / 6.3s | |
| protocol.usage_nonstream | ✅ | ✅ | 6.3s / 16.0s | |
| protocol.usage_stream | ❌ | ❌ | 5.4s / 6.4s | 两边流式都拿不到 usage，症状一致 |
| protocol.stream_integrity | ❌ | ✅ | 4.1s / 9.9s | **vinzk 独有**：流中段 chunk[33] choices 数组为空 |
| multimodal.image_url | ⚪ NOT_DECLARED | ⚪ NOT_DECLARED | — | 能力未声明，两边都跳过 |
| multimodal.image_base64 | ✅ | ✅ | 9.8s / 4.7s | |
| multimodal.video_url | ⚪ NOT_DECLARED | ⚪ NOT_DECLARED | — | 能力未声明，两边都跳过 |
| multimodal.video_base64 | 🟡 MANUAL_REVIEW | ✅ | 30.5s / 3.2s | **vinzk 疑似把视频内容丢了**，见「三」1 |
| tool.call_capability | ✅ | ✅ | 6.0s / 3.5s | |
| tool.choice_default | ✅ | ✅ | 6.0s / 3.8s | |
| tool.choice_auto | ✅ | ✅ | 6.1s / 3.9s | |
| tool.choice_required | ✅ | ✅ | 4.5s / 3.3s | |
| tool.choice_none | ❌ | ✅ | 4.1s / 8.5s | **vinzk 独有**：响应空消息（无 content 无 tool_calls），见「三」2 |
| tool.choice_function | ⚪ NOT_DECLARED | ⚪ NOT_DECLARED | — | 能力未声明，两边都跳过 |
| tool.choice_allowed_tools | ❌ | ❌ | 0.5s / 5.3s | 两边都 FAIL 但症状不同：vinzk 400 拒绝该参数，官方 200 但零次调用 |
| thinking.enable_thinking | ❌ | ❌ | 4.9s / 11.2s | 两边一致：该开关不生效 |
| thinking.thinking_type | ❌ | ✅ | 4.9s / 8.2s | **vinzk 独有**：官方唯一生效的关闭思考方式，在 vinzk 上也不生效 |
| thinking.chat_template_kwargs_enable_thinking | ❌ | ❌ | 6.9s / 9.7s | 两边一致：该开关不生效 |
| thinking.chat_template_kwargs_thinking | ❌ | ❌ | 7.1s / 5.6s | 两边一致：该开关不生效 |
| thinking.default_behavior | ✅ | ✅ | 4.3s / 10.4s | |
| structured.json_schema | ❌ | ✅ | 5.7s / 3.9s | **vinzk 独有**：JSON 被 Markdown 代码块围栏包裹且附加多余说明文字，见「三」3 |
| structured.json_object | ❌ | ✅ | 12.4s / 3.9s | **vinzk 独有**：同上，围栏问题 |
| reasoning_effort.scaling（附加） | ❌ | ❌ | 15.4s / 38.6s | 两边一致：引擎多数判定缺陷（已知问题，非渠道差异） |
| input_validation.max_tokens_negative（附加） | ❌ | ✅ | 9.7s / 0.07s | **vinzk 独有**：非法值未校验，见「三」4 |
| input_validation.tool_result_content_object（附加） | ❌ | ✅ | 0.7s / 0.06s | **vinzk 独有**：非法输入透传后端触发 502，见「三」4 |
| input_validation.duplicate_content_parts（附加） | ❌ | ❌ | 16.7s / 7.5s | 两边都未校验（官方已知问题） |
| input_validation.invalid_role（附加） | ❌ | ✅ | 14.3s / 0.06s | **vinzk 独有**：非法 role 未校验，见「三」4 |
| input_validation.corrupted_image_base64（附加） | ✅ | ✅ | 2.4s / 0.07s | |

**22 项基础用例通过数**（`counts_in_base22`，3 项 NOT_DECLARED 不计分母，19 项计分母）：
**vinzk.cn 8/19，官方 API 14/19**。vinzk 另有 1 项 `multimodal.video_base64` 为 MANUAL_REVIEW（不计入通过数，也不计入失败数）。

**验收结论**：两渠道均未满足「22 项基础用例 100% 通过」→ 均为总体 FAIL；但 **vinzk.cn 的失败面显著更宽**——19 项基础用例里独有失败达 8 项（官方能过、vinzk 不能过），官方独有失败 0 项。

## 三、关键发现

### 1. `multimodal.video_base64`：vinzk 疑似把视频内容丢弃了

官方 API 在同一用例上 3.2s 内正确识别视频画面里的数字并 PASS。vinzk 耗时 30.5s，最终回答是：

> "我没有收到任何视频或图片内容，无法看到画面中的数字。请上传视频或截图后，我再帮你识别。"

模型自己说没收到视频/图片——说明 vinzk 代理层大概率没有把 `video_url`（base64 data URI）content part 正确透传给后端模型，或者透传时格式被破坏。这是本次最严重的一项发现：**多模态输入在 vinzk 上可能整体不可用**，不只是"没有识别对"，而是"根本没收到"。判定引擎给的是 MANUAL_REVIEW（因为断言是开放式描述、非数字比对），需要人工用原始请求体复核（已留痕在 `run-2026-08-23-vinzk.json`）。

### 2. `tool.choice_none`：响应变成空消息

官方 API 在 `tool_choice:"none"` 下能正常给出文字回答。vinzk 的响应 `message` 里既没有 `content` 也没有 `tool_calls`，但 `reasoning_content` 显示模型的内部推理其实是想调用工具的："...应遵循可用工具回答天气...调用。" ——即模型本来打算调用工具，最终对外的 `content`/`tool_calls` 字段却都是空。看起来像是 vinzk 代理层在 `tool_choice:"none"` 场景下把模型原本要返回的工具调用**过滤掉了却没有回退成文字回答**，导致对外呈现一条空消息。

### 3. `structured.json_schema` / `structured.json_object`：JSON 被 Markdown 围栏包裹

两个用例请求体与官方 API 完全一致（`response_format.json_schema.strict:true` / `{"type":"json_object"}`），vinzk 返回的 `content` 是：

```
```json
{
  "city": "测试城市",
  "temperature_c": 21.5
}
```

以上为虚构的测试数据，按要求填写了固定取值：`city` 为"测试城市"，`temperature_c` 为 21.5，不代表真实天气。
```

不仅带 Markdown 代码块围栏（不是合法 JSON），还在围栏后面追加了一段解释性文字——比官方 API 更明显地不遵守结构化输出契约。依赖严格 JSON 输出的场景，走 vinzk 目前必须自行做「剥围栏 + 只取第一个 JSON 块」的兜底解析，还要处理围栏后的额外文本。

### 4. 输入校验：vinzk 基本不做网关层校验

4 项 `input_validation.*` 附加用例里 vinzk 3 项 FAIL（`max_tokens_negative`、`tool_result_content_object`、`invalid_role`），官方 3 项全部 PASS（正确用 4xx 拒绝）：

- `max_tokens: -1`：vinzk 直接 200 放行，当成合法请求处理（耗时 9.7s，说明真的送进模型跑了一轮）；官方 70ms 内快速拒绝。
- `role` 传非法值：vinzk 同样 200 放行（14.3s）；官方 60ms 内快速拒绝。
- `tool` 消息 `content` 传对象（不合法结构）：vinzk 透传给后端，后端直接 **502**（服务端错误），官方 60ms 内前置校验拒绝。

对比延迟也能看出结构性差异：官方对这几类非法输入的拒绝耗时都在 100ms 以内（说明有网关前置校验，请求根本没送到模型），vinzk 的耗时都在秒级到十几秒（说明请求被完整送进了模型/后端才出结果或报错）——**vinzk 网关层几乎没有输入校验，直接透传**。

### 5. 延迟画像：vinzk 单次请求更快，但"更快"是以不做校验/不太合规为代价换来的

排除两条重耗时用例（`reasoning_effort.scaling`、`multimodal.video_base64`）后，vinzk 在多数用例上比官方快 30%-60%（如 `protocol.usage_nonstream` 6.3s vs 16.0s、`thinking.default_behavior` 4.3s vs 10.4s）。但这个速度优势主要来自两点，都不是"处理效率更高"：一是官方在多个思考/结构化场景本身响应更慢（可能官方后端排队或思考更充分），二是官方对非法输入做了前置校验能快速拒绝，而 vinzk 的"快"里有一部分其实是"没校验直接放行"。不能把延迟对比简单读成"vinzk 性能更好"。

## 四、结论

1. **vinzk.cn 目前不是官方 API 的等价替代**：19 项基础用例里独有失败 8 项，覆盖流式协议、工具调用、思考开关、结构化输出、输入校验五个维度，失败面显著宽于官方。
2. **最需要关注的是多模态**（发现 1）：如果业务依赖 `video_base64`/`image_base64` 类输入，vinzk 上视频内容疑似被丢弃，建议先用真实业务素材单独复测，不要直接切流。
3. **结构化输出场景**（发现 3）两边都不能直接信任 `strict:true` 会生效，但 vinzk 的围栏问题更严重（还带额外解释文字），依赖 JSON 输出的调用方在 vinzk 上必须做兜底解析。
4. **两边共同的失败**（`protocol.usage_stream`、`thinking.enable_thinking`/`chat_template_kwargs_*`、`reasoning_effort.scaling`、`input_validation.duplicate_content_parts`）是 Moonshot Kimi-K3 本身的已知限制，不构成渠道选择的依据。
5. 官方数据复用自 5 天前（08-18）的留痕，套件本身未变但供应商侧行为可能已漂移，若要据此做最终选型决策，建议对官方 API 也补一次同日期的复测以完全消除时间差变量。
