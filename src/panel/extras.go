package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// mirrorPrefixes are tried in order; the empty entry means "direct".
var mirrorPrefixes = []string{
	"",
	"https://gh-proxy.com/",
	"https://ghfast.top/",
	"https://gh-proxy.net/",
}

func mirrorCandidates(rawURL string) []string {
	out := make([]string, 0, len(mirrorPrefixes))
	for _, p := range mirrorPrefixes {
		out = append(out, p+rawURL)
	}
	return out
}

func downloadWithMirrors(rawURL string) ([]byte, error) {
	var lastErr error
	for _, u := range mirrorCandidates(rawURL) {
		body, _, err := httpGet(u, nil)
		if err == nil && len(body) > 0 {
			return body, nil
		}
		if err != nil {
			lastErr = err
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("下载失败")
	}
	return nil, lastErr
}

// ---------------------------------------------------------------------------
// Geo databases
// ---------------------------------------------------------------------------

var geoAssets = []struct {
	Name string
	URL  string
}{
	{"GeoIP.dat", "https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/geoip.dat"},
	{"GeoSite.dat", "https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/geosite.dat"},
}

// UpdateGeo refreshes the geo databases in the kernel working directory.
func (a *App) UpdateGeo() error {
	var errs []string
	for _, asset := range geoAssets {
		body, err := downloadWithMirrors(asset.URL)
		if err != nil {
			errs = append(errs, asset.Name+": "+err.Error())
			continue
		}
		if len(body) < 1024 {
			errs = append(errs, asset.Name+": 文件内容异常")
			continue
		}
		if err := writeFileAtomic(filepath.Join(a.workDir, asset.Name), body, 0o644); err != nil {
			errs = append(errs, asset.Name+": "+err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

// ---------------------------------------------------------------------------
// Kernel upgrade
// ---------------------------------------------------------------------------

type releaseInfo struct {
	TagName string `json:"tag_name"`
}

func kernelAssetName(tag string) string {
	arch := runtime.GOARCH
	switch arch {
	case "arm64", "aarch64":
		arch = "arm64"
	default:
		arch = "amd64"
	}
	// the "compatible" build targets the lowest CPU baseline, safest for NAS boxes
	return fmt.Sprintf("mihomo-linux-%s-compatible-%s.gz", arch, tag)
}

// LatestKernelVersion queries GitHub for the newest mihomo release tag.
func LatestKernelVersion() (string, error) {
	body, _, err := httpGet("https://api.github.com/repos/MetaCubeX/mihomo/releases/latest", map[string]string{
		"Accept": "application/vnd.github+json",
	})
	if err != nil {
		body, err = downloadWithMirrors("https://api.github.com/repos/MetaCubeX/mihomo/releases/latest")
		if err != nil {
			return "", err
		}
	}
	var info releaseInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return "", fmt.Errorf("解析版本信息失败")
	}
	if info.TagName == "" {
		return "", fmt.Errorf("未获取到版本号")
	}
	return info.TagName, nil
}

// UpgradeKernel downloads and installs the requested mihomo release.
// When tag is empty the latest release is used.
func (a *App) UpgradeKernel(tag string) (string, error) {
	if tag == "" {
		latest, err := LatestKernelVersion()
		if err != nil {
			return "", err
		}
		tag = latest
	}
	asset := kernelAssetName(tag)
	url := fmt.Sprintf("https://github.com/MetaCubeX/mihomo/releases/download/%s/%s", tag, asset)
	body, err := downloadWithMirrors(url)
	if err != nil {
		return "", fmt.Errorf("下载内核失败: %w", err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("内核压缩包无效: %w", err)
	}
	defer zr.Close()

	tmpPath := a.binPath + ".new"
	out, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(out, zr); err != nil {
		out.Close()
		_ = os.Remove(tmpPath)
		return "", err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}
	st, err := os.Stat(tmpPath)
	if err != nil || st.Size() < 1024*1024 {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("下载的内核文件异常")
	}

	_ = os.Rename(a.binPath, a.binPath+".bak")
	if err := os.Rename(tmpPath, a.binPath); err != nil {
		_ = os.Rename(a.binPath+".bak", a.binPath)
		return "", err
	}
	_ = os.Chmod(a.binPath, 0o755)

	wasRunning := a.kernel.Desired()
	_ = a.kernel.Stop()
	time.Sleep(time.Second)
	if wasRunning {
		_ = a.kernel.Start()
	}
	return tag, nil
}
