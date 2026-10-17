#!/usr/bin/env bash
# 【已作废，默认拒绝执行】把「h3视频」渠道（渠道 16）改回 type=55（Sora）。
#
# 为什么作废：type=55 会走内置 `sora` 插件，它的 size 枚举是
# 720x1280/1280x720/1792x1024/1024x1792，产品侧发的 `768P` 会被拒
# （plugin_usage_invalid）。产品实际可用的配置是 type=35 + /api/minimax，
# 由内置 `hailuo` 插件承接 —— 见 fix-h3-hailuo.sh。
# 2026-10-17 实测：type=35 提交/轮询成功，视频下载地址正常返回。
#
# 只有确实要切回 Sora 路时才用：FORCE_RESTORE_H3_SORA=1 bash deploy/instance/restore-h3.sh
set -euo pipefail

if [ "${FORCE_RESTORE_H3_SORA:-0}" != "1" ]; then
  echo "已作废：type=55 会让产品的 768P 请求被 sora 插件拒绝。" >&2
  echo "要恢复可用配置请跑 fix-h3-hailuo.sh；确实要切回 Sora 路再加 FORCE_RESTORE_H3_SORA=1。" >&2
  exit 1
fi

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
backup = db_path.with_name(f"{db_path.name}.bak-restore-h3-{time.strftime('%Y%m%d-%H%M%S')}")
shutil.copy2(db_path, backup)
print(f"  备份 → {backup}")

db = sqlite3.connect(db_path)
db.execute(
    "update channels set type = 55, base_url = ? where id = 16",
    ("https://metaso.cn/api/openai",),
)
db.commit()
row = db.execute(
    "select id, type, status, base_url, models, coalesce(model_mapping,'(无)'), [group] from channels where id = 16"
).fetchone()
chid, ctype, status, base_url, models, mapping, groups = row
print(f"  渠道 {chid} → type={ctype}(Sora)｜{base_url}｜模型 {models}｜映射 {mapping}｜分组 {groups}｜状态 {status}")
PY

echo "== 1/3 改回原始配置"
remote "python3 - $DB" < "$TMP"

echo
echo "== 2/3 重启容器（渠道缓存要重读）"
remote "IMG=\$(docker inspect -f '{{.Config.Image}}' $CONTAINER)
docker rm -f $CONTAINER >/dev/null
docker run -d --name $CONTAINER --restart unless-stopped \
  -p 127.0.0.1:3010:3000 --env-file $D/env \
  -v $D/data:/data -v $D/logs:/app/logs \
  \$IMG >/dev/null
sleep 8
docker ps --filter name=$CONTAINER --format '  {{.Names}}|{{.Status}}'"

echo
echo "== 3/3 自检"
remote "echo -n '  实例: '; curl -s -o /dev/null -w '%{http_code}\n' --max-time 10 http://127.0.0.1:3010/api/status
echo -n '  product 里 MiniMax-H3: '; sqlite3 $DB 'select count(*) from abilities where \"group\" = \"product\" and model = \"MiniMax-H3\" and enabled;'
echo -n '  老业务: '; curl -s -o /dev/null -w '老 :3000 -> %{http_code}\n' --max-time 5 http://127.0.0.1:3000/api/status"

echo
echo "完成。渠道 16 已回到昨天能动之前的配置。"
echo "请用产品再调一次视频任务；如果还是 400，把那条 400 的响应体（harness 异常里的 cause）发我。"
