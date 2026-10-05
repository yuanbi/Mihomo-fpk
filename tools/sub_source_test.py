#!/usr/bin/env python3
"""订阅来源功能测试（链接 / NAS 文件路径 / 上传文件 / 向导导入）。

在开发机上拉起 Windows 版面板 + 一个本地 HTTP 订阅服务，端到端验证：
  1. install.env 导入「NAS 文件」来源
  2. 通过 HTTP 链接添加订阅
  3. multipart 上传订阅文件
  4. 上传内容可被替换（PUT multipart）
  5. 来源可在 链接 <-> 文件 之间切换
  6. 「完整配置」模式正确合并订阅自带规则
  7. 应用设置（config.env）能改订阅，且 keep 时不误改
用法：python tools/sub_source_test.py
"""
import base64
import io
import json
import os
import shutil
import subprocess
import sys
import threading
import time
import urllib.error
import urllib.request
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
PANEL = os.path.join(ROOT, "src", "panel", "panel.exe")
TMP = os.path.join(ROOT, ".workbuddy", "tmp-subtest")
ETC = os.path.join(TMP, "etc")
VAR = os.path.join(TMP, "var")
WORK = os.path.join(TMP, "files")          # 模拟 NAS 上的用户目录
PANEL_PORT = 19788
SUB_PORT = 19789
BASE = "http://127.0.0.1:%d" % PANEL_PORT

PASS, FAIL = 0, 0


def check(name, cond, detail=""):
    global PASS, FAIL
    if cond:
        PASS += 1
        print("  [PASS] %s" % name)
    else:
        FAIL += 1
        print("  [FAIL] %s %s" % (name, detail))


# --------------------------------------------------------------------------
# 测试用的订阅素材
# --------------------------------------------------------------------------

def yaml_nodes(names, extra=""):
    lines = ["proxies:"]
    for i, n in enumerate(names):
        lines += [
            "  - name: %s" % n,
            "    type: ss",
            "    server: 10.0.%d.%d" % (i // 250, i % 250 + 1),
            "    port: 8388",
            "    cipher: aes-128-gcm",
            "    password: testpass",
        ]
    return "\n".join(lines) + "\n" + extra


def yaml_full():
    return (
        "mixed-port: 7899\n"
        "external-controller: 0.0.0.0:9099\n"
        "secret: remote-secret\n"
        "proxies:\n"
        "  - name: 完整模式节点A\n"
        "    type: ss\n"
        "    server: 10.9.9.1\n"
        "    port: 8388\n"
        "    cipher: aes-128-gcm\n"
        "    password: testpass\n"
        "  - name: 完整模式节点B\n"
        "    type: ss\n"
        "    server: 10.9.9.2\n"
        "    port: 8388\n"
        "    cipher: aes-128-gcm\n"
        "    password: testpass\n"
        "proxy-groups:\n"
        "  - name: 远程策略组\n"
        "    type: select\n"
        "    proxies:\n"
        "      - 完整模式节点A\n"
        "      - 完整模式节点B\n"
        "rules:\n"
        "  - DOMAIN-SUFFIX,example.org,远程策略组\n"
        "  - MATCH,远程策略组\n"
    )


def link_list_b64(n):
    """生成 base64 的分享链接列表（模拟常见机场订阅）。"""
    lines = []
    for i in range(n):
        lines.append("ss://YWVzLTEyOC1nY206dGVzdHBhc3M=@10.8.0.%d:8388#链接节点%d" % (i + 1, i + 1))
    return base64.b64encode("\n".join(lines).encode()).decode()


# --------------------------------------------------------------------------
# HTTP 工具
# --------------------------------------------------------------------------

def req(method, path, body=None, headers=None, raw=False):
    url = path if path.startswith("http") else BASE + path
    data = None
    hdrs = dict(headers or {})
    if body is not None and not raw:
        data = json.dumps(body).encode()
        hdrs.setdefault("Content-Type", "application/json")
    elif raw:
        data = body
    r = urllib.request.Request(url, data=data, method=method, headers=hdrs)
    try:
        with urllib.request.urlopen(r, timeout=120) as resp:
            payload = resp.read()
            status = resp.status
    except urllib.error.HTTPError as e:
        payload = e.read()
        status = e.code
    try:
        return status, json.loads(payload.decode("utf-8"))
    except Exception:
        return status, {"_raw": payload.decode("utf-8", "replace")}


def multipart(fields, filename, content):
    boundary = "----mihomotest" + uuid.uuid4().hex
    buf = io.BytesIO()
    for k, v in fields.items():
        buf.write(("--%s\r\n" % boundary).encode())
        buf.write(('Content-Disposition: form-data; name="%s"\r\n\r\n' % k).encode())
        buf.write(str(v).encode())
        buf.write(b"\r\n")
    buf.write(("--%s\r\n" % boundary).encode())
    buf.write(('Content-Disposition: form-data; name="file"; filename="%s"\r\n' % filename).encode())
    buf.write(b"Content-Type: application/octet-stream\r\n\r\n")
    buf.write(content if isinstance(content, bytes) else content.encode())
    buf.write(b"\r\n")
    buf.write(("--%s--\r\n" % boundary).encode())
    return buf.getvalue(), "multipart/form-data; boundary=%s" % boundary


def wait_health(timeout=60):
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            with urllib.request.urlopen(BASE + "/api/health", timeout=5) as r:
                if r.status == 200:
                    return True
        except Exception:
            time.sleep(0.4)
    return False


# --------------------------------------------------------------------------
# 本地订阅服务（模拟机场）
# --------------------------------------------------------------------------

class SubHandler(BaseHTTPRequestHandler):
    payload = b""
    body_bytes = b""

    def do_GET(self):
        if self.path.startswith("/sub.txt"):
            body = self.payload
        else:
            body = self.body_bytes
        self.send_response(200)
        self.send_header("Content-Type", "text/plain; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.send_header("subscription-userinfo",
                         "upload=1111; download=2222; total=10240; expire=1893456000")
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *a):
        pass


