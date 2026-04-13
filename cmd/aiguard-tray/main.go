// aiguard-tray — Mac menu bar app for aiguard
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"log"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"fyne.io/systray"
)

const daemonURL = "http://127.0.0.1:7474"

// httpClient with short timeout so we never block the UI.
var httpClient = &http.Client{Timeout: 3 * time.Second}

// daemonOnce ensures we only try to start the daemon once.
var daemonOnce sync.Once

// readyOnce ensures onReady logic only runs once even if systray calls it multiple times.
var readyOnce sync.Once

type iconColor int

const (
	iconGreen  iconColor = iota
	iconYellow
	iconRed
)

type statusResponse struct {
	Count       int `json:"count"`
	AIProcesses []struct {
		Name     string `json:"name"`
		ToolName string `json:"tool_name"`
		WillKill bool   `json:"will_kill"`
	} `json:"ai_processes"`
}

type alertsResponse []struct {
	ID      int    `json:"id"`
	Message string `json:"message"`
	Source  string `json:"source"`
}

var (
	mStatus   *systray.MenuItem
	mKillAll  *systray.MenuItem
	mAlerts   *systray.MenuItem
	mOpenDash *systray.MenuItem
	mQuit     *systray.MenuItem
)

func main() {
	// Kill any zombie daemon processes left from previous bad runs
	exec.Command("pkill", "-f", "aiguard daemon").Run()
	time.Sleep(300 * time.Millisecond)

	systray.Run(onReady, onExit)
}

func onReady() {
	// Guard: systray can call onReady multiple times on some macOS versions.
	// Only execute the setup logic once.
	readyOnce.Do(func() {
		setIcon(iconYellow)
		systray.SetTooltip("aiguard — AI Kill-Switch")

		mStatus = systray.AddMenuItem("Starting...", "Current daemon status")
		mStatus.Disable()
		systray.AddSeparator()

		mKillAll = systray.AddMenuItem("⛔  Terminate All AI Processes", "Terminate all AI processes now")
		systray.AddSeparator()

		mAlerts = systray.AddMenuItem("🔔  Alerts: none", "Recent suspicious activity")
		mAlerts.Disable()
		systray.AddSeparator()

		mOpenDash = systray.AddMenuItem("🌐  Open Dashboard", "Open status page in browser")
		systray.AddSeparator()

		mQuit = systray.AddMenuItem("Quit aiguard", "Stop tray app and daemon")

		// Start daemon once, then poll
		go func() {
			ensureDaemon()
			pollLoop()
		}()

		// Handle clicks
		go func() {
			for {
				select {
				case <-mKillAll.ClickedCh:
					handleKillAll()
				case <-mOpenDash.ClickedCh:
					exec.Command("open", daemonURL+"/").Start()
				case <-mQuit.ClickedCh:
					exec.Command("pkill", "-f", "aiguard daemon").Run()
					systray.Quit()
				}
			}
		}()
	})
}

func onExit() {
	log.Println("[tray] exiting")
}

// ensureDaemon starts the daemon if not already running. Runs only once.
func ensureDaemon() {
	daemonOnce.Do(func() {
		// Check if already reachable
		if _, err := httpClient.Get(daemonURL + "/status"); err == nil {
			log.Println("[tray] daemon already running")
			return
		}

		self, _ := os.Executable()
		daemonPath := filepath.Join(filepath.Dir(self), "aiguard")
		log.Printf("[tray] starting daemon: %s daemon", daemonPath)

		cmd := exec.Command(daemonPath, "daemon")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			log.Printf("[tray] failed to start daemon: %v", err)
			mStatus.SetTitle("⚠️  Could not start daemon")
			return
		}
		log.Printf("[tray] daemon started (PID %d)", cmd.Process.Pid)

		// Wait up to 5 seconds for daemon to become ready
		for i := 0; i < 10; i++ {
			time.Sleep(500 * time.Millisecond)
			if _, err := httpClient.Get(daemonURL + "/status"); err == nil {
				log.Println("[tray] daemon is ready")
				return
			}
		}
		log.Println("[tray] daemon did not become ready in time")
	})
}

