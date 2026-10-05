/*
 * fnOS Mihomo 面板 —— MetaCubeXD 引导脚本
 *
 * 目的：让内置的 MetaCubeXD 自动把「后端地址」指向本面板自身（同源），
 * 用户无需手动填写地址，也不会误连到浏览器本机的 127.0.0.1:9090。
 *
 * 原理：面板会把 /version、/proxies、/configs 等路径反向代理到 mihomo 的
 * external-controller，并在代理层注入密钥，所以浏览器只需要访问同源地址。
 */
(function () {
  'use strict';

  // 面板可能挂在两种位置，都必须算对后端地址：
  //   1) 直接监听 9788 端口 ——  document.baseURI = http://NAS:9788/
  //   2) 飞牛 CGI 同源网关 ——  baseURI 形如
  //      https://NAS/cgi/ThirdParty/<appname>/index.cgi/   （MetaCubeXD 自己再多一层 /dashboard/）
  // 用 <base>/baseURI 反推，既不用手填地址，也避免了
  // 「HTTPS 页面里请求 http://NAS:9788 被浏览器按混合内容拦截」。
  var base = '';
  try {
    base = String(document.baseURI || location.href).replace(/\/+$/, '');
    base = base.replace(/\/dashboard$/, '');
  } catch (e) {
    base = '';
  }
  if (!base) {
    base = (typeof location !== 'undefined' && location.origin) ? location.origin : '';
  }
  window.__MIHOMO_BASE__ = base;

  var origin = base;

  window.__METACUBEXD_CONFIG__ = {
    defaultBackendURL: origin,
    githubToken: '',
  };

  // MetaCubeXD 官方集成钩子：宿主应用可直接注入后端端点。
  // secret 留空是有意的——密钥由面板在反向代理层注入，不暴露给浏览器。
  window.metacubexd = {
    endpoint: {
      url: origin,
      secret: '',
      label: 'Mihomo (fnOS)',
    },
  };

  // 清理历史遗留的环回地址端点。127.0.0.1:9090 之类的默认值来自
  // MetaCubeXD 的编译期回退值，它指向「浏览器所在机器」，在远程访问时永远连不通。
  try {
    var ls = window.localStorage;

    var parseURL = function (u) {
      try { return new URL(u, origin); } catch (e) { return null; }
    };

    var isLoopback = function (u) {
      var p = parseURL(u);
      if (!p) return false;
      var h = p.hostname;
      return h === '127.0.0.1' || h === 'localhost' || h === '0.0.0.0' || h === '::1' || h === '[::1]';
    };

    var isSameOrigin = function (u) {
      var p = parseURL(u);
      return !!p && p.origin === origin;
    };

    var rawList = ls.getItem('endpointList');
    if (rawList) {
      var list = JSON.parse(rawList);
      if (Object.prototype.toString.call(list) === '[object Array]') {
        var kept = [];
        for (var i = 0; i < list.length; i++) {
          var item = list[i];
          if (!item || !item.url) continue;
          // 保留：本面板自身的端点，以及一切非环回地址的端点
          if (isLoopback(item.url) && !isSameOrigin(item.url)) continue;
          kept.push(item);
        }
        if (kept.length !== list.length) {
          ls.setItem('endpointList', JSON.stringify(kept));
        }

        // 若当前选中的端点已被清理，则取消选中，让上面注入的端点自动生效
        var rawSel = ls.getItem('selectedEndpoint');
        if (rawSel) {
          var sel = rawSel;
          try { sel = JSON.parse(rawSel); } catch (e) { /* 原始字符串即为 id */ }
          var alive = false;
          for (var j = 0; j < kept.length; j++) {
            if (kept[j] && kept[j].id === sel) { alive = true; break; }
          }
          if (!alive) ls.removeItem('selectedEndpoint');
        }
      }
    }
  } catch (e) {
    /* localStorage 不可用时忽略：同源默认值依然生效 */
  }
})();
