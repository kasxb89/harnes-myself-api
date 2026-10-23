# 客户端在线与工作量观测

本文说明客户端在线与工作量遥测的接口、统计口径和兼容约定，不代表任何环境已经完成测试、上线或客户端发布。接入时应检查目标服务实际返回的能力。

这些数据是**客户端观测**，不构成计费记录、上游任务事实、结算或退款凭据，也不能用于解除生成锁、释放并发、取消任务或授权重新提交付费请求。

## 一、在线状态与历史会话

原有 `POST /api/client-activity/heartbeat` 保留五个字段：`session_id`、`event`、`platform`、`app_version`、`foreground`，请求体上限仍为 2048 字节。它描述客户端进程及登录周期内的在线状态，不描述聊天、分镜任务或上游生成状态。

`GET /api/client-activity/sessions` 新增以下可选查询参数，保留原有分页、用户名、状态和时间戳字段：

| 参数 | 值与语义 |
| --- | --- |
| `scope=live` | 当前有效心跳且未明确结束的在线进程；按最后心跳判断，跨日仍可见，不受“今天开始”条件限制。调用方不发送日期或 `time_basis`。 |
| `scope=history` | 历史会话，可配合日期范围与 `time_basis`。 |
| `time_basis=last_seen` | 按最后心跳时间筛选历史会话。新版管理页的历史列表默认使用此口径。 |
| `time_basis=started` | 按会话开始时间筛选历史会话。 |
| 省略新增参数 | 保留旧版按开始时间范围筛选的行为；`history` 省略 `time_basis` 也沿用 `started`。 |

`time_basis` 仅用于 `scope=history`；`scope=live` 不接受 `status=offline`。原有 `status=foreground/background/offline` 不改名，新增 `observation_detail=live/expired/ended` 用来区分当前有效、观测过期和明确结束。心跳超时不能称为客户端明确退出。

全局在线卡片与工作量不受列表用户名或日期筛选影响：`online_users` 是去重用户数，`runtime_sessions` 是当前在线进程周期数，两者都不是聊天数。`started_sessions` 保留所选期间内开始的会话数含义；原累计时长仍是这些会话的进程累计值，不是按日切片或消除设备重叠后的精确使用时长。账号调用日志也不等于该客户端独占的调用。

## 二、能力发现与鉴权

匿名可读的 `GET /api/status` 仅在 `data.client_activity_workload` 提供以下四项静态能力，不读取用户行来生成能力响应：

```json
{
  "schema_version": 1,
  "snapshot_path": "/api/client-activity/workload-snapshot",
  "heartbeat_interval_seconds": 30,
  "workload_timeout_seconds": 90
}
```

公共能力中不得加入用户、账号、进程、聊天、项目、任务标识、序号、覆盖率、统计数量、认证状态、凭据或指纹。只有对应路由、校验、鉴权与持久化能力实际可用时才应暴露此能力。

`snapshot_path` 相对已配置的 API 基址解析，必须保留 `/harness` 等部署前缀，不能当作任意远程地址或仅相对裸域名解析。能力缺失或版本不支持时，继续正常心跳，将工作量记为未采集；不得试探付费接口或猜测另一套请求格式。

上报接口为 `POST /api/client-activity/workload-snapshot`，使用 `application/json`，仅接受当前有效的交互式登录会话（session-only）。旧式或有作用域的个人访问令牌（PAT）、模型转发密钥均不能用于上报。用户身份来自服务端认证上下文，请求体没有 `user_id` 或 `account_id`。

上报只需普通用户报告自己的观测，不需要管理员权限。管理读取接口均要求登录会话、管理员角色和 `UserActivityRead`（`user_activity:read`）；界面也须随权限撤销清空展示，并隔离不同登录账号的查询缓存。

## 三、快照请求

请求体上限为 **262144 字节（256 KiB）**，`activities` 与 `shots` 合计最多 **256 行**。不得为了发送成功而静默截断当前快照。

