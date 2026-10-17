#!/usr/bin/env python3
"""把本机实例的定价表镜像到新实例。

新实例的 `ModelRatio` 来自**现网导出**，而现网那 236 条里没有 `deepseek-v4-flash`、
`glm-5.2` 这些自研模型；再叠上 `POST /api/setup` 把 `SelfUseModeEnabled` 写成了 `false`，
于是调用直接 `400 model_price_error`（relay.model_price_not_configured）。
本机实例这 251 条是全的，所以以本机为准镜像过去。

用法（本地跑，出 SQL，管道送进目标库 —— 与 seed-options 同一套路）：

    python3 mirror-pricing.py | ssh <host> 'sqlite3 <目标库>'

    python3 mirror-pricing.py --sql-only            # 只看要写什么
    SOURCE=pg:... python3 mirror-pricing.py         # 换源库

只搬下面这些键，都是"这个部署怎么算钱"的口径；不含任何密钥。
"""

import argparse
import os
import subprocess
import sys

# 只搬定价与计费口径。刻意不含：分组比率映射（GroupRatio 等，按用户要求单独处理过）、
# 短信/合规/展示类、以及 SystemName/theme 这类实例自己的东西。
KEYS = [
    # 定价主体：少一个模型就会 400 model_price_error
    "ModelRatio",
    "CompletionRatio",
    "ModelPrice",
    # 其它计价表
    "ImageRatio",
    "CacheRatio",
    "CreateCacheRatio",
    "AudioRatio",
    "AudioCompletionRatio",
    # 计费模式
    "billing_setting.billing_mode",
    "billing_setting.billing_expr",
    "billing_setting.plugin_billing_expr",
    # 未定价模型是否放行 —— setup 那次把它冲成了 false
    "SelfUseModeEnabled",
    # 行为口径
    "quota_setting.enable_free_model_pre_consume",
    "RetryTimes",
    "QuotaForNewUser",
]

PSQL = "/opt/homebrew/bin/psql"
DEFAULT_SOURCE = "pg:postgresql://newapi:newapi123@127.0.0.1:5432/new-api"


def split_dsn(dsn: str) -> tuple[str, str]:
    kind, _, rest = dsn.partition(":")
    if kind not in ("pg", "sqlite") or not rest:
        raise SystemExit(f"无法识别的连接串：{dsn}（应为 pg:<DSN> 或 sqlite:<路径>）")
    return kind, rest


def read_source(dsn: str, keys: list[str]) -> dict[str, str]:
    kind, target = split_dsn(dsn)
    if kind != "pg":
        raise SystemExit("本脚本只从 PostgreSQL 源库读（本机实例就是 PG）")
    quoted = ",".join("'" + key.replace("'", "''") + "'" for key in keys)
    env = dict(os.environ, PGPASSWORD=target.split("//")[-1].split("@")[0].split(":")[-1])
    result = subprocess.run(
        [PSQL, target, "-At", "-F", "\x1f", "-c", f"select key, value from options where key in ({quoted});"],
        capture_output=True,
        text=True,
        env=env,
    )
    if result.returncode != 0:
        raise SystemExit(f"读源库失败：{result.stderr.strip()}")
    found: dict[str, str] = {}
    for line in result.stdout.splitlines():
        if not line.strip():
            continue
        key, _, value = line.partition("\x1f")
        found[key] = value
    missing = [key for key in keys if key not in found]
    if missing:
        print(f"警告：源库没有这些键，跳过：{', '.join(missing)}", file=sys.stderr)
    return found


def literal(value: str) -> str:
    return "'" + value.replace("'", "''") + "'"


def main() -> None:
    parser = argparse.ArgumentParser(description="镜像本机定价表到新实例：把幂等 SQL 打到 stdout。")
    parser.add_argument("--source", default=os.environ.get("SOURCE", DEFAULT_SOURCE))
    args = parser.parse_args()

    options = read_source(args.source, KEYS)
    lines = ["begin;"]
    for key, value in options.items():
        lines.append(
            f"insert or replace into options (key, value) values ({literal(key)}, {literal(value)});"
        )
    lines.append("commit;")
    print("\n".join(lines))


if __name__ == "__main__":
    main()
