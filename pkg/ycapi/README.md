# YCAPI 图片画幅与报价档位

`catalog.json` 保留原采购渠道、113 条价格记录和既有 offer ID。`image-capabilities.json` 单独记录图片比例，并按原 offer 限制可用值；同一模型的不同分辨率可能由不同渠道交付，不能把模型总比例列表直接应用到每档。

比例来自 YC 官方公开模型目录，并用现有固定采购 lane 的非收费 `pricing-estimate/batch` 核实。2026-11-05 核查 201 个组合，其中 151 个同时满足原 lane、渠道比例枚举、与该档方形相同的估价和冻结额，以及未调整参数。成功估价但渠道枚举未声明的值也不开放。此次没有发送生成请求。

当前 7 款图片模型可选择多种比例；`ycapi-gemini-3.1-flash-lite-image` 的既有渠道只声明 `1:1`。Gemini Pro 的 2K 档与 1K/4K 档、Gemini Flash 各分辨率档的比例范围不同，目录和提交校验均保留这些差异。

客户端统一把图片画幅传为 `size`，分辨率独立传为 `resolution`。插件根据模型把画幅映射到上游的 `size` 或 `aspect_ratio`，再用原 offer 注入其分辨率、质量和采购 lane。禁止通过比例、别名冲突或额外数量切换报价档。旧任务的 `spec`、`image_count`、状态和结算事实不变。

公开价格为每个可用画幅展开同档售价，使现有客户端偏好可以完整显示积分。原方形行及其有日期的官方价格参考保留；其他画幅不附带未经核实的原厂价格参考。

扩展画幅要求实际执行不可变插件 `1.0.2` 或更新版本。旧 `1.0.0/1.0.1` override 继续发布原方形目录和价格，避免新菜单先于实际执行能力生效。公开 `x-task-plugin.version` 保持已发布客户端要求的 `1.0.0`，实际版本由 `x-task-plugin-runtime` 单独声明。部署时必须上传并激活新插件版本，不能只替换内置源码；旧插件记录及原任务必须保留。

`1.0.3` 为八款图片模型开放已通过实际收费出图、解码和原任务产物读取的图生图规格。`image-capabilities.json` 的 `edit.offers` 记录每档证据摘要，公开 `x-image-edit.specifications` 只包含这些规格。目前每次允许一张不超过 5 MiB 的 PNG、JPEG 或 WebP 参考图，输出仍为一张，沿用原售价和 offer ID；不支持蒙版。Flash 的 0.5K、NB 2.1 的 2K/4K 未通过当前固定渠道实测，不开放编辑。

客户端先通过无需生成扣费的 `/v1/uploads/images` 上传，网关按当前账号可用的 YCAPI 模型选择原渠道并转发 `/v1/files`；只有合法的素材回执才能进入编辑。NB 2.1 必须使用上传回执，禁止将不受该渠道支持的内嵌图片静默转成文生图。可灵的请求明确加入“图1”指代。生成请求固定原 lane，并以原任务 ID 作为上游去重标识。

支持 `x-image-task-submit` 的客户端可发送 `Prefer: respond-async`，网关取得原异步任务后立即返回 202 和公共任务号。后台轮询、原任务 GET 和签名产物下载继续处理同一任务；默认同步接口保留。图片结果必须保留原任务和产物元信息，客户端完成下载、解码和发布后才报告完成。元信息保留不等于在网关持久保存远端图片字节，下载失败只能继续读取原产物，不能重发生成。

验证命令：

```sh
go test ./pkg/ycapi -count=1
go test ./router -run '^TestYcapi' -count=1
go test ./model -run 'Ycapi' -count=1
```

目录与价格还需通过实际发布客户端的规格、偏好和价格消费者联调。离线 wire 测试与免费估价不等于逐模型收费出图实测。

## 当前媒体合同与 V2 请求构造

`media-contracts.json` 单独保留 2026-11-23 审查的 31 款图片和 42 款视频逻辑合同，不替换旧 58 条采购目录。`MediaContracts`、`LookupMediaContract` 返回独立副本；`Available` 只表示供应商目录声明，不代表生产路由授权、报价可用或生成资格。

