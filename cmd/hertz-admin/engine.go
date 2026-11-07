package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var errEngineExternal = errors.New("HERTZ engine is running outside HERTZ Admin and cannot be stopped from here")

// Engine owns the child HERTZ process. Keeping the engine in a child process
// makes the admin UI robust: it can restart the engine without restarting the
// tray application, and an engine panic cannot take down the admin UI.
type Engine struct {
	opMu sync.Mutex // serializes Start/Stop/Restart

	mu        sync.Mutex
	settings  Settings
	dir       string
	asset     []byte
	external  string // optional path to a development engine binary
	logger    *slog.Logger
	cmd       *exec.Cmd
	done      chan struct{}
	logFile   *os.File
	running   bool
	owned     bool
	starting  bool
	lastError string
}

func NewEngine(settings Settings, dir string, asset []byte, external string, logger *slog.Logger) *Engine {
	return &Engine{
		settings: settings,
		dir:      dir,
		asset:    asset,
		external: external,
		logger:   logger,
	}
}

func (e *Engine) SetSettings(s Settings) {
	e.mu.Lock()
	e.settings = s
	e.mu.Unlock()
}

func (e *Engine) Settings() Settings {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.settings
}

func (e *Engine) binaryPath() string {
	return filepath.Join(e.dir, "engine", "hertz-engine.exe")
}

func (e *Engine) logPath() string {
	return filepath.Join(e.dir, "logs", "hertz.log")
}

// Snapshot is a lock-protected view of the engine state for the admin UI.
type EngineSnapshot struct {
	Running   bool
	Owned     bool
	Starting  bool
	LastError string
	LogPath   string
}

func (e *Engine) Snapshot() EngineSnapshot {
	e.mu.Lock()
	defer e.mu.Unlock()
	return EngineSnapshot{
		Running:   e.running,
		Owned:     e.owned,
		Starting:  e.starting,
		LastError: e.lastError,
		LogPath:   e.logPath(),
	}
}

func (e *Engine) resolveBinary() (string, error) {
	if e.external != "" {
		abs, err := filepath.Abs(e.external)
		if err != nil {
			return "", err
		}
		if _, err := os.Stat(abs); err != nil {
			return "", fmt.Errorf("external engine %s: %w", abs, err)
		}
		return abs, nil
	}
	if len(e.asset) == 0 {
		return "", errors.New("embedded HERTZ engine is missing; build with scripts/build-admin.ps1")
	}
	path := e.binaryPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	want := sha256.Sum256(e.asset)
	if current, err := os.ReadFile(path); err == nil && sha256.Sum256(current) == want {
		return path, nil
	}

	// Windows cannot replace a running executable. Stop() is called by Start()
	// before resolveBinary, so no engine of ours should be running here.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, e.asset, 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return path, nil
}

// Start launches the engine unless it is already running. An engine that is
// already answering /healthz is adopted as externally managed instead of
// spawning a second process that would fight over the same ports.
func (e *Engine) Start() error {
	e.opMu.Lock()
	defer e.opMu.Unlock()
	return e.startLocked()
}

func (e *Engine) startLocked() error {
	e.mu.Lock()
	if e.running || e.starting {
		e.mu.Unlock()
		return nil
	}
	e.starting = true
	e.lastError = ""
	s := e.settings
	e.mu.Unlock()

	defer func() {
		e.mu.Lock()
		e.starting = false
		e.mu.Unlock()
	}()

	if e.healthy(1 * time.Second) {
		e.mu.Lock()
		e.running = true
		e.owned = false
		e.mu.Unlock()
		e.logger.Info("adopted an already-running HERTZ engine", "http_port", s.HTTPPort)
		return nil
	}

	binary, err := e.resolveBinary()
	if err != nil {
		e.setError(err)
		return err
	}
	if err := os.MkdirAll(filepath.Dir(e.logPath()), 0o700); err != nil {
		e.setError(err)
		return err
	}
	if err := rotateLog(e.logPath(), 5<<20); err != nil {
		e.logger.Warn("could not rotate engine log", "err", err)
	}
	logFile, err := os.OpenFile(e.logPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		e.setError(err)
		return err
	}

	cmd := exec.Command(binary)
	cmd.Dir = e.dir
	cmd.Env = cleanHertzEnv(os.Environ(), s.engineEnv())
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	hideProcessWindow(cmd)

	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		if e.healthy(1 * time.Second) {
			e.mu.Lock()
			e.running = true
			e.owned = false
			e.mu.Unlock()
			e.logger.Info("adopted an already-running HERTZ engine", "http_port", s.HTTPPort)
			return nil
		}
		e.setError(err)
		return err
	}
	// Keep the engine inside a kill-on-close job object so that an admin crash
	// never leaves a hidden HERTZ process holding the ports.
	if err := assignJobObject(cmd); err != nil {
		e.logger.Warn("could not assign engine to job object", "err", err)
	}

	done := make(chan struct{})
	e.mu.Lock()
	e.cmd = cmd
	e.done = done
	e.logFile = logFile
	e.running = true
	e.owned = true
	e.mu.Unlock()

	go func() {
		waitErr := cmd.Wait()
		close(done)
		e.mu.Lock()
		if e.cmd == cmd {
			e.cmd = nil
			e.running = false
			e.owned = false
			if waitErr != nil {
				e.lastError = waitErr.Error()
			}
		}
		e.mu.Unlock()
		if waitErr != nil {
			e.logger.Warn("HERTZ engine exited", "err", waitErr)
		} else {
			e.logger.Info("HERTZ engine stopped")
		}
	}()

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if e.healthy(750 * time.Millisecond) {
			e.logger.Info("HERTZ engine ready", "http_port", s.HTTPPort)
			return nil
		}
		select {
		case <-done:
			err := fmt.Errorf("HERTZ engine exited during startup; see %s", e.logPath())
			e.setError(err)
			return err
		case <-time.After(150 * time.Millisecond):
		}
	}
	err = fmt.Errorf("HERTZ engine did not answer /healthz within 15s; see %s", e.logPath())
	e.stopLocked()
	e.setError(err)
	return err
}

