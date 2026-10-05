#!/bin/bash
# ---------------------------------------------------------------------------
# fnOS CGI 同源代理 —— Mihomo 代理
#
# 作用：把 /cgi/ThirdParty/<appname>/index.cgi/<路径> 原样转发到本机面板
#       (127.0.0.1:9788)，让桌面 iframe 与飞牛 Web 端「同源」。
#
# 为什么需要它：直接把 iframe 指向 http://NAS:9788 时，如果飞牛桌面本身跑在
#       HTTPS 上，浏览器会以「混合内容 (Mixed Content)」为由直接拦截该 iframe，
#       表现为窗口一片空白。走同源网关后，iframe 继承飞牛页面的协议与登录态，
#       HTTP / HTTPS 下都能正常内嵌，也不会再新开标签页。
#
# 已知限制：CGI 是「一来一回」模型，无法承载 WebSocket。因此 MetaCubeXD 的
#       实时流量 / 实时日志流式推送不生效（节点切换、订阅管理等功能不受影响）。
#       需要完整实时图表时，请使用「直达端口」入口。
#
# 安装注意：fnpack 打包会丢失执行位，安装/升级脚本里会补 chmod 0755。
# 命名注意：飞牛 CGI 网关硬性要求入口可执行文件必须叫 index.cgi，
#       换成其它名字会被网关直接拒绝（报 "cgi executable must be index.cgi"）。
# ---------------------------------------------------------------------------

CGI_NAME="index.cgi"
TARGET="http://127.0.0.1:9788"

METHOD="${REQUEST_METHOD:-GET}"
URI="${REQUEST_URI:-}"

# 拆出对外前缀（回写给面板，用于注入 <base>）与需要转发的路径。
# /cgi/ThirdParty/Mihomo-fpk/index.cgi/api/status?x=1
#   prefix -> /cgi/ThirdParty/Mihomo-fpk/index.cgi
#   after  -> /api/status?x=1
if [ -n "$URI" ] && [ "${URI#*$CGI_NAME}" != "$URI" ]; then
    prefix="${URI%%$CGI_NAME*}$CGI_NAME"
    after="${URI#*$CGI_NAME}"
else
    prefix=""
    after="${PATH_INFO:-/}"
fi

[ -n "$after" ] || after="/"
case "$after" in
/*) ;;
*) after="/$after" ;;
esac

target_url="$TARGET$after"

hdr_file=$(mktemp 2>/dev/null) || hdr_file=""
body_file=$(mktemp 2>/dev/null) || body_file=""
[ -n "$hdr_file" ] || hdr_file="/tmp/mihomo-cgi-h.$$"
[ -n "$body_file" ] || body_file="/tmp/mihomo-cgi-b.$$"

cleanup() { rm -f "$hdr_file" "$body_file" 2>/dev/null; }
trap cleanup EXIT

# --noproxy '*'：目标固定是本机回环地址，强制绕过 http_proxy/https_proxy，
#   否则 NAS 上若配了系统级代理，curl 会去连代理而不是本地面板。
args=(-sS --http1.1 --noproxy '*' -X "$METHOD" -D "$hdr_file" -o "$body_file" -H "Expect:")
args+=(-H "X-Forwarded-Prefix: $prefix")

[ -n "${HTTP_COOKIE:-}" ] && args+=(-H "Cookie: $HTTP_COOKIE")
[ -n "${HTTP_ACCEPT:-}" ] && args+=(-H "Accept: $HTTP_ACCEPT")
[ -n "${HTTP_AUTHORIZATION:-}" ] && args+=(-H "Authorization: $HTTP_AUTHORIZATION")
[ -n "${HTTP_RANGE:-}" ] && args+=(-H "Range: $HTTP_RANGE")
[ -n "${HTTP_IF_NONE_MATCH:-}" ] && args+=(-H "If-None-Match: $HTTP_IF_NONE_MATCH")
[ -n "${HTTP_IF_MODIFIED_SINCE:-}" ] && args+=(-H "If-Modified-Since: $HTTP_IF_MODIFIED_SINCE")
[ -n "${HTTP_ORIGIN:-}" ] && args+=(-H "Origin: $HTTP_ORIGIN")
[ -n "${HTTP_REFERER:-}" ] && args+=(-H "Referer: $HTTP_REFERER")
[ -n "${HTTP_ACCEPT_LANGUAGE:-}" ] && args+=(-H "Accept-Language: $HTTP_ACCEPT_LANGUAGE")

has_body=0
if [ -n "${CONTENT_LENGTH:-}" ] && [ "$CONTENT_LENGTH" != "0" ]; then
    has_body=1
fi

if [ "$has_body" -eq 1 ]; then
    args+=(-H "Content-Type: ${CONTENT_TYPE:-application/octet-stream}")
    curl "${args[@]}" --data-binary @- "$target_url" >/dev/null 2>&1
else
    curl "${args[@]}" "$target_url" >/dev/null 2>&1
fi
curl_rc=$?

# 面板没起来时给出可读提示，而不是让用户对着空白窗口发呆
if [ "$curl_rc" -ne 0 ] || [ ! -s "$hdr_file" ]; then
    printf 'Status: 502 Bad Gateway\r\n'
    printf 'Content-Type: text/html; charset=utf-8\r\n'
    printf 'Cache-Control: no-store\r\n'
    printf '\r\n'
    printf '<!doctype html><html lang="zh-CN"><head><meta charset="utf-8">'
    printf '<title>Mihomo 代理</title></head>'
    printf '<body style="font-family:-apple-system,Segoe UI,sans-serif;padding:2.5em;color:#334155;line-height:1.7">'
    printf '<h2 style="margin:0 0 .6em">无法连接到 Mihomo 面板</h2>'
    printf '<p>本机 9788 端口没有响应。请回到飞牛应用中心，确认「Mihomo 代理」处于运行状态后刷新本窗口。</p>'
    printf '</body></html>'
    exit 0
fi

# 转成标准 CGI 输出：Status 行 + 过滤逐跳头 + 空行 + 原始字节体。
# 注意 body 必须用 cat 原样输出，不能经过任何文本处理，否则会破坏二进制资源。
# 这里刻意用 bash 内建 + 单个 awk，避免为每个头字段拉起一串外部进程。
IFS= read -r status_line <"$hdr_file" 2>/dev/null
status_line="${status_line%$'\r'}"
rest="${status_line#* }"
code="${rest%% *}"
if [ "$rest" = "$code" ]; then reason=""; else reason="${rest#* }"; fi
case "$code" in
'' | *[!0-9]*) code=502; reason="Bad Gateway" ;;
esac
[ -n "$reason" ] || reason="OK"

printf 'Status: %s %s\r\n' "$code" "$reason"
awk 'NR == 1 { next }
     { sub(/\r$/, "") }
     /^(transfer-encoding|connection|keep-alive|status):/ { next }
     $0 == "" { next }
     { printf "%s\r\n", $0 }' "$hdr_file"
printf '\r\n'
cat "$body_file"

exit 0
