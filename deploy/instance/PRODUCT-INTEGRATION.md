# 产品对接这台 harness-api

给 Codex 的对接说明。**所有路径、字段都是在本仓库源码里逐条核过的**，不是印象。

部署对象：`8.133.202.230` 上并行新增的实例 `yunshu-harness-newapi`（只绑 `127.0.0.1:3010`，
对外只经 nginx 子路径暴露）。

---

## 1. 地址：前缀是硬要求

| 用途 | 地址 |
| --- | --- |
| 产品侧根 | `https://8.133.202.230/harness` |
| 推理（OpenAI 兼容） | `https://8.133.202.230/harness/v1` |
| 公开状态 | `GET https://8.133.202.230/harness/api/status` |

**这台机器根路径的 `/api/...`、`/v1/...` 属于另一套在跑的生产系统，绝对不能走。**
产品所有请求都必须带 `/harness` 前缀 —— nginx 会把前缀剥掉再转给本实例。

---

## 2. 登录：手机验证码，一次拿回两把凭据

本节的持久 challenge／登录交换契约是 2026-10-21 源码候选，尚未部署。新客户端显式选择 `contract_version:2`；旧客户端省略所有新增字段时保持 v1 登录兼容，当前线上旧接口不能据此推断已支持恢复。

**第一步，发码**

```
POST /harness/api/user/phone/challenge
Content-Type: application/json

{"contract_version": 2, "phone_e164": "+8613800138000", "idempotency_key": "<客户端随机生成并保存的发码键>"}
```

`idempotency_key` 使用客户端安全随机数，至少 128 位熵，允许 22–128 字符的字母、数字、`_`、`-`（可用 UUID）。发起请求前保存，同一请求恢复时保持不变。同键修改手机号会冲突。

返回 `data.challenge_id`、`data.delivery_status`、`data.expires_at`（Unix 秒）、`data.resend_after_seconds`（重发等待秒数）、`data.expires_in_minutes`。
同一号码成功发码冷却 60 秒、5 小时最多 5 次；受理未知也保留防重发预算。号码使用 E.164（带 `+86`）。

`accepted` 表示供应商已受理；`unknown` 表示可能已经发送，收到短信后仍可输入验证码；`sending` 表示仍在处理；`not_sent` 表示确定未发送。原请求保留期间，相同发码键只恢复状态，不再次发送；客户端不得复用过期请求的键。配置错误或明确拒绝不占成功次数和发送冷却；只有明确收到 `data.delivery_status=not_sent` 时，用户主动重发才生成新键。错误码本身不能证明未发送：同键恢复时若数据库读取失败，可能只有 `PHONE_CHALLENGE_UNAVAILABLE` 而没有原请求状态，应保留原键恢复。未知或处理中不能自动重发；未知请求的防重发等待持续到验证码过期。

**第二步，校验并登录**

```
POST /harness/api/user/phone/login
Content-Type: application/json

{"contract_version": 2, "phone_e164": "+8613800138000", "code": "123456", "challenge_id": "<发码回执>", "idempotency_key": "<另一个随机登录交换键>"}
```

登录交换键与发码键不同。发送前保存，同一次登录的超时、响应丢失或暂时失败均用相同手机号、challenge、验证码和交换键恢复；修改请求内容须使用新的交换键。正确验证码后的账户、赠送、relay key 或会话创建失败不会消费验证码。成功交换在 5 分钟恢复期内重放恢复同一会话，不重复注册、赠送或创建令牌；退出、撤销、安全版本变更或 refresh 轮换后禁止恢复旧结果。

返回 `data`：

| 字段 | 说明 |
| --- | --- |
| `access_token` | 调管理接口用，**15 分钟**过期 |
| `access_expires_at` | 过期时间戳 |
| `session` | 含 `sid`、`current` |
| `user` | 含 `id`、`username`、`quota` |
| **`relay_key`** | **`sk-` 开头的本账号中转 key —— 调模型就用它** |
| `relay_key_id` | 该 key 的令牌 id |

成功的新交换和恢复都通过 `Set-Cookie` 下发匹配的 `new_api_refresh`（30 天）。客户端应连同 access、relay 凭据一起保存，失败不能清除原有有效会话。

