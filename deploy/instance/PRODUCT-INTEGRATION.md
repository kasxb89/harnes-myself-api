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
当前 `default` 分组下有 22 个模型条目，含 `deepseek-v4-flash`、`deepseek-v4-pro`、`glm-5.1`、
`glm-5.2`、`kimi-k2.6`、`kimi-k2.7-code`、`qwen3.6-flash`、`qwen3.6-plus`、`qwen3.7-max`、
`qwen3.7-plus`、`qwen3.8-max`、`ys-1.0`、`gpt-image-2`、`MiniMax-H3` 等。

Gemini 格式走 `/harness/v1beta/...`。

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

日志类型：`1` 充值、`2` 消费、`6` 退款。`other` 字段是 **JSON 字符串**（要再 parse 一次）。

管理端聚合（需管理员）：`GET /harness/api/data/?start_timestamp=&end_timestamp=`。

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
