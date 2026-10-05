package main

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// App version, surfaced in the panel.
const panelVersion = "1.0.3"

var (
	kverMu  sync.Mutex
	kverVal string
	kverAt  time.Time

	upgradeMu sync.Mutex
)

func (a *App) kernelVersionCached() string {
	kverMu.Lock()
	defer kverMu.Unlock()
	if kverVal != "" && time.Since(kverAt) < 5*time.Minute {
		return kverVal
	}
	if v := a.kernel.Version(); v != "" {
		kverVal = v
		kverAt = time.Now()
	}
	return kverVal
}

// Handler builds the HTTP routing table.
func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeOK(w, map[string]any{"version": panelVersion})
	})
	mux.HandleFunc("GET /api/status", a.handleStatus)
	mux.HandleFunc("GET /api/settings", a.handleGetSettings)
	mux.HandleFunc("PUT /api/settings", a.handlePutSettings)
	mux.HandleFunc("POST /api/settings", a.handlePutSettings)

	mux.HandleFunc("GET /api/subs", a.handleListSubs)
	mux.HandleFunc("POST /api/subs", a.handleAddSub)
	mux.HandleFunc("PUT /api/subs/{id}", a.handleEditSub)
	mux.HandleFunc("DELETE /api/subs/{id}", a.handleDeleteSub)
	mux.HandleFunc("POST /api/subs/{id}/update", a.handleRefreshSub)
	mux.HandleFunc("POST /api/subs/{id}/activate", a.handleActivateSub)

	mux.HandleFunc("GET /api/nodes", a.handleNodes)

	mux.HandleFunc("POST /api/kernel/{action}", a.handleKernelAction)
	mux.HandleFunc("GET /api/kernel/version", a.handleKernelVersion)
	mux.HandleFunc("GET /api/kernel/check-update", a.handleKernelCheckUpdate)
	mux.HandleFunc("POST /api/kernel/upgrade", a.handleKernelUpgrade)
	mux.HandleFunc("GET /api/logs", a.handleLogs)
	mux.HandleFunc("POST /api/geo/update", a.handleGeoUpdate)
	mux.HandleFunc("GET /api/config", a.handleConfigDump)

	mux.HandleFunc("POST /api/login", a.handleLogin)
	mux.HandleFunc("POST /api/logout", a.handleLogout)

	// GET patterns also match HEAD requests in Go 1.22+ ServeMux.
	// /dashboard 必须单独注册：只注册 "/dashboard/" 时 ServeMux 会补发一个
	// 302 到 "/dashboard/"，而那个 Location 是相对根路径的，在飞牛 CGI 同源
	// 网关（/cgi/ThirdParty/.../index.cgi/dashboard）下会跳出应用目录造成 404。
	mux.HandleFunc("GET /dashboard", a.handleDashboard)
	mux.HandleFunc("GET /dashboard/", a.handleDashboard)
	// 兜底路由必须覆盖全部方法：内置面板要调用 mihomo 的
	// PATCH /proxies/{name}（切换节点）、PUT /configs（重载）、DELETE /connections
	// 等写接口，仅注册 "GET /" 会让这些请求落到 404。
	mux.HandleFunc("/", a.handleRoot)

	return a.withAuth(mux)
}

const authCookie = "mihomo_panel_auth"

func (a *App) sessionToken(pw string) string {
	s := a.store.Get()
	sum := sha256.Sum256([]byte("mihomo-fpk|" + pw + "|" + s.Secret))
	return hex.EncodeToString(sum[:])
}

func (a *App) authorized(r *http.Request, pw string) bool {
	c, err := r.Cookie(authCookie)
	if err != nil || c.Value == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(c.Value), []byte(a.sessionToken(pw))) == 1
}

// wantsHTML reports whether the request is a browser page navigation rather than
// an XHR/fetch call or a sub-resource fetch.
func wantsHTML(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	if r.Header.Get("Sec-Fetch-Mode") == "navigate" {
		return true
	}
	accept := r.Header.Get("Accept")
	return strings.Contains(accept, "text/html")
}