| 顶层字段 | 约定 |
| --- | --- |
| `schema_version` | 必填整数，当前为 `1`。 |
| `runtime_session_id` | 必填规范小写 UUIDv4；复用当前进程／登录周期已有的心跳 `session_id`，不是另造工作量进程标识，也不是后台私有登录标识。 |
| `sequence` | 必填整数，范围 `1` 至 `9007199254740991`，在同一进程周期内单调递增，与客户端墙上时间无关。 |
| `complete` | 必须为 `true`；表示各已采集维度的完整当前观测，并携带尚未确认的结束记录。 |
| `collections` | 必填对象，精确包含下述五个维度，每项为 `collected` 或 `not_collected`。 |
| `activities` | 必填数组，包含四种活动类型；同一快照内 `activity_id` 不得重复。 |
| `shots` | 必填数组，每行为真实 `project_id`、`shot_id`；同一项目分镜组合不得重复。它仅表示计划绑定，不证明已提交或正在生成。 |

五个采集维度为 `chat_activity`、`project_run`、`storyboard_task`、`local_operation`、`shots`。标记 `not_collected` 的维度不得携带对应行；尚无真实生命周期来源的本地操作或分镜计划可明确保持未采集，不应补造记录。

`collected` 且当前数组为空只表达该上报进程当前采集的结果，不能直接当成全平台零值，也不能自动结束此前尚未明确结束的活动。

### 活动字段与阶段

所有活动必填 `activity_id`、`kind`、`work_state`、`phase`。附加字段按类型允许，不能把所有可选字段任意放入所有类型：

| `kind` | 含义与附加字段 | `active` 的合法 `phase` |
| --- | --- | --- |
| `chat_activity` | 真实根聊天活动；必填 `chat_session_id`，另可有结束结果 `outcome`。 | `busy` |
| `project_run` | 真实项目工作流；必填 `project_id`，可有 `chat_session_id`、`local_job_id`、`outcome`。 | `waiting`、`preparing`、`accepted`、`queued`、`generating`、`saving`、`editing` |
| `storyboard_task` | 一次真实媒体提交／执行尝试，独立图片或视频请求也属于此类；必填 `attempt_id`，可有根聊天、项目、父活动、分镜、平台任务、本地工作标识及结束结果。 | `waiting`、`preparing`、`submitting`、`accepted`、`queued`、`generating`、`saving` |
| `local_operation` | 单独观测到的真实本地操作；可有根聊天、项目、父活动、本地工作标识及结束结果。 | `downloading`、`saving`、`composing`、`publishing` |

附加字段的协议名为 `chat_session_id`、`project_id`、`parent_activity_id`、`shot_ids`、`attempt_id`、`api_task_id`、`local_job_id`、`outcome`。

- `work_state=unknown` 必须同时使用 `phase=unknown`，不能携带 `outcome`。
- `work_state=ended` 必须使用 `phase=ended`，并携带 `outcome=succeeded/failed/cancelled/interrupted` 之一；其他状态不得携带 `outcome`。
- `accepted` 仅说明原任务号已确认；`queued`、`generating` 必须来自真实上游状态，不能由普通 429、模型调用记录或笼统“运行中”文字推断。
- `saving` 不等于已经采集了精确下载阶段；笼统项目保存／编辑回调不能伪造成单独的合成本地操作。

不透明标识须稳定且不含内容：一般标识长 1 至 128 字节，只允许 ASCII 字母、数字、点、下划线、冒号和连字符，不能为 `.` 或 `..`。`api_task_id` 上限为 256 字节，必须是当前用户真实拥有的原平台任务标识，不能是上游地址。关联关系只在确实存在时填写，独立任务不要求伪造项目或父活动。

`attempt_id` 为执行前已持久化的规范小写 UUIDv4。查询原任务、重连和重新上报必须复用原尝试标识及原任务号；只有真正新授权的执行才产生新尝试。`local_job_id` 只是进程内诊断标识，不能单独充当持久执行身份。

同一用户同一活动的类型和已有非空身份关联不能替换；真实关联之后才确定时可补齐原先缺失字段。父活动须为同用户真实项目活动且无环，平台任务归属由服务端核对，失败不泄露他人资源是否存在。

## 四、序号、回执与存活时间

服务端应将序号、规范化内容摘要、接受时间和类型化记录作为一次原子发布；提交成功之后才返回接受回执。同一进程周期由一个发布者串行发送，允许序号跳号。

| 请求情况 | 回执与效果 |
| --- | --- |
| 更高序号 | 完整校验并原子接受，`recorded=true`、`disposition=accepted`。 |
| 相同序号、相同内容 | HTTP 200，`recorded=false`、`disposition=duplicate`；不刷新记录或存活期。 |
| 更低序号 | HTTP 200，`recorded=false`、`disposition=out_of_order`；返回当前 `accepted_sequence`，不刷新存活期。 |
| 相同序号、不同内容 | HTTP 409，`ACTIVITY_SEQUENCE_CONFLICT`；不修改数据。 |

