package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	groupSelect = "🚀 节点选择"
	groupAuto   = "♻️ 自动选择"
	groupDirect = "🎯 全球直连"
	groupAd     = "🛑 广告拦截"
	groupFinal  = "🐟 漏网之鱼"

	healthCheckURL = "https://www.gstatic.com/generate_204"
)

// yamlConfig controls the field order of the generated configuration file.
type yamlConfig struct {
	MixedPort               int              `yaml:"mixed-port"`
	AllowLan                bool             `yaml:"allow-lan"`
	BindAddress             string           `yaml:"bind-address,omitempty"`
	Mode                    string           `yaml:"mode"`
	LogLevel                string           `yaml:"log-level"`
	IPv6                    bool             `yaml:"ipv6"`
	ExternalController      string           `yaml:"external-controller"`
	Secret                  string           `yaml:"secret,omitempty"`
	UnifiedDelay            bool             `yaml:"unified-delay"`
	TCPConcurrent           bool             `yaml:"tcp-concurrent"`
	FindProcessMode         string           `yaml:"find-process-mode"`
	Profile                 map[string]any   `yaml:"profile"`
	GeodataMode             bool             `yaml:"geodata-mode"`
	GeoAutoUpdate           bool             `yaml:"geo-auto-update"`
	DNS                     map[string]any   `yaml:"dns,omitempty"`
	Sniffer                 map[string]any   `yaml:"sniffer,omitempty"`
	Tun                     map[string]any   `yaml:"tun,omitempty"`
	ProxyProviders          map[string]any   `yaml:"proxy-providers,omitempty"`
	ProxyGroups             []map[string]any `yaml:"proxy-groups,omitempty"`
	RuleProviders           map[string]any   `yaml:"rule-providers,omitempty"`
	Rules                   []string         `yaml:"rules"`
}

func dnsBlock(s *Settings) map[string]any {
	dns := map[string]any{
		"enable":              true,
		"listen":              s.DNS.Listen,
		"ipv6":                s.DNS.IPv6,
		"enhanced-mode":       s.DNS.EnhancedMode,
		"default-nameserver":  []string{"223.5.5.5", "119.29.29.29", "114.114.114.114"},
		"nameserver":          s.DNS.Nameserver,
		"fallback":            s.DNS.Fallback,
		"use-hosts":           true,
		"respect-rules":       false,
		"fallback-filter": map[string]any{
			"geoip":  true,
			"geoip-code": "CN",
			"ipcidr": []string{"240.0.0.0/4", "0.0.0.0/32"},
		},
	}
	if s.DNS.EnhancedMode == "fake-ip" {
		dns["fake-ip-range"] = "198.18.0.1/16"
		dns["fake-ip-filter"] = s.DNS.FakeIPFilter
	}
	return dns
}

func tunBlock(s *Settings) map[string]any {
	hijack := s.Tun.DNSHijack
	if len(hijack) == 0 {
		hijack = []string{"any:53"}
	}
	tun := map[string]any{
		"enable":                true,
		"stack":                 s.Tun.Stack,
		"device":                s.Tun.Device,
		"auto-route":            s.Tun.AutoRoute,
		"auto-detect-interface": true,
		"dns-hijack":            hijack,
		"mtu":                   s.Tun.MTU,
	}
	if s.Tun.AutoRedirect {
		tun["auto-redirect"] = true
	}
	return tun
}

func snifferBlock(s *Settings) map[string]any {
	return map[string]any{
		"enable": true,
		"sniff": map[string]any{
			"HTTP": map[string]any{"ports": []any{80, "8080-8880"}, "override-destination": true},
			"TLS":  map[string]any{"ports": []any{443, 8443}},
			"QUIC": map[string]any{"ports": []any{443, 8443}},
		},
		"skip-domain": []string{
			"Mijia Cloud",
			"dlg.io.mi.com",
			"+.push.apple.com",
		},
	}
}

func commonProfile() map[string]any {
	return map[string]any{
		"store-selected": true,
		"store-fake-ip":  true,
	}
}

// buildConfig returns the configuration written to etc/config.yaml.
func buildConfig(s *Settings, a *App) ([]byte, error) {
	if sub, ok := activeSub(s); ok && sub.Mode == "full" {
		rawPath := filepath.Join(a.subsDir, sub.ID+".yaml")
		if data, err := os.ReadFile(rawPath); err == nil {
			if out, err := mergeFullConfig(data, s); err == nil {
				return out, nil
			}
		}
	}
	return buildBuiltinConfig(s, a)
}

func activeSub(s *Settings) (Sub, bool) {
	if s.ActiveSub == "" {
		return Sub{}, false
	}
	for _, sub := range s.Subs {
		if sub.ID == s.ActiveSub {
			return sub, true
		}
	}
	return Sub{}, false
}

