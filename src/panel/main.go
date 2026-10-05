package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// resolveAppDest falls back to the directory above bin/ for local runs.
func resolveAppDest(v string) string {
	if v != "" {
		return v
	}
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	dir := filepath.Dir(exe)
	if filepath.Base(dir) == "bin" {
		return filepath.Dir(dir)
	}
	return dir
}

func main() {
	var (
		listen  = flag.String("listen", "", "面板监听地址，例如 0.0.0.0:9788")
		appDest = flag.String("appdest", envOr("TRIM_APPDEST", ""), "应用安装目录")
		etcDir  = flag.String("etc", envOr("TRIM_PKGETC", ""), "配置目录")
		varDir  = flag.String("var", envOr("TRIM_PKGVAR", ""), "运行时数据目录")
		showVer = flag.Bool("version", false, "打印版本号")
	)
	flag.Parse()

	if *showVer {
		fmt.Println("mihomo-panel", panelVersion)
		return
	}

	resolvedDest := resolveAppDest(*appDest)
	if *etcDir == "" || *varDir == "" {
		base := envOr("MIHOMO_PANEL_HOME", ".")
		if *etcDir == "" {
			*etcDir = filepath.Join(base, "etc")
		}
		if *varDir == "" {
			*varDir = filepath.Join(base, "var")
		}
	}

	if *listen == "" {
		port := 9788
		probe := NewStore(filepath.Join(*etcDir, "settings.json"))
		if err := probe.Load(); err == nil {
			port = probe.Get().PanelPort
		}
		*listen = fmt.Sprintf("0.0.0.0:%d", port)
	}

	logPath := filepath.Join(*varDir, "logs", "panel.log")
	_ = ensureDir(filepath.Dir(logPath))
	if lf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
		log.SetOutput(io.MultiWriter(os.Stderr, lf))
		defer lf.Close()
	}
	log.SetFlags(log.LstdFlags)

	app := NewApp(resolvedDest, *etcDir, *varDir, *listen)
	if err := app.Init(); err != nil {
		log.Fatalf("初始化失败: %v", err)
	}

	srv := &http.Server{
		Addr:              *listen,
		Handler:           app.Handler(),
		ReadHeaderTimeout: 15 * time.Second,
	}

	go func() {
		log.Printf("面板已启动 http://%s (应用目录 %s)", *listen, resolvedDest)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("监听 %s 失败: %v", *listen, err)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh
	log.Printf("收到信号 %v，正在退出…", sig)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	_ = app.Kernel().Stop()
	log.Printf("已退出")
}
