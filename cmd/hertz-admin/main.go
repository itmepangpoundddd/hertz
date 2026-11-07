// Command hertz-admin is the double-clickable HERTZ for Windows. It embeds the
// HERTZ engine, supervises it as a child process, and serves a local admin UI.
//
// First launch: console stays visible, the browser opens the setup wizard.
// Later launches: the console is hidden and the app lives in the system tray.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/getlantern/systray"

	"hertz/internal/version"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	if err := run(logger); err != nil {
		logger.Error("HERTZ Admin stopped", "err", err)
		showConsole()
		messageBox("HERTZ", err.Error(), true)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	configureConsole()
	settings, err := loadSettings()
	if err != nil {
		return fmt.Errorf("could not read settings: %w", err)
	}
	firstRun := !settings.SetupComplete
	noTray := envBool("HERTZ_ADMIN_NO_TRAY")
	noBrowser := envBool("HERTZ_ADMIN_NO_BROWSER")

	if !noTray {
		if firstRun {
			showConsole()
		} else {
			hideConsole()
		}
	}

	// A second double-click must not fight over port 8090 or spawn a second
	// engine. Reuse the running admin and bring its UI to the front.
	if existing, err := detectRunningAdmin(); existing != "" {
		logger.Info("HERTZ Admin is already running", "url", existing)
		if !noBrowser {
			_ = openBrowser(existing)
		}
		return nil
	} else if err != nil {
		return fmt.Errorf("port %d is in use by another program; close it and try again", adminPort())
	}

	app := NewApp(settings, version.Version, logger)
	if err := app.StartServer(); err != nil {
		if errors.Is(err, syscall.EADDRINUSE) || strings.Contains(err.Error(), "Only one usage") {
			return fmt.Errorf("port %d is already in use; close the program using it and try again", adminPort())
		}
		return fmt.Errorf("could not start the admin page: %w", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		app.Shutdown(ctx)
	}()

	if firstRun {
		showConsole()
		printFirstRunBanner(app)
		if !noBrowser {
			if err := openBrowser(app.adminURL() + "/setup"); err != nil {
				logger.Warn("could not open the browser automatically", "err", err)
			}
		}

		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		defer signal.Stop(sig)
		select {
		case <-app.SetupDone():
			logger.Info("first-run setup complete")
		case s := <-sig:
			logger.Info("shutdown requested during setup", "signal", s.String())
			return nil
		}
	} else if noTray {
		// Headless mode is used by tests and advanced operators. Start the
		// engine before waiting so the status endpoint is useful immediately.
		if err := app.engine.Start(); err != nil {
			logger.Error("engine did not start", "err", err)
		}
	}

	if noTray {
		logger.Info("HERTZ Admin running without tray", "url", app.adminURL())
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
		logger.Info("shutdown requested")
		return nil
	}

	// First run: setup already started the engine. Later runs start it from the
	// tray callback so the icon appears immediately.
	hideConsole()
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
		systray.Quit()
	}()

	tray := &trayApp{app: app, logger: logger}
	systray.Run(tray.onReady, tray.onExit)
	return nil
}

func printFirstRunBanner(app *App) {
	fmt.Println()
	fmt.Println("  HERTZ — Live audio streaming")
	fmt.Println("  ─────────────────────────────────────────")
	fmt.Printf("  Admin page: %s\n", app.adminURL())
	fmt.Println()
	fmt.Println("  Your browser will open the first-run setup page automatically.")
	fmt.Println("  If it does not, copy the link above into your browser.")
	fmt.Println()
	fmt.Println("  After setup, this window hides and HERTZ keeps running in the system tray.")
	fmt.Println("  Quit from the HERTZ icon next to the clock.")
	fmt.Println()
}

// detectRunningAdmin returns the dashboard URL when another HERTZ Admin owns the
// loopback admin port. The returned error is non-nil only when a foreign process
// answered at that port.
func detectRunningAdmin() (string, error) {
	client := http.Client{Timeout: 800 * time.Millisecond}
	resp, err := client.Get("http://" + adminAddr() + "/healthz")
	if err != nil {
		return "", nil
	}
	defer resp.Body.Close()
	var body struct {
		App           string `json:"app"`
		SetupComplete bool   `json:"setup_complete"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil || body.App != "hertz-admin" {
		return "", errors.New("another program is using the admin port")
	}
	path := "/"
	if !body.SetupComplete {
		path = "/setup"
	}
	host := adminAddr()
	if strings.HasPrefix(host, "0.0.0.0:") || strings.HasPrefix(host, ":") {
		_, port, err := net.SplitHostPort(host)
		if err == nil {
			host = net.JoinHostPort("127.0.0.1", port)
		}
	}
	return "http://" + host + path, nil
}

func envBool(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