已有二次验证的账户返回 `success:true`、`code:PHONE_LOGIN_VERIFICATION_REQUIRED` 和 `data.require_verification/flow_token/methods/expires_at`，其中 `methods` 是形如 `[{"method":"2fa","available":true}]` 的对象数组。只展示明确可用的方法；TOTP 和备用码均通过 `POST /harness/api/user/login/verify` 提交 `{"flow_token":"...","method":"2fa","code":"..."}`。这是待验证中间态，没有 access token、relay key 或登录 cookie；完成既有安全验证后才返回登录凭据。同交换键只恢复仍有效的同一个验证流程。通用二次验证完成接口的响应丢失恢复不属于本次新增幂等保证。

错误保留 `success:false/message/message_key`，新增顶层 `code` 和整数 `retry_after_seconds`；有等待期时同时返回 `Retry-After`。供应商原始错误、验证码和凭据不进入客户端错误文案。

| `code` | 客户端处理 |
| --- | --- |
| `PHONE_LOGIN_DISABLED` | 管理员关闭入口 |
| `PHONE_INVALID_PARAMETERS` | 检查当前协议必填字段和格式，不自动降级重试 |
| `PHONE_RATE_LIMITED` | 按返回等待时间显示倒计时 |
| `PHONE_CHALLENGE_UNAVAILABLE` | 只有 `delivery_status=not_sent` 时允许手动换新发码键；缺少明确状态时保留原键恢复 |
| `PHONE_DELIVERY_UNKNOWN` | 保留 challenge 和验证码输入，不能自动重发 |
| `PHONE_CHALLENGE_PENDING` | 保留同发码键恢复状态 |
| `PHONE_CODE_INVALID_OR_EXPIRED` | 统一显示验证码错误或过期 |
| `PHONE_LOGIN_UNAVAILABLE` | 暂时失败，保留同登录交换键重试 |
| `PHONE_CHALLENGE_CONSUMED` | 已消费、恢复期到期或因撤销／刷新轮换等不可恢复；不能凭旧码重建会话 |
| `PHONE_IDEMPOTENCY_CONFLICT` | 同键请求发生改变，停止自动重试 |

v1 发码仍接受 `{"phone_e164":"..."}`，登录仍接受 `{"phone_e164":"...","code":"..."}`，与 v2 共用持久发码预算、未知结果防重发、验证码猜测次数、账户状态和二次验证检查。v1 的正确验证码与账户、赠送、relay key、会话一起原子提交，内部失败保留验证码；成功后验证码只能消费一次。v1 没有客户端交换键，因此不保证成功响应丢失后的会话恢复，也不能仅凭已消费验证码重新签发会话。部分新增字段缺失或显式 v2 缺字段返回参数错误，不能借此绕过新模式。

**首次验证该号码会自动注册**（落 `default` 分组），不需要单独的注册接口 ——
网页注册在这条入口上是关掉的。

`relay_key` 是每个用户一把、由服务端持有并复用（同名 `harness`），
**登录即得，不需要另外调令牌接口**。退出登录会撤销会话，但**不会**撤销这把 key。

---

## 3. 续期与退出：必须带 Origin

```
POST /harness/api/user/auth/refresh     # 凭 cookie 换新的 access_token（cookie 会轮换）
POST /harness/api/user/auth/logout      # 撤销服务端会话
```

**两个都必须带 `Origin: https://8.133.202.230`**，否则后端同源守卫直接拒绝。
刷新接口会轮换 refresh cookie，**并发刷新会互相作废** —— 客户端要做串行化。

注意路径：是 `/api/user/auth/logout`。**`/api/user/logout` 不存在（404）**。

---

## 4. 调模型

```
POST /harness/v1/chat/completions
Authorization: Bearer <relay_key>
Content-Type: application/json

{"model": "deepseek-v4-flash", "messages": [{"role":"user","content":"hi"}], "stream": true}
```

就是 OpenAI 那套请求体。**流式可用** —— `/harness/v1/...` 这条路由单独配了不缓冲。

可用模型现拉：`GET /harness/v1/models`（同样带 `Authorization: Bearer <relay_key>`）。

**手机登录的账号落在 `product` 分组**，该组当前 19 个模型：`claode-5`、`deepseek-flash`、
`deepseek-v4-flash`、`deepseek-v4-flash-0731`、`deepseek-v4-pro`、`glm-5.1`、`glm-5.2`、`gp-5.5`、
`gpt-image-2`、`kimi-k2.6`、`kimi-k2.7-code`、`MiniMax-H3`、`qwen3.6-flash`、`qwen3.6-plus`、
`qwen3.7-max`、`qwen3.7-plus`、`qwen3.8-max`、`ys-1.0`、`yunshu-internal-gpt-image-2`。
该组倍率 1，即用户实付。

