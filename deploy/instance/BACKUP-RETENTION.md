# harnes-api 发布备份保留规则

服务器只保留当前发布的完整备份与父链上最近两套完整备份，共三套。保留对应当前运行容器、两级回滚容器、镜像和冻结操作器。版本由登记文件中的完整 revision/parent_revision 确定，不按目录修改时间排序。

服务器登记文件：`/opt/yunshu-harness-newapi-20261016/maintenance/retention-registry.json`。维护工具 `backup_retention.py` 与仓库相同，不需要重建或重启 API。历史 `redeploy.sh` 已停用；当前发布应从最新发布回执的配置、实际卷和保留当前 DB/env 的操作器继续。

## 每次发布必须执行

1. 备份前执行 `check --phase before-backup`，显式提供正数 `--growth-bytes`，预算覆盖备份、构建上下文、暂存程序与镜像增长。扣除预算后仍须至少可用 3GiB、使用率低于 93%。未完成上次归档清理时阻断下一次备份或构建。
2. 同次检查提供 `--pending-revision`、`--pending-backup`、`--pending-output`，生成四小时有效、绑定当前实例和登记链的候选凭据。完成备份后，候选 `retention-manifest.json` 的 `pending_release` 必须绑定凭据 SHA、候选 revision、parent revision 和 live identity。
3. 备份后、构建前及切换前再次执行相应 `check`，构建前仍需显式正数增长预算，并传 `--pending-ticket`。唯一已绑定的本轮第四套候选备份可进入同轮构建；其它欠清理或未知版本不能借此放行。已有部署锁内应调用模块函数，避免嵌套申请同一锁。
4. 发布成功且健康验证通过后，`register` 登记真实已运行的新版本与完整备份。通过 `plan` 得到当前加最近两套的保留集合，先完成异机归档及完整回读验证，再签收、`apply`，恢复三套。不能把缺少归档证明的第四套直接删除。

完整备份包装清单须有 `complete:true`、完整 revision、database/configuration/creative_content/volumes/binary/image 六项组件，以及每个必要文件的字节数和 SHA256。原备份清单本身也纳入验证；不要改写历史数据库或旧清单。

每条发布还须登记 `artifacts.staged_binary` 与 `artifacts.operator_dir`，分别限定为本实例根目录的 `new-api-<release>` 和 `/tmp/yunshu-api-<release>`。新版本通过 `inspect-artifacts` 生成登记值，冻结源码与依赖须绑定 `release.json` 中的完整哈希清单；操作器的运行日志可以继续写入。仅同轮构建前可尚无暂存程序，切换前两类产物必须齐备且校验通过。旧登记只能使用 `migrate-artifacts` 的明确源文件清单及原登记 SHA 做元数据迁移，不修改历史源码。未知同命名空间产物会阻断下一次备份或构建，不会按通配符自动删除。

## 异机归档与清理

使用 `backup_archive.mjs archive` 接收只读 SSH 压缩 TAR 流（gzip，或显式指定 `--compression zstd`），直接写入异机的私有加密目录，不在服务器生成大型压缩包。zstd 流须有帧校验和，解码窗口最多512MiB。档案使用 AES-256-GCM，目录0700、文件0600；随机密钥独立放在操作者的私有密钥目录，并长期保留。不要把数据库、env、私有日志或密钥上传到公开对象存储或 Git。

`archive` 仅代表加密传输完成。必须再执行 `verify`，从磁盘完整读取密文、验证 GCM 与压缩流校验和、逐条核对 TAR 内文件和 SHA256；硬链接必须能在档案内部还原。镜像档案还要验证配置、层与预期镜像集合。校验器只流式读取，不解包到服务目录。

先用 `archive-manifest --plan PLAN --kind files --output EXPECTED` 从计划生成完整文件清单；存在待删镜像或容器时，再以 `--kind docker` 导出必须归档的镜像集合。文件清单同时覆盖旧备份、暂存程序和操作器全树（含日志）。

`sign-receipt --plan PLAN --verification ENVELOPE --key HMAC_KEY --output RECEIPT` 只能在异机运行。`ENVELOPE` 为 schema2，绑定 canonical `plan_sha256`；其 `archives` 数组逐份指定真实 `metadata`、`expected`、`aes_key` 和 `verified_receipt` 的绝对路径。文件档案必须恰好一份且覆盖全部源；Docker档案须无重复地覆盖全部待删镜像身份，不能用文件档案代替。旧版 `verified:true` 自述证明不能签收。

签收需要 Node.js 25及以上和工具旁固定哈希的 `backup_archive.mjs`、`verify_backup_archive.py`；每份档案会再次真实双遍回读，验证 AES-GCM、压缩校验和、TAR完整性、密文和metadata的SHA、当前AES密钥与实际验证所用密钥的SHA，以及0700目录/0600文件与owner。HMAC密钥须在档案目录之外并与AES密钥不同。签收后 `apply` 在服务器持共享部署锁，检查签名、有效期、实时容器、源指纹与引用，不需要把异机AES密钥复制到服务器。仅按明确允许清单移除过期备份、暂存程序、操作器、已退出的本API回滚容器和无其它容器引用的专属镜像标签。禁止全局 prune、force 删除、删除卷或重启其它服务。

清理后重新核验三套完整备份、当前与两级回滚、服务状态、容量和审计回执。若中途出现实例变化、源文件变化或服务异常，保留未完成状态并停止后续清理，不伪造完成。

历史备份二进制可能共享硬链接；物理占用须按唯一inode统计。镜像同样共享层，不能相加虚拟大小推算释放量。程序回滚保留当前数据库、env及客户新写入，不能使用旧整库覆盖当前状态。