// withAuth enforces the optional access password on every route.
func (a *App) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pw := a.store.Get().AccessPassword
		if pw == "" {
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/api/login" || r.URL.Path == "/api/logout" {
			next.ServeHTTP(w, r)
			return
		}
		if a.authorized(r, pw) {
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") || !wantsHTML(r) {
			// 内核 API、XHR 与静态子资源一律返回 401 JSON。若给它们返回
			// login.html，MetaCubeXD 会把 HTML 当 JSON 解析，表现为「无法连接后端」。
			writeErr(w, http.StatusUnauthorized, "未登录或登录已过期")
			return
		}
		page := filepath.Join(a.panelDir, "login.html")
		if fileExists(page) {
			w.Header().Set("Cache-Control", "no-store")
			serveHTMLFile(w, r, page, basePath(r)+"/")
			return
		}
		http.Error(w, "需要登录", http.StatusUnauthorized)
	})
}

func (a *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求内容无效")
		return
	}
	pw := a.store.Get().AccessPassword
	if pw == "" {
		writeOK(w, nil)
		return
	}
	if subtle.ConstantTimeCompare([]byte(req.Password), []byte(pw)) != 1 {
		writeErr(w, http.StatusUnauthorized, "密码错误")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     authCookie,
		Value:    a.sessionToken(pw),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   7 * 24 * 3600,
	})
	writeOK(w, nil)
}

func (a *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     authCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
	writeOK(w, nil)
}

// ---------------------------------------------------------------------------
// status & settings
// ---------------------------------------------------------------------------

func (a *App) handleStatus(w http.ResponseWriter, r *http.Request) {
	s := a.store.Get()
	k := a.kernel

	sub, hasSub := activeSub(&s)
	active := map[string]any{"configured": hasSub}
	if hasSub {
		active["id"] = sub.ID
		active["name"] = sub.Name
		active["mode"] = sub.Mode
		active["nodeCount"] = sub.NodeCount
		active["updatedAt"] = sub.UpdatedAt
		active["lastError"] = sub.LastError
		active["upload"] = sub.Upload
		active["download"] = sub.Download
		active["total"] = sub.Total
		active["expire"] = sub.Expire
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true,
		"panel": map[string]any{
			"version": panelVersion,
			"uptime":  int64(time.Since(a.startedAt).Seconds()),
			"listen":  a.listen,
		},
		"kernel": map[string]any{
			"running":      k.Running(),
			"desired":      k.Desired(),
			"pid":          k.Pid(),
			"version":      a.kernelVersionCached(),
			"uptime":       k.Uptime(),
			"restartCount": k.RestartCount(),
			"lastExit":     k.LastExit(),
			"logPath":      k.LogPath(),
		},
		"ports": map[string]any{
			"mixed":      s.MixedPort,
			"controller": s.ControllerPort,
			"panel":      s.PanelPort,
		},
		"settings": map[string]any{
			"mode":          s.Mode,
			"logLevel":      s.LogLevel,
			"allowLan":      s.AllowLan,
			"proxyEnabled":  s.ProxyEnabled,
			"unifiedDelay":  s.UnifiedDelay,
			"tcpConcurrent": s.TCPConcurrent,
			"snifferEnable": s.SnifferEnable,
			"tun":           s.Tun,
			"dns":           s.DNS,
			"customRules":   s.CustomRules,
			"secret":        s.Secret,
			"hasPassword":   s.AccessPassword != "",
		},
		"active": active,
		"sys": map[string]any{
			"lanIp":   lanIPv4(),
			"workDir": a.workDir,
			"etcDir":  a.etc,
		},
	})
}

// handleNodes lists the nodes of the currently active subscription.
func (a *App) handleNodes(w http.ResponseWriter, r *http.Request) {
	type node struct {
		Name   string `json:"name"`
		Type   string `json:"type"`
		Server string `json:"server"`
		Port   int    `json:"port"`
	}

	s := a.store.Get()
	sub, ok := activeSub(&s)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "nodes": []node{}})
		return
	}

	candidates := []string{
		filepath.Join(a.provDir, sub.ID+".yaml"),
		filepath.Join(a.subsDir, sub.ID+".yaml"),
	}
	var data []byte
	for _, p := range candidates {
		if b, err := os.ReadFile(p); err == nil && len(b) > 0 {
			data = b
			break
		}
	}
	if data == nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "nodes": []node{}})
		return
	}

	var doc struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		writeErr(w, http.StatusInternalServerError, "节点文件解析失败")
		return
	}

	out := make([]node, 0, len(doc.Proxies))
	for _, p := range doc.Proxies {
		out = append(out, node{
			Name:   toStr(p["name"]),
			Type:   toStr(p["type"]),
			Server: toStr(p["server"]),
			Port:   toInt(p["port"]),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "nodes": out})
}

