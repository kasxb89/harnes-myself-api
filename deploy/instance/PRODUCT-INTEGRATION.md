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

**第一步，发码**

```
POST /harness/api/user/phone/challenge
Content-Type: application/json

{"phone_e164": "+8613800138000"}
```

返回 `data.resend_after_seconds`（重发冷却）、`data.expires_in_minutes`（有效期）。
同一号码 60 秒冷却、每小时上限 5 次。号码必须是 E.164（带 `+86`）。

**第二步，校验并登录**

```
POST /harness/api/user/phone/login
Content-Type: application/json

{"phone_e164": "+8613800138000", "code": "123456"}
```

返回 `data`：

| 字段 | 说明 |
| --- | --- |
| `access_token` | 调管理接口用，**15 分钟**过期 |
| `access_expires_at` | 过期时间戳 |
| `session` | 含 `sid`、`current` |
| `user` | 含 `id`、`username`、`quota` |
| **`relay_key`** | **`sk-` 开头的本账号中转 key —— 调模型就用它** |
| `relay_key_id` | 该 key 的令牌 id |

同时 `Set-Cookie` 下发 refresh cookie（30 天）。

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

## 10. 自检（对接完先跑这几条）

```bash
BASE=https://8.133.202.230/harness

# 1) 实例活着
curl -s $BASE/api/status | head -c 200

# 2) 发码（换成你自己的号码）
curl -s -X POST $BASE/api/user/phone/challenge \
  -H 'Content-Type: application/json' -d '{"phone_e164":"+8613800138000"}'

# 3) 登录 → 从返回里取 access_token 与 relay_key
curl -s -X POST $BASE/api/user/phone/login \
  -H 'Content-Type: application/json' \
  -d '{"phone_e164":"+8613800138000","code":"<收到的码>"}'

# 4) 用 relay_key 调一次模型
curl -s $BASE/v1/chat/completions -H "Authorization: Bearer <relay_key>" \
  -H 'Content-Type: application/json' \
  -d '{"model":"deepseek-v4-flash","messages":[{"role":"user","content":"hi"}]}'

# 5) 看余额
curl -s $BASE/api/user/self -H "Authorization: Bearer <access_token>"
```
