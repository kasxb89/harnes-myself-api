#!/usr/bin/env python3
"""把源实例的「短信」「合规开关」与「展示口径」选项带到新实例的 options 表。

新实例起的是空库时，手机登录发不出验证码（`SmsAliyun*` 为空），
兑换码与兑换也被合规开关挡住（`payment_setting.compliance_*` 未设），
控制台还会按默认口径显示美元（`general_setting.quota_display_type` 未设）。
这三组值都只是 options 表的行，这里从源库读出来、生成幂等 SQL、应用到目标库。

值一律从源库读或从环境变量取，脚本里没有明文密钥。

用法：
  # 出 SQL 自己执行（值不落盘）
  python3 seed_options.py --sql-only | ssh <host> 'sqlite3 /path/one-api.db'

  # 直接应用到目标库
  python3 seed_options.py --target sqlite:/path/to/one-api.db
  python3 seed_options.py --target pg:postgresql://user:pw@host:5432/new-api

  # 换一个确认时间（默认逐字沿用源库的确认时间与操作人）
  python3 seed_options.py --target ... --confirmed-at now

环境变量可覆盖源库值，用于源库不在手边的场合：
  SMS_ALIYUN_ACCESS_KEY_ID / SMS_ALIYUN_ACCESS_KEY_SECRET /
  SMS_ALIYUN_SIGN_NAME / SMS_ALIYUN_TEMPLATE_CODE
"""

import argparse
import os
import shlex
import subprocess
import sys
import time

# 手机登录发码用的四个后台选项（见 common/sms.go）。
SMS_KEYS = [
    "SmsAliyunAccessKeyId",
    "SmsAliyunAccessKeySecret",
    "SmsAliyunSignName",
    "SmsAliyunTemplateCode",
]

# 合规确认写入的五项（见 controller/payment_compliance.go）。
COMPLIANCE_KEYS = [
    "payment_setting.compliance_confirmed",
    "payment_setting.compliance_terms_version",
    "payment_setting.compliance_confirmed_at",
    "payment_setting.compliance_confirmed_by",
    "payment_setting.compliance_confirmed_ip",
]

# 展示口径：不搬的话新实例按代码默认走 USD（控制台显示 $）。
# 缺键只告警不报错——它不影响功能，只是显示货币不同。
DISPLAY_KEYS = [
    "general_setting.quota_display_type",
]

# 空库时阿里云短信的签名与模板不会变——它们是报备过的常量，不是每台一个。
SMS_ENV = {
    "SmsAliyunAccessKeyId": "SMS_ALIYUN_ACCESS_KEY_ID",
    "SmsAliyunAccessKeySecret": "SMS_ALIYUN_ACCESS_KEY_SECRET",
    "SmsAliyunSignName": "SMS_ALIYUN_SIGN_NAME",
    "SmsAliyunTemplateCode": "SMS_ALIYUN_TEMPLATE_CODE",
}
DEFAULT_SIGN_NAME = "德易融天津信息咨询"
DEFAULT_TEMPLATE_CODE = "SMS_318921183"

DEFAULT_SOURCE = os.environ.get("SEED_OPTIONS_SOURCE")


def split_dsn(dsn: str) -> tuple[str, str]:
    """拆出 (kind, 连接目标)。kind 是 pg 或 sqlite。"""
    kind, _, rest = dsn.partition(":")
    if kind not in ("pg", "sqlite") or not rest:
        raise SystemExit(f"无法识别的连接串：{dsn}（应为 pg:<DSN> 或 sqlite:<路径>）")
    return kind, rest


def psql(dsn: str, sql: str) -> str:
    result = subprocess.run(
        ["psql", dsn, "-v", "ON_ERROR_STOP=1", "-At", "-F", "\x1f", "-c", sql],
        capture_output=True,
        text=True,
    )
    if result.returncode != 0:
        raise SystemExit(f"psql 失败：{result.stderr.strip()}")
    return result.stdout


def sqlite(path: str, sql: str) -> str:
    result = subprocess.run(
        ["sqlite3", "-batch", "-separator", "\x1f", path, sql],
        capture_output=True,
        text=True,
    )
    if result.returncode != 0:
        raise SystemExit(f"sqlite3 失败：{result.stderr.strip()}")
    return result.stdout


