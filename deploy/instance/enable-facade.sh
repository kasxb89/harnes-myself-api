#!/usr/bin/env bash
# 给并行 harness 实例（127.0.0.1:3010）加一个 nginx 子路径入口 /harness/。
#
# 在服务器上跑：ssh -i <key> root@8.133.202.230 'bash -s' < enable-facade.sh
#
# 做三件事：写两个 nginx 配置文件、在站点里加一行 include、reload。
# reload 前先备份站点文件并跑 nginx -t；校验不过就回滚且不 reload。
# 不新增监听端口，不碰 3000/4310/4320/4323 的任何既有路由，不重启 nginx（只 reload）。
set -euo pipefail

D=${INSTANCE_DIR:-/opt/yunshu-harness-newapi-20261016}
SITE=/etc/nginx/sites-available/yunshu-ip-test
BACKUP=$D/backup/yunshu-ip-test.$(date +%Y%m%d-%H%M%S)

mkdir -p "$D/backup"
cp -a "$SITE" "$BACKUP"
echo "已备份站点文件 → $BACKUP"

cat > /etc/nginx/conf.d/yunshu-harness-limits.conf <<'NGINX_LIMITS'
# 并行 harness 实例（127.0.0.1:3010）的独立限流与日志。
# zone 与 log_format 都另起名字，不共用 /new-api 那套，两边互不影响。
limit_req_zone $binary_remote_addr zone=yunshu_harness:10m rate=20r/s;
log_format yunshu_harness_safe '$time_iso8601 $remote_addr $request_method $uri $status $request_time';
NGINX_LIMITS

cat > /etc/nginx/snippets/yunshu-harness-console.conf <<'NGINX_SNIPPET'
# 并行 harness 实例（127.0.0.1:3010）的对外入口，与 /new-api 那套并列。
# 不新增监听端口，不碰 3000/4310/4320/4323 的任何既有路由。

location = /harness { return 308 /harness/; }

# 推理/中转路径单独一条：流式补全要边走边吐，既不能缓冲也不能做 sub_filter。
location ~ ^/harness/(v1|v1beta|v1beta1|v2|pg|mj|suno|relay)(/|$) {
    limit_req zone=yunshu_harness burst=100 nodelay;
    limit_req_status 429;
    client_max_body_size 100m;

    # 正则 location 里 proxy_pass 不能带 URI，先剥前缀再透传。
    rewrite ^/harness/(.*)$ /$1 break;
    proxy_pass http://127.0.0.1:3010;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-Proto https;
    proxy_set_header X-Forwarded-Prefix /harness;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $remote_addr;
    proxy_set_header Connection "";

    proxy_buffering off;
    proxy_cache off;
    proxy_connect_timeout 5s;
    proxy_send_timeout 3600s;
    proxy_read_timeout 3600s;

    proxy_cookie_path ~^(/.*)$ "/harness$1; Secure; HttpOnly; SameSite=Strict";

    access_log /var/log/nginx/yunshu-harness-access.log yunshu_harness_safe;
    error_log /var/log/nginx/yunshu-harness-error.log crit;
}

