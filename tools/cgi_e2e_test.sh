#!/bin/bash
# 端到端回归测试：面板 + 飞牛 CGI 同源网关仿真器，验证 index.cgi 网关链路。
# 用法：bash tools/cgi_e2e_test.sh
set -u
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
PY="${PY:-C:/Users/luoxi/.workbuddy/binaries/python/versions/3.13.12/python.exe}"
TMP="$ROOT/.workbuddy/tmp-test"
GW="http://127.0.0.1:19000/cgi/ThirdParty/Mihomo-fpk/index.cgi"
DIRECT="http://127.0.0.1:9788"

# 注意：--noproxy 的值必须由 shell 正确传参，所以用函数而不是字符串变量。
# 超时给到 90s：Git Bash 在 Windows 上每次拉起外部命令要 0.3~1.5s，
# CGI 脚本里若干次进程创建会被放大；真机 Linux 上是毫秒级。
c() { curl -sS -m 90 --noproxy '*' "$@"; }

cleanup() {
  [ -n "${PANEL_PID:-}" ] && kill "$PANEL_PID" 2>/dev/null
  [ -n "${SIM_PID:-}" ] && kill "$SIM_PID" 2>/dev/null
  wait 2>/dev/null
  return 0
}
trap cleanup EXIT

rm -rf "$TMP"; mkdir -p "$TMP/etc" "$TMP/var"

"$ROOT/src/panel/panel.exe" \
  -appdest "$ROOT/app" -etc "$TMP/etc" -var "$TMP/var" \
  -listen "127.0.0.1:9788" >"$TMP/panel.log" 2>&1 &
PANEL_PID=$!

"$PY" "$ROOT/tools/cgi_gateway_sim.py" >"$TMP/sim.log" 2>&1 &
SIM_PID=$!

ok=0
for _ in $(seq 1 40); do
  if c -o /dev/null "$DIRECT/api/health" 2>/dev/null; then ok=1; break; fi
  sleep 0.3
done
[ "$ok" = 1 ] || { echo "面板未能启动，日志："; cat "$TMP/panel.log"; exit 1; }
sleep 0.5

pass=0; fail=0
check() { # 名称 期望子串 实际
  if printf '%s' "$3" | grep -qF -- "$2"; then
    echo "  [PASS] $1"; pass=$((pass+1))
  else
    echo "  [FAIL] $1  期望包含: $2"
    echo "         实际: $(printf '%s' "$3" | head -c 220)"
    fail=$((fail+1))
  fi
}
check_not() { # 名称 禁止子串 实际
  if printf '%s' "$3" | grep -qE -- "$2"; then
    echo "  [FAIL] $1  不应出现: $2"; fail=$((fail+1))
  else
    echo "  [PASS] $1"; pass=$((pass+1))
  fi
}

echo "== 1. 网关根路径（控制台首页）=="
r=$(c -i "$GW/" 2>&1)
check "HTTP 200" "200 OK" "$r"
check "注入 <base> 前缀" "base href=\"/cgi/ThirdParty/Mihomo-fpk/index.cgi/\"" "$r"
check "返回控制台 HTML" "<!DOCTYPE html>" "$r"

echo "== 2. 网关下 API 转发 =="
check "api/health 通" '"ok":true' "$(c "$GW/api/health" 2>&1)"
check "api/status 通" '"panel"' "$(c "$GW/api/status" 2>&1)"

echo "== 3. 网关下 Clash 面板（MetaCubeXD）=="
r=$(c -i "$GW/dashboard/" 2>&1)
check "dashboard 200" "200 OK" "$r"
check "MetacubeXD 页面" "metacubexd" "$r"
check "config.js 可访问" "__MIHOMO_BASE__" "$(c "$GW/dashboard/config.js" 2>&1)"

echo "== 4. 写方法能穿过网关（不被降级成 404/405）=="
r=$(c -i -X POST -H 'Content-Type: application/json' -d '{"url":"","mode":"nodes"}' "$GW/api/subs" 2>&1)
check_not "POST 未被 404/405 拒绝" "^HTTP/1.1 (404|405)" "$r"

echo "== 5. 二进制资源经网关不被破坏 =="
d=$(c "$DIRECT/favicon.svg" | sha256sum | cut -d' ' -f1)
v=$(c "$GW/favicon.svg" | sha256sum | cut -d' ' -f1)
if [ -n "$d" ] && [ "$d" = "$v" ]; then
  echo "  [PASS] 字节级一致 ($d)"; pass=$((pass+1))
else
  echo "  [FAIL] 直连=$d 网关=$v"; fail=$((fail+1))
fi

echo
echo "结果：PASS=$pass FAIL=$fail"
[ "$fail" -eq 0 ]
