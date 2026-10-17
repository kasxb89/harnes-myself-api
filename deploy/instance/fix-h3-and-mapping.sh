#!/usr/bin/env bash
# 修两处渠道配置，并重启容器让缓存生效（一条命令做完）：
#
#   1. h3视频（渠道 16）被配成了 Sora 渠道 + metaso 的 /api/openai 入口，
#      改成你本机的样子：type 35（MiniMax）+ https://metaso.cn/api/minimax；
#   2. 渠道 9 在合并时丢了 model_mapping（gp-5.5→gpt-5.5、claode-5→claude-fable-5），补回来。
#
# 本地跑：bash deploy/instance/fix-h3-and-mapping.sh
set -euo pipefail

HOST=${HOST:-8.133.202.230}
KEY=${KEY:-$HOME/.ssh/codex-yunshu-audit-20260826}
D=${INSTANCE_DIR:-/opt/yunshu-harness-newapi-20261016}
CONTAINER=yunshu-harness-newapi

remote() { ssh -i "$KEY" -o BatchMode=yes "root@$HOST" "$@"; }

echo "== 1/4 备份并改配置"
remote "cp -a $D/data/one-api.db $D/backup/one-api.db.fix-channels-\$(date +%Y%m%d-%H%M%S) && echo '  已备份'" | tail -1
remote "sqlite3 $D/data/one-api.db" <<'SQL'
update channels set type = 35, base_url = 'https://metaso.cn/api/minimax' where id = 16;
update channels set model_mapping = '{"qwen3.6-flash":"qwen3.8-flash","gp-5.5":"gpt-5.5","claode-5":"claude-fable-5"}' where id = 9;
SQL
echo "  已写入"

echo
echo "== 2/4 改完的样子（16 应为 type=35 / api/minimax；9 的映射应有三条）"
remote "sqlite3 -header -column $D/data/one-api.db 'select id, type, base_url, model_mapping from channels where id in (9,16);'"

echo
echo "== 3/4 重启容器（配置要先落库，再让缓存重读）"
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
echo -n '  渠道数: '; sqlite3 $D/data/one-api.db 'select count(*) from channels;'
echo -n '  product 模型数: '; sqlite3 $D/data/one-api.db 'select count(distinct model) from abilities where \"group\" = \"product\" and enabled;'
echo -n '  老业务: '; curl -s -o /dev/null -w '老 :3000 -> %{http_code}\n' --max-time 5 http://127.0.0.1:3000/api/status"

echo
echo "完成。h3 那条现在可以在后台点「测试」验证；若仍报模型不存在，那是 metaso 那边的模型名/密钥问题。"
