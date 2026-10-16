#!/usr/bin/env bash
# 把本机跑通的微信支付配置搬到并行 harness 实例。
#
# 在本地跑：bash deploy/instance/import-wechat-pay.sh
#
# 值从本机 launchd 任务 com.newapi.server 的命令行里读（那是本机实例实际在用的那套）。
# 商户私钥/平台公钥两个 PEM 经 scp 上服务器，落在容器挂载卷内的 secrets/ 下；
# 临时文件用完即删。源容器与本机实例一概不改。
set -euo pipefail

HOST=${HOST:-8.133.202.230}
KEY=${KEY:-$HOME/.ssh/codex-yunshu-audit-20260826}
D=${INSTANCE_DIR:-/opt/yunshu-harness-newapi-20261016}
CONTAINER=yunshu-harness-newapi
LABEL=com.newapi.server
SECRET_DIR_ON_SERVER=$D/data/secrets
SECRET_DIR_IN_CONTAINER=/data/secrets
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

remote() { ssh -i "$KEY" -o BatchMode=yes "root@$HOST" "$@"; }

echo "== 1/6 从本机 launchd 任务读微信配置"
launchctl print "gui/$(id -u)/$LABEL" > "$TMP/launchd.txt" || {
  echo "  读不到 launchctl print gui/$(id -u)/$LABEL" >&2; exit 1; }
python3 - "$TMP/launchd.txt" "$TMP/wechat.env" "$TMP/pem-paths" <<'PY'
import re, sys
text = open(sys.argv[1]).read()
found = {}
for name, whole, quoted, bare in re.findall(r"export (WECHAT_[A-Z0-9_]+)=('([^']*)'|(\S+))", text):
    found[name] = quoted or bare
# 本机为联调调过的两个节奏参数不带上：新实例用代码默认值（120s / 1800s）。
drop = {"WECHAT_API_BASE", "WECHAT_RECONCILE_INTERVAL_SECONDS", "WECHAT_RECONCILE_MIN_AGE_SECONDS"}
kept = {k: v for k, v in found.items() if k not in drop}
need = ["WECHAT_APP_ID", "WECHAT_MCH_ID", "WECHAT_API_V3_KEY", "WECHAT_MERCHANT_SERIAL",
        "WECHAT_PLATFORM_PUBLIC_KEY_ID"]
missing = [k for k in need if not kept.get(k)]
if missing:
    sys.exit("本机缺这些：" + ", ".join(missing))
if len(kept["WECHAT_API_V3_KEY"]) != 32:
    sys.exit("APIV3 密钥长度不是 32")
paths = ["WECHAT_MERCHANT_PRIVATE_KEY_PATH", "WECHAT_PLATFORM_PUBLIC_KEY_PATH", "WECHAT_NOTIFY_URL"]
out = {
    "WECHAT_PAY_ENABLED": "true",
    **{k: v for k, v in kept.items() if k not in paths},
    "WECHAT_MERCHANT_PRIVATE_KEY_PATH": "/data/secrets/wechat_merchant_private_key.pem",
    "WECHAT_PLATFORM_PUBLIC_KEY_PATH": "/data/secrets/wechat_pay_public_key.pem",
    "WECHAT_NOTIFY_URL": "https://8.133.202.230/harness/api/user/wechat/notify",
}
with open(sys.argv[2], "w") as fh:
    for k, v in out.items():
        fh.write(f"{k}={v}\n")
with open(sys.argv[3], "w") as fh:
    fh.write(kept.get("WECHAT_MERCHANT_PRIVATE_KEY_PATH", "") + "\n")
    fh.write(kept.get("WECHAT_PLATFORM_PUBLIC_KEY_PATH", "") + "\n")
print("  读到并整理出 " + str(len(out)) + " 项：" + ", ".join(sorted(out)))
PY
chmod 600 "$TMP/wechat.env"

echo
echo "== 2/6 找本机的两张证书"
MERCH_KEY=$(sed -n '1p' "$TMP/pem-paths")
PLAT_KEY=$(sed -n '2p' "$TMP/pem-paths")
echo "  商户私钥：$MERCH_KEY"
echo "  平台公钥：$PLAT_KEY"
for f in "$MERCH_KEY" "$PLAT_KEY"; do
  [ -f "$f" ] || { echo "  找不到 $f" >&2; exit 1; }