def start_sub_server():
    srv = ThreadingHTTPServer(("127.0.0.1", SUB_PORT), SubHandler)
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    return srv


# --------------------------------------------------------------------------
# 面板进程
# --------------------------------------------------------------------------

def panel_env():
    env = dict(os.environ)
    for k in ("HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy", "ALL_PROXY", "all_proxy"):
        env.pop(k, None)
    env["NO_PROXY"] = "127.0.0.1,localhost"
    env["no_proxy"] = "127.0.0.1,localhost"
    return env


def start_panel():
    out = open(os.path.join(TMP, "panel.log"), "ab", buffering=0)
    p = subprocess.Popen(
        [PANEL, "-appdest", os.path.join(ROOT, "app"), "-etc", ETC, "-var", VAR,
         "-listen", "127.0.0.1:%d" % PANEL_PORT],
        stdout=out, stderr=out, env=panel_env(),
    )
    if not wait_health():
        print("面板启动失败，日志：")
        with open(os.path.join(TMP, "panel.log"), encoding="utf-8", errors="replace") as f:
            print(f.read()[-3000:])
        p.kill()
        sys.exit(1)
    return p


def stop_panel(p):
    p.terminate()
    try:
        p.wait(timeout=15)
    except Exception:
        p.kill()
    time.sleep(0.6)


def write_env(name, values):
    lines = ["%s=\"%s\"" % (k, v) for k, v in values.items()]
    with open(os.path.join(ETC, name), "w", encoding="utf-8", newline="\n") as f:
        f.write("\n".join(lines) + "\n")


def subs_by_name(subs, name):
    for s in subs:
        if s["name"] == name:
            return s
    return None


def validate_config(mihomo_bin, workdir):
    """把面板生成的配置交给真实内核做语法+资源检查。"""
    time.sleep(1.5)
    src = os.path.join(ETC, "config.yaml")
    with open(src, encoding="utf-8") as f:
        text = f.read()
    # mihomo 的 -t 会按 -d 指定的目录解析 ./providers/... 相对路径，
    # 所以把配置放到运行目录里再校验，跟 cmd/main 启动内核的方式一致。
    staged = os.path.join(workdir, "config.test.yaml")
    with open(staged, "w", encoding="utf-8", newline="\n") as f:
        f.write(text)
    p = subprocess.run([mihomo_bin, "-t", "-d", workdir, "-f", staged],
                       capture_output=True, timeout=120, env=panel_env())
    out = (p.stdout + p.stderr).decode("utf-8", "replace")
    check("内核校验通过（%s）" % ("完整配置" if "远程策略组" in text else "内置规则"),
          p.returncode == 0, out.strip()[-500:])
    return out


# --------------------------------------------------------------------------

