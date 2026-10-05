package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// App wires together the store, kernel supervisor and HTTP server.
type App struct {
	appDest string
	etc     string
	varDir  string
	listen  string

	panelDir     string
	dashboardDir string
	geoDir       string

	store  *Store
	kernel *Kernel

	binPath   string
	cfgPath   string
	workDir   string
	provDir   string
	subsDir   string
	logPath   string
	pidFile   string
	panelLog  string

	startedAt time.Time
	proxy     *httputil.ReverseProxy
}

func NewApp(appDest, etc, varDir, listen string) *App {
	a := &App{
		appDest:      appDest,
		etc:          etc,
		varDir:       varDir,
		listen:       listen,
		panelDir:     filepath.Join(appDest, "panel"),
		dashboardDir: filepath.Join(appDest, "dashboard"),
		geoDir:       filepath.Join(appDest, "geo"),
		binPath:      filepath.Join(appDest, "bin", "mihomo"),
		workDir:      filepath.Join(varDir, "mihomo"),
		cfgPath:      filepath.Join(etc, "config.yaml"),
		provDir:      filepath.Join(varDir, "mihomo", "providers"),
		subsDir:      filepath.Join(etc, "subs"),
		logPath:      filepath.Join(varDir, "logs", "mihomo.log"),
		panelLog:     filepath.Join(varDir, "logs", "panel.log"),
		pidFile:      filepath.Join(varDir, "mihomo.pid"),
		startedAt:    time.Now(),
	}
	a.store = NewStore(filepath.Join(etc, "settings.json"))
	a.kernel = NewKernel(a.binPath, a.workDir, a.cfgPath, a.logPath, a.pidFile)
	return a
}

// Init prepares directories, config and starts the kernel when enabled.
func (a *App) Init() error {
	for _, d := range []string{
		a.etc, a.varDir, a.workDir, a.provDir, a.subsDir,
		filepath.Join(a.varDir, "logs"), filepath.Dir(a.panelLog),
	} {
		if err := ensureDir(d); err != nil {
			return fmt.Errorf("创建目录 %s 失败: %w", d, err)
		}
	}
	if err := a.store.Load(); err != nil {
		log.Printf("读取设置失败，使用默认值: %v", err)
	}
	a.syncGeo()
	imported := a.importWizardEnv()

	if err := a.ApplyConfig(); err != nil {
		log.Printf("生成初始配置失败: %v", err)
	}

	if a.store.Get().ProxyEnabled {
		if err := a.kernel.Start(); err != nil {
			log.Printf("启动内核失败: %v", err)
		}
	}
	a.setupProxy()

	for _, id := range imported {
		go func(id string) {
			if err := a.UpdateSub(id, true); err != nil {
				log.Printf("导入向导订阅失败: %v", err)
			}
		}(id)
	}
	return nil
}

// importWizardEnv consumes the environment files written by the fnOS install
// wizard (install.env) and by the app-settings form (config.env). It returns
// the ids of subscriptions that still need their first refresh.
func (a *App) importWizardEnv() []string {
	var ids []string
	if id := a.applyWizardEnv(filepath.Join(a.etc, "install.env"), true); id != "" {
		ids = append(ids, id)
	}
	if id := a.applyWizardEnv(filepath.Join(a.etc, "config.env"), false); id != "" {
		ids = append(ids, id)
	}
	return ids
}

// normalizeSubSource maps the wizard's source selector onto a stored value.
func normalizeSubSource(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case subSourceURL, "link", "http", "https":
		return subSourceURL
	case subSourceFile, "path", "local":
		return subSourceFile
	case subSourceUpload:
		return subSourceUpload
	}
	return ""
}

