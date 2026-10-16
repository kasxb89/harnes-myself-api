#!/usr/bin/env bash
# 把现网渠道导出包导进并行 harness 实例（8.133.202.230:3010）。
#
# 在本地跑：bash deploy/instance/import-channels.sh
#
# channels.sql 含上游真实 Key，所以只在本机 → 服务器之间传，用完立刻删。
# 全程只动 yunshu-harness-newapi 这一个容器；现有 10 个容器与 nginx 一概不碰。
set -euo pipefail

HOST=${HOST:-8.133.202.230}
KEY=${KEY:-$HOME/.ssh/codex-yunshu-audit-20260826}
D=${INSTANCE_DIR:-/opt/yunshu-harness-newapi-20261016}
DB=$D/data/one-api.db
CONTAINER=yunshu-harness-newapi
EXPORT=${EXPORT_DIR:-/Users/kasxbls/Desktop/新科技/newapi-channel-export-20261014}
SEED=$(cd "$(dirname "$0")/../seed-options" && pwd)/seed_options.py
TMP=import-tmp-$(date +%Y%m%d-%H%M%S)

remote() { ssh -i "$KEY" -o BatchMode=yes "root@$HOST" "$@"; }

FILES="channels.sql abilities.sql task_plugins.sql models.sql options.sql"
for f in $FILES; do
  [ -f "$EXPORT/$f" ] || { echo "缺文件：$EXPORT/$f" >&2; exit 1; }
done

echo "== 1/6 备份目标库"
remote "cp -a $DB $D/backup/one-api.db.\$(date +%Y%m%d-%H%M%S) && ls -l $D/backup/ | tail -3"

echo
echo "== 2/6 上传导出包到 $D/$TMP"
remote "mkdir -p $D/$TMP && chmod 700 $D/$TMP"
for f in $FILES; do
  scp -q -i "$KEY" -o BatchMode=yes "$EXPORT/$f" "root@$HOST:$D/$TMP/$f"
done
remote "chmod 600 $D/$TMP/*.sql && ls -l $D/$TMP"

echo
echo "== 3/6 停容器并导入（channels → abilities → task_plugins → models → options）"
remote "docker stop $CONTAINER >/dev/null && echo '  容器已停'"
for f in channels.sql abilities.sql task_plugins.sql models.sql options.sql; do
  remote "sqlite3 -bail $DB < $D/$TMP/$f" && echo "  $f 已应用"
done

echo
echo "== 4/6 重放种子（options.sql 会覆写 payment_setting.compliance_*，让它改回我们的）"
if python3 "$SEED" --sql-only | remote "sqlite3 -bail $DB"; then
  echo "  种子已重放"
else
  echo "  ！种子重放失败（本地 PG 没起？）—— 导出包里的合规值本就是 true/v1，不影响使用；稍后可单独补跑" >&2
fi

echo
echo "== 5/6 起容器"
remote "docker start $CONTAINER >/dev/null && sleep 8 && docker ps --filter name=$CONTAINER --format '{{.Names}}|{{.Status}}'"

echo
echo "== 6/6 核对（不打印任何 Key）"
remote "DB=$DB
echo '  渠道：'
sqlite3 \$DB \"select '    status=' || status || '  ' || count(*) || ' 条' from channels group by status;\"
echo '  分组映射（abilities）：'
sqlite3 \$DB \"select '    ' || [group] || '  ' || count(*) || ' 条' from abilities group by [group];\"
echo '  任务插件：'
sqlite3 \$DB \"select '    id=' || id || ' v' || version || ' active=' || active from task_plugins;\"
echo '  模型元数据：' \$(sqlite3 \$DB 'select count(*) from models;')
echo '  短信与合规选项仍在：' \$(sqlite3 \$DB \"select count(*) from options where key like 'SmsAliyun%' or key like 'payment_setting%';\") '条'
echo '  实例健康：'
curl -s -o /dev/null -w '    127.0.0.1:3010/api/status -> %{http_code}\n' --max-time 10 http://127.0.0.1:3010/api/status
echo '  既有业务：'
curl -s -o /dev/null -w '    老 :3000/api/status -> %{http_code}\n' --max-time 5 http://127.0.0.1:3000/api/status
docker ps --format '    {{.Names}}' | sort | tr '\n' ' '; echo"

echo
echo "== 清理上传的 SQL（含上游 Key，不留）"
remote "rm -rf $D/$TMP && echo '  已删除 $D/$TMP'"
echo
echo "完成。"