其余分组：`yunshu-managed` 16 个（倍率 0，免费托管组，不是给产品用户用的）、
`default` 5 个、`yunshu-image-private` 1 个。

### 4.1 `MiniMax-H3` 是**异步视频任务**，不是聊天

别用 `/v1/chat/completions`。它走 new-api 的任务插件通道（内置 `hailuo` 插件）：

```
POST /harness/v1/video/generations           # 提交任务，返回 task_id
GET  /harness/v1/video/generations/{task_id} # 轮询到完成
```

请求体：

```json
{ "model": "MiniMax-H3", "prompt": "……", "duration": 5, "size": "768P" }
```

- `duration`：**4–15 秒**整数；也可写 `seconds`（**数字或字符串都接受**，例如 `"seconds": 5`）。
- `size`：**`768P` 或 `2K`**；也可写 `resolution`。产品两个都发也没关系。
- 可选参考图走图片字段（文生视频、图生视频、多模态参考都支持）。

**模型名就叫 `MiniMax-H3`**，不要改成 `sora-2` 或 `Minimax-H3` —— 渠道上没有做名字映射。

用户资金不足只按 **HTTP 403 + 网关 `code=insufficient_user_quota`** 识别；
不要根据错误文本、一般 403、上游余额或令牌额度不足引导用户充值。
原生插件错误响应仅对服务端标记的本地计费错误保留此码，上游同名错误码不能透传为用户资金不足。
此码不保证上游尚未受理：提交后追加预扣失败也可能返回它，客户端必须保留提交结果未知的状态，不能自动重提。

**计费是按秒**（`seconds × ¥0.3`），不是按次 —— 4 秒 = ¥1.2 = 1200 积分，预扣值可以直接从
轮询响应的 `data.quota` 看到。产品侧报价时注意口径。

Gemini 格式走 `/harness/v1beta/...`。

---

### 4.2 DeepSeek 官方忙闲时计费（2026-10-20）

