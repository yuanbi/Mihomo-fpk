#!/usr/bin/env python3
"""打包产物自检：核对 fpk 里的 manifest、文件清单、脚本权限与版本一致性。

用法：python tools/verify_fpk.py [fpk 路径]
"""
import io
import os
import re
import sys
import tarfile

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
FPK = sys.argv[1] if len(sys.argv) > 1 else os.path.join(ROOT, "Mihomo-fpk.fpk")

# 这些目录/文件只用于开发，绝不能被打进 fpk
FORBIDDEN = ("src/", "tools/", ".workbuddy/", "fnpack.exe", "panel.exe", ".git/")

problems = []
notes = []


def fail(msg):
    problems.append(msg)
    print("  ✗ " + msg)


def ok(msg):
    print("  ✓ " + msg)


print("检查 %s (%.1f MB)" % (FPK, os.path.getsize(FPK) / 1048576.0))

with tarfile.open(FPK, "r:gz") as tf:
    names = tf.getnames()

    # 1. 顶层结构
    print("\n[1] 顶层结构")
    for expect in ("manifest", "app.tgz", "ICON.PNG", "ICON_256.PNG"):
        if expect in names:
            ok("存在 %s" % expect)
        else:
            fail("缺少 %s" % expect)
    for d in ("cmd/", "config/", "wizard/"):
        if any(n.startswith(d) for n in names):
            ok("存在 %s" % d)
        else:
            fail("缺少 %s" % d)

    # 2. 开发目录泄漏
    print("\n[2] 是否误打包开发文件")
    leaked = [n for n in names if any(n.startswith(p) or n == p for p in FORBIDDEN)]
    if leaked:
        for n in leaked[:10]:
            fail("不应打包：%s" % n)
    else:
        ok("没有泄漏 src/ tools/ .workbuddy/ 等开发目录")

    # 3. manifest
    print("\n[3] manifest")
    manifest = tf.extractfile("manifest").read().decode("utf-8")
    fields = {}
    for line in manifest.splitlines():
        if "=" in line:
            k, v = line.split("=", 1)
            fields[k.strip()] = v.strip()

    version = fields.get("version", "")
    ok("version = %s" % version)
    if not re.match(r"^\d+\.\d+\.\d+$", version):
        fail("version 格式异常")
    for k in ("appname", "display_name", "platform", "source",
              "desktop_uidir", "desktop_applaunchname", "service_port", "changelog"):
        if k not in fields:
            fail("manifest 缺少字段 %s" % k)
    if fields.get("maintainer_url") != "https://github.com/yuanbi":
        fail("maintainer_url 不是 https://github.com/yuanbi：%s" % fields.get("maintainer_url"))
    else:
        ok("maintainer_url = https://github.com/yuanbi")
    if not fields.get("changelog", "").startswith(version):
        fail("changelog 没有以当前版本号开头")
    else:
        ok("changelog 以 %s 开头" % version)

    # 4. app.tgz 内容
    print("\n[4] app.tgz")
    appfile = tf.extractfile("app.tgz")
    app = tarfile.open(fileobj=io.BytesIO(appfile.read()), mode="r:gz")
    anames = app.getnames()

    for expect in ("bin/mihomo", "bin/mihomo-panel", "ui/config", "ui/index.cgi",
                   "panel/index.html", "panel/app.js", "dashboard/index.html"):
        if expect in anames:
            ok("存在 %s" % expect)
        else:
            fail("app.tgz 缺少 %s" % expect)

    if any("proxy.cgi" in n for n in anames):
        fail("app.tgz 里仍有 proxy.cgi（网关只认 index.cgi）")
    else:
        ok("没有残留 proxy.cgi")

    # 5. ui/config 与 manifest 一致性
    print("\n[5] 桌面入口与 manifest 一致性")
    uicfg = app.extractfile("ui/config").read().decode("utf-8")
    launch = fields.get("desktop_applaunchname", "")
    if '"%s"' % launch in uicfg:
        ok("desktop_applaunchname=%s 在 ui/config 中存在" % launch)
    else:
        fail("ui/config 里找不到 %s" % launch)
    if "/cgi/ThirdParty/%s/index.cgi" % fields.get("appname") in uicfg:
        ok("网关入口指向 index.cgi")
    else:
        fail("ui/config 未使用 index.cgi 网关入口")

    # 6. 二进制版本与 ELF
    print("\n[6] 面板二进制")
    binfo = app.getmember("bin/mihomo-panel")
    data = app.extractfile("bin/mihomo-panel").read()
    if data[:4] == b"\x7fELF" and data[18] == 62:
        ok("是 x86-64 ELF（%.1f MB）" % (len(data) / 1048576.0))
    else:
        fail("bin/mihomo-panel 不是 x86-64 ELF")
    notes.append("app.tgz 内文件权限均为 0o%o（依赖安装脚本补执行位）" % (binfo.mode & 0o777))

    # 7. 生命周期脚本权限声明
    print("\n[7] 生命周期脚本")
    for script in ("cmd/main", "cmd/install_callback", "cmd/upgrade_callback"):
        if script not in names:
            fail("缺少 %s" % script)
            continue
        body = tf.extractfile(script).read().decode("utf-8")
        if "ui/index.cgi" in body:
            ok("%s 补了 index.cgi 执行位" % script)
        else:
            fail("%s 没有 chmod ui/index.cgi" % script)

    cc = tf.extractfile("cmd/config_callback").read().decode("utf-8") \
        if "cmd/config_callback" in names else ""
    if "config.env" in cc:
        ok("config_callback 会写 config.env")
    else:
        fail("config_callback 未处理 config.env")

print()
for n in notes:
    print("· " + n)
if problems:
    print("\n发现 %d 个问题：" % len(problems))
    for p in problems:
        print("  - " + p)
    sys.exit(1)
print("\n全部检查通过。")
