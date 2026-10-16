# 新实例部署时带上的三组选项

新实例起空库时有三个坑：**手机登录发不出验证码**（`SmsAliyun*` 为空）、
**兑换码与兑换被合规开关挡住**（`payment_setting.compliance_*` 未设）、
**控制台按默认口径显示美元**（`general_setting.quota_display_type` 未设，默认 USD → 显示 `$`）。
这三组值都只是 `options` 表的行，`seed_options.py` 从源库读一份、生成幂等 SQL、写到目标库。

## 部署时

源库连接串必须通过环境变量 `SEED_OPTIONS_SOURCE` 或 `--source` 显式提供，例如 `sqlite:/path/to/source.db` 或 `pg:postgresql://user:password@host:5432/database`。脚本不包含本机连接串或凭据；真实值请由仓库外的环境配置提供。

```sh
# 目标库是 SQLite（与线上现有 new-api 同形）
python3 deploy/seed-options/seed_options.py --target sqlite:/var/lib/new-api/one-api.db

# 目标库是 PostgreSQL
python3 deploy/seed-options/seed_options.py --target pg:postgresql://user:pw@host:5432/new-api

# 目标机连不上源库时：本地出 SQL、管道送过去，值不落磁盘
python3 deploy/seed-options/seed_options.py --sql-only | ssh <host> 'sqlite3 /path/one-api.db'
```

写完重启/等一个同步周期（`SYNC_FREQUENCY`）即可生效，选项是启动或定时从库里重读的。

## 注意

- **出 SQL 里含阿里云 AccessKeySecret 明文。** 别把它写进仓库文件——`harnes-myself-api`
  是公开仓库。管道直接送进目标库，或在仓库外落盘后立刻删。
- 值优先取源库，其次取环境变量 `SMS_ALIYUN_*`；签名与模板这两个报备过的常量有内置默认值。
- 合规确认时间与操作人默认**逐字沿用源库**（同一个人、同一版条款）。想要部署当下的时间用
  `--confirmed-at now`。
- 源库缺键会直接报错退出，不会静默少写几行。
- 这两组是**唯一**需要搬的选项。按张/阶梯计价的开关不在此列——那类日志字段会改变消费日志的
  形状，本实例只服务自家 harness，不涉及。

## 为什么不用后台 API

`SmsAliyun*` 能走 `PUT /api/option/`，但合规确认**故意不给 API**：
`controller/payment_compliance.go:29` 拒绝 access token（只认后台会话），
`controller/option.go:206` 也把 `payment_setting.compliance_*` 从通用设置接口里挡掉。
所以直接写库是唯一一条一次做完两者的路；也可以改成后台登录 root 手点一次确认。