成功响应使用原有 `success/message/data` 封装。`data` 包含 `schema_version`、`recorded`、`disposition`、`accepted_sequence`、`server_time`、`heartbeat_interval_seconds`、`workload_timeout_seconds`。内部规范化内容摘要与私有登录标识不返回给调用方或管理页面。

未拿到回执的 HTTP 重试须复用完全相同的序号和内容；新的周期性观测即使状态相同，也要递增序号。客户端按单调计时器安排约 30 秒上报，不能用可回拨的墙上时钟控制序号或节流。切换账号、登录或宿主销毁时取消待发送请求，旧响应不得更新新的登录周期。

当前有效观测要求：服务端接受的快照年龄满足 `0 ≤ age < 90 秒`，对应心跳仍有效且未明确结束、绑定的登录仍有效，且该行包含于最新完整的已采集维度中。未来时间产生的负年龄不算有效；快照不负责创建或续期在线心跳。

### 未知、遗漏与明确结束

- 提交后丢失受理结果，或本地观察者退出但没有远端终态凭据，应记为 `unknown`，不能记成排队、成功、失败或取消。
- 新快照省略旧的非终态活动时，旧活动成为“最新未上报／状态未知”，不能自动视为结束，不能纳入已知活动总数；摘要仍须反映其未知状态。
- 维度改为 `not_collected` 时，也不能把此前活动变成零值或合成结束事件。
- TTL 过期或明确结束在线进程，只表示当前观测不再有效，不证明上游任务已停止；保留尚未确认任务的历史不确定性。
- 明确的 `ended` 记录作为结束标记保留，客户端须持续携带，直到包含它的快照序号得到确认，之后可以省略。已结束的活动标识不能在任何进程中重新打开。
- 真正新启动的本地恢复／查询执行可以使用新的活动标识，但原媒体尝试与平台任务标识不变，不能用新标识绕过未知提交保护。

## 五、统计口径与管理读取

管理读取包括：

- `GET /api/client-activity/summary`：原在线与期间指标，另加 `runtime_sessions` 和可选 `workload`。
- `GET /api/client-activity/sessions`：会话列表，新增前述范围、历史时间口径和观测细分状态。
- `GET /api/client-activity/sessions/:id`：原 `session`、`usage`，另加可选安全工作量详情。

一次摘要或详情响应须使用统一服务端观测时间和一致的数据读取边界，不能把不同读取时点的新旧快照、在线数或覆盖率拼在一起。

摘要 `workload` 使用 `schema_version=1`、`scope=global_live`、`server_time`，并分为 `known`、`uncertainty`、`coverage` 三组：

| `known` 字段 | 计数口径 |
| --- | --- |
| `generating_chat_sessions` | 有已知进行中工作观测的真实根聊天，按用户与根聊天标识去重。包括明确关联的后台创作工作和本地合成，不仅是上游渲染。 |
| `projects` | 当前已知活动项目，按用户与项目标识去重。 |
| `planned_shots` | 单独采集的计划分镜，按用户、项目与分镜标识去重。 |
| `task_attempts` | 当前已知活动媒体尝试，按用户与持久 `attempt_id` 去重，不按进程或分镜绑定数量重复计算。 |
| `accepted_tasks`、`queued_tasks`、`generating_tasks`、`saving_tasks` | 原尝试在对应明确阶段的去重计数，不把已受理等同于正在生成。 |
| `editing_projects` | 本地项目阶段为 `editing` 或 `saving` 的活动项目，界面称“编辑或保存中的项目”，不推断精确合成。 |
| `local_operations` | 单独采集的活动本地操作，按用户与活动身份去重；未采集时不能补造。 |

一个根聊天关联 8 个活动分镜任务，应是 **1 个进行中的创作聊天、8 个任务尝试**，不是 9 个会话。项目、分镜、尝试、本地操作不能与父聊天相加成一个总数。本地合成期间可仍有 1 个进行中的创作聊天，但上游生成任务数为 0；仅在实际已知和覆盖情况允许时才展示这样的零值。

