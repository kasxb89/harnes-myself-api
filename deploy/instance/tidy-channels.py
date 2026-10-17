#!/usr/bin/env python3
"""整理 harness 实例的渠道：删假的/死的，合同上游的，并把剩下的都对 product 打开。

在服务器上跑：

    python3 tidy-channels.py /opt/yunshu-harness-newapi-20261016/data/one-api.db

**删**（逐条核对过）

    1   与 9 同上游同模型，但用测试密钥且已停用 —— 假的
    2   上游是容器内主机名 http://yunshu-chat-failover:18081，本实例没有这个东西；测试密钥
    3   已停用（VSTECS deepseek-v4-flash）
    4   已停用（VSTECS kimi-k2.7-code）
    5   已停用（VSTECS glm-5.2）
    6   已停用（VSTECS qwen3.6-flash）
    13  已停用（OpenRouter 免费池）
    17  与 14 同上游、同模型、同密钥，纯复制

**合**（同上游且**同密钥**，合并后模型取并集、分组取并集）

    10 → 9    同为 https://www.dmxapi.cn，同密钥，模型并成 14 个
    17 → 14   同为 https://api.zlrxjh.top，同密钥

**不合**：15 与 18 虽然同上游 https://toapis.cn，但**密钥不同**，且 18 是 type 61
（toapis-image 任务插件生图）、15 是 type 1 普通渠道 —— 合成一条必然死一个，故保留两条。

**最后**：剩下的每条渠道的 `group` 都补上 `product`（只加不减），并按 new-api 自己的规则
（`group × models`，`enabled` 跟渠道状态）重算全部 abilities。

跑前先备份库。
"""

import json
import shutil
import sqlite3
import sys
import time
from pathlib import Path

REMOVE = [
    (1, "与 9 同上游同模型，但用测试密钥且已停用"),
    (2, "上游是容器内主机名，本实例不存在；测试密钥"),
    (3, "已停用"),
    (4, "已停用"),
    (5, "已停用"),
    (6, "已停用"),
    (13, "已停用"),
    (17, "与 14 同上游同模型同密钥，纯复制"),
]

# (源, 目标)：源渠道的模型与分组并入目标，然后删掉源。
MERGE = [
    (10, 9),
    (17, 14),
]

GROUP = "product"


def split_csv(value: str | None) -> list[str]:
    return [item for item in (value or "").split(",") if item]


def union(items: list[str], extra: list[str]) -> list[str]:
    seen = list(items)
    for item in extra:
        if item not in seen:
            seen.append(item)
    return seen


def merge_mappings(target: str | None, source: str | None) -> str:
    """合并两条渠道的 model_mapping —— 各自主张自己的键，目标侧优先。

    漏掉这一步会把上游认的模型名丢掉：例如 gp-5.5 本该在发出前改写成 gpt-5.5，
    合并不带映射就会原样发出去，上游报模型不存在。
    """
    try:
        merged = json.loads(source or "{}")
    except ValueError:
        merged = {}
    try:
        merged.update(json.loads(target or "{}"))
    except ValueError:
        pass
    if not merged:
        return target or ""
    return json.dumps(merged, ensure_ascii=False, separators=(",", ":"))


def main() -> None:
    if len(sys.argv) != 2:
        sys.exit(__doc__)
    db_path = Path(sys.argv[1])
    backup = db_path.with_name(
        f"{db_path.name}.bak-tidy-channels-{time.strftime('%Y%m%d-%H%M%S')}"
    )
    shutil.copy2(db_path, backup)
    print(f"备份 → {backup}")

    db = sqlite3.connect(db_path)
    try:
        before = db.execute("select count(*) from channels").fetchone()[0]

        for source_id, target_id in MERGE:
            source = db.execute(
                "select models, [group], model_mapping from channels where id = ?",
                (source_id,),
            ).fetchone()
            target = db.execute(
                "select models, [group], model_mapping from channels where id = ?",
                (target_id,),
            ).fetchone()
            if source is None or target is None:
                print(f"  合并 {source_id} → {target_id}：有一边不存在，跳过")
                continue
            models = union(split_csv(target[0]), split_csv(source[0]))
            groups = union(split_csv(target[1]), split_csv(source[1]))
            mapping = merge_mappings(target[2], source[2])
            db.execute(
                "update channels set models = ?, [group] = ?, model_mapping = ? where id = ?",
                (",".join(models), ",".join(groups), mapping, target_id),
            )
            db.execute("delete from abilities where channel_id = ?", (source_id,))
            db.execute("delete from channels where id = ?", (source_id,))
            print(
                f"  合并 {source_id} → {target_id}：模型 {len(models)} 个，分组 {','.join(groups)}"
            )

        for channel_id, reason in REMOVE:
            row = db.execute(
                "select name, status, base_url from channels where id = ?", (channel_id,)
            ).fetchone()
            if row is None:
                print(f"  渠道 {channel_id} 不存在，跳过")
                continue
            name, status, base_url = row
            abilities = db.execute(
                "delete from abilities where channel_id = ?", (channel_id,)
            ).rowcount
            db.execute("delete from channels where id = ?", (channel_id,))
            print(f"  删渠道 {channel_id}（{name}｜状态{status}｜{base_url}）—— {reason}；清映射 {abilities} 条")

        # 剩下的全部对 product 打开，并按渠道重算 abilities。
        rebuilt = 0
        for row in db.execute(
            "select id, models, [group], status, priority, weight, tag from channels"
        ).fetchall():
            channel_id, models_field, group_field, status, priority, weight, tag = row
            groups = union(split_csv(group_field), [GROUP])
            db.execute(
                "update channels set [group] = ? where id = ?",
                (",".join(groups), channel_id),
            )
            db.execute("delete from abilities where channel_id = ?", (channel_id,))
            enabled = 1 if status == 1 else 0
            for model in split_csv(models_field):
                for group in groups:
                    db.execute(
                        'insert or replace into abilities ("group", model, channel_id, enabled, priority, weight, tag)'
                        " values (?, ?, ?, ?, ?, ?, ?)",
                        (group, model, channel_id, enabled, priority, weight, tag),
                    )
                    rebuilt += 1

        db.commit()

        after = db.execute("select count(*) from channels").fetchone()[0]
        print(f"\n渠道数：{before} → {after}，重算映射 {rebuilt} 条。剩下：")
        for row in db.execute(
            "select id, status, base_url, [group], models from channels order by id"
        ):
            channel_id, status, base_url, groups, models = row
            print(
                f"  {channel_id:>3} 状态{status} {base_url:<34} {len(split_csv(models))} 个模型｜{groups}"
            )

        print()
        for group, count in db.execute(
            'select "group", count(distinct model) from abilities where enabled group by "group" order by "group"'
        ):
            print(f"  分组 {group}: {count} 个可用模型")
    finally:
        db.close()


if __name__ == "__main__":
    main()
