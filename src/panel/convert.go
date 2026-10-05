package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// b64decode is lenient: it accepts standard, URL-safe, padded and un-padded input.
func b64decode(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	s = strings.Map(func(r rune) rune {
		switch r {
		case '\n', '\r', ' ', '\t':
			return -1
		}
		return r
	}, s)
	s = strings.TrimRight(s, "=")
	s = strings.ReplaceAll(s, "-", "+")
	s = strings.ReplaceAll(s, "_", "/")
	return base64.RawStdEncoding.DecodeString(s)
}

func toInt(v any) int {
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(t))
		return n
	case json.Number:
		n, _ := t.Int64()
		return int(n)
	}
	return 0
}

func toStr(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	case json.Number:
		return t.String()
	case bool:
		if t {
			return "true"
		}
		return "false"
	case nil:
		return ""
	}
	return fmt.Sprint(v)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func splitHostPort(s string) (string, int, error) {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	if i := strings.IndexByte(s, '?'); i >= 0 {
		s = s[:i]
	}
	host, portStr, err := net.SplitHostPort(s)
	if err != nil {
		// fall back to the last colon separator
		idx := strings.LastIndexByte(s, ':')
		if idx < 0 {
			return "", 0, fmt.Errorf("无法解析地址: %s", s)
		}
		host, portStr = s[:idx], s[idx+1:]
	}
	port, err := strconv.Atoi(strings.TrimSpace(portStr))
	if err != nil || port <= 0 || port > 65535 {
		return "", 0, fmt.Errorf("端口无效: %s", portStr)
	}
	return strings.Trim(host, "[]"), port, nil
}

func fragName(u *url.URL) string {
	if u.Fragment == "" {
		return ""
	}
	if decoded, err := url.QueryUnescape(u.Fragment); err == nil {
		return decoded
	}
	return u.Fragment
}

