#!/usr/bin/env python3
"""飞牛 CGI 同源网关的本地仿真器。

真实环境里 fnOS 的 nginx 会把 /cgi/ThirdParty/<appname>/index.cgi/<剩余路径>
交给 CGI 执行。这里用最小的 HTTP -> CGI 适配器复刻那一层，用来在开发机上
端到端验证 app/ui/index.cgi，无需真机。

注意：飞牛网关硬性要求入口可执行文件名为 index.cgi。
"""
import http.server
import os
import socketserver
import subprocess
import sys
import urllib.parse

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(HERE)  # 仓库根目录
SCRIPT = os.path.join(ROOT, "app", "ui", "index.cgi")
PREFIX = "/cgi/ThirdParty/Mihomo-fpk/index.cgi"
PORT = 19000

# 飞牛网关的硬性规则：入口 CGI 的名字必须是 index.cgi，否则直接拒绝执行。
# 这里一并复刻，免得本地测通了、上机才炸。
if os.path.basename(SCRIPT) != "index.cgi":
    sys.exit("fatal: cgi executable must be index.cgi (got %s)" % os.path.basename(SCRIPT))


class Handler(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def _run(self):
        url = urllib.parse.urlsplit(self.path)
        if not url.path.startswith(PREFIX):
            self.send_error(404)
            return
        rest = url.path[len(PREFIX):] or "/"

        length = int(self.headers.get("Content-Length") or 0)
        body = self.rfile.read(length) if length else b""

        env = dict(os.environ)
        env.update({
            "REQUEST_METHOD": self.command,
            "REQUEST_URI": self.path,
            "PATH_INFO": rest,
            "QUERY_STRING": url.query,
            "SCRIPT_FILENAME": SCRIPT,
            "SCRIPT_NAME": PREFIX,
            "SERVER_PROTOCOL": "HTTP/1.1",
            "GATEWAY_INTERFACE": "CGI/1.1",
            "REMOTE_ADDR": self.client_address[0],
            "CONTENT_LENGTH": str(length),
        })
        if self.headers.get("Content-Type"):
            env["CONTENT_TYPE"] = self.headers["Content-Type"]
        for k, v in self.headers.items():
            key = "HTTP_" + k.upper().replace("-", "_")
            if key in ("HTTP_CONTENT_LENGTH", "HTTP_CONTENT_TYPE"):
                continue
            env[key] = v

        p = subprocess.run(["bash", SCRIPT], input=body, env=env,
                           stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        out = p.stdout
        sep = out.find(b"\r\n\r\n")
        if sep < 0:
            self.send_error(502, "bad CGI output")
            sys.stderr.write(p.stderr.decode("utf-8", "replace"))
            return
        head = out[:sep].decode("utf-8", "replace")
        payload = out[sep + 4:]

        status, reason, hdrs = 200, "OK", []
        for i, line in enumerate(head.split("\r\n")):
            if not line:
                continue
            if i == 0 and line.lower().startswith("status:"):
                parts = line.split(None, 2)
                status = int(parts[1])
                reason = parts[2] if len(parts) > 2 else "OK"
                continue
            if ":" in line:
                k, v = line.split(":", 1)
                hdrs.append((k.strip(), v.strip()))

        # send_response_only：不要额外塞 Server / Date 头，真实网关只会
        # 原样透传 CGI 自己输出的头，仿真器也应如此。
        self.send_response_only(status, reason)
        for k, v in hdrs:
            self.send_header(k, v)
        self.send_header("Content-Length", str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)

    do_GET = _run
    do_HEAD = _run
    do_POST = _run
    do_PUT = _run
    do_PATCH = _run
    do_DELETE = _run

    def log_message(self, *args):
        pass


socketserver.ThreadingTCPServer.allow_reuse_address = True
with socketserver.ThreadingTCPServer(("127.0.0.1", PORT), Handler) as srv:
    srv.serve_forever()