func (a *App) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "settings": a.store.Get()})
}

type settingsUpdate struct {
	MixedPort      *int          `json:"mixedPort"`
	ControllerPort *int          `json:"controllerPort"`
	AllowLan       *bool         `json:"allowLan"`
	Mode           *string       `json:"mode"`
	LogLevel       *string       `json:"logLevel"`
	Secret         *string       `json:"secret"`
	ProxyEnabled   *bool         `json:"proxyEnabled"`
	UnifiedDelay   *bool         `json:"unifiedDelay"`
	TCPConcurrent  *bool         `json:"tcpConcurrent"`
	SnifferEnable  *bool         `json:"snifferEnable"`
	CustomRules    *[]string    `json:"customRules"`
	Tun            *TunSettings `json:"tun"`
	DNS            *DNSSettings `json:"dns"`
	AccessPassword *string      `json:"accessPassword"`
}

func (a *App) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	var upd settingsUpdate
	if err := decodeBody(r, &upd); err != nil {
		writeErr(w, http.StatusBadRequest, "请求内容无效: "+err.Error())
		return
	}
	before := a.store.Get()

	if err := a.store.Update(func(s *Settings) {
		if upd.MixedPort != nil {
			s.MixedPort = *upd.MixedPort
		}
		if upd.ControllerPort != nil {
			s.ControllerPort = *upd.ControllerPort
		}
		if upd.AllowLan != nil {
			s.AllowLan = *upd.AllowLan
		}
		if upd.Mode != nil {
			s.Mode = *upd.Mode
		}
		if upd.LogLevel != nil {
			s.LogLevel = *upd.LogLevel
		}
		if upd.Secret != nil && strings.TrimSpace(*upd.Secret) != "" {
			s.Secret = strings.TrimSpace(*upd.Secret)
		}
		if upd.ProxyEnabled != nil {
			s.ProxyEnabled = *upd.ProxyEnabled
		}
		if upd.UnifiedDelay != nil {
			s.UnifiedDelay = *upd.UnifiedDelay
		}
		if upd.TCPConcurrent != nil {
			s.TCPConcurrent = *upd.TCPConcurrent
		}
		if upd.SnifferEnable != nil {
			s.SnifferEnable = *upd.SnifferEnable
		}
		if upd.CustomRules != nil {
			s.CustomRules = *upd.CustomRules
		}
		if upd.Tun != nil {
			s.Tun = *upd.Tun
		}
		if upd.DNS != nil {
			s.DNS = *upd.DNS
		}
		if upd.AccessPassword != nil {
			s.AccessPassword = strings.TrimSpace(*upd.AccessPassword)
		}
	}); err != nil {
		writeErr(w, http.StatusInternalServerError, "保存设置失败: "+err.Error())
		return
	}

	after := a.store.Get()
	a.setupProxy()

	if err := a.ApplyConfig(); err != nil {
		writeErr(w, http.StatusInternalServerError, "生成配置失败: "+err.Error())
		return
	}

	needsRestart := before.MixedPort != after.MixedPort ||
		before.ControllerPort != after.ControllerPort ||
		!tunEqual(before.Tun, after.Tun) ||
		before.DNS.Listen != after.DNS.Listen ||
		before.DNS.Enable != after.DNS.Enable ||
		before.AllowLan != after.AllowLan

	switch {
	case !after.ProxyEnabled:
		_ = a.kernel.Stop()
	case !before.ProxyEnabled && after.ProxyEnabled:
		_ = a.kernel.Start()
	case a.kernel.Running() && needsRestart:
		_ = a.kernel.Restart()
	case a.kernel.Running():
		if err := a.reloadController(); err != nil {
			log.Printf("热重载失败: %v", err)
		}
	}

	// keep the current session usable right after enabling a password
	if after.AccessPassword != "" {
		http.SetCookie(w, &http.Cookie{
			Name:     authCookie,
			Value:    a.sessionToken(after.AccessPassword),
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   7 * 24 * 3600,
		})
	}

	writeOK(w, map[string]any{"settings": after})
}