// linkToProxy converts a share link into a mihomo proxy map.
func linkToProxy(link string) (map[string]any, error) {
	link = strings.TrimSpace(link)
	lower := strings.ToLower(link)
	switch {
	case strings.HasPrefix(lower, "ssr://"):
		return parseSSR(link)
	case strings.HasPrefix(lower, "ss://"):
		return parseSS(link)
	case strings.HasPrefix(lower, "vmess://"):
		return parseVmess(link)
	case strings.HasPrefix(lower, "vless://"):
		return parseXrayStyle(link, "vless")
	case strings.HasPrefix(lower, "trojan://"):
		return parseTrojan(link)
	case strings.HasPrefix(lower, "hysteria2://"), strings.HasPrefix(lower, "hy2://"):
		return parseHysteria2(link)
	case strings.HasPrefix(lower, "hysteria://"):
		return parseHysteria2(link)
	case strings.HasPrefix(lower, "tuic://"):
		return parseTuic(link)
	case strings.HasPrefix(lower, "socks5://"), strings.HasPrefix(lower, "socks://"):
		return parseSocks(link)
	}
	return nil, fmt.Errorf("不支持的协议: %s", truncate(link, 32))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func parseSS(link string) (map[string]any, error) {
	body := link[len("ss://"):]
	name := ""
	if i := strings.IndexByte(body, '#'); i >= 0 {
		if n, err := url.QueryUnescape(body[i+1:]); err == nil {
			name = n
		} else {
			name = body[i+1:]
		}
		body = body[:i]
	}
	rawQuery := ""
	if i := strings.IndexByte(body, '?'); i >= 0 {
		rawQuery = body[i+1:]
		body = body[:i]
	}
	body = strings.TrimSuffix(body, "/")

	var method, password, hostport string
	if i := strings.LastIndexByte(body, '@'); i >= 0 {
		userinfo := body[:i]
		hostport = body[i+1:]
		dec, err := b64decode(userinfo)
		if err != nil {
			dec = []byte(userinfo)
		}
		parts := strings.SplitN(string(dec), ":", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("ss 用户信息解析失败")
		}
		method, password = parts[0], parts[1]
	} else {
		dec, err := b64decode(body)
		if err != nil {
			return nil, fmt.Errorf("ss 链接解码失败")
		}
		s := string(dec)
		at := strings.LastIndexByte(s, '@')
		if at < 0 {
			return nil, fmt.Errorf("ss 链接格式错误")
		}
		parts := strings.SplitN(s[:at], ":", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("ss 链接格式错误")
		}
		method, password = parts[0], parts[1]
		hostport = s[at+1:]
	}

	host, port, err := splitHostPort(hostport)
	if err != nil {
		return nil, err
	}
	p := map[string]any{
		"name":     name,
		"type":     "ss",
		"server":   host,
		"port":     port,
		"cipher":   strings.ToLower(method),
		"password": password,
		"udp":      true,
	}

	if plugin := queryValue(rawQuery, "plugin"); plugin != "" {
		segs := strings.Split(plugin, ";")
		opts := map[string]any{}
		for _, seg := range segs[1:] {
			kv := strings.SplitN(seg, "=", 2)
			if len(kv) == 2 {
				opts[kv[0]] = kv[1]
			}
		}
		switch segs[0] {
		case "obfs-local", "simple-obfs":
			po := map[string]any{}
			if v, ok := opts["obfs"]; ok {
				po["mode"] = toStr(v)
			}
			if v, ok := opts["obfs-host"]; ok {
				po["host"] = toStr(v)
			}
			p["plugin"] = "obfs"
			p["plugin-opts"] = po
		case "v2ray-plugin":
			po := map[string]any{}
			if v, ok := opts["mode"]; ok {
				po["mode"] = toStr(v)
			}
			if v, ok := opts["host"]; ok {
				po["host"] = toStr(v)
			}
			if v, ok := opts["path"]; ok {
				po["path"] = toStr(v)
			}
			if v, ok := opts["tls"]; ok {
				po["tls"] = toStr(v) != ""
			}
			p["plugin"] = "v2ray-plugin"
			p["plugin-opts"] = po
		}
	}
	return p, nil
}

// queryValue extracts a value from a raw query string without strict parsing
// (Go's url.ParseQuery rejects ';' separators that sip002 plugins use).
func queryValue(rawQuery, key string) string {
	for _, part := range strings.Split(rawQuery, "&") {
		if part == "" {
			continue
		}
		if i := strings.IndexByte(part, '='); i >= 0 {
			k := part[:i]
			if k == key {
				if v, err := url.QueryUnescape(part[i+1:]); err == nil {
					return v
				}
				return part[i+1:]
			}
		}
	}
	return ""
}

func parseSSR(link string) (map[string]any, error) {
	body := strings.TrimPrefix(link, "ssr://")
	body = strings.TrimPrefix(body, "SSR://")
	dec, err := b64decode(body)
	if err != nil {
		return nil, fmt.Errorf("ssr 解码失败")
	}
	s := string(dec)
	rawQuery := ""
	if i := strings.Index(s, "/?"); i >= 0 {
		rawQuery = s[i+2:]
		s = s[:i]
	} else {
		s = strings.TrimSuffix(s, "/")
	}
	parts := strings.SplitN(s, ":", 6)
	if len(parts) < 6 {
		return nil, fmt.Errorf("ssr 字段不足")
	}
	port, err := strconv.Atoi(parts[1])
	if err != nil {
		return nil, fmt.Errorf("ssr 端口无效")
	}
	passRaw, err := b64decode(parts[5])
	if err != nil {
		return nil, fmt.Errorf("ssr 密码解码失败")
	}
	name := ""
	if r := queryValueSSR(rawQuery, "remarks"); r != "" {
		if d, err := b64decode(r); err == nil {
			name = string(d)
		} else {
			name = r
		}
	}
	p := map[string]any{
		"name":     name,
		"type":     "ssr",
		"server":   parts[0],
		"port":     port,
		"cipher":   strings.ToLower(parts[3]),
		"password": string(passRaw),
		"protocol": parts[2],
		"obfs":     parts[4],
		"udp":      true,
	}
	if v := queryValueSSR(rawQuery, "protoparam"); v != "" {
		if d, err := b64decode(v); err == nil {
			p["protocol-param"] = string(d)
		}
	}
	if v := queryValueSSR(rawQuery, "obfsparam"); v != "" {
		if d, err := b64decode(v); err == nil {
			p["obfs-param"] = string(d)
		}
	}
	return p, nil
}

func queryValueSSR(rawQuery, key string) string {
	s := url.Values{}
	for _, part := range strings.Split(rawQuery, "&") {
		if i := strings.IndexByte(part, '='); i > 0 {
			s.Set(part[:i], part[i+1:])
		}
	}
	return s.Get(key)
}

func parseVmess(link string) (map[string]any, error) {
	body := strings.TrimPrefix(link, "vmess://")
	body = strings.TrimPrefix(body, "VMESS://")
	if i := strings.IndexByte(body, '#'); i >= 0 {
		body = body[:i]
	}
	dec, err := b64decode(body)
	if err != nil {
		return nil, fmt.Errorf("vmess 解码失败")
	}
	var m map[string]any
	if err := json.Unmarshal(dec, &m); err != nil {
		return nil, fmt.Errorf("vmess JSON 解析失败")
	}
	server := toStr(m["add"])
	port := toInt(m["port"])
	uuid := toStr(m["id"])
	if server == "" || port == 0 || uuid == "" {
		return nil, fmt.Errorf("vmess 缺少必要字段")
	}
	name := sanitizeName(toStr(m["ps"]))
	cipher := strings.ToLower(toStr(m["scy"]))
	if cipher == "" {
		cipher = "auto"
	}
	network := strings.ToLower(toStr(m["net"]))
	if network == "" {
		network = "tcp"
	}
	p := map[string]any{
		"name":    name,
		"type":    "vmess",
		"server":  server,
		"port":    port,
		"uuid":    uuid,
		"alterId": toInt(m["aid"]),
		"cipher":  cipher,
		"udp":     true,
		"network": network,
	}

	tls := strings.ToLower(toStr(m["tls"]))
	sni := firstNonEmpty(toStr(m["sni"]), toStr(m["host"]))
	if tls == "tls" || tls == "reality" {
		p["tls"] = true
		if sni != "" {
			p["servername"] = sni
		}
	}
	if fp := toStr(m["fp"]); fp != "" {
		p["client-fingerprint"] = fp
	}
	applyTransport(p, network, toStr(m["path"]), toStr(m["host"]), toStr(m["type"]))
	return p, nil
}

func applyTransport(p map[string]any, network, path, host, headerType string) {
	switch network {
	case "ws":
		po := map[string]any{}
		if path != "" {
			po["path"] = path
		} else {
			po["path"] = "/"
		}
		if host != "" {
			po["headers"] = map[string]any{"Host": host}
		}
		p["ws-opts"] = po
	case "grpc":
		gs := path
		if gs == "" {
			gs = host
		}
		p["grpc-opts"] = map[string]any{"grpc-service-name": gs}
	case "h2", "http":
		ho := map[string]any{}
		if path != "" {
			ho["path"] = path
		}
		if host != "" {
			ho["host"] = []string{host}
		}
		p["h2-opts"] = ho
	case "httpupgrade":
		po := map[string]any{}
		if path != "" {
			po["path"] = path
		}
		if host != "" {
			po["headers"] = map[string]any{"Host": host}
		}
		p["http-opts"] = map[string]any{"path": []string{path}}
		p["network"] = "httpupgrade"
		p["http-upgrade-opts"] = po
	case "tcp":
		if headerType == "http" && host != "" {
			p["network"] = "http"
			p["http-opts"] = map[string]any{
				"path": []string{"/"},
				"headers": map[string]any{"Host": []string{host}},
			}
		}
	}
}

// parseXrayStyle handles vless:// links.
func parseXrayStyle(link, kind string) (map[string]any, error) {
	u, err := url.Parse(link)
	if err != nil {
		return nil, fmt.Errorf("%s 链接解析失败", kind)
	}
	q := u.Query()
	host := u.Hostname()
	port, _ := strconv.Atoi(u.Port())
	if host == "" || port == 0 {
		return nil, fmt.Errorf("%s 缺少服务器地址", kind)
	}
	name := fragName(u)
	if name == "" {
		name = host
	}
	uuid := ""
	if u.User != nil {
		uuid = u.User.Username()
	}
	if uuid == "" {
		return nil, fmt.Errorf("%s 缺少 UUID", kind)
	}
	network := strings.ToLower(q.Get("type"))
	if network == "" {
		network = "tcp"
	}
	p := map[string]any{
		"name":    name,
		"type":    kind,
		"server":  host,
		"port":    port,
		"uuid":    uuid,
		"udp":     true,
		"network": network,
	}
	security := strings.ToLower(q.Get("security"))
	sni := firstNonEmpty(q.Get("sni"), q.Get("peer"), q.Get("host"))
	if security == "tls" || security == "reality" {
		p["tls"] = true
		if sni != "" {
			p["servername"] = sni
		}
		if alpn := q.Get("alpn"); alpn != "" {
			p["alpn"] = strings.Split(alpn, ",")
		}
		if fp := q.Get("fp"); fp != "" {
			p["client-fingerprint"] = fp
		}
		if security == "reality" {
			ro := map[string]any{}
			if pbk := q.Get("pbk"); pbk != "" {
				ro["public-key"] = pbk
			}
			if sid := q.Get("sid"); sid != "" {
				ro["short-id"] = sid
			}
			p["reality-opts"] = ro
		}
	}
	if flow := q.Get("flow"); flow != "" {
		p["flow"] = flow
	}
	if q.Get("encryption") != "" {
		p["encryption"] = q.Get("encryption")
	}
	applyTransport(p, network, q.Get("path"), q.Get("host"), "")
	return p, nil
}

func parseTrojan(link string) (map[string]any, error) {
	u, err := url.Parse(link)
	if err != nil {
		return nil, fmt.Errorf("trojan 链接解析失败")
	}
	q := u.Query()
	host := u.Hostname()
	port, _ := strconv.Atoi(u.Port())
	if host == "" || port == 0 {
		return nil, fmt.Errorf("trojan 缺少服务器地址")
	}
	password := ""
	if u.User != nil {
		password = u.User.Username()
	}
	name := fragName(u)
	if name == "" {
		name = host
	}
	p := map[string]any{
		"name":     name,
		"type":     "trojan",
		"server":   host,
		"port":     port,
		"password": password,
		"udp":      true,
	}
	if sni := firstNonEmpty(q.Get("sni"), q.Get("peer"), q.Get("host")); sni != "" {
		p["sni"] = sni
	}
	if v := q.Get("allowInsecure"); v == "1" || strings.EqualFold(v, "true") {
		p["skip-cert-verify"] = true
	}
	network := strings.ToLower(q.Get("type"))
	if network == "" {
		network = "tcp"
	}
	if network == "ws" || network == "grpc" {
		p["network"] = network
		applyTransport(p, network, q.Get("path"), q.Get("host"), "")
	}
	return p, nil
}

func parseHysteria2(link string) (map[string]any, error) {
	u, err := url.Parse(link)
	if err != nil {
		return nil, fmt.Errorf("hysteria2 链接解析失败")
	}
	q := u.Query()
	host := u.Hostname()
	port, _ := strconv.Atoi(u.Port())
	if host == "" || port == 0 {
		return nil, fmt.Errorf("hysteria2 缺少服务器地址")
	}
	password := ""
	if u.User != nil {
		password = u.User.Username()
		if pw, ok := u.User.Password(); ok {
			password = password + ":" + pw
		}
	}
	name := fragName(u)
	if name == "" {
		name = host
	}
	p := map[string]any{
		"name":     name,
		"type":     "hysteria2",
		"server":   host,
		"port":     port,
		"password": password,
	}
	if sni := firstNonEmpty(q.Get("sni"), q.Get("peer")); sni != "" {
		p["sni"] = sni
	}
	if v := q.Get("insecure"); v == "1" || strings.EqualFold(v, "true") {
		p["skip-cert-verify"] = true
	}
	if obfs := q.Get("obfs"); obfs != "" {
		p["obfs"] = obfs
		if op := q.Get("obfs-password"); op != "" {
			p["obfs-password"] = op
		}
	}
	if alpn := q.Get("alpn"); alpn != "" {
		p["alpn"] = strings.Split(alpn, ",")
	}
	return p, nil
}

func parseTuic(link string) (map[string]any, error) {
	u, err := url.Parse(link)
	if err != nil {
		return nil, fmt.Errorf("tuic 链接解析失败")
	}
	q := u.Query()
	host := u.Hostname()
	port, _ := strconv.Atoi(u.Port())
	if host == "" || port == 0 {
		return nil, fmt.Errorf("tuic 缺少服务器地址")
	}
	uuid, password := "", ""
	if u.User != nil {
		uuid = u.User.Username()
		password, _ = u.User.Password()
	}
	name := fragName(u)
	if name == "" {
		name = host
	}
	p := map[string]any{
		"name":           name,
		"type":           "tuic",
		"server":         host,
		"port":           port,
		"uuid":           uuid,
		"password":       password,
		"udp-relay-mode": "native",
	}
	if sni := firstNonEmpty(q.Get("sni"), q.Get("peer")); sni != "" {
		p["sni"] = sni
	}
	if v := q.Get("allow_insecure"); v == "1" {
		p["skip-cert-verify"] = true
	}
	if alpn := q.Get("alpn"); alpn != "" {
		p["alpn"] = strings.Split(alpn, ",")
	}
	if cc := q.Get("congestion_control"); cc != "" {
		p["congestion-controller"] = cc
	}
	return p, nil
}

func parseSocks(link string) (map[string]any, error) {
	u, err := url.Parse(link)
	if err != nil {
		return nil, fmt.Errorf("socks 链接解析失败")
	}
	host := u.Hostname()
	port, _ := strconv.Atoi(u.Port())
	if host == "" || port == 0 {
		return nil, fmt.Errorf("socks 缺少服务器地址")
	}
	name := fragName(u)
	if name == "" {
		name = host
	}
	p := map[string]any{
		"name":   name,
		"type":   "socks5",
		"server": host,
		"port":   port,
		"udp":    true,
	}
	if u.User != nil {
		user := u.User.Username()
		pass, _ := u.User.Password()
		if user != "" {
			p["username"] = user
		}
		if pass != "" {
			p["password"] = pass
		}
	}
	return p, nil
}
