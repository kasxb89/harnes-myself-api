# harness-myself-api

「云枢」桌面产品（DeepSeek Harness）的自建 API 网关。它基于 [new-api](https://github.com/QuantumNous/new-api)
修改而来，作为产品**唯一的模型出口与计费后端**：客户端的模型列表、余额、登录和充值全部由这一个实例提供。

本仓库是自用定制版，不是上游的分支候选，也不跟随上游的发版节奏。

## 与原版的差异

### 手机号登录

手机号即账号身份，首次验证成功时建档，之后直接登录。影子用户名沿用 `ys<数字>` 命名，
所以历史账号会被自动接管而不是产生重复身份。

| 端点 | 作用 |
| --- | --- |
| `POST /api/user/phone/challenge` | 下发验证码，带每号码 60 秒冷却与每小时 5 次上限 |
| `POST /api/user/phone/login` | 校验验证码并登录，返回与密码登录相同的会话凭据 |

验证码下发走 webhook（`PHONE_SMS_WEBHOOK_URL`，POST `{phone_e164, code}`）；本地开发可以用
固定码旁路（`PHONE_SMS_DEV_BYPASS` + 白名单号码）。**没有配 webhook 且号码不在白名单时，验证码发不出去**。

首次建档会按 `PHONE_REGISTRATION_BONUS_YUAN` 发放一次性额度（默认 10 元）。

### 登录即持有归属自己的 relay key

`model.EnsureRelayToken` 在手机登录时为本账号签发一个固定名 `harness`、永不过期、不限额的
token，随登录响应一起返回。客户端用它调用模型接口，于是**请求计到登录者账上**，
而不是记在某个共享的部署 key 上。重复登录复用同一个 token，不会撑大 tokens 表。

### 微信支付 Native 扫码充值

`POST /api/user/wechat/native` 下单返回二维码，`GET /api/user/wechat/order` 查询状态，
微信回调 `POST /api/user/wechat/notify` 入账。回调做平台验签（RSA）、时间戳新鲜度校验、
AEAD_AES_256_GCM 解密，并核对支付金额后才放额度，重复通知幂等。

另有一个后台对账任务兜底：回调丢失时主动向微信查询订单状态并在确认收款后入账，
所以丢一条通知不会让已付款的订单永远挂着。

## 运行

常规的 new-api 部署方式（见 `docker-compose.yml`），外加下列环境变量。

**手机登录**

| 变量 | 说明 |
| --- | --- |
| `PHONE_LOGIN_ENABLED` | 打开手机登录端点 |
| `PHONE_SMS_WEBHOOK_URL` | 验证码下发地址，生产环境必填 |
| `PHONE_SMS_WEBHOOK_TOKEN` | webhook 的 Bearer token，可选 |
| `PHONE_SMS_DEV_BYPASS` | 本地开发用固定码旁路，**不可用于生产** |
| `PHONE_SMS_DEV_CODE` | 旁路使用的固定验证码 |
| `PHONE_SMS_DEV_ALLOWED_PHONES` | 旁路白名单，逗号分隔的 E.164；为空时旁路拒绝所有号码 |
| `PHONE_LOGIN_DEFAULT_GROUP` | 新手机账号所在分组，默认 `default` |
| `PHONE_REGISTRATION_BONUS_YUAN` | 首次建档赠送的人民币金额，默认 10 |

**微信支付**

| 变量 | 说明 |
| --- | --- |
| `WECHAT_PAY_ENABLED` | 打开微信支付端点 |
| `WECHAT_APP_ID` / `WECHAT_MCH_ID` | 应用与商户号 |
| `WECHAT_API_V3_KEY` | APIv3 密钥，32 位 |
| `WECHAT_MERCHANT_SERIAL` / `WECHAT_MERCHANT_PRIVATE_KEY_PATH` | 商户证书序列号与私钥 |
| `WECHAT_PLATFORM_PUBLIC_KEY_ID` / `WECHAT_PLATFORM_PUBLIC_KEY_PATH` | 微信平台证书序列号与公钥 |
| `WECHAT_NOTIFY_URL` | 回调地址，必须公网可达的 HTTPS |
| `WECHAT_RECONCILE_INTERVAL_SECONDS` | 对账轮询间隔，默认 120 |
| `WECHAT_RECONCILE_MIN_AGE_SECONDS` | 订单多久后才交由对账判定，默认 1800 |

所有凭据齐备时 `WechatPayConfigured()` 才返回 true，缺任何一项都只会拒绝下单而不会半途失败。

## AI 创作内容管理

管理员侧边栏的「AI 创作管理」（`/creation-content`）分别维护图片与视频的页面文案、分类、创作模板和风格／运镜预设。模板支持中英文提示词、真实封面上传、可选演示视频和排序；编辑弹窗内可直接「发布」或「保存草稿」，已发布模板可直接「转为草稿」，仅服务端保存成功后关闭弹窗。模板提交会一并保存页面中其他有效的修改；若其他必填内容尚未完整，则不提交且保留模板编辑。保存后的已发布内容由产品端动态读取，空清单不会填入示例模板。页面文案、分类与预设仍由页面顶部「保存内容」提交。分类停用时，其下模板不向用户展示。

运营内容保存在 `CREATION_CONTENT_DIR` 指定目录（默认 `data/creation-content`），不修改数据库结构。部署时应持久化并备份整个目录。清单采用版本校验和原子替换；遇到版本冲突，后台会保留当前编辑，重新加载前请复制需要保留的修改。文件按内容哈希去重并永久保留，删除条目不会删除素材文件。

公开读取接口为 `GET /api/creation/content`，素材为 `GET /api/creation/assets/:id`（视频支持 Range）。后台读写接口为 `GET/PUT /api/creation/admin/content`、`POST /api/creation/admin/assets`，沿用管理员浏览器会话保护。图片只允许 PNG、JPEG、WebP，最大 10 MiB；视频只允许 MP4、WebM，最大 100 MiB。素材内容签名由服务端校验，不能填写外部 URL。

## 产品客户端更新

后台「系统设置 → 运维 → 客户端更新」（`/system-settings/operations/desktop-update`）维护客户端更新开关，以及 macOS Apple 芯片、macOS Intel、Windows x64 各自的更新源目录。地址应指向包含 `nightly` 渠道元数据与对应已签名安装包的目录，不能填写单个安装包地址。正式地址使用 HTTPS；HTTP 仅用于本机 loopback 开发。默认关闭更新且地址留空，已有配置可以清空。

配置沿用现有 options 存储和最高管理员权限。公开 `GET /api/status` 的 `data.desktop_update` 仅返回开关、固定的 `nightly` 渠道和三个平台的公开目录地址；不返回签名私钥、令牌或请求头。保存配置不会上传安装包，也不会执行安装。

客户端正式包使用打包时绑定的后台来源读取配置；构建输入为 `DSH_DESKTOP_UPDATE_CONFIG_ORIGIN`。已接入的客户端在下一次检查时使用后台最新设置，未接入此能力的旧客户端需要先分发新版安装包。下载和安装仍执行客户端原有的签名校验与用户确认流程；未配置、停用或读取失败不会被当成“已是最新版本”。

## 许可证与归属

本项目是 [new-api](https://github.com/QuantumNous/new-api)（作者 **QuantumNous**，AGPL-3.0）
的修改版，**保留原项目的名称、署名与许可证**。按 AGPL-3.0 的要求，本仓库同样以 AGPL-3.0 分发，
完整许可证见 [LICENSE](LICENSE)；上游原始说明文档原样保留在 [README.upstream.md](README.upstream.md)，
并另见 [NOTICE](NOTICE)。

再分发或以网络服务形式提供时，请一并保留上述归属信息与许可证。
