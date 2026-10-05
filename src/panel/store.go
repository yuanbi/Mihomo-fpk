package main

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
	"time"
)

// Subscription sources. A subscription is either pulled from a URL, read from
// a file on the NAS, or uploaded once from the browser (cached on disk).
const (
	subSourceURL    = "url"
	subSourceFile   = "file"
	subSourceUpload = "upload"
)

// Sub describes one subscription source.
type Sub struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Source string `json:"source"` // url | file | upload
	URL    string `json:"url,omitempty"`
	Path   string `json:"path,omitempty"` // NAS path when Source == file
	// Wizard marks the subscription created by the install wizard or the
	// fnOS app-settings form, so later edits update it instead of piling up.
	Wizard    bool   `json:"wizard,omitempty"`
	Mode      string `json:"mode"` // "nodes" = extract proxies & use builtin rules, "full" = use the remote config as-is
	UpdatedAt string `json:"updatedAt"`
	NodeCount int    `json:"nodeCount"`
	Format    string `json:"format"`
	LastError string `json:"lastError"`

	Upload   int64 `json:"upload"`
	Download int64 `json:"download"`
	Total    int64 `json:"total"`
	Expire   int64 `json:"expire"` // unix seconds
}

// TunSettings mirrors the parts of mihomo's tun block that users care about.
type TunSettings struct {
	Enable       bool     `json:"enable"`
	Stack        string   `json:"stack"`
	AutoRoute    bool     `json:"autoRoute"`
	AutoRedirect bool     `json:"autoRedirect"`
	Device       string   `json:"device"`
	DNSHijack    []string `json:"dnsHijack"`
	MTU          int      `json:"mtu"`
}

// DNSSettings mirrors mihomo's dns block.
type DNSSettings struct {
	Enable       bool     `json:"enable"`
	Listen       string   `json:"listen"`
	EnhancedMode string   `json:"enhancedMode"`
	Nameserver   []string `json:"nameserver"`
	Fallback     []string `json:"fallback"`
	IPv6         bool     `json:"ipv6"`
	FakeIPFilter []string `json:"fakeIpFilter"`
}

// Settings is the persistent panel configuration (etc/settings.json).
type Settings struct {
	PanelPort      int    `json:"panelPort"`
	ControllerPort int    `json:"controllerPort"`
	MixedPort      int    `json:"mixedPort"`
	AllowLan       bool   `json:"allowLan"`
	Mode           string `json:"mode"` // rule / global / direct
	LogLevel       string `json:"logLevel"`
	Secret         string `json:"secret"`
	ProxyEnabled   bool   `json:"proxyEnabled"`
	ActiveSub      string `json:"activeSub"`
	Subs           []Sub  `json:"subs"`

	Tun TunSettings `json:"tun"`
	DNS DNSSettings `json:"dns"`

	UnifiedDelay  bool     `json:"unifiedDelay"`
	TCPConcurrent bool     `json:"tcpConcurrent"`
	SnifferEnable bool     `json:"snifferEnable"`
	CustomRules   []string `json:"customRules"`

	// AccessPassword optionally protects the panel and the kernel API proxy.
	AccessPassword string `json:"accessPassword,omitempty"`

	Updated string `json:"updated,omitempty"`
}

func defaultSettings() *Settings {
	return &Settings{
		PanelPort:      9788,
		ControllerPort: 9790,
		MixedPort:      7890,
		AllowLan:       true,
		Mode:           "rule",
		LogLevel:       "info",
		Secret:         randomHex(8),
		ProxyEnabled:   true,
		ActiveSub:      "",
		Subs:           []Sub{},
		Tun: TunSettings{
			Enable:       false,
			Stack:        "mixed",
			AutoRoute:    true,
			AutoRedirect: false,
			Device:       "Mihomo",
			DNSHijack:    []string{"any:53"},
			MTU:          1500,
		},
		DNS: DNSSettings{
			Enable:       true,
			Listen:       "0.0.0.0:1053",
			EnhancedMode: "fake-ip",
			Nameserver: []string{
				"223.5.5.5",
				"119.29.29.29",
				"https://223.5.5.5/dns-query",
			},
			Fallback: []string{
				"https://1.1.1.1/dns-query",
				"https://8.8.8.8/dns-query",
				"tls://8.8.4.4:853",
			},
			IPv6: false,
			FakeIPFilter: []string{
				"*.lan", "*.local", "*.localdomain", "localhost.ptlogin2.qq.com",
				"+.msftconnecttest.com", "+.msftncsi.com",
			},
		},
		UnifiedDelay:  true,
		TCPConcurrent: true,
		SnifferEnable: true,
		CustomRules:   []string{},
	}
}

// Store holds settings with concurrent access helpers.
type Store struct {
	path string

	mu sync.RWMutex
	s  *Settings
}