// applyWizardEnv reads one wizard environment file and folds it into settings.
// installMode only creates a subscription when none exists yet, so reinstalling
// over kept data never clobbers the user's own subscriptions.
func (a *App) applyWizardEnv(path string, installMode bool) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	_ = removeQuietly(path)

	vals := parseEnvFile(string(data))

	if mp := strings.TrimSpace(vals["MIXED_PORT"]); mp != "" {
		if n, err := strconv.Atoi(mp); err == nil && n > 0 && n < 65536 {
			_ = a.store.Update(func(s *Settings) { s.MixedPort = n })
		}
	}

	url := strings.TrimSpace(vals["SUB_URL"])
	subPath := strings.TrimSpace(vals["SUB_PATH"])

	// 「应用设置」表单每次保存都会带上全部字段，所以显式给了 keep（不改）
	// 就必须真的什么都不动，不能让面板里刚改好的值被向导的默认值盖掉。
	switch strings.ToLower(strings.TrimSpace(vals["SUB_SOURCE"])) {
	case "keep", "none", "skip":
		return ""
	}

	source := normalizeSubSource(vals["SUB_SOURCE"])
	if source == "" {
		// 1.0.2 及更早的向导没有来源选择，有链接就按链接处理。
		if url != "" {
			source = subSourceURL
		}
	}
	if source == subSourceURL && url == "" {
		return ""
	}
	if source == subSourceFile && subPath == "" {
		return ""
	}
	if source == "" {
		return ""
	}

	rawMode := strings.ToLower(strings.TrimSpace(vals["SUB_MODE"]))
	keepMode := rawMode == "keep"
	mode := rawMode
	if mode != "full" {
		mode = "nodes"
	}

	// 向导托管的那条订阅优先复用，反复保存设置不会堆出一堆重复订阅。
	var existing Sub
	found := false
	for _, s := range a.store.Get().Subs {
		if s.Wizard {
			existing, found = s, true
			break
		}
	}
	if !found && installMode && len(a.store.Get().Subs) > 0 {
		return ""
	}

	id := existing.ID
	if !found {
		id = randomHex(6)
	}

	_ = a.store.Update(func(s *Settings) {
		if found {
			for i := range s.Subs {
				if s.Subs[i].ID != id {
					continue
				}
				s.Subs[i].Source = source
				s.Subs[i].URL = url
				s.Subs[i].Path = subPath
				if !keepMode {
					s.Subs[i].Mode = mode
				}
			}
		} else {
			s.Subs = append(s.Subs, Sub{
				ID:     id,
				Name:   "默认订阅",
				Source: source,
				URL:    url,
				Path:   subPath,
				Mode:   mode,
				Wizard: true,
			})
		}
		s.ActiveSub = id
	})
	return id
}

func parseEnvFile(text string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		i := strings.IndexByte(line, '=')
		if i <= 0 {
			continue
		}
		k := strings.ToUpper(strings.TrimSpace(line[:i]))
		v := strings.Trim(strings.TrimSpace(line[i+1:]), "\"")
		out[k] = v
	}
	return out
}

func (a *App) ControllerURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", a.store.Get().ControllerPort)
}

// syncGeo copies the bundled geo databases into the working directory.
func (a *App) syncGeo() {
	pairs := map[string]string{
		"GeoIP.dat":   "GeoIP.dat",
		"GeoSite.dat": "GeoSite.dat",
	}
	for src, dst := range pairs {
		s := filepath.Join(a.geoDir, src)
		d := filepath.Join(a.workDir, dst)
		if !fileExists(s) {
			continue
		}
		si, err1 := os.Stat(s)
		di, err2 := os.Stat(d)
		if err1 != nil {
			continue
		}
		if err2 == nil && di.Size() >= si.Size() {
			continue
		}
		data, err := os.ReadFile(s)
		if err != nil {
			continue
		}
		if err := writeFileAtomic(d, data, 0o644); err != nil {
			log.Printf("写入 %s 失败: %v", dst, err)
		}
	}
}

// ---------------------------------------------------------------------------
// Subscription handling
// ---------------------------------------------------------------------------

type parsedSub struct {
	Kind      string
	IsFull    bool
	Proxies   []map[string]any
	Raw       []byte
	NodeCount int
}

// parseSubscription understands both clash YAML configs and (base64) share-link lists.
func parseSubscription(body []byte) (*parsedSub, error) {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return nil, fmt.Errorf("订阅内容为空")
	}

	if strings.HasPrefix(trimmed, "{") || strings.Contains(trimmed, "proxies:") || strings.Contains(trimmed, "\"proxies\"") || strings.Contains(trimmed, "proxy-groups:") {
		var doc map[string]any
		if err := yaml.Unmarshal(body, &doc); err == nil && doc != nil {
			if raw, ok := doc["proxies"].([]any); ok && len(raw) > 0 {
				proxies := make([]map[string]any, 0, len(raw))
				for _, item := range raw {
					if m, ok := item.(map[string]any); ok {
						proxies = append(proxies, m)
					}
				}
				if len(proxies) > 0 {
					isFull := false
					if g, ok := doc["proxy-groups"].([]any); ok && len(g) > 0 {
						isFull = true
					}
					if r, ok := doc["rules"].([]any); ok && len(r) > 0 {
						isFull = true
					}
					return &parsedSub{
						Kind:      "clash",
						IsFull:    isFull,
						Proxies:   proxies,
						Raw:       body,
						NodeCount: len(proxies),
					}, nil
				}
			}
		}
	}

	text := trimmed
	if !strings.Contains(text, "://") {
		if dec, err := b64decode(text); err == nil {
			text = string(dec)
		}
	}

	proxies := []map[string]any{}
	var failed []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, "://") {
			continue
		}
		p, err := linkToProxy(line)
		if err != nil {
			failed = append(failed, err.Error())
			continue
		}
		proxies = append(proxies, p)
	}
	if len(proxies) == 0 {
		if len(failed) > 0 {
			return nil, fmt.Errorf("未能解析任何节点（%s）", failed[0])
		}
		return nil, fmt.Errorf("无法识别的订阅格式")
	}
	return &parsedSub{
		Kind:      "links",
		IsFull:    false,
		Proxies:   proxies,
		Raw:       []byte(text),
		NodeCount: len(proxies),
	}, nil
}