// pollLoop refreshes status every 2 seconds.
func pollLoop() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		updateStatus()
		updateAlerts()
	}
}

func updateStatus() {
	resp, err := httpClient.Get(daemonURL + "/status")
	if err != nil {
		mStatus.SetTitle("⚠️  Daemon not running")
		setIcon(iconYellow)
		systray.SetTooltip("aiguard — daemon offline")
		return
	}
	defer resp.Body.Close()

	var s statusResponse
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		return
	}

	if s.Count == 0 {
		setIcon(iconGreen)
		mStatus.SetTitle("✅  No AI processes running")
		systray.SetTooltip("aiguard — all clear")
	} else {
		killable := 0
		for _, p := range s.AIProcesses {
			if p.WillKill {
				killable++
			}
		}
		if killable > 0 {
			setIcon(iconRed)
			mStatus.SetTitle(fmt.Sprintf("🔴  %d process(es) — %d will be terminated", s.Count, killable))
			systray.SetTooltip(fmt.Sprintf("aiguard — %d killable AI processes", killable))
		} else {
			setIcon(iconYellow)
			mStatus.SetTitle(fmt.Sprintf("🟡  %d process(es) — monitoring", s.Count))
			systray.SetTooltip(fmt.Sprintf("aiguard — %d AI processes (monitor only)", s.Count))
		}
	}
}

func updateAlerts() {
	resp, err := httpClient.Get(daemonURL + "/alerts")
	if err != nil {
		return
	}
	defer resp.Body.Close()

	var alerts alertsResponse
	if err := json.NewDecoder(resp.Body).Decode(&alerts); err != nil {
		return
	}

	if len(alerts) == 0 {
		mAlerts.SetTitle("🔔  Alerts: none")
		mAlerts.Disable()
	} else {
		mAlerts.SetTitle(fmt.Sprintf("🚨  %d Alert(s) — click to view", len(alerts)))
		mAlerts.Enable()
		if len(alerts) > 0 {
			mAlerts.SetTooltip(alerts[len(alerts)-1].Message)
		}
	}
}

func handleKillAll() {
	mKillAll.SetTitle("Terminating...")
	mKillAll.Disable()

	resp, err := httpClient.Post(daemonURL+"/kill", "application/json", nil)
	if err != nil {
		mKillAll.SetTitle("⛔  Terminate All AI Processes")
		mKillAll.Enable()
		return
	}
	defer resp.Body.Close()

	var result struct{ Count int `json:"count"` }
	json.NewDecoder(resp.Body).Decode(&result)

	if result.Count == 0 {
		mKillAll.SetTitle("✅  Nothing to terminate")
	} else {
		mKillAll.SetTitle(fmt.Sprintf("✅  Terminated %d process(es)", result.Count))
	}
	time.AfterFunc(3*time.Second, func() {
		mKillAll.SetTitle("⛔  Terminate All AI Processes")
		mKillAll.Enable()
	})
}

func setIcon(c iconColor) {
	var col color.RGBA
	switch c {
	case iconGreen:
		col = color.RGBA{34, 197, 94, 255}
	case iconYellow:
		col = color.RGBA{234, 179, 8, 255}
	case iconRed:
		col = color.RGBA{239, 68, 68, 255}
	}
	systray.SetIcon(renderCircleIcon(col, 22))
}

func renderCircleIcon(c color.RGBA, size int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	draw.Draw(img, img.Bounds(), image.Transparent, image.Point{}, draw.Src)
	cx, cy := float64(size)/2, float64(size)/2
	r := float64(size)/2 - 1
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dx, dy := float64(x)-cx, float64(y)-cy
			if math.Sqrt(dx*dx+dy*dy) <= r {
				img.Set(x, y, c)
			}
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	return buf.Bytes()
}