func buildBuiltinConfig(s *Settings, a *App) ([]byte, error) {
	providers := map[string]any{}
	useList := []string{}

	if sub, ok := activeSub(s); ok {
		if fileExists(filepath.Join(a.provDir, sub.ID+".yaml")) {
			key := providerFileName(sub.ID)
			providers[key] = map[string]any{
				"type": "file",
				"path": "./providers/" + key,
				"health-check": map[string]any{
					"enable":   true,
					"url":      healthCheckURL,
					"interval": 300,
				},
			}
			useList = append(useList, key)
		}
	}

	groups := make([]map[string]any, 0, 5)
	if len(useList) > 0 {
		groups = append(groups,
			map[string]any{
				"name":    groupSelect,
				"type":    "select",
				"proxies": []string{groupAuto, "DIRECT"},
				"use":     useList,
			},
			map[string]any{
				"name":      groupAuto,
				"type":      "url-test",
				"use":       useList,
				"url":       healthCheckURL,
				"interval":  300,
				"tolerance": 50,
				"lazy":      true,
			},
		)
	} else {
		groups = append(groups, map[string]any{
			"name":    groupSelect,
			"type":    "select",
			"proxies": []string{"DIRECT"},
		})
	}
	groups = append(groups,
		map[string]any{"name": groupDirect, "type": "select", "proxies": []string{"DIRECT", groupSelect}},
		map[string]any{"name": groupAd, "type": "select", "proxies": []string{"REJECT", "DIRECT"}},
		map[string]any{"name": groupFinal, "type": "select", "proxies": []string{groupSelect, "DIRECT"}},
	)

	rules := []string{
		"GEOSITE,category-ads-all," + groupAd,
		"GEOSITE,private," + groupDirect,
		"GEOIP,private," + groupDirect + ",no-resolve",
		"GEOSITE,cn," + groupDirect,
		"GEOIP,cn," + groupDirect,
	}
	for _, r := range s.CustomRules {
		if r = strings.TrimSpace(r); r != "" {
			rules = append(rules, r)
		}
	}
	rules = append(rules, "MATCH,"+groupFinal)

	cfg := &yamlConfig{
		MixedPort:               s.MixedPort,
		AllowLan:                s.AllowLan,
		BindAddress:             "*",
		Mode:                    s.Mode,
		LogLevel:                s.LogLevel,
		IPv6:                    false,
		ExternalController:      fmt.Sprintf("127.0.0.1:%d", s.ControllerPort),
		Secret:                  s.Secret,
		UnifiedDelay:            s.UnifiedDelay,
		TCPConcurrent:           s.TCPConcurrent,
		FindProcessMode:         "off",
		Profile:                 commonProfile(),
		GeodataMode:             true,
		GeoAutoUpdate:           true,
		Rules:                   rules,
	}
	if s.DNS.Enable {
		cfg.DNS = dnsBlock(s)
	}
	if s.SnifferEnable {
		cfg.Sniffer = snifferBlock(s)
	}
	if s.Tun.Enable {
		cfg.Tun = tunBlock(s)
	}
	if len(providers) > 0 {
		cfg.ProxyProviders = providers
	}
	cfg.ProxyGroups = groups

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	header := "# 由 Mihomo 代理（飞牛 fnOS 应用）自动生成，手动修改将在下次保存设置时被覆盖\n" +
		"# 如需使用机场自带规则，请在面板中把订阅模式切换为「完整配置」。\n"
	return append([]byte(header), data...), nil
}

// mergeFullConfig injects local runtime settings into a remote config file.
func mergeFullConfig(raw []byte, s *Settings) ([]byte, error) {
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	if doc == nil {
		return nil, fmt.Errorf("订阅配置为空")
	}
	// drop keys that would clash with the panel managed ports
	for _, k := range []string{"port", "socks-port", "redir-port", "tproxy-port", "mixed-port",
		"external-controller", "external-controller-tls", "external-ui", "secret"} {
		delete(doc, k)
	}
	doc["mixed-port"] = s.MixedPort
	doc["allow-lan"] = s.AllowLan
	doc["bind-address"] = "*"
	doc["mode"] = s.Mode
	doc["log-level"] = s.LogLevel
	doc["ipv6"] = false
	doc["external-controller"] = fmt.Sprintf("127.0.0.1:%d", s.ControllerPort)
	doc["secret"] = s.Secret
	doc["unified-delay"] = s.UnifiedDelay
	doc["tcp-concurrent"] = s.TCPConcurrent
	doc["geodata-mode"] = true
	doc["geo-auto-update"] = true
	doc["profile"] = commonProfile()

	if s.DNS.Enable {
		doc["dns"] = dnsBlock(s)
	} else {
		delete(doc, "dns")
	}
	if s.Tun.Enable {
		doc["tun"] = tunBlock(s)
	} else {
		delete(doc, "tun")
	}
	if s.SnifferEnable {
		doc["sniffer"] = snifferBlock(s)
	} else {
		delete(doc, "sniffer")
	}

	out, err := yaml.Marshal(doc)
	if err != nil {
		return nil, err
	}
	header := "# 由 Mihomo 代理（飞牛 fnOS 应用）基于订阅「完整配置」生成，端口与 DNS/TUN 由面板接管。\n"
	return append([]byte(header), out...), nil
}
