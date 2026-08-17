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
- `{{prompts.xxx}}` / `{{fixtures.tools.xxx}}` → `fixtures` 节点对应值
- `{{material:<material_id>:url}}` / `{{material:<material_id>:base64}}` / `{{material:<material_id>:question_prompt}}` → 从 `materials/manifest.json` 取值；`url` 形态要求素材已通过自托管静态服务对外可达（见 manifest 中 `hosting` 字段的待确认状态）

`variants[].request_overrides` 与 `request_template.body` 做浅合并（顶层键覆盖），产出该变体的最终请求体。

## 尚未解决的假设（需 P1/P2 联调时核实，不代表本文件已默认成立）

1. `multimodal.video_url` / `multimodal.video_base64` 的 `video_url` content part 是本方案假设的 schema，PDF 原文未给出具体字段名。
2. `tool.choice_allowed_tools` 的请求体假设为 OpenAI 现行草案格式 `{type:allowed_tools, allowed_tools:{mode, tools}}`。
3. 素材 `url` 形态依赖测试台自托管静态文件对 tokenpanel（49.233.9.153）公网/内网可达，具体路径待部署阶段（设计方案 13 节）确认。