// ---------------------------------------------------------------------------
// subscriptions
// ---------------------------------------------------------------------------

// maxUploadSize caps browser-uploaded subscription files (32 MiB).
const maxUploadSize = 32 << 20

// subRequest is the payload of a add/edit subscription call. It accepts both
// a JSON body (link or NAS path) and multipart/form-data (uploaded file).
type subRequest struct {
	Name string `json:"name"`
	Mode string `json:"mode"`
	// Source is one of url / file / upload. Empty means "infer": a URL implies
	// url, an attached file implies upload, otherwise keep the current value.
	Source   string `json:"source"`
	URL      string `json:"url"`
	Path     string `json:"path"`
	Upload   []byte `json:"-"`
	FileName string `json:"-"`
}

func decodeSubRequest(r *http.Request) (subRequest, error) {
	var req subRequest
	ct := strings.ToLower(r.Header.Get("Content-Type"))
	if strings.HasPrefix(ct, "multipart/form-data") {
		if err := r.ParseMultipartForm(maxUploadSize); err != nil {
			return req, fmt.Errorf("解析上传内容失败")
		}
		if r.MultipartForm != nil {
			defer r.MultipartForm.RemoveAll()
		}
		req.Name = r.FormValue("name")
		req.Mode = r.FormValue("mode")
		req.Source = r.FormValue("source")
		req.URL = r.FormValue("url")
		req.Path = r.FormValue("path")

		file, hdr, ferr := r.FormFile("file")
		if ferr == nil {
			defer file.Close()
			data, rerr := io.ReadAll(io.LimitReader(file, maxUploadSize+1))
			if rerr != nil {
				return req, fmt.Errorf("读取上传文件失败")
			}
			if len(data) > maxUploadSize {
				return req, fmt.Errorf("文件过大（上限 32 MB）")
			}
			req.Upload = data
			req.FileName = hdr.Filename
		} else if normalizeSubSource(req.Source) == subSourceUpload {
			return req, fmt.Errorf("没有收到上传的文件")
		}
		if req.Source == "" {
			req.Source = subSourceUpload
		}
		return req, nil
	}
	if err := decodeBody(r, &req); err != nil {
		return req, err
	}
	return req, nil
}

// resolveSubSource normalises the requested source and validates its payload.
// On edit, an unspecified source keeps whatever the subscription already uses.
func resolveSubSource(req subRequest, cur Sub, isEdit bool) (source, rawURL, subPath string, err error) {
	source = normalizeSubSource(req.Source)
	rawURL = strings.TrimSpace(req.URL)
	subPath = strings.TrimSpace(req.Path)

	if source == "" {
		switch {
		case isEdit:
			source = cur.Source
		case rawURL != "":
			source = subSourceURL
		case subPath != "":
			source = subSourceFile
		case len(req.Upload) > 0:
			source = subSourceUpload
		}
	}

	switch source {
	case subSourceURL:
		if rawURL == "" {
			return "", "", "", fmt.Errorf("请填写订阅链接")
		}
		if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
			return "", "", "", fmt.Errorf("订阅链接必须以 http:// 或 https:// 开头")
		}
		subPath = ""
	case subSourceFile:
		if subPath == "" {
			return "", "", "", fmt.Errorf("请填写 NAS 上的订阅文件路径")
		}
		if strings.HasPrefix(subPath, "http://") || strings.HasPrefix(subPath, "https://") {
			return "", "", "", fmt.Errorf("订阅文件路径不能是网址，请把来源改为「订阅链接」")
		}
		rawURL = ""
	case subSourceUpload:
		rawURL = ""
		subPath = ""
	default:
		return "", "", "", fmt.Errorf("请选择订阅来源")
	}
	return source, rawURL, subPath, nil
}

