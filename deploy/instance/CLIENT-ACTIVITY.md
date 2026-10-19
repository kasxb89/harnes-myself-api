# 客户端使用情况

管理员在「用户活跃」页查看客户端会话，前端路径为 `/user-activity`（生产部署须保留 `/harness/user-activity` 前缀）。该功能需要发布新版服务端和新版云枢客户端；旧版客户端和未登录状态不会补报历史启动记录。

上线会话计数从登录后首次上报开始。同一个桌面进程退出登录后重登，或 Host 开始新的本机会话 epoch，都可能产生新会话；该计数不等同于操作系统层面的真实进程启动次数。

## 采集与状态

- 登录后的桌面进程生成随机 UUID，通过 `POST <base>/api/client-activity/heartbeat` 上报启动、心跳或退出。生产的 `<base>` 必须保留 `/harness`。身份仅取当前有效登录，不能替别的用户上报。
- 默认每 30 秒一次，前后台切换即时上报；90 秒收不到心跳即显示离线。退出只做尽力通知；断网、崩溃、休眠时根据超时离线，不能保证准确的退出时刻。
- `foreground=true` 表示客户端报告其窗口在前台使用；托盘常驻、锁屏或空闲应报 false。这个状态不代表用户每秒都在操作，也不能证明视频一直在播放。
- 在线/前台时长按服务端收到的相邻心跳间隔累计。超过 90 秒的间隔不补计，离线尾部不外推。前台累计使用前一次心跳的状态，误差约一个心跳间隔。多开客户端的会话时长各自累计，不能当作去重的人均时长。
- 一次 `stop` 后该会话不重新开启；重新登录或重新启动客户端要使用新 UUID。相同用户的同一 UUID 重试不创建额外会话。每用户每分钟最多 120 次上报、24 小时最多 100 个新会话，避免恶意刷写。
- 仅记录账户、随机进程会话标识、平台、应用版本、启动/最后心跳/退出时间及在线/前台累计秒数。不采集硬件指纹、键盘输入、聊天内容、本地文件路径或客户端请求中的 IP。

## 查看口径

- `GET <base>/api/client-activity/summary`：当前全局在线/前台用户去重人数，以及选定时间范围的启动会话、活跃账户、会话累计时长和实际模型调用/错误记录数。范围默认最近 7 天，最长 31 天。当前在线人数不受历史时间筛选影响。
- `GET <base>/api/client-activity/sessions`：按启动时间范围、精确用户名、状态筛选会话，支持分页。`status` 可为 `all`、`online`、`foreground`、`background`、`offline`。时长为选中会话的累计值，不是按日期边界裁剪后的时长。
- `GET <base>/api/client-activity/sessions/:id`：该会话的观测时间及其所属账户在会话时间窗内的模型汇总。所有读取接口仅管理员可用，个人访问令牌不能读取或写入该模块。
- 真正模型调用来自已有用量日志（成功消费记录），错误来自错误日志，不把心跳当作调用。错误记录可能包含渠道重试；它不等于失败的用户任务数。若关闭消费/错误日志，相应统计会缺失。
- 模型日志未携带桌面随机会话 UUID，同一账户多个设备的时间窗重叠时不能精确归因某个设备；详情展示的是该账户在会话时间窗内的调用，不能把多个会话的调用数直接相加当作全局总数。此模块不返回聊天内容、错误原文或凭据。

## 本地验证

`controller/client_activity_test.go` 集中验证服务端时间、重复/延迟心跳、前后台计时、休眠缺口、退出终态、跨用户隔离、实际日志统计及权限和输入边界。数据库矩阵使用独立本机测试库，分别验证 SQLite、MySQL、PostgreSQL 的新建/已有用户日志数据升级、重复迁移和唯一索引；主库与日志库分开配置。

本次验证使用 SQLite 3.50.4（项目 glebarez/modernc 驱动）、MySQL 8.0.45 和 PostgreSQL 18.3。升级基线为 `7e67d6b6f737` 中未改动的 `User`/`Log` 表结构及两账户、四条模型日志，不表示对所有历史版本或整个数据库的迁移认证。运行命令为配置本机 `ACTIVITY_MYSQL_DSN` / `ACTIVITY_POSTGRES_DSN` 后执行 `go test -p 1 ./controller -run TestClientActivityDatabaseMatrix -v -count=1`；三库新建/升级六场景通过，每场景重复迁移两次，并校验旧余额、旧日志及新表唯一索引。

鉴权复用现有服务端登录校验，新接口限制为交互登录会话。按 [OWASP Authentication Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html)、[Session Management Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html) 及 [ASVS 5.0.0](https://github.com/OWASP/ASVS/blob/v5.0.0_release/5.0/docs_en/OWASP_Application_Security_Verification_Standard_5.0.0_en.json) 核对本次适用的 V7.2.1、V7.4.1、V8.2.1–V8.2.3、V8.3.1：匿名和注销 JWT 不能续报，个人访问令牌被拒，用户不能指定他人身份，普通用户不能列出或读取会话详情；请求体、UUID、版本、平台、事件及查询范围在服务端限制。此验证只覆盖本次接口，不声明整个产品满足 ASVS 全标准。

新增表 `client_activity_sessions` 只在主库启动迁移。它不改变用户余额、用量日志格式、模型调用或计费流程。本文档不表示线上已部署或重启。
