package main

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/getlantern/systray"
)

// trayApp is the long-running Windows tray presence. The menu stays simple on
// purpose: the browser dashboard does the heavy lifting.
type trayApp struct {
	app    *App
	logger *slog.Logger

	mStatus    *systray.MenuItem
	mListeners *systray.MenuItem
	mOpen      *systray.MenuItem
	mCopy      *systray.MenuItem
	mSettings  *systray.MenuItem
	mPower     *systray.MenuItem
	mRestart   *systray.MenuItem
	mLogs      *systray.MenuItem
	mQuit      *systray.MenuItem

	refreshMu sync.Mutex
}

func (t *trayApp) onReady() {
	if icon, err := uiFS.ReadFile("ui/icon.ico"); err == nil {
		systray.SetIcon(icon)
	}
	systray.SetTooltip("HERTZ — Live audio streaming")

	t.mStatus = systray.AddMenuItem("Status: Starting…", "")
	t.mListeners = systray.AddMenuItem("Listeners: 0", "")
	systray.AddSeparator()
	t.mOpen = systray.AddMenuItem("Open dashboard", "Open the admin web page in your browser")
	t.mCopy = systray.AddMenuItem("Copy listener link", "Copy a link to share with listeners")
	t.mSettings = systray.AddMenuItem("Settings", "Edit ports and addresses")
	systray.AddSeparator()
	t.mPower = systray.AddMenuItem("Stop engine", "Start or stop the HERTZ engine")
	t.mRestart = systray.AddMenuItem("Restart engine", "Restart the HERTZ engine")
	systray.AddSeparator()
	t.mLogs = systray.AddMenuItem("Open log folder", "")
	t.mQuit = systray.AddMenuItem("Quit", "Stop HERTZ and remove the tray icon")

	go t.loop()
	go func() {
		if err := t.app.engine.Start(); err != nil {
			t.logger.Error("engine did not start", "err", err)
		}
		t.refresh()
	}()
}

func (t *trayApp) loop() {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-t.mOpen.ClickedCh:
			if err := openBrowser(t.app.adminURL() + "/"); err != nil {
				t.logger.Warn("open dashboard failed", "err", err)
			}
		case <-t.mSettings.ClickedCh:
			if err := openBrowser(t.app.adminURL() + "/settings"); err != nil {
				t.logger.Warn("open settings failed", "err", err)
			}
		case <-t.mCopy.ClickedCh:
			t.copyListenerLink()
		case <-t.mPower.ClickedCh:
			go t.togglePower()
		case <-t.mRestart.ClickedCh:
			go func() {
				if err := t.app.engine.Restart(); err != nil {
					messageBox("HERTZ", friendlyEngineError(err), true)
				}
				t.refresh()
			}()
		case <-t.mLogs.ClickedCh:
			dir := filepath.Dir(t.app.engine.logPath())
			if err := openFolder(dir); err != nil {
				t.logger.Warn("open log folder failed", "err", err)
			}
		case <-t.mQuit.ClickedCh:
			systray.Quit()
			return
		case <-ticker.C:
			go t.refresh()
		}
	}
}

func (t *trayApp) togglePower() {
	snap := t.app.engine.Snapshot()
	var err error
	if snap.Running {
		err = t.app.engine.Stop()
	} else {
		err = t.app.engine.Start()
	}
	if err != nil {
		messageBox("HERTZ", friendlyEngineError(err), true)
	}
	t.refresh()
}

func (t *trayApp) copyListenerLink() {
	share, err := t.app.newShare(t.app.CurrentSettings().LinkTTLHours)
	if err != nil {
		messageBox("HERTZ", "Could not create the listener link: "+err.Error(), true)
		return
	}
	if err := copyToClipboard(share.URL); err != nil {
		messageBox("HERTZ", "Could not copy the link: "+err.Error(), true)
		return
	}
	t.mCopy.SetTitle("Copied ✓")
	time.AfterFunc(2500*time.Millisecond, func() {
		t.mCopy.SetTitle("Copy listener link")
	})
}

func (t *trayApp) refresh() {
	t.refreshMu.Lock()
	defer t.refreshMu.Unlock()

	st := t.app.buildStatus()
	status := "Status: Stopped"
	switch {
	case st.Engine.Starting:
		status = "Status: Starting…"
	case !st.Engine.Running:
		status = "Status: Stopped"
	case st.Source.Online:
		status = "Status: On air"
	default:
		status = "Status: Waiting for source"
	}
	t.mStatus.SetTitle(status)
	t.mListeners.SetTitle(fmt.Sprintf("Listeners: %.0f", st.Listeners))
	systray.SetTooltip(fmt.Sprintf("HERTZ — %s · %.0f listeners", status, st.Listeners))

	switch {
	case st.Engine.Running && st.Engine.Managed:
		t.mPower.SetTitle("Stop engine")
		t.mPower.Enable()
		t.mRestart.Enable()
	case st.Engine.Running:
		t.mPower.SetTitle("Stop engine (external)")
		t.mPower.Disable()
		t.mRestart.Disable()
	default:
		t.mPower.SetTitle("Start engine")
		t.mPower.Enable()
		t.mRestart.Enable()
	}
}

func (t *trayApp) onExit() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	t.app.Shutdown(ctx)
}