def read_options(dsn: str, keys: list[str], missing_ok: bool = False) -> dict[str, str]:
    """从源库读出这些键的当前值；缺键默认报错，missing_ok 时只告警。"""
    kind, target = split_dsn(dsn)
    quoted = ",".join("'" + key.replace("'", "''") + "'" for key in keys)
    sql = f"select key, value from options where key in ({quoted});"
    raw = psql(target, sql) if kind == "pg" else sqlite(target, sql)
    found: dict[str, str] = {}
    for line in raw.splitlines():
        if not line.strip():
            continue
        key, _, value = line.partition("\x1f")
        found[key] = value
    missing = [key for key in keys if key not in found]
    if missing and missing_ok:
        print("警告：源库没有这些键，跳过（目标实例会用代码默认值）：" + ", ".join(missing), file=sys.stderr)
    elif missing:
        raise SystemExit("源库里缺少这些键，确认源实例是不是这台：" + ", ".join(missing))
    return found


def literal(value: str) -> str:
    return "'" + value.replace("'", "''") + "'"


def render(options: dict[str, str]) -> str:
    """一份在 pg 与 sqlite 上都能执行的幂等 upsert。"""
    lines = ["begin;"]
    for key, value in options.items():
        lines.append(
            f"insert into options (key, value) values ({literal(key)}, {literal(value)}) "
            f"on conflict (key) do update set value = excluded.value;"
        )
    lines.append("commit;")
    return "\n".join(lines)


def collect(args: argparse.Namespace) -> dict[str, str]:
    """源库读一份，再让环境变量覆盖缺省的短信常量。"""
    from_source: dict[str, str] = {}
    if not args.no_source:
        from_source = read_options(args.source, SMS_KEYS + COMPLIANCE_KEYS)
        from_source.update(read_options(args.source, DISPLAY_KEYS, missing_ok=True))

    options: dict[str, str] = {}
    for key in SMS_KEYS:
        override = os.environ.get(SMS_ENV[key])
        if override:
            options[key] = override
        elif key in from_source:
            options[key] = from_source[key]
        elif key == "SmsAliyunSignName":
            options[key] = DEFAULT_SIGN_NAME
        elif key == "SmsAliyunTemplateCode":
            options[key] = DEFAULT_TEMPLATE_CODE
        else:
            raise SystemExit(f"没有 {key}：源库里没有，也没有设 {SMS_ENV[key]}")

    for key in COMPLIANCE_KEYS:
        if key not in from_source:
            raise SystemExit(
                f"源库里没有 {key}，说明源实例还没确认合规；先用 root 会话在源实例后台确认一次。"
            )
        options[key] = from_source[key]

    for key in DISPLAY_KEYS:
        if key in from_source:
            options[key] = from_source[key]

    if args.confirmed_at == "now":
        options["payment_setting.compliance_confirmed_at"] = str(int(time.time()))
    return options


def main() -> None:
    parser = argparse.ArgumentParser(description="把短信与合规开关选项带到新实例。")
    parser.add_argument("--source", default=DEFAULT_SOURCE, help="源实例连接串，默认读取 SEED_OPTIONS_SOURCE")
    parser.add_argument("--target", help="目标实例连接串；配合 --sql-only 时可省略")
    parser.add_argument("--sql-only", action="store_true", help="只打印 SQL，不连接目标库")
    parser.add_argument("--no-source", action="store_true", help="不读源库，短信值全走环境变量")
    parser.add_argument(
        "--confirmed-at",
        choices=["source", "now"],
        default="source",
        help="逐字沿用源库的确认时间（默认），或改为当下",
    )
    args = parser.parse_args()

    if not args.no_source and not args.source:
        parser.error("请设置 SEED_OPTIONS_SOURCE 或提供 --source 源实例连接串")

    if not args.sql_only and not args.target:
        parser.error("要么给 --target，要么给 --sql-only")

    options = collect(args)
    statement = render(options)

    if args.sql_only:
        print(statement)
        return

    kind, target = split_dsn(args.target)
    if kind == "pg":
        psql(target, statement)
    else:
        sqlite(target, statement)
    print(
        "已写入 "
        + target
        + "："
        + ", ".join(options)
        + f"\n（重跑目标实例的命令：{shlex.join([sys.argv[0], '--target', args.target])}）"
    )


if __name__ == "__main__":
    main()
