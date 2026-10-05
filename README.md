# Mihomo 代理 · 飞牛 fnOS

> 把 [mihomo](https://github.com/MetaCubeX/mihomo)（Clash.Meta）内核装进飞牛 fnOS 应用中心，
> 自带 Web 控制台与 Clash 面板，桌面图标**直接在飞牛窗口内打开**，不用新开标签页。

![version](https://img.shields.io/badge/version-1.0.3-blue)
![platform](https://img.shields.io/badge/platform-x86__64-lightgrey)
![fnOS](https://img.shields.io/badge/fnOS-%E2%89%A5%201.1.8-green)
![mihomo](https://img.shields.io/badge/mihomo-v1.19.32-orange)
![license](https://img.shields.io/badge/license-MIT-brightgreen)

---

## 简介

这是一个原生服务型 FPK 应用（不是 Docker），把 mihomo 内核作为一个**系统服务**跑在飞牛 NAS 上：

- 内核以 **root** 运行，支持 **TUN 透明代理**，可以让整个局域网设备直接走 NAS 代理
- 应用自带一个**用 Go 从零写的控制台**，负责订阅管理、配置生成、内核守护与升级
- 内置 **MetaCubeXD** 作为进阶面板，适合看实时流量图表与连接明细

面向的典型场景：家里一台 NAS 常开，希望手机 / 电视 / 电脑不装任何客户端就能上网走代理。

## 特性

| 能力 | 说明 |
|---|---|
| **订阅管理** | 支持 **订阅链接**、**NAS 上的订阅文件**、**浏览器上传文件** 三种来源，可随时切换 |
| **两种使用方式** | 「内置规则」只提取节点、套用内置的分流规则；「订阅自带规则」直接用机场的完整配置 |
| **节点切换** | 面板里直接切节点 / 策略组，也可在 MetaCubeXD 里精细调整 |
| **TUN 透明代理** | 一键开启 TUN + DNS 劫持，局域网设备把网关指到 NAS 即可上网 |
| **DNS 自定义** | fake-ip / redir-host、上游 DNS、fallback、国内域名加速，可单独配置 |
| **内核与数据升级** | 面板内检查 / 升级 mihomo 内核，一键更新 GeoIP / GeoSite 数据 |
| **访问密码** | 可选的单密码保护，开启后面板与内核接口都需要登录 |
| **运行日志** | 面板内查看服务与内核日志，排查问题不用 SSH |
| **双面板并存** | 自研控制台（轻量、中文、管订阅）与 MetaCubeXD（图表与连接明细）共用同一个内核接口，两边操作实时同步 |

## 三个桌面入口

安装后飞牛桌面会出现三个图标，**用途不同**：

| 图标 | 打开方式 | 适用场景 |
|---|---|---|
| **Mihomo 代理** | 飞牛窗口内嵌（CGI 同源） | 默认推荐。HTTP / HTTPS 下都能正常显示，且复用飞牛的登录鉴权 |
| **Clash 面板** | 飞牛窗口内嵌（CGI 同源） | MetaCubeXD 的完整界面，同样内嵌打开 |
| **Mihomo 代理（直达端口）** | 飞牛窗口内嵌，直连 9788 | 图表与 WebSocket 实时数据最完整；**若飞牛桌面跑在 HTTPS 上会被浏览器拦截而空白** |

前两个入口走飞牛自带的 **CGI 同源网关** `/cgi/ThirdParty/Mihomo-fpk/index.cgi/`，与飞牛桌面同源，
因此不受 HTTPS 混合内容限制；代价是网关不支持 WebSocket，所以实时图表要用第三个入口看。

## 端口一览

| 端口 | 用途 | 监听 |
|---|---|---|
| **9788** | 控制台 Web 服务（`manifest` 里的 `service_port`） | `0.0.0.0` |
| **9790** | mihomo `external-controller`（面板与 MetaCubeXD 调用） | 仅 `127.0.0.1` |
| **7890** | mixed 代理端口（HTTP / SOCKS 混合入口） | 跟随内核配置 |

9788 与 9790 均可在面板「设置」里修改。

## 安装

### 方式一：下载现成的 fpk（推荐）

1. 到本仓库的 **Releases** 页面下载 `Mihomo-fpk.fpk`
2. 飞牛「应用中心」→ 右上角「手动安装」→ 选择该文件
3. 安装向导里填订阅来源（链接或 NAS 文件路径）、使用方式、代理端口
4. 安装完成后**重启一次应用中心**，让桌面图标重读配置：

   ```bash
   sudo systemctl restart trim_app_center.service
   ```

> 没有订阅也能装。跳过向导里的订阅，装完后在控制台「订阅」页再添加即可。

### 方式二：自己打包

见下文「从源码构建」。

## 使用

1. 点桌面图标打开控制台，先到 **订阅** 页确认订阅状态（节点数、流量、到期时间）
2. **概览** 页可开关代理内核、查看当前入口地址；局域网设备把网关 / DNS 指向 NAS IP 即可上网
3. 需要 TUN 透明代理时，到 **设置 → TUN 透明代理** 打开开关并保存
4. 想看实时流量图表，用 **Mihomo 代理（直达端口）** 那个图标，或直接访问 `http://<NAS-IP>:9788`

### 订阅来源怎么选

- **订阅链接**：填机场给的 `http(s)://` 地址
- **NAS 上的订阅文件**：把 yaml 文件放 NAS 上，填**绝对路径**（文件管理 → 右键 → 详细信息 → 复制原始路径）
- **浏览器上传**：仅在控制台「订阅」页可用，直接从电脑选文件

> 注意：飞牛的**安装向导只支持 7 种字段类型**（`text` / `password` / `radio` / `checkbox` / `select` / `switch` / `tips`），
> 没有文件上传也没有多行文本框。所以安装阶段的「订阅文件」只能填路径，真正的上传要在控制台里做。

## 从源码构建

**环境要求**：Go **1.26+**（`go.mod` 声明 `go 1.26.0`，本项目在 go1.27.1 上构建）、
Python 3、Node.js（仅用于前端语法自检）、目标平台 `linux/amd64`

```bash
# 1. 编译控制台（双平台：Windows 版仅用于本机联调，Linux 版才是要打包的）
cd src/panel
export GOPROXY=https://goproxy.cn,direct          # 国内直连 proxy.golang.org 容易失败
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o panel.exe .
GOOS=linux   GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o ../../app/bin/mihomo-panel .

# 2. 打包
cd ../..
./fnpack.exe build          # 产出 Mihomo-fpk.fpk
```

> 注意：`app/bin/mihomo` 约 **62 MB**（官方未修改的二进制），`app/geo/` 约 **20 MB**。
> 首次 `git push` 时 GitHub 会对超过 50 MB 的单个文件给出警告（硬上限 100 MB）。
> 如果不希望仓库这么大，可以把这两个目录改成 **Git LFS**，或者写一个脚本在打包前按需下载。

### 自检工具

仓库里的 `tools/` 都是**不需要真机**的验证脚本，改完代码建议跑一遍：

```bash
bash tools/cgi_e2e_test.sh                                  # 起本地面板 + 复刻飞牛 CGI 网关，10 项断言
python tools/sub_source_test.py                             # 订阅三种来源全链路，42 项断言
python tools/verify_fpk.py                                  # 解包 fpk 自检（源码泄漏 / 残留文件 / 二进制一致性）
node tools/check_panel_ids.js                               # 前端 getElementById 的 id 是否都存在于 HTML
python tools/make_icons.py                                  # 重新生成桌面图标
```

`cgi_gateway_sim.py` 是一个最小的 HTTP→CGI 适配器，复刻了飞牛 `/cgi/ThirdParty/<appname>/index.cgi/<剩余路径>`
的路由与 CGI 环境变量，用来在开发机上端到端验证内嵌链路。

## 项目结构

```
.
├── manifest                  # FPK 清单：应用名 / 版本 / 端口 / 权限 / 更新日志
├── fnpack.exe                # 飞牛官方打包工具
├── app/                      # 会被打进 app.tgz 的运行时目录
│   ├── bin/
│   │   ├── mihomo            # mihomo 内核（第三方二进制，见「来源标注」）
│   │   └── mihomo-panel      # 本项目的 Go 控制台
│   ├── dashboard/            # MetaCubeXD 构建产物（第三方，见「来源标注」）
│   ├── geo/                  # GeoIP.dat / GeoSite.dat（第三方数据集）
│   ├── panel/                # 自研控制台前端（原生 HTML/CSS/JS，无构建步骤）
│   └── ui/
│       ├── config            # 桌面图标定义（三个入口）
│       ├── index.cgi         # CGI 同源网关，转发到 127.0.0.1:9788
│       └── images/           # 图标
├── cmd/                      # 生命周期脚本：install / upgrade / config / uninstall / main
├── config/                   # privilege（run-as root）、resource
├── wizard/                   # 安装向导 / 应用设置 / 卸载向导（JSON）
├── src/panel/                # Go 控制台源码（11 个 .go 文件，仅依赖 yaml.v3）
└── tools/                    # 开发期自检与仿真脚本
```

## 常见问题

**Q：桌面图标点开是一片空白**

多半是 CGI 网关没跑起来。飞牛硬性要求网关入口脚本的文件名必须是 `index.cgi`，其它名字会被直接拒绝执行。
确认 `app/ui/index.cgi` 存在且可执行（`fnpack` 打包会丢失执行位，安装/升级脚本里有 `chmod 0755` 兜底）。

**Q：装了新版本，桌面图标还是老的**

重启一次应用中心：`sudo systemctl restart trim_app_center.service`。

**Q：三个入口里「直达端口」那个是空白的**

飞牛桌面跑在 HTTPS 上时，iframe 里加载 `http://<NAS-IP>:9788` 会被浏览器按混合内容拦截。
把该入口的 `type` 改成 `"url"` 即可（会新开标签页），或者改用前两个同源入口。

**Q：面板改过的设置被「应用设置」保存后复原了**

应用设置的表单**每次保存都会提交全部字段**。所以端口这类字段留空表示「保持当前值」，
后端会先判 keep 再决定是否赋值——不要给它写死默认值。

**Q：订阅文件填了路径但提示不存在**

路径必须是 **NAS 上的绝对路径**（不是飞牛文件管理器里看到的虚拟路径），
且要保证应用（以 root 运行）能读到。可以在控制台「订阅」页重新指定，或改用上传方式。

## 第三方组件与来源标注

本项目是**自研控制台 + 开源内核 / 面板 / 数据集**的组合。下面标注每一部分的来源、用途与许可，
便于分清哪些是原创、哪些来自上游。

### 参与本项目的上游项目

| 组件 | 来源项目 | 在本项目中的用途 | 许可 | 是否修改 |
|---|---|---|---|---|
| **mihomo 内核** | [MetaCubeX/mihomo](https://github.com/MetaCubeX/mihomo) `v1.19.32` | 代理内核本体，以官方 `linux-amd64` 二进制形式内置（`app/bin/mihomo`） | MIT | 未修改二进制 |
| **MetaCubeXD 面板** | [MetaCubeX/metacubexd](https://github.com/MetaCubeX/metacubexd) | 进阶 Web 面板（`app/dashboard/`），用其构建产物 | MIT | **有修改**：后端默认地址改为同源、补同源路由、`config.js` 适配反代前缀 |
| **GeoIP / GeoSite 数据** | [MetaCubeX/meta-rules-dat](https://github.com/MetaCubeX/meta-rules-dat) | 分流用的地理位置与域名数据集（`app/geo/*.dat`） | GPL-3.0 | 未修改数据文件 |
| **参考实现** | [nelvko/clash-for-linux-install](https://github.com/nelvko/clash-for-linux-install) | **思路参考**：订阅解析（Clash YAML / base64 链接）、配置生成与 systemd 守护的组织方式 | MIT | 未复制代码，仅参考设计 |
| **FPK 结构参考** | [tnnevol/fn-os-apps](https://github.com/tnnevol/fn-os-apps) | **结构参考**：FPK 目录约定、manifest 字段、生命周期脚本写法 | AGPL-3.0 | 未复制代码，仅参考结构 |
| **YAML 解析库** | [go-yaml/yaml](https://github.com/go-yaml/yaml)（`gopkg.in/yaml.v3`） | 控制台唯一的外部 Go 依赖 | MIT / Apache-2.0 双许可 | 未修改 |
| **打包工具** | 飞牛官方 `fnpack`（仓库内 `fnpack.exe`） | 把目录打成 `.fpk` | 飞牛官方工具 | 未修改 |
| **平台与文档** | [飞牛 fnOS 开发者文档](https://developer.fnnas.com/docs/guide/) / [fnOS 应用文档](https://fnapps-doc.tnnevol.cn/development/quick-start) | FPK 规范、向导（wizard）字段、CGI 网关与桌面入口规则的依据 | — | — |

### 本项目原创部分

以下内容为本项目自行编写，未基于上述项目派生：

- `src/panel/` —— Go 控制台后端（路由、鉴权、订阅解析、配置生成、内核守护与升级、反向代理）
- `app/panel/` —— 控制台前端（原生 HTML / CSS / JS，零构建、零框架）
- `app/ui/index.cgi` —— CGI 同源网关脚本（二进制安全转发，绕开 HTTPS 混合内容拦截）
- `cmd/` —— 全部生命周期脚本（安装 / 升级 / 配置 / 卸载 / 服务主控）
- `wizard/` —— 安装向导与应用设置的表单定义
- `config/` —— 权限与资源声明
- `tools/` —— 全部自检、仿真与测试脚本

### 关于内置的第三方二进制

`app/bin/mihomo` 是上游**未修改的官方发行二进制**，`app/geo/*.dat` 与 `app/dashboard/**` 是上游的
**数据文件与构建产物**。它们与自研代码在同一个包里分发，但彼此相互独立、各自遵循自身许可。
如果你要二次分发本应用，请一并遵守上述各上游项目的许可条款（尤其是
[GPL-3.0 的 GeoIP / GeoSite 数据集](https://github.com/MetaCubeX/meta-rules-dat)）。

## 许可证

本仓库的**原创代码**（上节列出的全部自研部分）采用 **MIT License**。

内置的第三方组件版权归各上游项目所有，遵循其各自的许可，详见上表。

## 致谢

感谢 [MetaCubeX](https://github.com/MetaCubeX) 维护 mihomo、MetaCubeXD 与 meta-rules-dat 这些
高质量的开源项目；感谢 [nelvko/clash-for-linux-install](https://github.com/nelvko/clash-for-linux-install)
与 [tnnevol/fn-os-apps](https://github.com/tnnevol/fn-os-apps) 提供的实践参考；感谢飞牛官方完整清晰的
开发者文档，让 FPK 打包这件事没有太多黑盒。

维护者：[yuanbi](https://github.com/yuanbi)