func fetchSubscription(rawURL string) ([]byte, http.Header, error) {
	body, hdr, err := httpGet(rawURL, nil)
	if err == nil {
		return body, hdr, nil
	}
	// retry once ignoring certificate errors (self hosted panels often use self-signed certs)
	client := &http.Client{
		Timeout: 90 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
		},
	}
	req, rerr := http.NewRequest(http.MethodGet, rawURL, nil)
	if rerr != nil {
		return nil, nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, rerr := client.Do(req)
	if rerr != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, resp.Header, fmt.Errorf("HTTP %d %s", resp.StatusCode, resp.Status)
	}
	buf := make([]byte, 0, 64<<10)
	tmp := make([]byte, 32<<10)
	for {
		n, e := resp.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if e != nil {
			break
		}
		if len(buf) > 64<<20 {
			break
		}
	}
	return buf, resp.Header, nil
}

func parseUserInfo(h http.Header) (up, down, total, expire int64) {
	v := strings.TrimSpace(h.Get("subscription-userinfo"))
	if v == "" {
		return
	}
	for _, part := range strings.Split(v, ";") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			continue
		}
		n, _ := strconv.ParseInt(strings.TrimSpace(kv[1]), 10, 64)
		switch strings.TrimSpace(kv[0]) {
		case "upload":
			up = n
		case "download":
			down = n
		case "total":
			total = n
		case "expire":
			expire = n
		}
	}
	return
}

// subSourcePath resolves the on-disk file backing a file/upload subscription.
// Relative names live inside the subscription cache dir, which is where files
// uploaded from the browser are stored.
func (a *App) subSourcePath(sub Sub) string {
	p := strings.TrimSpace(sub.Path)
	if p == "" {
		return filepath.Join(a.subsDir, sub.ID+".upload")
	}
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(a.subsDir, p)
}

// readSubSource returns the raw subscription payload. HTTP headers are only
// populated when the payload came from the network, so quota info from a
// local file never overwrites previously known traffic values.
func (a *App) readSubSource(sub Sub) ([]byte, http.Header, error) {
	switch sub.Source {
	case subSourceFile, subSourceUpload:
		p := a.subSourcePath(sub)
		data, err := os.ReadFile(p)
		if err != nil {
			if os.IsNotExist(err) {
				if sub.Source == subSourceUpload {
					return nil, nil, fmt.Errorf("本地订阅文件尚未上传，请在下方重新选择文件")
				}
				return nil, nil, fmt.Errorf("订阅文件不存在：%s", p)
			}
			return nil, nil, fmt.Errorf("读取订阅文件失败：%v", err)
		}
		if len(bytes.TrimSpace(data)) == 0 {
			return nil, nil, fmt.Errorf("订阅文件内容为空：%s", p)
		}
		return data, nil, nil
	default:
		if strings.TrimSpace(sub.URL) == "" {
			return nil, nil, fmt.Errorf("订阅链接为空")
		}
		return fetchSubscription(sub.URL)
	}
}

func (a *App) setSubError(id string, err error) {
	_ = a.store.Update(func(s *Settings) {
		for i := range s.Subs {
			if s.Subs[i].ID == id {
				s.Subs[i].LastError = err.Error()
			}
		}
	})
}