func NewStore(path string) *Store {
	return &Store{path: path, s: defaultSettings()}
}

// Load reads settings from disk, creating defaults when missing.
// Unknown or missing fields keep their default value.
func (st *Store) Load() error {
	st.mu.Lock()
	defer st.mu.Unlock()

	data, err := os.ReadFile(st.path)
	if err != nil {
		if os.IsNotExist(err) {
			return st.saveLocked()
		}
		return err
	}
	fresh := defaultSettings()
	if err := json.Unmarshal(data, fresh); err != nil {
		return err
	}
	normalize(fresh)
	st.s = fresh
	return nil
}

func normalize(s *Settings) {
	if s.PanelPort <= 0 || s.PanelPort > 65535 {
		s.PanelPort = 9788
	}
	if s.MixedPort <= 0 || s.MixedPort > 65535 {
		s.MixedPort = 7890
	}
	if s.ControllerPort <= 0 || s.ControllerPort > 65535 {
		s.ControllerPort = 9790
	}
	if s.Secret == "" {
		s.Secret = randomHex(8)
	}
	switch s.Mode {
	case "rule", "global", "direct":
	default:
		s.Mode = "rule"
	}
	switch s.LogLevel {
	case "silent", "error", "warning", "info", "debug":
	default:
		s.LogLevel = "info"
	}
	if s.Tun.Stack == "" {
		s.Tun.Stack = "mixed"
	}
	if s.Tun.Device == "" {
		s.Tun.Device = "Mihomo"
	}
	if len(s.Tun.DNSHijack) == 0 {
		s.Tun.DNSHijack = []string{"any:53"}
	}
	if s.Tun.MTU <= 0 {
		s.Tun.MTU = 1500
	}
	if s.DNS.Listen == "" {
		s.DNS.Listen = "0.0.0.0:1053"
	}
	if s.DNS.EnhancedMode == "" {
		s.DNS.EnhancedMode = "fake-ip"
	}
	if s.Subs == nil {
		s.Subs = []Sub{}
	}
	if s.CustomRules == nil {
		s.CustomRules = []string{}
	}
	for i := range s.Subs {
		// 兼容 1.0.2 及更早的配置：那时只有 URL 一种来源，没有 source 字段。
		switch s.Subs[i].Source {
		case subSourceURL, subSourceFile, subSourceUpload:
		default:
			if strings.TrimSpace(s.Subs[i].URL) != "" {
				s.Subs[i].Source = subSourceURL
			} else {
				s.Subs[i].Source = subSourceUpload
			}
		}
		if s.Subs[i].Source == subSourceFile {
			s.Subs[i].Path = strings.TrimSpace(s.Subs[i].Path)
		}
		if s.Subs[i].Mode != "full" {
			s.Subs[i].Mode = "nodes"
		}
	}
}

func (st *Store) saveLocked() error {
	st.s.Updated = time.Now().Format(time.RFC3339)
	data, err := json.MarshalIndent(st.s, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(st.path, data, 0o600)
}

// Save persists the current settings.
func (st *Store) Save() error {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.saveLocked()
}

// Get returns a deep-enough copy of the settings (slice values are copied).
// Empty slices stay non-nil so JSON encodes them as [] instead of null.
func (st *Store) Get() Settings {
	st.mu.RLock()
	defer st.mu.RUnlock()
	cp := *st.s
	cp.Subs = append([]Sub{}, st.s.Subs...)
	cp.CustomRules = append([]string{}, st.s.CustomRules...)
	cp.DNS = st.s.DNS
	cp.DNS.Nameserver = append([]string{}, st.s.DNS.Nameserver...)
	cp.DNS.Fallback = append([]string{}, st.s.DNS.Fallback...)
	cp.DNS.FakeIPFilter = append([]string{}, st.s.DNS.FakeIPFilter...)
	cp.Tun = st.s.Tun
	cp.Tun.DNSHijack = append([]string{}, st.s.Tun.DNSHijack...)
	return cp
}

// Update applies fn to the settings and persists the result.
func (st *Store) Update(fn func(*Settings)) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	fn(st.s)
	normalize(st.s)
	return st.saveLocked()
}

// tunEqual reports whether two TunSettings are equivalent (slices compared by value).
func tunEqual(a, b TunSettings) bool {
	if a.Enable != b.Enable || a.Stack != b.Stack || a.AutoRoute != b.AutoRoute ||
		a.AutoRedirect != b.AutoRedirect || a.Device != b.Device || a.MTU != b.MTU {
		return false
	}
	return stringSliceEqual(a.DNSHijack, b.DNSHijack)
}

func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (st *Store) FindSub(id string) (Sub, bool) {
	st.mu.RLock()
	defer st.mu.RUnlock()
	for _, s := range st.s.Subs {
		if s.ID == id {
			return s, true
		}
	}
	return Sub{}, false
}