// saveUpload stores a browser-uploaded subscription file in the cache dir.
// The file name is derived from the subscription id, never from user input.
func (a *App) saveUpload(id string, data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("上传的文件为空")
	}
	if len(data) > maxUploadSize {
		return fmt.Errorf("文件过大（上限 32 MB）")
	}
	return writeFileAtomic(filepath.Join(a.subsDir, id+".upload"), data, 0o600)
}

func (a *App) removeSubFiles(id string) {
	_ = removeQuietly(filepath.Join(a.provDir, id+".yaml"))
	_ = removeQuietly(filepath.Join(a.subsDir, id+".yaml"))
	_ = removeQuietly(filepath.Join(a.subsDir, id+".upload"))
}

func (a *App) handleListSubs(w http.ResponseWriter, r *http.Request) {
	s := a.store.Get()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "subs": s.Subs, "activeSub": s.ActiveSub})
}

func (a *App) handleAddSub(w http.ResponseWriter, r *http.Request) {
	req, err := decodeSubRequest(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "请求内容无效："+err.Error())
		return
	}
	source, rawURL, subPath, verr := resolveSubSource(req, Sub{}, false)
	if verr != nil {
		writeErr(w, http.StatusBadRequest, verr.Error())
		return
	}
	if req.Mode != "full" {
		req.Mode = "nodes"
	}
	id := randomHex(6)
	name := sanitizeName(req.Name)
	if name == "未命名" && req.FileName != "" {
		base := filepath.Base(req.FileName)
		name = sanitizeName(strings.TrimSuffix(base, filepath.Ext(base)))
	}
	if name == "未命名" {
		name = "订阅 " + id[:4]
	}

	if source == subSourceUpload {
		if err := a.saveUpload(id, req.Upload); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	err = a.store.Update(func(s *Settings) {
		s.Subs = append(s.Subs, Sub{
			ID:     id,
			Name:   name,
			Source: source,
			URL:    rawURL,
			Path:   subPath,
			Mode:   req.Mode,
		})
		if s.ActiveSub == "" {
			s.ActiveSub = id
		}
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "保存失败: "+err.Error())
		return
	}

	updateErr := a.UpdateSub(id, true)
	resp := map[string]any{"id": id}
	if updateErr != nil {
		resp["warning"] = updateErr.Error()
	}
	writeOK(w, resp)
}

func (a *App) handleEditSub(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	cur, ok := a.store.FindSub(id)
	if !ok {
		writeErr(w, http.StatusNotFound, "订阅不存在")
		return
	}
	req, err := decodeSubRequest(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "请求内容无效："+err.Error())
		return
	}
	source, rawURL, subPath, verr := resolveSubSource(req, cur, true)
	if verr != nil {
		writeErr(w, http.StatusBadRequest, verr.Error())
		return
	}
	if req.Mode != "full" {
		req.Mode = "nodes"
	}
	if source == subSourceUpload && len(req.Upload) > 0 {
		if err := a.saveUpload(id, req.Upload); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	name := sanitizeName(req.Name)
	if name == "未命名" {
		name = cur.Name
	}

	changed := source != cur.Source || rawURL != cur.URL || subPath != cur.Path
	_ = a.store.Update(func(s *Settings) {
		for i := range s.Subs {
			if s.Subs[i].ID != id {
				continue
			}
			s.Subs[i].Name = name
			s.Subs[i].Source = source
			s.Subs[i].URL = rawURL
			s.Subs[i].Path = subPath
			s.Subs[i].Mode = req.Mode
		}
	})

	if changed || len(req.Upload) > 0 {
		if err := a.UpdateSub(id, true); err != nil {
			writeOK(w, map[string]any{"warning": err.Error()})
			return
		}
	} else {
		a.refresh(true)
	}
	writeOK(w, nil)
}

func (a *App) handleDeleteSub(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := a.store.FindSub(id); !ok {
		writeErr(w, http.StatusNotFound, "订阅不存在")
		return
	}
	_ = a.store.Update(func(s *Settings) {
		kept := s.Subs[:0]
		for _, sub := range s.Subs {
			if sub.ID != id {
				kept = append(kept, sub)
			}
		}
		s.Subs = append([]Sub(nil), kept...)
		if s.ActiveSub == id {
			s.ActiveSub = ""
			if len(s.Subs) > 0 {
				s.ActiveSub = s.Subs[0].ID
			}
		}
	})
	a.removeSubFiles(id)
	a.refresh(true)
	writeOK(w, nil)
}

