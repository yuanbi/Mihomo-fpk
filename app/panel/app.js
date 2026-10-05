/* Mihomo 代理 · 控制台脚本 */
(function () {
    'use strict';

    var $ = function (id) { return document.getElementById(id); };
    var settingsCache = null;
    var logTimer = null;

    /* ------------------------------------------------------------------ */
    /* helpers                                                             */
    /* ------------------------------------------------------------------ */

    function toast(msg, kind) {
        var el = $('toast');
        el.textContent = msg;
        el.className = 'toast show' + (kind ? ' ' + kind : '');
        clearTimeout(el._t);
        el._t = setTimeout(function () { el.className = 'toast'; }, 3200);
    }

    function api(path, options) {
        options = options || {};
        // FormData 必须原样交给浏览器，让它自己带 multipart 边界，不能 JSON 化。
        var isForm = (typeof FormData !== 'undefined') && (options.body instanceof FormData);
        if (options.body && typeof options.body !== 'string' && !isForm) {
            options.body = JSON.stringify(options.body);
            options.headers = Object.assign({ 'Content-Type': 'application/json' }, options.headers || {});
        }
        return fetch(path, options).then(function (res) {
            return res.json().catch(function () { return {}; }).then(function (data) {
                if (!res.ok || data.ok === false) {
                    throw new Error((data && data.error) || ('请求失败 (' + res.status + ')'));
                }
                return data;
            });
        });
    }

    /* 订阅来源：分段单选 + 按选项显示对应输入行 */
    function currentSource(name) {
        var el = document.querySelector('input[name="' + name + '"]:checked');
        return el ? el.value : 'url';
    }

    function bindSourceSeg(name, rows) {
        var sync = function () {
            var v = currentSource(name);
            Object.keys(rows).forEach(function (k) {
                var el = $(rows[k]);
                if (el) el.hidden = (k !== v);
            });
        };
        Array.prototype.forEach.call(
            document.querySelectorAll('input[name="' + name + '"]'),
            function (r) { r.addEventListener('change', sync); }
        );
        sync();
        return sync;
    }

    function checkRadio(name, value) {
        var el = document.querySelector('input[name="' + name + '"][value="' + value + '"]');
        if (el) el.checked = true;
    }

    function subSourceText(s) {
        var src = s.source || 'url';
        if (src === 'file') return 'NAS 文件 ' + (s.path || '（未填写路径）');
        if (src === 'upload') return '已上传的本地文件';
        return s.url || '';
    }

    function subSourceTag(s) {
        return { url: '链接', file: 'NAS 文件', upload: '本地文件' }[s.source || 'url'] || '链接';
    }

    function buildSubFormData(name, mode, source, file) {
        var fd = new FormData();
        fd.append('name', name);
        fd.append('mode', mode);
        fd.append('source', source);
        fd.append('file', file, file.name);
        return fd;
    }

    function fmtDuration(sec) {
        sec = Number(sec) || 0;
        if (sec <= 0) return '-';
        var d = Math.floor(sec / 86400);
        var h = Math.floor((sec % 86400) / 3600);
        var m = Math.floor((sec % 3600) / 60);
        var s = Math.floor(sec % 60);
        if (d > 0) return d + ' 天 ' + h + ' 小时';
        if (h > 0) return h + ' 小时 ' + m + ' 分';
        if (m > 0) return m + ' 分 ' + s + ' 秒';
        return s + ' 秒';
    }

    function fmtBytes(n) {
        n = Number(n) || 0;
        if (n <= 0) return '0 B';
        var u = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];
        var i = 0;
        while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
        return n.toFixed(n >= 100 || i === 0 ? 0 : 2) + ' ' + u[i];
    }

    function fmtExpire(ts) {
        ts = Number(ts) || 0;
        if (ts <= 0) return '-';
        var d = new Date(ts * 1000);
        var left = Math.floor((d.getTime() - Date.now()) / 86400000);
        var text = d.getFullYear() + '-' + pad(d.getMonth() + 1) + '-' + pad(d.getDate());
        if (left >= 0) return text + '（剩 ' + left + ' 天）';
        return text + '（已过期）';
    }

    function pad(n) { return n < 10 ? '0' + n : '' + n; }

    function esc(s) {
        return String(s == null ? '' : s).replace(/[&<>"']/g, function (c) {
            return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
        });
    }

    function linesToArray(text) {
        return String(text || '').split('\n').map(function (s) { return s.trim(); })
            .filter(function (s) { return s.length > 0; });
    }

    /* ------------------------------------------------------------------ */
    /* status                                                              */
    /* ------------------------------------------------------------------ */

    function refreshStatus() {
        return api('api/status').then(function (d) {
            var k = d.kernel || {};
            var badge = $('kernelBadge');
            if (k.running) {
                badge.textContent = '运行中';
                badge.className = 'badge badge-run';
            } else if (k.desired) {
                badge.textContent = '启动中…';
                badge.className = 'badge badge-idle';
            } else {
                badge.textContent = '已停止';
                badge.className = 'badge badge-stop';
            }

            $('ovKernel').textContent = k.running ? '运行中' : '已停止';
            $('ovPid').textContent = k.pid || '-';
            $('ovVersion').textContent = k.version || '未知';
            $('ovUptime').textContent = k.running ? fmtDuration(k.uptime) : '-';
            $('ovRestarts').textContent = k.restartCount || 0;

            var s = d.settings || {};
            var p = d.ports || {};
            var sys = d.sys || {};
            var lanAddr = (sys.lanIp || '本机IP') + ':' + p.mixed;

            $('ovMixed').textContent = p.mixed;
            $('ovLanAddr').textContent = lanAddr;
            $('ovAllowLan').textContent = s.allowLan ? '已开启' : '仅本机';
            $('ovMode').textContent = { rule: '规则模式', global: '全局模式', direct: '直连模式' }[s.mode] || s.mode;
            $('ovTun').textContent = (s.tun && s.tun.enable) ? '已开启' : '未开启';
            $('footVersion').textContent = 'Mihomo 代理 v' + ((d.panel || {}).version || '');

            var a = d.active || {};
            if (a.configured) {
                $('ovSubName').textContent = a.name || '-';
                $('ovSubNodes').textContent = (a.nodeCount || 0) + ' 个';
                $('ovSubUpdated').textContent = a.updatedAt || '从未更新';
                if (a.total > 0) {
                    $('ovSubTraffic').textContent = fmtBytes((a.upload || 0) + (a.download || 0)) + ' / ' + fmtBytes(a.total);
                } else if ((a.upload || 0) + (a.download || 0) > 0) {
                    $('ovSubTraffic').textContent = fmtBytes((a.upload || 0) + (a.download || 0));
                } else {
                    $('ovSubTraffic').textContent = '-';
                }
                $('ovSubExpire').textContent = fmtExpire(a.expire);
            } else {
                $('ovSubName').textContent = '未配置';
                $('ovSubNodes').textContent = '-';
                $('ovSubUpdated').textContent = '-';
                $('ovSubTraffic').textContent = '-';
                $('ovSubExpire').textContent = '-';
            }

            $('btnStart').disabled = !!k.desired;
            $('btnStop').disabled = !k.desired;
            return d;
        }).catch(function (e) {
            $('kernelBadge').textContent = '无法连接服务';
            $('kernelBadge').className = 'badge badge-stop';
            console.error(e);
        });
    }

    function kernelAction(action) {
        var labels = { start: '启动', stop: '停止', restart: '重启', reload: '重载' };
        return api('api/kernel/' + action, { method: 'POST' }).then(function () {
            toast('已' + (labels[action] || action) + '代理', 'ok');
            setTimeout(refreshStatus, 600);
        }).catch(function (e) { toast(e.message, 'err'); });
    }

    /* ------------------------------------------------------------------ */
    /* subscriptions                                                       */
    /* ------------------------------------------------------------------ */

    function loadSubs() {
        return api('api/subs').then(function (d) {
            renderSubs(d.subs || [], d.activeSub || '');
        }).catch(function (e) { toast(e.message, 'err'); });
    }

    function renderSubs(subs, activeSub) {
        var box = $('subList');
        if (!subs.length) {
            box.innerHTML = '<div class="empty">还没有订阅，请在上方添加。</div>';
            return;
        }
        box.innerHTML = subs.map(function (s) {
            var isActive = s.id === activeSub;
            var traffic = s.total > 0
                ? fmtBytes((s.upload || 0) + (s.download || 0)) + ' / ' + fmtBytes(s.total)
                : ((s.upload || 0) + (s.download || 0) > 0 ? fmtBytes((s.upload || 0) + (s.download || 0)) : '未知');
            return '' +
                '<div class="sub-item' + (isActive ? ' active' : '') + '">' +
                '  <div class="sub-main">' +
                '    <div class="sub-title">' + esc(s.name) +
                (isActive ? '<span class="tag tag-on">使用中</span>' : '') +
                '<span class="tag">' + esc(subSourceTag(s)) + '</span>' +
                '<span class="tag">' + (s.mode === 'full' ? '订阅规则' : '内置规则') + '</span>' +
                '</div>' +
                '    <div class="sub-meta">' + esc(subSourceText(s)) + '</div>' +
                '    <div class="sub-meta">节点 ' + (s.nodeCount || 0) + ' 个 · 流量 ' + traffic +
                ' · 到期 ' + fmtExpire(s.expire) + ' · 更新于 ' + esc(s.updatedAt || '未更新') + '</div>' +
                (s.lastError ? '<div class="sub-error">' + esc(s.lastError) + '</div>' : '') +
                '  </div>' +
                '  <div class="row-actions" style="margin:0">' +
                (isActive ? '' : '<button class="btn btn-sm" data-act="use" data-id="' + s.id + '">启用</button>') +
                '    <button class="btn btn-sm" data-act="update" data-id="' + s.id + '">更新</button>' +
                '    <button class="btn btn-sm btn-danger" data-act="del" data-id="' + s.id + '">删除</button>' +
                '  </div>' +
                '</div>';
        }).join('');
    }

    function bindSubList() {
        $('subList').addEventListener('click', function (ev) {
            var btn = ev.target.closest('button[data-act]');
            if (!btn) return;
            var id = btn.getAttribute('data-id');
            var act = btn.getAttribute('data-act');
            btn.disabled = true;
            var done = function (msg) {
                toast(msg, 'ok');
                loadSubs();
                refreshStatus();
                loadNodes();
            };
            var fail = function (e) { toast(e.message, 'err'); btn.disabled = false; };

            if (act === 'use') {
                api('api/subs/' + id + '/activate', { method: 'POST' }).then(function () { done('已切换到该订阅'); }).catch(fail);
            } else if (act === 'update') {
                toast('正在更新订阅…');
                api('api/subs/' + id + '/update', { method: 'POST' }).then(function () { done('订阅已更新'); }).catch(fail);
            } else if (act === 'del') {
                if (!confirm('确定删除该订阅吗？')) { btn.disabled = false; return; }
                api('api/subs/' + id, { method: 'DELETE' }).then(function () { done('订阅已删除'); }).catch(fail);
            }
        });
    }

    var SYNC_SUB_SOURCE = bindSourceSeg('subSource', {
        url: 'subUrlRow', upload: 'subFileRow', file: 'subPathRow'
    });

    function submitAddSub() {
        var source = currentSource('subSource');
        var name = $('subName').value.trim();
        var mode = $('subMode').value;
        var body;

        if (source === 'url') {
            var url = $('subUrl').value.trim();
            if (!url) { toast('请填写订阅链接', 'err'); return; }
            if (!/^https?:\/\//i.test(url)) { toast('订阅链接必须以 http:// 或 https:// 开头', 'err'); return; }
            body = { name: name, mode: mode, source: 'url', url: url };
        } else if (source === 'file') {
            var p = $('subPath').value.trim();
            if (!p) { toast('请填写 NAS 上的订阅文件路径', 'err'); return; }
            body = { name: name, mode: mode, source: 'file', path: p };
        } else {
            var f = $('subFile').files[0];
            if (!f) { toast('请选择要上传的订阅文件', 'err'); return; }
            body = buildSubFormData(name, mode, 'upload', f);
        }

        toast('正在添加订阅…');
        api('api/subs', { method: 'POST', body: body }).then(function (d) {
            $('subForm').reset();
            checkRadio('subSource', 'url');
            SYNC_SUB_SOURCE();
            if (d.warning) { toast('已添加，但读取订阅失败：' + d.warning, 'err'); } else { toast('订阅添加成功', 'ok'); }
            loadSubs();
            refreshStatus();
            loadNodes();
        }).catch(function (e) { toast(e.message, 'err'); });
    }

    $('subForm').addEventListener('submit', function (ev) {
        ev.preventDefault();
        submitAddSub();
    });

    /* ------------------------------------------------------------------ */
    /* nodes                                                               */
    /* ------------------------------------------------------------------ */

    function loadNodes() {
        return api('api/nodes').then(function (d) {
            var list = d.nodes || [];
            var box = $('nodeList');
            if (!list.length) {
                box.innerHTML = '<div class="empty">暂无节点，请先添加并更新订阅。</div>';
                return;
            }
            box.innerHTML = list.map(function (n) {
                return '<div class="node-item"><span class="nm">' + esc(n.name) +
                    '</span><span class="ty">' + esc(n.type || '') + ' · ' + esc(n.server || '') +
                    (n.port ? ':' + n.port : '') + '</span></div>';
            }).join('');
        }).catch(function (e) { toast(e.message, 'err'); });
    }

    /* ------------------------------------------------------------------ */
    /* settings                                                            */
    /* ------------------------------------------------------------------ */

    function fillSettings(s) {
        settingsCache = s;
        $('setMixedPort').value = s.mixedPort;
        $('setMode').value = s.mode;
        $('setLogLevel').value = s.logLevel;
        $('setAllowLan').checked = !!s.allowLan;
        $('setUnifiedDelay').checked = !!s.unifiedDelay;
        $('setTcpConcurrent').checked = !!s.tcpConcurrent;
        $('setSniffer').checked = !!s.snifferEnable;

        var dns = s.dns || {};
        $('setDnsEnable').checked = !!dns.enable;
        $('setDnsListen').value = dns.listen || '';
        $('setDnsMode').value = dns.enhancedMode || 'fake-ip';
        $('setDnsNameserver').value = (dns.nameserver || []).join('\n');
        $('setDnsFallback').value = (dns.fallback || []).join('\n');

        var tun = s.tun || {};
        $('setTunEnable').checked = !!tun.enable;
        $('setTunStack').value = tun.stack || 'mixed';
        $('setTunDevice').value = tun.device || 'Mihomo';
        $('setTunMtu').value = tun.mtu || 1500;
        $('setTunAutoRoute').checked = !!tun.autoRoute;
        $('setTunAutoRedirect').checked = !!tun.autoRedirect;
        $('setTunHijack').value = (tun.dnsHijack || []).join('\n');

        $('setCustomRules').value = (s.customRules || []).join('\n');
        $('setSecret').value = s.secret || '';
        $('setAccessPassword').value = s.accessPassword || '';
    }

    function loadSettings() {
        return api('api/settings').then(function (d) {
            fillSettings(d.settings || {});
        }).catch(function (e) { toast(e.message, 'err'); });
    }

    $('settingsForm').addEventListener('submit', function (ev) {
        ev.preventDefault();
        var payload = {
            mixedPort: Number($('setMixedPort').value) || 7890,
            mode: $('setMode').value,
            logLevel: $('setLogLevel').value,
            allowLan: $('setAllowLan').checked,
            unifiedDelay: $('setUnifiedDelay').checked,
            tcpConcurrent: $('setTcpConcurrent').checked,
            snifferEnable: $('setSniffer').checked,
            customRules: linesToArray($('setCustomRules').value),
            secret: $('setSecret').value.trim(),
            accessPassword: $('setAccessPassword').value.trim(),
            dns: {
                enable: $('setDnsEnable').checked,
                listen: $('setDnsListen').value.trim() || '0.0.0.0:1053',
                enhancedMode: $('setDnsMode').value,
                nameserver: linesToArray($('setDnsNameserver').value),
                fallback: linesToArray($('setDnsFallback').value),
                ipv6: false,
                fakeIpFilter: (settingsCache && settingsCache.dns && settingsCache.dns.fakeIpFilter) || []
            },
            tun: {
                enable: $('setTunEnable').checked,
                stack: $('setTunStack').value,
                device: $('setTunDevice').value.trim() || 'Mihomo',
                mtu: Number($('setTunMtu').value) || 1500,
                autoRoute: $('setTunAutoRoute').checked,
                autoRedirect: $('setTunAutoRedirect').checked,
                dnsHijack: linesToArray($('setTunHijack').value)
            }
        };
        api('api/settings', { method: 'PUT', body: payload }).then(function (d) {
            if (d.settings) fillSettings(d.settings);
            toast('设置已保存并下发', 'ok');
            refreshStatus();
        }).catch(function (e) { toast(e.message, 'err'); });
    });

    $('btnReloadSettings').addEventListener('click', function () {
        loadSettings().then(function () { toast('已重新载入', 'ok'); });
    });

    /* ------------------------------------------------------------------ */
    /* 设置 → 订阅来源（与安装向导改的是同一条订阅）                        */
    /* ------------------------------------------------------------------ */

    var subsCache = [];
    var SYNC_SET_SUB_SOURCE = bindSourceSeg('setSubSource', {
        url: 'setSubUrlRow', upload: 'setSubFileRow', file: 'setSubPathRow'
    });

    function findSub(id) {
        for (var i = 0; i < subsCache.length; i++) {
            if (subsCache[i].id === id) return subsCache[i];
        }
        return null;
    }

    function fillSubSourceCard(id) {
        var s = findSub(id);
        if (!s) return;
        checkRadio('setSubSource', s.source || 'url');
        $('setSubUrl').value = s.url || '';
        $('setSubPath').value = s.path || '';
        $('setSubFile').value = '';
        $('setSubModeFull').checked = s.mode === 'full';
        SYNC_SET_SUB_SOURCE();
        $('setSubHint').textContent = '当前：' + subSourceText(s) +
            ' · 节点 ' + (s.nodeCount || 0) + ' 个 · 更新于 ' + (s.updatedAt || '未更新');
    }

    function loadSubSourceCard() {
        return api('api/subs').then(function (d) {
            subsCache = d.subs || [];
            var sel = $('setSubSelect');
            if (!subsCache.length) {
                sel.innerHTML = '<option value="">（暂无订阅）</option>';
                $('setSubHint').textContent = '还没有订阅，请先到「订阅」标签页添加。';
                $('btnSubSave').disabled = true;
                $('btnSubUpdate').disabled = true;
                return;
            }
            sel.innerHTML = subsCache.map(function (s) {
                return '<option value="' + esc(s.id) + '">' + esc(s.name) + '</option>';
            }).join('');
            var want = findSub(d.activeSub) ? d.activeSub : subsCache[0].id;
            sel.value = want;
            $('btnSubSave').disabled = false;
            $('btnSubUpdate').disabled = false;
            fillSubSourceCard(want);
        }).catch(function (e) {
            $('setSubHint').textContent = '读取订阅失败：' + e.message;
        });
    }

    $('setSubSelect').addEventListener('change', function () {
        fillSubSourceCard($('setSubSelect').value);
    });

    function afterSubSaved(d) {
        if (d && d.warning) { toast('已保存，但读取订阅失败：' + d.warning, 'err'); }
        else { toast('订阅来源已保存', 'ok'); }
        loadSubSourceCard();
        loadSubs();
        refreshStatus();
        loadNodes();
    }

    $('btnSubSave').addEventListener('click', function () {
        var id = $('setSubSelect').value;
        if (!id) { toast('没有可修改的订阅', 'err'); return; }
        var cur = findSub(id) || {};
        var source = currentSource('setSubSource');
        var mode = $('setSubModeFull').checked ? 'full' : 'nodes';
        var body;

        if (source === 'url') {
            var url = $('setSubUrl').value.trim();
            if (!url) { toast('请填写订阅链接', 'err'); return; }
            if (!/^https?:\/\//i.test(url)) { toast('订阅链接必须以 http:// 或 https:// 开头', 'err'); return; }
            body = { name: cur.name || '', mode: mode, source: 'url', url: url };
        } else if (source === 'file') {
            var p = $('setSubPath').value.trim();
            if (!p) { toast('请填写 NAS 上的订阅文件路径', 'err'); return; }
            body = { name: cur.name || '', mode: mode, source: 'file', path: p };
        } else {
            var f = $('setSubFile').files[0];
            if (!f) { toast('请选择要上传的订阅文件', 'err'); return; }
            body = buildSubFormData(cur.name || '', mode, 'upload', f);
        }

        toast('正在保存订阅来源…');
        api('api/subs/' + id, { method: 'PUT', body: body })
            .then(afterSubSaved)
            .catch(function (e) { toast(e.message, 'err'); });
    });

    $('btnSubUpdate').addEventListener('click', function () {
        var id = $('setSubSelect').value;
        if (!id) { toast('没有可更新的订阅', 'err'); return; }
        toast('正在更新订阅…');
        api('api/subs/' + id + '/update', { method: 'POST' }).then(function () {
            toast('订阅已更新', 'ok');
            loadSubSourceCard();
            loadSubs();
            refreshStatus();
            loadNodes();
        }).catch(function (e) { toast(e.message, 'err'); });
    });

    /* ------------------------------------------------------------------ */
    /* logs                                                                */
    /* ------------------------------------------------------------------ */

    function loadLogs() {
        var src = $('logSource').value;
        var lines = $('logLines').value;
        return api('api/logs?source=' + src + '&lines=' + lines).then(function (d) {
            var arr = d.lines || [];
            var box = $('logBox');
            var pinned = box.scrollTop + box.clientHeight >= box.scrollHeight - 24;
            box.textContent = arr.length ? arr.join('\n') : '暂无日志。';
            if (pinned) box.scrollTop = box.scrollHeight;
        }).catch(function (e) { $('logBox').textContent = '读取日志失败：' + e.message; });
    }

    function setupLogTimer() {
        if (logTimer) { clearInterval(logTimer); logTimer = null; }
        if ($('logAuto').checked) {
            logTimer = setInterval(function () {
                if ($('tab-logs').classList.contains('active')) loadLogs();
            }, 3000);
        }
    }

    /* ------------------------------------------------------------------ */
    /* quick actions                                                       */
    /* ------------------------------------------------------------------ */

    $('btnQuickUpdate').addEventListener('click', function () {
        api('api/subs').then(function (d) {
            if (!d.activeSub) { toast('请先添加订阅', 'err'); return; }
            toast('正在更新订阅…');
            return api('api/subs/' + d.activeSub + '/update', { method: 'POST' }).then(function () {
                toast('订阅已更新', 'ok');
                refreshStatus();
                loadSubs();
                loadNodes();
            });
        }).catch(function (e) { toast(e.message, 'err'); });
    });

    $('btnGeoUpdate').addEventListener('click', function () {
        toast('正在更新规则库，请稍候…');
        api('api/geo/update', { method: 'POST' }).then(function () {
            toast('规则库已更新', 'ok');
        }).catch(function (e) { toast(e.message, 'err'); });
    });

    $('btnKernelUpdate').addEventListener('click', function () {
        api('api/kernel/check-update').then(function (d) {
            if (!d.hasUpdate) { toast('当前内核已是最新版本 ' + d.current, 'ok'); return; }
            if (!confirm('发现新版本 ' + d.latest + '（当前 ' + d.current + '），是否立即升级？\n升级过程约需 1-2 分钟。')) return;
            toast('正在下载并替换内核…');
            api('api/kernel/upgrade', { method: 'POST', body: { tag: d.latest } }).then(function (r) {
                toast('内核已升级到 ' + (r.version || r.tag), 'ok');
                refreshStatus();
            }).catch(function (e) { toast(e.message, 'err'); });
        }).catch(function (e) { toast(e.message, 'err'); });
    });

    /* ------------------------------------------------------------------ */
    /* tabs & boot                                                         */
    /* ------------------------------------------------------------------ */

    $('tabs').addEventListener('click', function (ev) {
        var btn = ev.target.closest('.tab');
        if (!btn) return;
        var name = btn.getAttribute('data-tab');
        Array.prototype.forEach.call(document.querySelectorAll('.tab'), function (t) {
            t.classList.toggle('active', t === btn);
        });
        Array.prototype.forEach.call(document.querySelectorAll('.panel'), function (p) {
            p.classList.toggle('active', p.id === 'tab-' + name);
        });
        if (name === 'subs') loadSubs();
        if (name === 'nodes') loadNodes();
        if (name === 'settings') { loadSettings(); loadSubSourceCard(); }
        if (name === 'logs') loadLogs();
    });

    $('btnStart').addEventListener('click', function () { kernelAction('start'); });
    $('btnStop').addEventListener('click', function () { kernelAction('stop'); });
    $('btnRestart').addEventListener('click', function () { kernelAction('restart'); });
    $('btnReloadNodes').addEventListener('click', function () { loadNodes(); toast('已刷新节点列表', 'ok'); });
    $('btnReloadLogs').addEventListener('click', function () { loadLogs(); });
    $('logAuto').addEventListener('change', setupLogTimer);
    $('logSource').addEventListener('change', loadLogs);
    $('logLines').addEventListener('change', loadLogs);

    // 「查看生成的配置」改为页内弹层。飞牛桌面里应用是 iframe 窗口，
    // 用 target="_blank" 会再弹一个浏览器标签页，破坏内嵌体验。
    $('linkConfig').addEventListener('click', function (ev) {
        ev.preventDefault();
        var box = $('cfgBox');
        box.textContent = '加载中…';
        $('cfgModal').hidden = false;
        api('api/config').then(function (d) {
            box.textContent = d.config || '（空）';
        }).catch(function (e) {
            box.textContent = '读取失败：' + e.message;
        });
    });
    $('btnCfgClose').addEventListener('click', function () { $('cfgModal').hidden = true; });
    $('cfgModal').addEventListener('click', function (ev) {
        if (ev.target === $('cfgModal')) $('cfgModal').hidden = true;
    });
    document.addEventListener('keydown', function (ev) {
        if (ev.key === 'Escape' && !$('cfgModal').hidden) $('cfgModal').hidden = true;
    });

    bindSubList();
    setupLogTimer();

    refreshStatus();
    loadSubs();
    var statusTimer = setInterval(refreshStatus, 5000);
    window.addEventListener('beforeunload', function () { clearInterval(statusTimer); });
})();