def main():
    global PASS, FAIL
    shutil.rmtree(TMP, ignore_errors=True)
    for d in (ETC, VAR, WORK):
        os.makedirs(d, exist_ok=True)

    nodes_file = os.path.join(WORK, "nas-nodes.yaml")
    with open(nodes_file, "w", encoding="utf-8", newline="\n") as f:
        f.write(yaml_nodes(["NAS节点1", "NAS节点2", "NAS节点3"]))

    full_file = os.path.join(WORK, "nas-full.yaml")
    with open(full_file, "w", encoding="utf-8", newline="\n") as f:
        f.write(yaml_full())

    SubHandler.payload = link_list_b64(4).encode()
    SubHandler.body_bytes = yaml_nodes(["链接节点1", "链接节点2"]).encode()
    srv = start_sub_server()

    # --- 安装向导：用 NAS 文件作为订阅来源 -------------------------------
    write_env("install.env", {
        "SUB_SOURCE": "file", "SUB_PATH": nodes_file,
        "SUB_MODE": "nodes", "MIXED_PORT": "7891",
    })

    print("== 1. 安装向导选择「NAS 文件」来源 ==")
    panel = start_panel()
    time.sleep(3)  # 等首次 UpdateSub 完成
    _, d = req("GET", "/api/subs")
    subs = d.get("subs") or []
    nas = subs_by_name(subs, "默认订阅")
    check("向导创建了订阅", nas is not None, d)
    if nas:
        check("来源为 file", nas.get("source") == "file", nas.get("source"))
        check("路径已记录", nas.get("path") == nodes_file, nas.get("path"))
        check("解析出 3 个节点", nas.get("nodeCount") == 3, nas.get("nodeCount"))
        check("被设为当前订阅", d.get("activeSub") == nas["id"])
    _, st = req("GET", "/api/status")
    check("向导端口 7891 已生效", st["ports"]["mixed"] == 7891,
          st["ports"]["mixed"])
    _, cfg = req("GET", "/api/config")
    text = cfg.get("config", "")
    prov = os.path.join(VAR, "mihomo", "providers", nas["id"] + ".yaml") if nas else ""
    check("配置引用了 provider 文件", nas and (nas["id"] + ".yaml") in text, text[:400])
    check("provider 文件已生成", bool(prov) and os.path.exists(prov))
    if prov and os.path.exists(prov):
        with open(prov, encoding="utf-8") as f:
            check("provider 内含 NAS 节点", "NAS节点1" in f.read())

    print("== 2. 通过 HTTP 链接添加订阅 ==")
    _, d = req("POST", "/api/subs", {
        "name": "链接订阅", "mode": "nodes", "source": "url",
        "url": "http://127.0.0.1:%d/sub.txt" % SUB_PORT,
    })
    check("添加成功", d.get("ok") is True, d)
    link_id = d.get("id")
    _, sd = req("GET", "/api/subs")
    link = subs_by_name(sd.get("subs") or [], "链接订阅")
    check("链接订阅解析出 4 个节点", link and link.get("nodeCount") == 4, link)
    check("流量信息来自响应头", link and link.get("total") == 10240, link)

    print("== 3. 上传订阅文件 ==")
    body, ctype = multipart(
        {"name": "上传订阅", "mode": "nodes", "source": "upload"},
        "my-sub.yaml", yaml_nodes(["上传节点1", "上传节点2", "上传节点3", "上传节点4", "上传节点5"]),
    )
    _, d = req("POST", "/api/subs", body, {"Content-Type": ctype}, raw=True)
    check("上传添加成功", d.get("ok") is True, d)
    up_id = d.get("id")
    _, sd = req("GET", "/api/subs")
    up = subs_by_name(sd.get("subs") or [], "上传订阅")
    check("来源为 upload", up and up.get("source") == "upload", up)
    check("解析出 5 个节点", up and up.get("nodeCount") == 5, up)
    check("上传内容已落盘", os.path.exists(os.path.join(ETC, "subs", up_id + ".upload")))

    print("== 4. 替换已上传的文件（PUT multipart）==")
    body, ctype = multipart(
        {"name": "上传订阅", "mode": "nodes", "source": "upload"},
        "my-sub.yaml", yaml_nodes(["新上传节点1", "新上传节点2"]),
    )
    _, d = req("PUT", "/api/subs/" + up_id, body, {"Content-Type": ctype}, raw=True)
    check("替换成功", d.get("ok") is True, d)
    _, sd = req("GET", "/api/subs")
    up = subs_by_name(sd.get("subs") or [], "上传订阅")
    check("节点数变为 2", up and up.get("nodeCount") == 2, up)

    print("== 5. 来源互相切换（链接 -> NAS 文件）==")
    _, d = req("PUT", "/api/subs/" + link_id, {
        "name": "链接订阅", "mode": "nodes", "source": "file", "path": nodes_file,
    })
    check("切换成功", d.get("ok") is True, d)
    _, sd = req("GET", "/api/subs")
    lk = subs_by_name(sd.get("subs") or [], "链接订阅")
    check("来源变为 file", lk and lk.get("source") == "file", lk)
    check("URL 已清空", lk and not lk.get("url"), lk)
    check("节点数跟到 3", lk and lk.get("nodeCount") == 3, lk)

    print("== 6. 完整配置模式合并订阅自带规则 ==")
    _, d = req("PUT", "/api/subs/" + link_id, {
        "name": "链接订阅", "mode": "full", "source": "file", "path": full_file,
    })
    check("切到完整配置成功", d.get("ok") is True, d)
    req("POST", "/api/subs/%s/activate" % link_id)
    _, cfg = req("GET", "/api/config")
    text = cfg.get("config", "")
    check("保留订阅策略组", "远程策略组" in text, text[:300])
    check("保留订阅规则", "DOMAIN-SUFFIX,example.org" in text)
    check("端口被面板接管", "mixed-port: 7891" in text, text[:400])
    check("密钥被面板接管", "secret: remote-secret" not in text)

    print("== 7. 应用设置（config.env）改为链接来源 ==")
    stop_panel(panel)
    write_env("config.env", {
        "SUB_SOURCE": "url", "SUB_URL": "http://127.0.0.1:%d/sub.txt" % SUB_PORT,
        "SUB_MODE": "nodes", "MIXED_PORT": "",
    })
    panel = start_panel()
    time.sleep(3)
    _, sd = req("GET", "/api/subs")
    subs = sd.get("subs") or []
    check("没有新增重复订阅（仍是 3 条）", len(subs) == 3,
          [s["name"] for s in subs])
    wiz = subs_by_name(subs, "默认订阅")
    check("默认订阅被改为链接来源", wiz and wiz.get("source") == "url", wiz)
    check("节点数跟到 4", wiz and wiz.get("nodeCount") == 4, wiz)
    check("默认订阅成为当前订阅", sd.get("activeSub") == wiz["id"])
    check("config.env 已被消费",
          not os.path.exists(os.path.join(ETC, "config.env")))
    _, st = req("GET", "/api/status")
    check("留空端口时保持原值 7891", st["ports"]["mixed"] == 7891,
          st["ports"]["mixed"])

    print("== 8. 应用设置选 keep 时不误改 ==")
    stop_panel(panel)
    write_env("config.env", {
        "SUB_SOURCE": "keep", "SUB_URL": "http://127.0.0.1:%d/other.yaml" % SUB_PORT,
        "SUB_MODE": "keep", "MIXED_PORT": "",
    })
    panel = start_panel()
    time.sleep(2.5)
    _, sd = req("GET", "/api/subs")
    wiz = subs_by_name(sd.get("subs") or [], "默认订阅")
    check("keep 时来源不变", wiz and wiz.get("source") == "url", wiz)
    check("keep 时链接不变", wiz and wiz.get("url", "").endswith("/sub.txt"), wiz)
    check("keep 时节点数不变", wiz and wiz.get("nodeCount") == 4, wiz)

    print("== 9. 错误输入被拒绝 ==")
    s, d = req("POST", "/api/subs", {"name": "x", "source": "url", "url": "ftp://a/b"})
    check("非 http 链接被拒", s == 400 and d.get("ok") is False, (s, d))
    s, d = req("POST", "/api/subs", {"name": "x", "source": "file", "path": ""})
    check("空路径被拒", s == 400, (s, d))
    s, d = req("POST", "/api/subs", {"name": "x", "source": "file",
                                     "path": "https://example.com/a.yaml"})
    check("文件路径填成网址被拒", s == 400, (s, d))

    print("== 10. 删除订阅会清掉上传的缓存文件 ==")
    req("DELETE", "/api/subs/" + up_id)
    check("上传文件已删除",
          not os.path.exists(os.path.join(ETC, "subs", up_id + ".upload")))

    # --- 用真实内核校验生成的配置 ---------------------------------------
    # 测试用内核刻意放在项目目录之外，避免被 fnpack 打进 fpk。
    mihomo_bin = os.environ.get(
        "MIHOMO_TEST_BIN",
        os.path.join(os.path.dirname(ROOT), ".tools", "mihomo-windows-amd64.exe"))
    if os.path.exists(mihomo_bin):
        print("== 11. 用真实 mihomo 内核校验生成的配置 ==")
        work = os.path.join(VAR, "mihomo")
        # 内置规则模式（引用外部 provider 文件）
        _, sd = req("GET", "/api/subs")
        nas = subs_by_name(sd.get("subs") or [], "默认订阅")
        req("PUT", "/api/subs/" + nas["id"], {
            "name": nas["name"], "mode": "nodes", "source": "file", "path": nodes_file,
        })
        validate_config(mihomo_bin, work)
        # 完整配置模式（订阅自带 rules / proxy-groups）
        req("PUT", "/api/subs/" + nas["id"], {
            "name": nas["name"], "mode": "full", "source": "file", "path": full_file,
        })
        validate_config(mihomo_bin, work)
    else:
        print("== 11. 跳过内核校验（未找到 %s）==" % mihomo_bin)

    stop_panel(panel)
    srv.shutdown()

    print()
    print("结果：PASS=%d FAIL=%d" % (PASS, FAIL))
    return 0 if FAIL == 0 else 1


if __name__ == "__main__":
    sys.exit(main())