# 控制台与普通 API：这里才需要改写前端里的绝对路径。
location /harness/ {
    auth_basic off;
    auth_request off;
    limit_req zone=yunshu_harness burst=100 nodelay;
    limit_req_status 429;
    client_max_body_size 100m;

    # 开号只走手机验证码；网页注册与初始化接口一律关掉。
    if ($uri ~ "^/harness/(sign-up|api/user/register|api/user/auth/register)(/|$)") { return 403; }
    if ($uri ~ "^/harness/api/setup(/|$)") { return 403; }

    proxy_pass http://127.0.0.1:3010/;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-Proto https;
    proxy_set_header X-Forwarded-Prefix /harness;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $remote_addr;
    proxy_set_header Accept-Encoding "";
    proxy_set_header Connection "";

    proxy_cache off;
    proxy_connect_timeout 5s;
    proxy_read_timeout 120s;

    proxy_cookie_path ~^(/.*)$ "/harness$1; Secure; HttpOnly; SameSite=Strict";
    proxy_redirect http://127.0.0.1:3010/ /harness/;
    proxy_redirect ~^(/(?!harness(?:/|$)).*)$ /harness$1;

    proxy_hide_header Cache-Control;
    proxy_hide_header Expires;
    proxy_hide_header ETag;
    add_header Cache-Control "no-store" always;
    add_header X-Content-Type-Options nosniff always;
    add_header Referrer-Policy no-referrer always;
    add_header X-Frame-Options DENY always;

    access_log /var/log/nginx/yunshu-harness-access.log yunshu_harness_safe;
    error_log /var/log/nginx/yunshu-harness-error.log crit;

    sub_filter_once off;
    sub_filter_types application/javascript text/javascript text/css;
    sub_filter '({routeTree:' '({basepath:"/harness",routeTree:';
    sub_filter '.p="/"' '.p="/harness/"';
    sub_filter '"/api/' '"/harness/api/';
    sub_filter '`/api/' '`/harness/api/';
    sub_filter '"/logo.png"' '"/harness/logo.png"';
    sub_filter '"/favicon.ico"' '"/harness/favicon.ico"';
    sub_filter 'window.location.replace("/sign-in")' 'window.location.replace("/harness/sign-in")';
    sub_filter '"/sign-in"!==window.location.pathname' '"/harness/sign-in"!==window.location.pathname';
    sub_filter 'src="/static/' 'src="/harness/static/';
    sub_filter 'href="/static/' 'href="/harness/static/';
    sub_filter 'url(/static/' 'url(/harness/static/';
    sub_filter 'this.resolveRedirect=e=>{' 'this.resolveRedirect=e=>{if(typeof e.options.href==="string"&&e.options.href.startsWith("/")&&!e.options.href.startsWith("//")&&e.options.href!=="/harness"&&!e.options.href.startsWith("/harness/")){e.options.href="/harness"+e.options.href;e.headers.set("Location",e.options.href)}';
}
NGINX_SNIPPET

chmod 644 /etc/nginx/conf.d/yunshu-harness-limits.conf /etc/nginx/snippets/yunshu-harness-console.conf

if ! grep -q "yunshu-harness-console.conf" "$SITE"; then
  sed -i "s|^\(    include /etc/nginx/snippets/yunshu-newapi-console.conf;\)$|\1\n    include /etc/nginx/snippets/yunshu-harness-console.conf;|" "$SITE"
fi

echo
echo "=== 站点里现有的 snippet include："
grep -n "include /etc/nginx/snippets/" "$SITE"
echo
echo "=== 与备份的差异（应该只有一行 include）："
diff "$BACKUP" "$SITE" || true

echo
if nginx -t; then
  systemctl reload nginx
  echo "配置校验通过，已 reload。"
  # reload 返回时新 worker 可能还没接上，自检等一拍，否则会读到旧的 403。
  sleep 3
else
  echo "配置校验不过 —— 已回滚，未 reload。"
  cp -a "$BACKUP" "$SITE"
  rm -f /etc/nginx/conf.d/yunshu-harness-limits.conf /etc/nginx/snippets/yunshu-harness-console.conf
  exit 1
fi

echo
echo "=== 新入口自检："
curl -s  -o /dev/null -w "  127.0.0.1:3010 直连 /api/status  -> %{http_code}\n" --max-time 10 http://127.0.0.1:3010/api/status
curl -sk -o /dev/null -w "  https /harness/api/status        -> %{http_code}\n" --max-time 10 -H "Host: 8.133.202.230" https://127.0.0.1/harness/api/status
curl -sk -o /dev/null -w "  https /harness/ (控制台)         -> %{http_code}\n" --max-time 10 -H "Host: 8.133.202.230" https://127.0.0.1/harness/
echo "=== 既有路由自检（应与部署前一致）："
curl -sk -o /dev/null -w "  https /api/v1/models/catalog     -> %{http_code}\n" --max-time 10 -H "Host: 8.133.202.230" https://127.0.0.1/api/v1/models/catalog
curl -sk -o /dev/null -w "  https /new-api/                  -> %{http_code}\n" --max-time 10 -H "Host: 8.133.202.230" https://127.0.0.1/new-api/
curl -sk -o /dev/null -w "  https / (应为 403)               -> %{http_code}\n" --max-time 10 -H "Host: 8.133.202.230" https://127.0.0.1/