`uncertainty` 分别返回 `unknown_task_attempts`、`conflicting_task_attempts`、`unknown_chat_sessions`、`unknown_projects`、`expired_runtime_sessions`、`unreported_runtime_sessions`。其中，`expired_runtime_sessions` 是曾接受快照但观测已失效的当前在线进程；`unreported_runtime_sessions` 是从未接受工作量快照的当前在线进程，旧客户端不能误标为“过期”。同一任务出现相互冲突的新鲜观测时记入冲突／未知，不能随意选一个阶段计入已知总数。

`coverage` 包含 `online_runtime_sessions`、`reporting_runtime_sessions`，以及五个维度各自的 `collected_runtime_sessions` 和 `coverage=complete/partial/not_collected`。完整覆盖要求当前在线进程对该维度都有有效的已采集观测；已知正数可以在部分覆盖时展示，但须标明只是已知数量。一个进程已采集为空，不能代表其他旧版或未上报进程也为空。

旧服务可省略 `workload`、`runtime_sessions`、`observation_detail`；旧客户端可未提供某些采集维度。读取端继续展示可用在线信息，将工作量标为未采集或未知，**不能补成已测量的 0**，也不能从模型调用日志反推聊天、任务、下载或合成状态。

### 详情边界

工作量详情只包含 `schema_version`、`observation_status`、`accepted_at`、`accepted_sequence`、五项采集标记、安全活动与分镜行，以及 `truncated`。活动行的观测状态为 `live/unknown/ended/expired/not_reported`；整体观测为 `live/expired/ended/not_collected`。

返回的活动与分镜合计最多 256 行；`truncated=true` 必须提示并非完整历史。历史 `not_reported` 行可能省略已回收的旧 `shot_ids` 绑定内容，当前快照绑定保持完整，因此此接口也不是完整历史绑定档案。客户端上报的结束结果不能当成上游结算、退款或取消证明。

## 六、隐私与严格白名单

请求拒绝未知或重复 JSON 键、不合法 UTF-8、错误标量类型、必填空值、不合法类型／状态／阶段和重复身份；没有 `revision` 等别名，也没有轮次或模型／工具事件系统。

只允许前述稳定且不含内容的标识与状态。禁止上传或公开对话、提示词、标题、模型或工具细节、原始进度文本、文件路径、参考地址、令牌、登录指纹、客户端声明的用户身份、时间戳或时长。管理详情按字段白名单展示，不做任意对象转储，不展示后台私有登录标识或内部摘要。

## 七、存储预算与失败处理

工作量遥测使用保守的存储预留预算：**全局 512 MiB（536870912 字节），每用户 16 MiB（16777216 字节）**。这是遥测记录的预算计数，不是物理磁盘占用测量，也不是客户余额、积分或模型额度，不能替代运维磁盘保护。

当一次快照新增存储超出任一预算时，返回 HTTP **429**、`code=ACTIVITY_SNAPSHOT_CAPACITY`，原子拒绝本次遥测，不接受序号、不刷新工作量存活期。不得因此删除业务历史、媒体任务或客户内容，也不通过定时清理其他业务来腾出预算。容量与限流失败仅影响遥测，不改变正常心跳、登录、生成结果或用户余额。

| HTTP 状态／代码 | 调用方处理边界 |
| --- | --- |
| 400 `ACTIVITY_SNAPSHOT_INVALID` | 请求或归属／关联不合法；通用错误不披露他人资源。 |
| 401 | 登录无效或已撤销；停止旧登录周期的上报。 |
| 403 | 凭据不符合 session-only 或读取权限不足。 |
| 409 `ACTIVITY_SEQUENCE_CONFLICT`／`ACTIVITY_RUNTIME_ENDED` | 序号、不可变身份冲突或进程已结束；不得重开结束活动。 |
| 413 `ACTIVITY_SNAPSHOT_TOO_LARGE` | 超过报文上限；不静默截断或重提媒体。 |
| 429 | 独立遥测频率限制，或上述容量错误；不等于上游排队。 |
| 5xx `ACTIVITY_SNAPSHOT_UNAVAILABLE` | 观测暂不可用，不转成创作失败。 |

遥测使用独立的有界限流，不与登录、续期、短信共用额度。失败只做有界遥测恢复，不触发上游请求、重新生成、下载、退款、并发释放或账号／配置修改；未知的原任务仍走原有恢复核对流程。