本实例采用 [DeepSeek 官方人民币价格](https://api-docs.deepseek.com/zh-cn/quick_start/pricing/)，
单位为元 / 百万 Token。北京时间周一至周五（不含中国法定节假日）
09:00–12:00、14:00–18:00 为高峰；其余时间、周末及法定节假日全天为空闲。
开始时刻包含，结束时刻不包含；周末即使调休上班也按空闲计费。

| 模型 | 时段 | 输入未命中缓存 | 输入命中缓存 | 输出 |
| --- | --- | ---: | ---: | ---: |
| Flash（含兼容别名） | 空闲 | 1 | 0.02 | 4 |
| Flash（含兼容别名） | 高峰 | 2 | 0.04 | 8 |
| V4 Pro | 空闲 | 4.5 | 0.15 | 13.5 |
| V4 Pro | 高峰 | 9 | 0.30 | 27 |

使用 `tiered_expr` 模式及 `deepseek_peak()` 条件，账单记录 `peak` / `off_peak` 命中档位。
请求的可信服务器开始时间在预扣时冻结，结算沿用同一时间，跨时段不改价。
节假日来自共享日历 `pkg/billingexpr/deepseek_calendar.json`；当前覆盖 2026 年官方放假安排。
未知年份按空闲收费并由服务端告警，待官方安排公布后更新日历，不能默认为无节假日。

本实例显式采用人民币价覆盖：原始计费表达式的系数仍为 USD，因此须以当前
`usd_exchange_rate` 换算。不要把官方美元价按汇率转换后当成官方人民币价，两张官方价表不同。
`python3 deploy/instance/deepseek-official-pricing.py --usd-exchange-rate <当前值>`
只生成可审核草稿，不写生产；可用 `--snapshot` 合并现有单模型配置与版本，
然后通过现有模型定价管理界面或带版本检查的 `PATCH /api/option/model_pricing` 保存。
若以后修改展示汇率，须重新换算本实例人民币价格覆盖。内置美元默认价继续尊重管理员显式覆盖。
历史账单保留原计费规则，不追溯补扣。

---

### 4.3 参考图上传（2026-10-20）

`yunshu-internal-gpt-image-2` 的本地参考图先逐张上传，再提交图片生成：

```http
POST /harness/v1/uploads/images
Authorization: Bearer <当前用户的 relay_key>
Content-Type: multipart/form-data; boundary=<客户端自动生成>
```

表单只接受一次 `model=yunshu-internal-gpt-image-2` 和一个 `file`，拒绝重复或额外字段及查询参数。
文件须为实际 PNG、JPEG、静态 WebP 或单帧 GIF，单张不超过 **10 MiB（10485760 字节）**、**16777216 总像素**（例如 4096×4096；此限制针对总像素数，不是单边尺寸）。
表单须不压缩（不发送 `Content-Encoding`，或使用 `identity`）；动画 GIF/WebP 返回 415。
服务端校验文件类型、尺寸及完整性后，使用当前可用模型渠道的私有密钥上传；客户端不能指定渠道、上游地址或 `purpose`。

成功为 HTTP 200：

```json
{"success":true,"data":{"id":"upload_…","url":"https://files.toapis.cn/uploads/…","mime_type":"image/png","size":79}}
```

客户端必须验证 `success=true`、完整字段类型、允许的 MIME、正整数大小及 HTTPS URL，然后将 `data.url` 加入既有
`POST /harness/toapis/v1/images/generations` 的 `reference_images`（最多 6 张，`n=1`）。
回执 URL 只接受 `files.toapis.cn` 或 `files.toapis.com` 下的 `/uploads/`，不得包含凭据、查询或片段。
上传失败须明确提示，不能静默丢弃参考图继续文生图。普通 OpenAI `/v1/images/edits` 继续走原有路径。

错误采用 OpenAI `error` 对象：401 表示用户凭据失败，403 表示权限或模型限制，400 为表单错误，
413 / `image_upload_too_large` 为超限，415 / `image_upload_unsupported_type` 为文件无效，429 为频率或并发超限，
503 / `image_upload_unavailable`（或路由的 `model_not_found`）为模型渠道不可用，
502 / `image_upload_upstream_error` 为上游失败或回执无效，504 / `image_upload_timeout` 为超时。
上游 401/403 也净化为 502，不能伪装成用户余额不足。

上传免费，不预扣、不创建生成任务、消费日志或管理员素材，也不在网关永久保存照片；文件保存于供应商上传服务。
每用户每分钟最多 30 次，单进程最多 4 路并发，限流和并发检查在读取图片前执行；上传请求总期限 30 秒。
后续生成仍沿用原价格、权限和余额不足识别规则。供应商上传契约见
[官方接口文档](https://github.com/toapis/docs/blob/main/docs/cn/api-reference/uploads/images.mdx)。

---

### 4.4 内部图片生成请求合同（2026-10-20）

`yunshu-internal-gpt-image-2` 使用 `toapis-image@1.1.1`，插件源码维护于
`deploy/instance/toapis-image-plugin.js`。原生路径仍为：

```http
POST /harness/toapis/v1/images/generations
Authorization: Bearer <当前用户的 relay_key>
Content-Type: application/json

{"model":"yunshu-internal-gpt-image-2","prompt":"……","n":1,"reference_images":["https://files.toapis.cn/uploads/…"]}
```

生成请求只使用 `model`、原样 `prompt`、固定 `n=1`，以及可选的原样 `reference_images`。
`n` 可以省略，省略时按 1 张规范化；提供其他值会拒绝。参考图最多 6 个 HTTPS URL，
本地文件须先按 4.3 上传并验证回执，文生图可省略 `reference_images`。

输出尺寸及其他生成选项由供应商控制，客户端不提供这些选项。为兼容旧版客户端，
插件忽略 `size`、`quality`、`background`、`output_format`、`output_compression`、
`response_format`、`resolution` 及其他额外字段，不向上游透传，也不因额外字段单独拒绝旧请求。
插件在解码与构建上游请求时均按上述字段白名单创建新请求体；上游模型固定为 `gpt-image-2`。
这不会设置或更改供应商的默认分辨率、实际模型、计费规则、上传合同、响应结构或任务轮询路径。

---

### 4.5 Seedance 视频渠道（BlackrxAI）

这组型号使用 `blackrxai-video` 任务插件，入口是 OpenAI Video 协议：

```http
POST /harness/v1/videos
GET /harness/v1/videos/{task_id}
GET /harness/v1/videos/{task_id}/content
Authorization: Bearer <当前用户的 relay_key>
```

提交示例：

```json
{"model":"lec-mj-seedance-2-5-cd-720p","prompt":"海面上的日出，缓慢推进镜头","seconds":30,"size":"1280x720"}
```

必须使用实际可用目录中的精确 ID。下表为人民币客户售价；每条报价已包含该时长的全部视频费用。

| 型号 | 支持秒数（默认） | 客户售价 |
| --- | --- | --- |
| `lec-mj-seedance-2-5-cd-720p` | 30（30） | 8.50 元/条 |
| `sd-25-900-2` | 30（30） | 9.50 元/条 |
| `k2-sd2.5-720p` | 30（30） | 11.60 元/条 |
| `lec-sz-sd2-full-480p` | 30（30） | 11.50 元/条 |
| `video-sd25-480p` | 15–30 整数（30） | 0.70 元/秒 |
| `model_5` | 15–30 整数（30） | 0.53 元/秒 |
| `sd2.5-720p-x2` | 15–30 整数（30） | 0.62 元/秒 |
| `sd2-th-2.0-720p` | 15（15） | 3.90 元/条 |
| `bt-seedance-2.0` | 10、15（15） | 10 秒 2.50 元；15 秒 3.00 元 |
| `lec-rs-seedance-2-0-fast` | 15（15） | 4.00 元/条 |
| `sd20-933-720p` | 15（15） | 4.70 元/条 |
| `sd2-hn2-480-pro` | 15（15） | 5.80 元/条 |
| `v3-sd2.0-933-720p` | 15（15） | 6.00 元/条 |
| `sd-20-9/3/3-1` | 15（15） | 6.50 元/条 |
| `sd-20-903` | 15（15） | 3.30 元/条 |

`seconds` 接受整数或整数字符串；兼容 `duration`，两者同时提供时必须相等。
客户端应使用上述精确档位，不应按模型名称推断为 4–15 秒或 1–12 秒。
`size` 仅接受 `720x1280`、`1280x720`、`1024x1792`、`1792x1024`，不能传 `720p`、`480p` 或额外 `resolution` 字段。
型号名称中的分辨率是供应商线路名称，最终文件像素需以实际产物为准。

JSON 的 `images` / `image_urls` 是 HTTP(S) URL 数组，`image_url` 为单 URL；
视频与音频使用 `video_urls` / `audio_urls` 数组（也兼容 `videos` / `audios` 数组）。
本地素材使用 `multipart/form-data`：`images`、`videos`、`audios` 文件字段可重复，
单个 `input_reference` 图片兼容字段优先排列；URL 字段也可重复。
文件和 URL 合并计入每型号素材上限，超限或不支持的素材明确报错。
`data:` URL、`metadata` 素材以及未声明字段不受支持，不能丢弃参考素材后继续生成。
`gen_type` 可省略；无参考素材归为 `text_to_video`，有参考素材为 `reference_to_video`。
每次任务只生成一条视频，`n` 省略或为 1。

客户端保存网关公开任务 ID，通过 GET 恢复查询与下载；超时不能自动再次 POST。
插件使用网关生成的任务 ID 作为上游幂等键，仅保护同一次网关请求的内部重试，
不表示客户端多次独立 POST 已具备端到端幂等。
费用按合法请求档位预扣，完成时沿用冻结档位；失败走既有退款链。
定价中的按次基础费用与每秒费用须合并展示总价，不能只把每秒附加项显示为全部售价。

新渠道仅开放 `product` 分组，使用倍率 1。生产生效型号与价格以公开目录和发布回执为准。
图片模型不在本次接入范围。供应商积分、人民币和下文产品积分是三种不同单位。

---

## 5. 积分换算：new-api 的 quota ≠ 产品积分

**这一条最容易搞错，务必按这个来：**

```
产品积分 = quota ÷ quota_per_unit × usd_exchange_rate × 1000
```

- `quota_per_unit` 和 `usd_exchange_rate` 从 `GET /harness/api/status` 实时读，**不要写死**
- 当前两者是 `500000` 和 `7.3`，所以 **1 元 = 1000 积分**
- new-api 内部每元入账 `500000 ÷ 7.3 = 68493` 个 quota —— 这是对的，**不要去改它**

用户余额：`GET /harness/api/user/self`（Bearer access_token）→ `data.quota`。

---

## 6. 充值

```
POST /harness/api/user/wechat/native
Authorization: Bearer <access_token>

{"amount": 1}
```

**`amount` 的单位是人民币元**（不是分、不是美元、不是积分）。
返回 `data {out_trade_no, code_url, expires_at, quota, amount}` —— 把 `code_url` 渲染成二维码给用户扫。

轮询：

```
GET /harness/api/user/wechat/order?out_trade_no=<out_trade_no>
```

`data.status` 取值：`pending` / `processing` / `succeeded` / `failed` / `closed` / `review_required`。

**支付回调由微信直接打到这台实例**（`https://8.133.202.230/harness/api/user/wechat/notify`），
产品侧不用接收、也不用自己算到账。到账积分 = 金额 × 1000。

---

## 7. 创作内容（AI 创作管理）

```
GET /harness/api/creation/content      # 公开，无需鉴权
```

返回 `data.image` 与 `data.video`，各含 `categories` / `templates` / `presets`，
以及 `data.revision`（内容版本号，用它做客户端缓存失效）。

封面图与演示视频：`GET /harness/api/creation/assets/{assetId}`（URL 就在模板的
`coverAssetId` / `demoVideoAssetId` 里）。

**这个公开接口只返回 `status=published` 的模板和 `enabled=true` 的分类** ——
后台里的草稿不会出现在产品端。

---

## 8. 用量

```
GET /harness/api/log/self?type=2&p=1&page_size=100   # 用户态，只返回自己的（Bearer access_token）
GET /harness/api/log/?type=2&p=1&page_size=100       # 管理员，全部（Bearer access_token，需管理员）
```

日志类型：`1` 包含充值、兑换与管理员调额，不能当作实付收入；`2` 消费、`6` 退款。`other` 字段是 **JSON 字符串**（要再 parse 一次）。

管理端聚合（需管理员）：`GET /harness/api/data/?start_timestamp=&end_timestamp=`。

### 8.1 运营管理与额度明细

侧边栏「运营管理」位于「常规」下方，仅管理员可见，集中提供经营看板、额度明细、
用户活跃、调用报错及 AI 创作管理。额度明细页面为 `/harness/balance-ledger`，查询接口：

```
GET /harness/api/balance-ledger/?p=1&page_size=20&category=all
```

支持类别、受影响用户 ID 和起止时间筛选；统计覆盖完整筛选范围，不仅是当前页。
注册赠送、在线充值、管理员调额、兑换码、邀请奖励、签到及余额购买订阅分别分类。

- 实际收款取成功支付订单的 `money`，按到账时间 `complete_time` 归属；包含现金购买订阅，
  这类订单的钱包额度变动为零；不重复累计对应日志。订单未保存币种时按支付渠道单列，
  不能将不同或未知币种直接相加当作人民币。
- 管理员调额显示操作者、修改前后额度及实际差额；「覆盖为 10 元」的差额是修改后减修改前，
  不能记成额外充值 10 元。调额不计入实付充值。
- 旧注册赠送等记录可能只保留 `¥10.00` 这样的展示金额；页面保留该原文，但不按当前汇率
  反推精确额度。额度汇总明确标注历史缺少原始额度的笔数，不能据此把历史赠送当作零。
- 新增注册赠送、签到和兑换日志附带原始额度变动元数据；本次不改变已有注册赠送规则，
  也不补发历史奖励。邀请奖励等旧格式记录仍按已有展示金额保留。
- 手机号专属赠送与账户创建在同一主库事务中落库，提交后分别记录全局赠送和手机号赠送。
  重复登录不会重复发放或记账。历史覆盖无法证明完整时，页面明确提示，不显示虚假的零赠送。

---

## 9. 必须知道的约束

1. **限流**：这条入口每 IP 20 req/s（突发 100），超出返回 429。
2. **被有意挡住的接口**：`/harness/api/setup`、`/harness/api/user/register`、
   `/harness/api/sign-up` 一律 403 —— 账号只从手机验证码产生。
3. **时区**：服务端是 `Asia/Shanghai`，「今日」= 本地零点。
4. **别把账户服务指过来**：这台是 harness 自用的实例，不是那套生产账本。
5. **直连 `127.0.0.1:3010` 只有服务器本机能用**，产品一律走
   `https://8.133.202.230/harness`。

---

## 10. 客户端默认聊天模型与排列偏好（schema1）

公开读取 `GET /harness/api/status` 的 `data.client_model_config`，无管理员偏好或存量配置结构不合法时也返回固定对象：

```json
{"schema_version":1,"default_chat_model":"","model_order":[]}
```

这是一份展示偏好，不是用户模型权限。客户端只对当前用户实际可用的聊天模型应用默认值；默认不可用时选可用聊天模型降级，不能加入用户无权使用的型号。仅对尚未手动选择的状态应用后台默认，保留明确选择和已有会话。按 `model_order` 的精确 ID 排序，未列型号稳定追加；空默认和空数组表示没有对应管理员偏好。读取响应禁止缓存。

独立管理页 `/client-model-config` 位于常规下的运营管理分组。排序编辑直接列出全部真实模型，每行填写 1 至 999999 的正整数；小数字优先，空白型号按目录顺序排后，同号按当前目录顺序排列。输入期间不重排行，保存为 `model_order` 精确 ID 数组，保存后数字规范为连续编号。数字仅是编辑方式，不增加 API 字段或模型权限，仍最多优先排列 256 项。三个管理接口均使用管理员登录会话（包括 role10，个人访问令牌和旧管理令牌不能使用），返回禁止缓存；PUT 复用来源校验：

| 方法 | 路径 | 成功 data |
| --- | --- | --- |
| GET | `/harness/api/client-model-config` | 上述完整三字段对象 |
| PUT | `/harness/api/client-model-config` | 本次提交并保存的完整三字段对象 |
| GET | `/harness/api/client-model-config/models` | `{"models":[{"id":"精确ID","display_name":"显示名","chat_eligible":true}]}` |

管理成功均为 `{"success":true,"data":...}`。PUT Content-Type 为 `application/json`，请求体直接使用完整三字段对象；单对象保存在一个 option 内，数据库成功后才更新读取值。没有新的表或字段迁移，旧通用 option 接口拒绝改写该项。周期 option 同步与本地保存串行，避免旧快照回退刚保存的偏好；不同节点仍按既有同步周期传播。

只支持 schema1；拒绝缺字段、未知或重复字段、null、错误类型、尾随第二对象、重复型号、无效UTF8、首尾空白和控制字符。排序最多256项，精确ID最多255 UTF8字节，请求体最多256KiB。保存时重新读取启用能力及启用渠道，拒绝未知/下线ID；非空默认必须是可用聊天候选，不要求它同时出现在排序数组。图片、视频、任务、embedding/rerank及仅压缩端点等非聊天型号可排序，不能设为默认。元数据接受原有数组和对象两种endpoint声明；不改路由权限和计费。

已保存型号后来下线时保留旧偏好供管理员查看，页面显式提示并要求清理后保存，客户端仍需运行时过滤。失败保留上一份完整配置和页面草稿。错误为HTTP400（合同或型号错误）、413（体积超限）、500（安全的目录/保存失败消息），携带 `success:false/message`，不返回数据库或渠道凭据。并发管理员保存采用整对象后写覆盖，不做逐字段合并。

具体上线提交以发布回执为准。联调使用独立合成数据库；发布不会将合成型号或联调偏好写入生产。

## 11. 自检（对接完先跑这几条）

```bash
BASE=https://8.133.202.230/harness

# 1) 实例活着
curl -s $BASE/api/status | head -c 200

# 2) 发码（本地候选协议，先保存随机发码键；不要用循环自动重发）
curl -s -X POST $BASE/api/user/phone/challenge \
  -H 'Content-Type: application/json' \
  -d '{"contract_version":2,"phone_e164":"+8613800138000","idempotency_key":"<已保存的随机发码键>"}'

# 3) 登录 → 从返回里取 access_token 与 relay_key
curl -s -X POST $BASE/api/user/phone/login \
  -H 'Content-Type: application/json' \
  -d '{"contract_version":2,"phone_e164":"+8613800138000","code":"<收到的码>","challenge_id":"<发码回执>","idempotency_key":"<已保存的随机登录交换键>"}'

# 4) 用 relay_key 调一次模型
curl -s $BASE/v1/chat/completions -H "Authorization: Bearer <relay_key>" \
  -H 'Content-Type: application/json' \
  -d '{"model":"deepseek-v4-flash","messages":[{"role":"user","content":"hi"}]}'

# 5) 看余额
curl -s $BASE/api/user/self -H "Authorization: Bearer <access_token>"
```
