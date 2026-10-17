#!/usr/bin/env bash
# 把「h3视频」渠道（渠道 16）配成你本机那条路（B）：
#
#   type = 35（MiniMax）→ 走 new-api **内置**的 hailuo 插件（plugins/tasks/hailuo/plugin.js，
#                         与 sora 并列的 builtin 之一，随二进制发布，不需要额外装）
#   base_url = https://metaso.cn/api/minimax
#   models = MiniMax-H3          ← 产品侧看到、调用的名字
#   model_mapping = 无           ← 插件把名字原样发上游，不需要改写
#
# 计费也已就位：billing_setting.plugin_billing_expr 里有
#   {"hailuo::MiniMax-H3": "u(\"seconds\") * 0.0410959"}   ← 按秒 ¥0.3/秒
#
# 对照：厂商文档首选的 Sora 路（type 55 + /api/openai + 模型 sora-2 + 映射）走的是内置
# sora 插件、按次计价，且产品侧得改叫 sora-2。两条路只能整体选一条 —— 这里选 B。
#
# 本地跑：bash deploy/instance/fix-h3-hailuo.sh
set -euo pipefail

HOST=${HOST:-8.133.202.230}
KEY=${KEY:-$HOME/.ssh/codex-yunshu-audit-20260826}
D=${INSTANCE_DIR:-/opt/yunshu-harness-newapi-20261016}
CONTAINER=yunshu-harness-newapi
DB=$D/data/one-api.db

remote() { ssh -i "$KEY" -o BatchMode=yes "root@$HOST" "$@"; }

TMP=$(mktemp)
trap 'rm -f "$TMP"' EXIT
cat > "$TMP" <<'PY'
import shutil, sqlite3, sys, time
from pathlib import Path

db_path = Path(sys.argv[1])
backup = db_path.with_name(f"{db_path.name}.bak-h3-hailuo-{time.strftime('%Y%m%d-%H%M%S')}")
shutil.copy2(db_path, backup)
print(f"  备份 → {backup}")

db = sqlite3.connect(db_path)
db.execute(
    "update channels set type = 35, base_url = ?, models = ?, model_mapping = NULL where id = 16",
    ("https://metaso.cn/api/minimax", "MiniMax-H3"),
)
row = db.execute(
    "select models, [group], status, priority, weight, tag from channels where id = 16"
).fetchone()
models_field, groups_field, status, priority, weight, tag = row
groups = [g for g in (groups_field or "").split(",") if g]
db.execute("delete from abilities where channel_id = 16")
enabled = 1 if status == 1 else 0
added = 0
for model in [m for m in (models_field or "").split(",") if m]:
    for group in groups:
        db.execute(
            'insert or replace into abilities ("group", model, channel_id, enabled, priority, weight, tag)'
            " values (?, ?, ?, ?, ?, ?, ?)",
            (group, model, 16, enabled, priority, weight, tag),
        )
        added += 1
db.commit()
print("  渠道 16 → type=35 MiniMax｜https://metaso.cn/api/minimax｜模型 MiniMax-H3｜无映射")
print(f"  重算映射 {added} 条，分组 {','.join(groups)}")

expr = db.execute(
    "select value from options where key = 'billing_setting.plugin_billing_expr'"
).fetchone()
print(f"  按秒计费表达式: {expr[0] if expr else '(缺！)'}")
PY

echo "== 1/4 改配置并重算这条渠道的映射"
remote "python3 - $DB" < "$TMP"

echo
echo "== 2/4 改完的样子（type 应为 35；无映射）"
remote "sqlite3 -header -column $DB 'select id, type, base_url, models, coalesce(model_mapping, \"(无)\") as mapping from channels where id = 16;'"

echo
echo "== 3/4 重启容器"
remote "IMG=\$(docker inspect -f '{{.Config.Image}}' $CONTAINER)
docker rm -f $CONTAINER >/dev/null
docker run -d --name $CONTAINER --restart unless-stopped \
  -p 127.0.0.1:3010:3000 --env-file $D/env \
  -v $D/data:/data -v $D/logs:/app/logs \
  \$IMG >/dev/null
sleep 8
docker ps --filter name=$CONTAINER --format '  {{.Names}}|{{.Status}}'"

echo
echo "== 4/4 自检"
remote "echo -n '  实例: '; curl -s -o /dev/null -w '%{http_code}\n' --max-time 10 http://127.0.0.1:3010/api/status
echo -n '  渠道数: '; sqlite3 $DB 'select count(*) from channels;'
echo -n '  product 里 MiniMax-H3: '; sqlite3 $DB 'select count(*) from abilities where \"group\" = \"product\" and model = \"MiniMax-H3\" and enabled;'
echo -n '  老业务: '; curl -s -o /dev/null -w '老 :3000 -> %{http_code}\n' --max-time 5 http://127.0.0.1:3000/api/status"

echo
echo "完成。产品侧仍调 MiniMax-H3，走异步视频任务："
echo "  提交 POST /v1/video/generations   轮询 GET /v1/video/generations/{task_id}"
echo "  入参 model=MiniMax-H3、prompt、duration(4-15 秒)、size(768P 或 2K)，可用 seconds/resolution 别名。"