func (a *App) handleRefreshSub(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := a.UpdateSub(id, true); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	sub, _ := a.store.FindSub(id)
	writeOK(w, map[string]any{"sub": sub})
}

func (a *App) handleActivateSub(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := a.store.FindSub(id); !ok {
		writeErr(w, http.StatusNotFound, "订阅不存在")
		return
	}
	_ = a.store.Update(func(s *Settings) { s.ActiveSub = id })
	a.refresh(true)
	writeOK(w, nil)
}

// ---------------------------------------------------------------------------
// kernel control
// ---------------------------------------------------------------------------

func (a *App) handleKernelAction(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	switch action {
	case "start":
		_ = a.store.Update(func(s *Settings) { s.ProxyEnabled = true })
		if err := a.ApplyConfig(); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := a.kernel.Start(); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	case "stop":
		_ = a.store.Update(func(s *Settings) { s.ProxyEnabled = false })
		if err := a.kernel.Stop(); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	case "restart":
		_ = a.store.Update(func(s *Settings) { s.ProxyEnabled = true })
		if err := a.ApplyConfig(); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := a.kernel.Restart(); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	case "reload":
		if err := a.ApplyConfig(); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if a.kernel.Running() {
			if err := a.reloadController(); err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
	default:
		writeErr(w, http.StatusBadRequest, "未知操作: "+action)
		return
	}
	writeOK(w, map[string]any{"running": a.kernel.Running()})
}

func (a *App) handleKernelVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": a.kernelVersionCached()})
}

func (a *App) handleKernelCheckUpdate(w http.ResponseWriter, r *http.Request) {
	latest, err := LatestKernelVersion()
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	current := a.kernelVersionCached()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"current":   current,
		"latest":    latest,
		"hasUpdate": current == "" || !strings.Contains(current, strings.TrimPrefix(latest, "v")),
	})
}

func (a *App) handleKernelUpgrade(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Tag string `json:"tag"`
	}
	_ = decodeBody(r, &req)

	if !upgradeMu.TryLock() {
		writeErr(w, http.StatusConflict, "已有升级任务正在进行")
		return
	}
	defer upgradeMu.Unlock()

	tag, err := a.UpgradeKernel(strings.TrimSpace(req.Tag))
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	kverMu.Lock()
	kverVal = ""
	kverAt = time.Time{}
	kverMu.Unlock()

	writeOK(w, map[string]any{"tag": tag, "version": a.kernelVersionCached()})
}

func (a *App) handleGeoUpdate(w http.ResponseWriter, r *http.Request) {
	if err := a.UpdateGeo(); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	if a.kernel.Running() {
		_ = a.kernel.Restart()
	}
	writeOK(w, nil)
}

func (a *App) handleLogs(w http.ResponseWriter, r *http.Request) {
	lines := 300
	if v := r.URL.Query().Get("lines"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 5000 {
			lines = n
		}
	}
	source := r.URL.Query().Get("source")
	file := a.logPath
	if source == "panel" {
		file = a.panelLog
	}
	out, err := tailFile(file, lines)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "lines": []string{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "lines": out})
}

func (a *App) handleConfigDump(w http.ResponseWriter, r *http.Request) {
	data, err := readFileString(a.cfgPath)
	if err != nil {
		writeErr(w, http.StatusNotFound, "配置文件不存在")
		return
	}
	secret := a.store.Get().Secret
	if secret != "" {
		data = strings.ReplaceAll(data, secret, "******")
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "config": data})
}

// ---------------------------------------------------------------------------
// static assets & API proxy
// ---------------------------------------------------------------------------

func (a *App) handleRoot(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if p == "" || p == "." {
		a.servePanel(w, r, "index.html")
		return
	}
	if strings.Contains(p, "..") {
		http.NotFound(w, r)
		return
	}
	full := filepath.Join(a.panelDir, filepath.FromSlash(p))
	if fileExists(full) {
		a.servePanel(w, r, full)
		return
	}
	a.proxyTo(w, r, r.URL.Path)
}