`ValidateMediaContractParams(modelID, params, assets)` 是报价与提交共用的逻辑校验入口。`params` 使用供应商原字段，素材统一规范化为 `[]string`；`assets` 按精确素材引用索引，由可信上传或媒体探测层提供类型、字节数、尺寸、时长、帧率及格式。不得拿请求自报的 metadata 或 URL 扩展名充当实际探测。素材超限直接拒绝，不截断、不忽略，也不把图生请求降为文生。适用的默认值会补入返回副本；原生 mode、枚举大小写、素材顺序和源约束均保持。

`AssetFields(contract)` 明确给出字段名、素材类型、原生单值/数组形状、数量范围及源已声明的 MIME 类型。没有源格式限制时，`mime_types` 为空，不补猜测的白名单。`images/videos/audios` 始终是原生数组，即使最多只允许一份素材；`url` 的单值角色由三个确切超分/去字幕模型声明。Midjourney 的 `cref/sref` 是单值图片引用，也参与素材与意图校验。`MediaContractSchema` 的内部素材属性统一是字符串数组，`x-media-assets` 和 `x-media-native-shape` 声明构造原生信封时的形状。

先调用 `ValidateMediaContractParams`，再将返回参数交给 `BuildMediaRuntimeRequest(modelID, params, laneID, publicTaskID, intent)`。`laneID` 和 `publicTaskID` 必须来自服务端已选报价路线及持久任务身份；这两个值不属于可编辑模型参数。

`BuildMediaRuntimeRequest` 只返回 `MediaRuntimeRequest`，不发网络请求、不修改任务或账务。意图只接受 `text/reference/frames/transform`：文生不能夹带素材，其他意图必须保留素材；帧与处理意图必须匹配原模型操作。构造器重复逻辑检查，但不替代前一步真实素材 metadata 校验。返回 `model_id` 为合同的原 `native_id`，图片带 `operation=generate/edit`，视频省略 operation；`params.url/cref/sref` 转成单值字符串，`images/videos/audios` 保持有序数组，不新增 frame 字段或改写 mode。

V2 原生统一入口与轮询路径来自保留的官方 OpenAPI：提交 `POST /api/v1/model-runtime/invoke`，读取 `GET /v1/tasks/{task_id}` 或原兼容 `GET /api/v1/model-runtime/tasks/{task_id}`。本包只构造信封，执行绑定及原任务恢复由 Host 适配器负责。逻辑校验通过不证明任意画幅、质量和清晰度组合能在当前采购 lane 路由；完整参数还需走同一份信封的免费 dry-run 和真实估价。

内置插件 `1.0.4` 的 V2 路径仅接受 Host 注入的私有 `ctx.mediaGenerationV2`：`nativeRequest` 为上述构造器的完整信封，`usageFacts` 为服务端核验的事实，另带 `kind` 和十进制字符串 `retailCny`。客户端 JSON 中同名对象不会启用该路径。实际提交必须把 `client_request_id` 绑定到已持久化的公共任务 ID；新增媒体 ID 没有私有上下文便拒绝执行。旧模型的参数归一化、原 offer ID、采购 lane、表达式及旧任务查询路径保持。

媒体 usageSchema 保留旧 `spec` 并增加 `media-v2`，按类型使用 `image_count` 或 `seconds`，另有 `input_image_count/input_video_seconds/input_audio_seconds`、`intent` 和 `resolution/quality/mode/aspect_ratio`。四个规格字段从原合同及旧 offer 的字符串枚举提取，并包含空串代表不适用。素材数量与时长来自可信探测，不能由客户端报价格事实。V2 原样使用 Host 已选的旧 offer 或新 `media-v2`，不走旧 offer 自动选择；报价与提交继续使用同一份已配置计费表达式。

V2 只恢复原 native task ID，保留独立状态标记，并通过 `/v1/tasks/{task_id}` 继续读取。`data[]` 优先作为完整有序产物列表，最多 64 项；图片、视频、音频和普通文件均保留，SVG 作为 `file`。单产物沿用原键，多产物键的数字后缀保留全局顺序。渲染使用 Host 生成的产物内容 URL；读取远端产物不携带供应商凭据。任务用时不能充作输出视频时长，返回原生 usage 也不会覆盖 Host 冻结的 V2 事实。
