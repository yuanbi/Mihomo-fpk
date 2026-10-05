package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Kernel supervises the mihomo child process.
type Kernel struct {
	bin     string
	workDir string
	cfgPath string
	logPath string
	pidFile string

	mu           sync.Mutex
	cmd          *exec.Cmd
	desired      bool
	looping      bool
	startedAt    time.Time
	restartCount int
	lastExit     string
}

func NewKernel(bin, workDir, cfgPath, logPath, pidFile string) *Kernel {
	return &Kernel{
		bin:     bin,
		workDir: workDir,
		cfgPath: cfgPath,
		logPath: logPath,
		pidFile: pidFile,
	}
}

func (k *Kernel) BinPath() string  { return k.bin }
func (k *Kernel) ConfigPath() string { return k.cfgPath }
func (k *Kernel) LogPath() string  { return k.logPath }

// Start marks the kernel as desired-running and spins up the supervisor loop.
func (k *Kernel) Start() error {
	k.mu.Lock()
	if k.desired {
		k.mu.Unlock()
		return nil
	}
	k.desired = true
	k.startedAt = time.Now()
	needLoop := !k.looping
	if needLoop {
		k.looping = true
	}
	k.mu.Unlock()

	if needLoop {
		go k.loop()
	}
	return nil
}

// Stop terminates the kernel and prevents the supervisor from restarting it.
func (k *Kernel) Stop() error {
	k.mu.Lock()
	k.desired = false
	cmd := k.cmd
	k.mu.Unlock()

	if cmd == nil || cmd.Process == nil {
		return nil
	}
	pid := cmd.Process.Pid
	_ = signalProc(cmd, true)
	for i := 0; i < 100; i++ {
		if !processAlive(pid) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if processAlive(pid) {
		_ = signalProc(cmd, false)
		time.Sleep(500 * time.Millisecond)
	}
	return nil
}

// Restart cycles the kernel process.
func (k *Kernel) Restart() error {
	if err := k.Stop(); err != nil {
		return err
	}
	time.Sleep(500 * time.Millisecond)
	return k.Start()
}

// Running reports whether the supervised process is alive.
func (k *Kernel) Running() bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.cmd != nil && k.cmd.Process != nil && processAlive(k.cmd.Process.Pid)
}

// Desired reports the user intent (should the proxy be up).
func (k *Kernel) Desired() bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.desired
}

func (k *Kernel) Pid() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.cmd == nil || k.cmd.Process == nil {
		return 0
	}
	return k.cmd.Process.Pid
}

func (k *Kernel) Uptime() int64 {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.cmd == nil || k.startedAt.IsZero() {
		return 0
	}
	return int64(time.Since(k.startedAt).Seconds())
}

func (k *Kernel) RestartCount() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.restartCount
}

func (k *Kernel) LastExit() string {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.lastExit
}

func (k *Kernel) loop() {
	for {
		k.mu.Lock()
		if !k.desired {
			k.looping = false
			k.mu.Unlock()
			return
		}
		k.mu.Unlock()

		cmd, logFile, err := k.spawn()
		if err != nil {
			k.mu.Lock()
			k.lastExit = err.Error()
			k.mu.Unlock()
			log.Printf("mihomo 启动失败: %v", err)
			select {
			case <-time.After(5 * time.Second):
			}
			continue
		}

		waitErr := cmd.Wait()
		_ = logFile.Close()

		k.mu.Lock()
		k.cmd = nil
		if waitErr != nil {
			k.lastExit = waitErr.Error()
		} else {
			k.lastExit = "exited"
		}
		desired := k.desired
		if desired {
			k.restartCount++
			k.startedAt = time.Now()
		}
		k.mu.Unlock()
		_ = os.Remove(k.pidFile)

		if !desired {
			k.mu.Lock()
			k.looping = false
			k.mu.Unlock()
			return
		}
		log.Printf("mihomo 进程退出 (%v)，3 秒后自动重启", waitErr)
		select {
		case <-time.After(3 * time.Second):
		}
	}
}

func (k *Kernel) spawn() (*exec.Cmd, *os.File, error) {
	if !fileExists(k.bin) {
		return nil, nil, fmt.Errorf("内核文件不存在: %s", k.bin)
	}
	if err := ensureDir(k.workDir); err != nil {
		return nil, nil, err
	}
	logFile, err := os.OpenFile(k.logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, nil, err
	}
	cmd := exec.Command(k.bin, "-d", k.workDir, "-f", k.cfgPath)
	cmd.Dir = k.workDir
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = append(os.Environ(), "SAFE_PATHS=/")
	configureProc(cmd)

	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return nil, nil, err
	}

	k.mu.Lock()
	k.cmd = cmd
	k.startedAt = time.Now()
	k.mu.Unlock()

	_ = os.WriteFile(k.pidFile, []byte(strconv.Itoa(cmd.Process.Pid)), 0o644)
	log.Printf("mihomo 已启动 pid=%d", cmd.Process.Pid)
	return cmd, logFile, nil
}

// Version reports the kernel version by running `mihomo -v`.
func (k *Kernel) Version() string {
	out, err := exec.Command(k.bin, "-v").CombinedOutput()
	if err != nil {
		return ""
	}
	text := strings.TrimSpace(string(out))
	// typical: "Mihomo Meta v1.19.32 linux amd64 with go1.25.1 ..."
	if v := strings.Fields(text); len(v) >= 3 {
		for _, f := range v {
			if strings.HasPrefix(f, "v") && strings.ContainsAny(f, "0123456789") {
				return f
			}
		}
	}
	return text
}
