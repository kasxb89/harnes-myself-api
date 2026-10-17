#!/usr/bin/env python3
"""把 harness 实例的渠道与分组对 product 打开，让手机登录的用户有聊天模型可用。

在服务器上跑（DB 路径作参数）：

    python3 open-product-group.py /opt/yunshu-harness-newapi-20261016/data/one-api.db

为什么需要它：渠道是从**现网导出**导入的，而现网没有 `product` 分组 —— 所以
`abilities` 里一条 product 映射都没有；能服务 deepseek 系模型的渠道（9、11）只挂在
`yunshu-managed`（免费组，倍率 0），而 `default` 组里那些映射指向的是已禁用渠道
（enabled=0），于是模型列表里只剩生图那两个、聊天模型一调就 503 model_not_found。

做法完全照本机那台（权威参考）来，且只做加法：

1. 给本机上带 product 的那几条渠道的 `group` 追加 `product`；
2. 按与 new-api `Channel.AddAbilities` 完全相同的规则补出 product 的 `abilities` 行
   （group × models，enabled = 渠道是否启用，priority/weight/tag 照抄渠道）；
3. 给分组相关的选项补上 `product`（倍率 1，即用户实付）。

幂等：重复跑不会重复插入；跑前会先备份库。
"""

import json
import shutil
import sqlite3
import sys
import time
from pathlib import Path

# 本机上带 product 的渠道，逐条核对过。
CHANNELS = [9, 11, 12, 14, 16, 17, 18]
GROUP = "product"

# 需要补上 product 的选项。倍率一律 1，与本机一致（用户实付，不是免费组）。
RATIO_OPTIONS = ["GroupRatio", "group_ratio_setting.group_ratio", "TopupGroupRatio"]
LIST_OPTIONS = ["AutoGroups"]
MAP_OPTIONS = ["UserUsableGroups"]


def upsert_option(db: sqlite3.Connection, key: str, value: str) -> None:
    db.execute("insert or replace into options (key, value) values (?, ?)", (key, value))


def main() -> None:
    if len(sys.argv) != 2:
        sys.exit(__doc__)
    db_path = Path(sys.argv[1])
    backup = db_path.with_name(
        f"{db_path.name}.bak-open-product-{time.strftime('%Y%m%d-%H%M%S')}"
    )
    shutil.copy2(db_path, backup)
    print(f"备份 → {backup}")

    db = sqlite3.connect(db_path)
    try:
        added_groups = 0
        added_abilities = 0
        for channel_id in CHANNELS:
            row = db.execute(
                'select "group", models, status, priority, weight, tag from channels where id = ?',
                (channel_id,),
            ).fetchone()
            if row is None:
                print(f"  渠道 {channel_id} 不存在，跳过")
                continue
            groups_field, models_field, status, priority, weight, tag = row
            groups = [g for g in (groups_field or "").split(",") if g]
            if GROUP not in groups:
                groups.append(GROUP)
                db.execute(
                    'update channels set "group" = ? where id = ?',
                    (",".join(groups), channel_id),
                )
                added_groups += 1
            enabled = 1 if status == 1 else 0
            models = [m for m in (models_field or "").split(",") if m]
            for model in models:
                cursor = db.execute(
                    'insert or ignore into abilities ("group", model, channel_id, enabled, priority, weight, tag)'
                    " values (?, ?, ?, ?, ?, ?, ?)",
                    (GROUP, model, channel_id, enabled, priority, weight, tag),
                )
                added_abilities += cursor.rowcount
            print(f"  渠道 {channel_id}: 分组 → {','.join(groups)}，模型 {len(models)} 个，启用={enabled}")

        for key in RATIO_OPTIONS:
            row = db.execute("select value from options where key = ?", (key,)).fetchone()
            current = json.loads(row[0]) if row and row[0] else {}
            if current.get(GROUP) == 1:
                print(f"  {key}: 已含 product，跳过")
                continue
            current[GROUP] = 1
            upsert_option(db, key, json.dumps(current, separators=(",", ":"), ensure_ascii=False))
            print(f"  {key}: 补上 product=1")

        for key in LIST_OPTIONS:
            row = db.execute("select value from options where key = ?", (key,)).fetchone()
            current = json.loads(row[0]) if row and row[0] else []
            if GROUP in current:
                print(f"  {key}: 已含 product，跳过")
                continue
            current.append(GROUP)
            upsert_option(db, key, json.dumps(current, separators=(",", ":"), ensure_ascii=False))
            print(f"  {key}: 补上 product")

        for key in MAP_OPTIONS:
            row = db.execute("select value from options where key = ?", (key,)).fetchone()
            current = json.loads(row[0]) if row and row[0] else {}
            if GROUP in current:
                print(f"  {key}: 已含 product，跳过")
                continue
            current[GROUP] = "product 分组"
            upsert_option(db, key, json.dumps(current, separators=(",", ":"), ensure_ascii=False))
            print(f"  {key}: 补上 product")

        # 已有用户里落在 default 的，本来是登录默认组缺失所致，一并挪到 product。
        moved = db.execute(
            'update users set "group" = ? where "group" = ?', (GROUP, "default")
        ).rowcount
        db.commit()
        print(f"用户：{moved} 个从 default 挪到 product")

        counts = db.execute(
            'select "group", count(*) from abilities group by "group" order by "group"'
        ).fetchall()
        print("abilities 现状：" + "，".join(f"{g} {n} 条" for g, n in counts))
        print(f"本次：渠道分组改写 {added_groups} 条，补映射 {added_abilities} 条")
    finally:
        db.close()


if __name__ == "__main__":
    main()
