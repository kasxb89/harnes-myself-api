#!/usr/bin/env bash
# 重新构建当前工作区（前端 + Go）并发部到并行 harness 实例（8.133.202.230:3010）。
#
# 在本地跑：bash deploy/instance/redeploy.sh
#
# 只碰 yunshu-harness-newapi 这一个容器：构建前端（Go 二进制内嵌 web/dist）→ 交叉编译 →
# 上传 → 重建镜像 → 用同一个 env 与挂载重建容器。
# 数据（/data，含 SQLite 与创作内容）与证书（/data/secrets）都在挂载卷上，不受重建影响。
set -euo pipefail

HOST=${HOST:-8.133.202.230}
KEY=${KEY:-$HOME/.ssh/codex-yunshu-audit-20260826}
D=${INSTANCE_DIR:-/opt/yunshu-harness-newapi-20261016}
CONTAINER=yunshu-harness-newapi
IMAGE=yunshu/harness-new-api:$(date +%Y%m%d)
REPO=$(cd "$(dirname "$0")/../.." && pwd)

remote() { ssh -i "$KEY" -o BatchMode=yes "root@$HOST" "$@"; }

echo "== 1/7 构建前端（产物会被 Go 二进制内嵌）"
cd "$REPO/web"
bun run build 2>&1 | tail -2

echo
echo "== 2/7 交叉编译（linux/amd64）"
cd "$REPO"
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o /tmp/new-api-linux-amd64 .
LOCAL_SHA=$(shasum -a 256 /tmp/new-api-linux-amd64 | cut -d' ' -f1)
echo "  sha256 $LOCAL_SHA"

echo
echo "== 3/7 上传"
remote "cp -a $D/app/new-api $D/backup/new-api.\$(date +%Y%m%d-%H%M%S) && echo '  旧二进制已备份'"
scp -q -i "$KEY" -o BatchMode=yes /tmp/new-api-linux-amd64 "root@$HOST:$D/app/new-api"

echo
echo "== 4/7 校验上传完整"
REMOTE_SHA=$(remote "sha256sum $D/app/new-api | cut -d' ' -f1")
if [ "$LOCAL_SHA" != "$REMOTE_SHA" ]; then
  echo "  哈希不一致，中止：本地 $LOCAL_SHA / 远端 $REMOTE_SHA" >&2
  exit 1
fi
echo "  一致"

echo
echo "== 5/7 重建镜像（${IMAGE}）"
remote "chmod +x $D/app/new-api && docker build -q -t $IMAGE $D/app >/dev/null && echo '  已构建'"

echo
echo "== 6/7 重建容器（同一个 env 与挂载）"
remote "docker rm -f $CONTAINER >/dev/null
docker run -d --name $CONTAINER --restart unless-stopped \
  -p 127.0.0.1:3010:3000 --env-file $D/env \
  -v $D/data:/data -v $D/logs:/app/logs \
  $IMAGE >/dev/null
sleep 8
docker ps --filter name=$CONTAINER --format '  {{.Names}}|{{.Status}}|{{.Ports}}'"

echo
echo "== 7/7 核对"
remote "echo -n '  实例 /api/status: '; curl -s -o /dev/null -w '%{http_code}\n' --max-time 10 http://127.0.0.1:3010/api/status
DB=$D/data/one-api.db
echo \"  渠道 \$(sqlite3 \$DB 'select count(*) from channels;')，映射 \$(sqlite3 \$DB 'select count(*) from abilities;')，用户 \$(sqlite3 \$DB 'select count(*) from users;')，货币 \$(sqlite3 \$DB \"select value from options where key='general_setting.quota_display_type';\")\"
echo -n '  创作内容: '; test -f $D/data/creation-content/content.json && echo '在' || echo '缺'
echo -n '  微信变量: '; docker inspect $CONTAINER --format '{{range .Config.Env}}{{println .}}{{end}}' | grep -c '^WECHAT_'
echo -n '  前端已更新: '; curl -s --max-time 10 http://127.0.0.1:3010/ | grep -o 'index\.[a-z0-9]*\.js' | head -1
echo -n '  既有业务: '; curl -s -o /dev/null -w '老 :3000 -> %{http_code}\n' --max-time 5 http://127.0.0.1:3000/api/status"

echo
echo "完成。旧二进制在 $D/backup/ 下，回退就是把它拷回 app/new-api 再跑一遍第 5、6 步。"