// basePath returns the external mount prefix when the panel is reached through
// the fnOS same-origin CGI gateway (/cgi/ThirdParty/<appname>/index.cgi).
// An empty result means the panel is being accessed directly on its own port.
func basePath(r *http.Request) string {
	p := strings.TrimSpace(r.Header.Get("X-Forwarded-Prefix"))
	if p == "" || p == "/" {
		return ""
	}
	return "/" + strings.Trim(p, "/")
}

// injectBase rewrites a served HTML document so every relative URL inside it
// resolves under the panel's real mount point. Without this, the same
// dashboard would ask the fnOS origin for /_nuxt/... (404) and the console would
// post to /api/... which belongs to fnOS, not to us.
//
// dir is the document's own directory, including the trailing slash
// ("" prefix -> "/" for the console, "/dashboard/" for MetaCubeXD).
func injectBase(page []byte, dir string) []byte {
	if !bytes.Contains(page, []byte("<head>")) {
		return page
	}
	out := bytes.Replace(page, []byte("<head>"),
		[]byte("<head><base href=\""+dir+"\">"), 1)

	// MetaCubeXD 的后端地址来自内联的 Nuxt payload，它在 config.js 之后执行，
	// 会覆盖引导脚本写入的值，所以这里一并改成按 <base> 推算。
	out = bytes.ReplaceAll(out, []byte("defaultBackendURL:location.origin"),
		[]byte("defaultBackendURL:(window.__MIHOMO_BASE__||location.origin)"))
	return out
}

// serveHTMLFile serves an HTML page with the mount-point fix-up applied.
func serveHTMLFile(w http.ResponseWriter, r *http.Request, full, dir string) {
	page, err := os.ReadFile(full)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	page = injectBase(page, dir)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(page)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(page)
	}
}

func (a *App) servePanel(w http.ResponseWriter, r *http.Request, name string) {
	full := name
	if !filepath.IsAbs(full) {
		full = filepath.Join(a.panelDir, filepath.FromSlash(full))
	}
	if strings.HasSuffix(strings.ToLower(full), ".html") {
		w.Header().Set("Cache-Control", "no-store")
		serveHTMLFile(w, r, full, basePath(r)+"/")
		return
	}
	http.ServeFile(w, r, full)
}

func (a *App) handleDashboard(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/dashboard")
	rest = strings.TrimPrefix(rest, "/")
	if rest == "" {
		rest = "index.html"
	}
	if strings.Contains(rest, "..") {
		http.NotFound(w, r)
		return
	}
	full := filepath.Join(a.dashboardDir, filepath.FromSlash(rest))
	if fileExists(full) {
		a.serveDashboard(w, r, full)
		return
	}
	if !strings.Contains(path.Base(rest), ".") {
		idx := filepath.Join(a.dashboardDir, "index.html")
		if fileExists(idx) {
			a.serveDashboard(w, r, idx)
			return
		}
	}
	// metacubexd may resolve its API base against the document directory
	a.proxyTo(w, r, "/"+rest)
}

// serveDashboard serves a file from the bundled MetaCubeXD distribution.
// Entry HTML and config.js must never be cached: config.js carries the
// same-origin backend hint that keeps the panel connected.
func (a *App) serveDashboard(w http.ResponseWriter, r *http.Request, full string) {
	base := filepath.Base(full)
	if strings.HasSuffix(base, ".html") || base == "config.js" || base == "sw.js" {
		w.Header().Set("Cache-Control", "no-store, must-revalidate")
	}
	if strings.HasSuffix(strings.ToLower(base), ".html") {
		serveHTMLFile(w, r, full, basePath(r)+"/dashboard/")
		return
	}
	http.ServeFile(w, r, full)
}

func (a *App) proxyTo(w http.ResponseWriter, r *http.Request, newPath string) {
	if a.proxy == nil {
		writeErr(w, http.StatusBadGateway, "内核代理未就绪")
		return
	}
	req := r.Clone(r.Context())
	u := *r.URL
	u.Path = newPath
	u.RawPath = ""
	req.URL = &u
	a.proxy.ServeHTTP(w, req)
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func decodeBody(r *http.Request, v any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(io.LimitReader(r.Body, 4<<20))
	return dec.Decode(v)
}

func readFileString(p string) (string, error) {
	data, err := readFileBytes(p)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
