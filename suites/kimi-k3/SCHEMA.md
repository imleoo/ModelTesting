# 套件定义文件格式（suite.v1.json）

供 P1 用例引擎读取执行，字段含义如下。

## 顶层字段

- `protocol.base_path`：请求路径，固定 `/v1/chat/completions`。
- `defaults.timeout_seconds` / `defaults.category_timeout_overrides`：单请求超时，按 `category` 匹配覆盖值，未命中用默认值。
- `fixtures.tools` / `fixtures.prompts`：用例间复用的工具定义与提示词，`request_template` 中以 `{{fixtures.tools.xxx}}`、`{{prompts.xxx}}` 引用。
- `materials_manifest`：多模态素材清单路径（见 `materials/manifest.json`）。

## `cases[]` 字段

| 字段 | 说明 |
|---|---|
| `id` | 全局唯一，`<category_key>.<name>` |
| `category` | 对应设计方案 05 节表格的中文类别 |
| `required_rule` | `fixed_required`（固定必过，能力声明不可豁免）/ `declared_required`（声明支持则必过，未声明记 `NOT_DECLARED`）/ `exempt_allowed`（可豁免，未声明记 `NOT_DECLARED`）/ `additional`（不计入 22 项分母） |
| `counts_in_base22` | 是否计入 PDF 22 项用例分母（`reasoning_effort.scaling` 为 `false`） |
| `capability_tag` | 对应 `CAPABILITY_PROFILE` 字段名；思考开关用 `thinking_toggle:<method_key>` 前缀，`method_key` 需与 `CAPABILITY_PROFILE.thinking_toggle_methods` 声明值一致 |
| `assertion_type` | 引擎内置断言函数名，定义见设计方案 04 节 4.2 表 |
| `repeat_attempts` | 单档/单变体的重复请求次数（目前仅 `reasoning_effort.scaling` 为 3，其余为 1） |
| `variants` | 可选；同一用例内的多次请求（如思考开关的开/关两次），每个变体产出一条 `CASE_ATTEMPT`，`variant_label` 写入该字段 |
| `material_ref` | 可选；引用 `materials/manifest.json` 中的素材 id 与形态（`url`/`base64`） |
| `pdf_ref` | 对应 PDF 章节号，便于审计追溯 |
| `notes` | 口径说明 / 工程假设，供人工复核 |

## 请求模板占位符

引擎渲染 `request_template.body` 时替换：

- `{{model_key}}` → `MODEL.model_key`
- `{{api_key}}` → 该模型的测试用 API Key（见设计方案 12 节，运行时注入，不落盘明文）
- `{{prompts.xxx}}` → 替换为 `fixtures.prompts` 对应字符串值，按普通字符串插值拼接
- `{{fixtures.tools.xxx}}` → **整节点替换**，不是字符串插值：当某个 JSON 节点的值**完全等于**该占位符字符串（如 `request_template.body.tools` 数组里的元素 `"{{fixtures.tools.get_weather}}"`）时，引擎须用 `fixtures.tools.get_weather` 对应的 JSON 对象**整体替换该数组元素**，产出的 `tools` 字段类型仍是对象数组；禁止先做字符串插值再当作字符串塞入数组（那样会产出字符串数组，破坏 PDF 2.4 要求的 `tools:[{type:"function",...}]` 结构）。本套件目前仅 `tools` 用到此规则，后续新增整节点占位符时同样适用。
- `{{material:<material_id>:url}}` / `{{material:<material_id>:base64}}` / `{{material:<material_id>:question_prompt}}` → 从 `materials/manifest.json` 取值；`url` 形态由 `hosting.base_url_resolution` + `hosting.frozen_url_path` 拼接得出（本地开发用 `local_dev_default`，生产由 `TESTBED_PUBLIC_BASE_URL` 环境变量提供），承载服务实现见 `cmd/materials-server`

`variants[].request_overrides` 与 `request_template.body` 做浅合并（顶层键覆盖），产出该变体的最终请求体。

## 思考内容 / reasoning_tokens 响应字段路径（P0 冻结默认值，供应商可覆盖）

设计方案 04 节 4.2 表用「`reasoning_content`（或等价字段）」「`reasoning_tokens`」描述响应字段，未给出穷举 schema。本套件冻结如下默认读取路径，供 `thinking_toggle_pair`、`default_thinking_matches_declaration`、`reasoning_effort_scaling` 三类断言使用：

| 断言用途 | 默认响应字段路径 | 说明 |
|---|---|---|
| 判断思考内容是否存在（非流式） | `choices[0].message.reasoning_content` | 与 `choices[0].message.content` 同级 |
| 判断思考内容是否存在（流式） | 各 `chunk.choices[0].delta.reasoning_content` 拼接后是否非空 | 与 `delta.content` 同级增量字段 |
| `reasoning_tokens` 取值 | `usage.completion_tokens_details.reasoning_tokens` | 对齐 OpenAI o-系列模型的 usage 扩展路径 |

若某供应商使用不同字段名（如非 `reasoning_content` 的等价字段），须在克隆套件时于该模型的 `CAPABILITY_PROFILE` 旁新增供应商级覆盖配置（字段路径本身不属于 `CAPABILITY_PROFILE` 已定义的能力布尔量，覆盖机制留待 P1 实现时按需扩展，不在本文件预先假设具体形态），并在克隆出的 `suite.vN.json` 中记录该差异，不修改本默认值。

## `answer_match` 匹配器行为

`deterministic_multimodal_qa` 断言使用 `materials/manifest.json` 顶层 `answer_match_definitions` 中登记的具体规则，本文件不重复定义，避免两处描述漂移。`digits_exact` 的判定表以该文件的 `answer_match_definitions.digits_exact.decision_rule` 为唯一权威：能提取出唯一可比对数字时按 PASS/FAIL 判定（唯一且正确→PASS，唯一但错误→FAIL）；提取不到或存在多个不同候选（无法确定唯一作答）时才置 `MANUAL_REVIEW`——与设计方案 04 节 deterministic_multimodal_qa 断言口径一致：只有『开放式描述、无法程序化比对』才进 `MANUAL_REVIEW`，能明确比对出错误答案必须判 FAIL。

## 尚未解决的假设（需 P1/P2 联调时核实，不代表本文件已默认成立）

1. `multimodal.video_url` / `multimodal.video_base64` 的 `video_url` content part 是本方案假设的 schema，PDF 原文未给出具体字段名。
2. `tool.choice_allowed_tools` 的请求体假设为 OpenAI 现行草案格式 `{type:allowed_tools, allowed_tools:{mode, tools}}`。
3. 素材 `url` 形态的路径规则与承载服务（`cmd/materials-server`）已冻结并本地验证通过，仅 `base_url` 指向的公网/内网主机待部署阶段（设计方案 13 节）确认，见 `materials/manifest.json` 各素材的 `hosting` 字段。
4. `reasoning_content` / `reasoning_tokens` 的响应字段路径为本文件冻结的默认假设（见上一节表格），非 PDF 逐字指定，供应商差异需在克隆套件时另行覆盖。
