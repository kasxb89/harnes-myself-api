#!/usr/bin/env bash
# 把「AI 创作管理」内容（分类 / 模板 / 预设 / 封面与演示视频资产）搬到并行 harness 实例。
#
# 在本地跑：bash deploy/instance/import-creation-content.sh
#
# 源默认是本机实例的数据目录（内容是在本机这边做的）；本机没有才回落到现网容器。
#   LOCAL_SRC=<目录>  覆盖本地源
#   SRC_DIR=<容器内路径>  强制从现网容器取（那个容器是 scratch 镜像，里面没有 sh，
#                         所以只能 docker cp 试拷，不能 docker exec）
#
# 这些内容不进数据库：service/creationcontent 从 CREATION_CONTENT_DIR 下的
# content.json + assets/ 读，每次请求都重新读盘，所以搬完不用重启容器。
set -euo pipefail

HOST=${HOST:-8.133.202.230}
KEY=${KEY:-$HOME/.ssh/codex-yunshu-audit-20260826}
D=${INSTANCE_DIR:-/opt/yunshu-harness-newapi-20261016}
DEST=$D/data/creation-content
STAGE=$D/creation-import
PROD=${PROD_CONTAINER:-yunshu-backend-new-api-1}
LOCAL_SRC=${LOCAL_SRC:-$HOME/Library/Application Support/yunshu-new-api/data/creation-content}
BASE=https://$HOST/harness

remote() { ssh -i "$KEY" -o BatchMode=yes "root@$HOST" "$@"; }

summary() {  # $1 = content.json 所在目录（本地可读）
  python3 -I -c "
import json
c = json.load(open('$1/content.json'))
print('  revision =', c.get('revision'))
for mode in ('image', 'video'):
    m = c.get(mode) or {}
    print(f'  {mode}: 分类 {len(m.get(\"categories\") or [])} 个, 模板 {len(m.get(\"templates\") or [])} 个, 预设 {len(m.get(\"presets\") or [])} 个')
    for t in (m.get('templates') or []):
        print('      -', (t.get('title') or {}).get('zh') or t.get('id'), '| status =', t.get('status'))
"
}

echo "== 1/5 定位源"
USE_LOCAL=0
if [ -f "$LOCAL_SRC/content.json" ]; then
  USE_LOCAL=1
  echo "  本机实例：$LOCAL_SRC"
  echo "  资产 $(ls "$LOCAL_SRC/assets" 2>/dev/null | wc -l | tr -d ' ') 个，共 $(du -sh "$LOCAL_SRC" | cut -f1)"
  summary "$LOCAL_SRC"
else
  echo "  本机没有，改从现网容器 ${PROD} 取"
  ENVDIR=$(remote "docker inspect $PROD --format '{{range .Config.Env}}{{println .}}{{end}}' 2>/dev/null | sed -n 's/^CREATION_CONTENT_DIR=//p'" 2>/dev/null | head -1 || true)
  FOUND=""
  for cand in "${SRC_DIR:-}" "$ENVDIR" /data/creation-content /data/data/creation-content /app/data/creation-content /app/creation-content /creation-content; do
    [ -n "$cand" ] || continue
    if remote "rm -rf $STAGE && mkdir -p $STAGE && docker cp '$PROD:$cand/.' '$STAGE/' 2>/dev/null && test -f $STAGE/content.json" 2>/dev/null; then
      FOUND=$cand; break
    fi
  done
  if [ -z "$FOUND" ]; then
    echo "  两边都没找到。带 LOCAL_SRC=<目录> 或 SRC_DIR=<容器内路径> 再跑。" >&2
    echo "  inspect 提示：docker inspect $PROD --format '{{.Config.WorkingDir}} {{range .Mounts}}{{.Source}}=>{{.Destination}} {{end}}'" >&2
    remote "rm -rf $STAGE"
    exit 1
  fi
  echo "  现网容器内：$FOUND"
fi

echo
echo "== 2/5 备份目标端现有内容（若有）"
remote "if [ -d $DEST ]; then mv $DEST $D/backup/creation-content.\$(date +%Y%m%d-%H%M%S); echo '  已挪走旧内容'; else echo '  目标端还没有内容，跳过'; fi"

echo
echo "== 3/5 传过去"
if [ "$USE_LOCAL" = 1 ]; then
  remote "rm -rf $STAGE && mkdir -p $STAGE"
  # macOS 的 tar 会把 xattr 写成 LIBARCHIVE 扩展头，服务器上的 GNU tar 不认识，刷一堆告警，关掉。
  tar czf - --no-xattrs -C "$LOCAL_SRC" . | remote "tar xzf - -C $STAGE"
else
  : # 远端试拷时已经落在 $STAGE
fi
remote "test -f $STAGE/content.json && echo \"  暂存就绪：资产 \$(ls $STAGE/assets 2>/dev/null | wc -l) 个\""

echo
echo "== 4/5 放到容器的 /data/creation-content"
remote "mkdir -p $DEST && cp -a $STAGE/. $DEST/ && chmod -R go-rwx $DEST && rm -rf $STAGE
  echo \"  资产：\$(ls $DEST/assets 2>/dev/null | wc -l) 个\""

echo
echo "== 5/5 核对（走外网入口，无需重启）"
# 这段 python 全用单引号、不用 f-string：外层是 bash 双引号，夹反斜杠转义在
# Python 3.11 及以下会因 f-string 表达式里有反斜杠而直接语法报错。
curl -sk --max-time 20 "$BASE/api/creation/content" | python3 -c "
import json, sys
c = json.load(sys.stdin).get('data') or {}
print('  revision =', c.get('revision'))
for mode in ('image', 'video'):
    m = c.get(mode) or {}
    print('  ' + mode + ': 对外可见分类', len(m.get('categories') or []), '个, 模板', len(m.get('templates') or []), '个, 预设', len(m.get('presets') or []), '个')
    for t in (m.get('templates') or []):
        print('      -', (t.get('title') or {}).get('zh') or t.get('id'))
" || echo "  ！解析失败，直接看：curl -sk $BASE/api/creation/content"

echo
echo "说明：对外只露出 status=published 的模板与 enabled 的分类，草稿（如「联调验证·」那两条）只在后台可见。"
echo "源容器 $PROD 未被改动。"