// Stop terminates only an engine spawned by this admin process.
func (e *Engine) Stop() error {
	e.opMu.Lock()
	defer e.opMu.Unlock()
	return e.stopLocked()
}

func (e *Engine) stopLocked() error {
	e.mu.Lock()
	cmd, done, owned, running := e.cmd, e.done, e.owned, e.running
	if !owned {
		if running {
			e.mu.Unlock()
			return errEngineExternal
		}
		e.running = false
		e.mu.Unlock()
		return nil
	}
	if cmd == nil {
		e.running = false
		e.owned = false
		e.mu.Unlock()
		return nil
	}
	e.mu.Unlock()

	_ = cmd.Process.Kill()
	if done != nil {
		select {
		case <-done:
		case <-time.After(4 * time.Second):
		}
	}
	e.mu.Lock()
	if e.cmd == cmd {
		e.cmd = nil
	}
	e.running = false
	e.owned = false
	// An intentional stop is not a failure; the wait goroutine may have
	// recorded the exit status of the killed process just before this.
	e.lastError = ""
	if e.logFile != nil {
		_ = e.logFile.Close()
		e.logFile = nil
	}
	e.mu.Unlock()
	return nil
}

// Restart stops an engine owned by this admin process and starts it again with
// the current settings. If an external engine is running, it is left alone.
func (e *Engine) Restart() error {
	e.opMu.Lock()
	defer e.opMu.Unlock()
	e.mu.Lock()
	owned, running := e.owned, e.running
	e.mu.Unlock()
	if running && !owned {
		return errEngineExternal
	}
	if owned {
		if err := e.stopLocked(); err != nil {
			return err
		}
	}
	return e.startLocked()
}

func (e *Engine) setError(err error) {
	e.mu.Lock()
	e.lastError = err.Error()
	e.mu.Unlock()
	e.logger.Error("engine lifecycle failed", "err", err)
}

func (e *Engine) healthy(timeout time.Duration) bool {
	s := e.Settings()
	url := fmt.Sprintf("http://127.0.0.1:%d/healthz", s.HTTPPort)
	client := http.Client{Timeout: timeout}
	resp, err := client.Get(url)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	return resp.StatusCode == http.StatusOK
}

// Health exposes a health probe to the admin status endpoint.
func (e *Engine) Health() bool {
	return e.healthy(900 * time.Millisecond)
}

// TailLog returns up to maxLines lines from the end of the engine log.
func (e *Engine) TailLog(maxLines int) string {
	f, err := os.Open(e.logPath())
	if err != nil {
		return ""
	}
	defer f.Close()
	const maxTail = 256 << 10
	if st, err := f.Stat(); err == nil && st.Size() > maxTail {
		_, _ = f.Seek(-maxTail, io.SeekEnd)
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxTail))
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\r\n"), "\n")
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return strings.Join(lines, "\n")
}

func rotateLog(path string, maxSize int64) error {
	st, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if st.Size() < maxSize {
		return nil
	}
	_ = os.Remove(path + ".1")
	return os.Rename(path, path+".1")
}

func cleanHertzEnv(base, hertz []string) []string {
	out := make([]string, 0, len(base)+len(hertz))
	for _, kv := range base {
		if strings.HasPrefix(kv, "HERTZ_") {
			continue
		}
		out = append(out, kv)
	}
	return append(out, hertz...)
}