done

echo
echo "== 3/6 上传（证书 + 环境变量文件）"
remote "mkdir -p $SECRET_DIR_ON_SERVER && chmod 700 $SECRET_DIR_ON_SERVER"
scp -q -i "$KEY" -o BatchMode=yes "$MERCH_KEY" "root@$HOST:$SECRET_DIR_ON_SERVER/wechat_merchant_private_key.pem"
scp -q -i "$KEY" -o BatchMode=yes "$PLAT_KEY" "root@$HOST:$SECRET_DIR_ON_SERVER/wechat_pay_public_key.pem"
scp -q -i "$KEY" -o BatchMode=yes "$TMP/wechat.env" "root@$HOST:$D/wechat.env.incoming"
remote "chmod 600 $SECRET_DIR_ON_SERVER/*.pem && echo '  证书：' && ls -l $SECRET_DIR_ON_SERVER"

echo
echo "== 4/6 校验证书是能用的 PEM（防止传坏）"
remote "openssl pkey -in $SECRET_DIR_ON_SERVER/wechat_merchant_private_key.pem -noout && echo '  商户私钥：可解析' || echo '  ！商户私钥不可解析'
openssl pkey -pubin -in $SECRET_DIR_ON_SERVER/wechat_pay_public_key.pem -noout && echo '  平台公钥：可解析' || echo '  ！平台公钥不可解析'"

echo
echo "== 5/6 合并进 env 并重建容器（env-file 改动必须重建）"
remote "set -e
grep -v '^WECHAT_' $D/env > $D/env.new || true
cat $D/wechat.env.incoming >> $D/env.new
mv $D/env.new $D/env
rm -f $D/wechat.env.incoming
chmod 600 $D/env
echo '  env 键：'; cut -d= -f1 $D/env | tr '\n' ' '; echo
docker rm -f $CONTAINER >/dev/null
docker run -d --name $CONTAINER --restart unless-stopped \
  -p 127.0.0.1:3010:3000 --env-file $D/env \
  -v $D/data:/data -v $D/logs:/app/logs \
  yunshu/harness-new-api:20261016 >/dev/null
sleep 8
docker ps --filter name=$CONTAINER --format '  {{.Names}}|{{.Status}}|{{.Ports}}'"

echo
echo "== 6/6 核对"
remote "echo -n '  微信变量齐备：'; docker inspect $CONTAINER --format '{{range .Config.Env}}{{println .}}{{end}}' | grep -c '^WECHAT_'
echo -n '  八个必填项都在（WechatPayConfigured 要求全非空）：'
docker inspect $CONTAINER --format '{{range .Config.Env}}{{println .}}{{end}}' | grep -E '^WECHAT_(APP_ID|MCH_ID|API_V3_KEY|MERCHANT_SERIAL|MERCHANT_PRIVATE_KEY_PATH|PLATFORM_PUBLIC_KEY_ID|PLATFORM_PUBLIC_KEY_PATH|NOTIFY_URL)=' | wc -l
docker inspect $CONTAINER --format '{{range .Config.Env}}{{println .}}{{end}}' | grep '^WECHAT_NOTIFY_URL='
echo -n '  容器内证书可见：'; docker exec $CONTAINER sh -c 'ls /data/secrets | tr \"\\n\" \" \"; echo'
echo -n '  实例健康：'; curl -s -o /dev/null -w '%{http_code}\n' --max-time 10 http://127.0.0.1:3010/api/status
echo -n '  回调路径可达（未鉴权，405/400 都算通）：'; curl -s -o /dev/null -w '%{http_code}\n' --max-time 10 -X POST http://127.0.0.1:3010/api/user/wechat/notify -H 'Content-Type: application/json' -d '{}'
echo -n '  既有业务：'; curl -s -o /dev/null -w '老 :3000 -> %{http_code}\n' --max-time 5 http://127.0.0.1:3000/api/status"

echo
echo "完成。本机实例与源容器都未被改动。"
echo "真正走一笔支付需要登录后的用户令牌，脚本验不了；登录后下一单 1 元即可确认。"