// UpdateSub refreshes the subscription from its configured source and rewrites
// its cached provider file.
func (a *App) UpdateSub(id string, apply bool) error {
	sub, ok := a.store.FindSub(id)
	if !ok {
		return fmt.Errorf("订阅不存在")
	}
	body, hdr, err := a.readSubSource(sub)
	if err != nil {
		a.setSubError(id, err)
		if apply {
			a.refresh(apply)
		}
		return err
	}
	if int64(len(body)) > 32<<20 {
		return fmt.Errorf("订阅内容过大")
	}

	parsed, perr := parseSubscription(body)
	if perr != nil {
		a.setSubError(id, perr)
		if apply {
			a.refresh(apply)
		}
		return perr
	}

	// keep the raw payload so "full" mode can reuse the remote config verbatim
	_ = writeFileAtomic(filepath.Join(a.subsDir, id+".yaml"), body, 0o600)

	// always export a provider file so switching modes is instant
	if err := a.writeProvider(id, parsed.Proxies); err != nil {
		return err
	}

	up, down, total, expire := parseUserInfo(hdr)
	now := time.Now().Format("2006-01-02 15:04:05")
	_ = a.store.Update(func(s *Settings) {
		for i := range s.Subs {
			if s.Subs[i].ID != id {
				continue
			}
			s.Subs[i].UpdatedAt = now
			s.Subs[i].NodeCount = parsed.NodeCount
			s.Subs[i].Format = parsed.Kind
			s.Subs[i].LastError = ""
			if hdr != nil {
				s.Subs[i].Upload = up
				s.Subs[i].Download = down
				s.Subs[i].Total = total
				s.Subs[i].Expire = expire
			}
		}
	})

	if apply {
		a.refresh(apply)
	}
	return nil
}

func (a *App) writeProvider(id string, proxies []map[string]any) error {
	doc := map[string]any{"proxies": proxies}
	data, err := yaml.Marshal(doc)
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(a.provDir, id+".yaml"), data, 0o644)
}

// ProviderFileName returns the provider file name used inside the working dir.
// providerFileName returns the on-disk name of a subscription's provider file.
// It must match the file written by saveProvider (provDir/<id>.yaml) and the
// path referenced from the generated config, otherwise mihomo loads an empty
// provider and the proxy groups end up with no nodes.
func providerFileName(id string) string { return id + ".yaml" }

// ---------------------------------------------------------------------------
// Config application
// ---------------------------------------------------------------------------

// refresh regenerates config.yaml and hot-reloads the kernel when it is running.
func (a *App) refresh(apply bool) {
	if err := a.ApplyConfig(); err != nil {
		log.Printf("生成配置失败: %v", err)
	}
	if !apply {
		return
	}
	if a.kernel.Running() {
		if err := a.reloadController(); err != nil {
			log.Printf("热重载配置失败，尝试重启内核: %v", err)
			_ = a.kernel.Restart()
		}
	} else if a.store.Get().ProxyEnabled {
		_ = a.kernel.Start()
	}
}

func (a *App) ApplyConfig() error {
	s := a.store.Get()
	data, err := buildConfig(&s, a)
	if err != nil {
		return err
	}
	return writeFileAtomic(a.cfgPath, data, 0o600)
}

func (a *App) reloadController() error {
	payload, _ := json.Marshal(map[string]string{"path": a.cfgPath})
	req, err := http.NewRequest(http.MethodPut, a.ControllerURL()+"/configs?force=true", strings.NewReader(string(payload)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.store.Get().Secret)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("内核返回 %d", resp.StatusCode)
	}
	return nil
}

func (a *App) setupProxy() {
	target, err := url.Parse(a.ControllerURL())
	if err != nil {
		log.Printf("解析控制端口失败: %v", err)
		return
	}
	p := httputil.NewSingleHostReverseProxy(target)
	original := p.Director
	p.Director = func(r *http.Request) {
		original(r)
		r.Host = target.Host
		r.Header.Set("Authorization", "Bearer "+a.store.Get().Secret)
		r.Header.Del("Cookie")
	}
	p.FlushInterval = -1
	p.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		writeErr(w, http.StatusBadGateway, "无法连接 mihomo 内核，请确认服务已启动")
	}
	a.proxy = p
}

// ---------------------------------------------------------------------------
// Convenience accessors
// ---------------------------------------------------------------------------

func (a *App) PanelDir() string     { return a.panelDir }
func (a *App) DashboardDir() string { return a.dashboardDir }
func (a *App) Store() *Store        { return a.store }
func (a *App) Kernel() *Kernel      { return a.kernel }
func (a *App) ConfigPath() string   { return a.cfgPath }
func (a *App) PanelLogPath() string { return a.panelLog }
func (a *App) WorkDir() string      { return a.workDir }
