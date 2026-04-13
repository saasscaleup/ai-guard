// aiguard — AI process kill switch daemon
// Usage:
//   aiguard daemon                       — start the background daemon + REST API
//   aiguard status                       — list running AI processes (truncated)
//   aiguard status --full                — list running AI processes (full cmdline)
//   aiguard kill                         — immediately kill all processes marked kill=true
//   aiguard kill --category <cat>        — kill only processes in one category
//   aiguard alerts                       — show recent suspicious activity alerts
//   aiguard list                         — show all known AI tools and their kill setting
//   aiguard enable  <name | all>         — mark a tool (or all tools) for termination
//   aiguard disable <name | all>         — exclude a tool (or all tools) from termination
//
// Categories: agent | infra | desktop | editor
package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/saasscaleup/ai-guard/internal/alerts"
	"github.com/saasscaleup/ai-guard/internal/api"
	"github.com/saasscaleup/ai-guard/internal/config"
	"github.com/saasscaleup/ai-guard/internal/suspicious"
	"github.com/saasscaleup/ai-guard/internal/watcher"
)

const (
	apiPort      = "7474"
	scanInterval = 2 * time.Second
)

func getWatchDirs() []string {
	dirs := []string{"/tmp"}
	if home := os.Getenv("HOME"); home != "" {
		dirs = append([]string{home}, dirs...)
	}
	return dirs
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "daemon":
		runDaemon()
	case "status":
		full := len(os.Args) > 2 && os.Args[2] == "--full"
		runStatus(full)
	case "kill":
		// aiguard kill           — kill all
		// aiguard kill --pid 123 — kill one specific PID
		if len(os.Args) == 4 && os.Args[2] == "--pid" {
			pid, err := strconv.Atoi(os.Args[3])
			if err != nil {
				fmt.Fprintf(os.Stderr, "invalid PID %q: must be a number\n", os.Args[3])
				os.Exit(1)
			}
			runKillPID(int32(pid))
		} else {
			runKill()
		}
	case "alerts":
		runAlerts()
	case "rules":
		if len(os.Args) == 2 {
			runRulesList()
		} else if len(os.Args) == 4 && (os.Args[2] == "enable" || os.Args[2] == "disable") {
			runSetRule(os.Args[3], os.Args[2] == "enable")
		} else {
			fmt.Fprintln(os.Stderr, "usage: aiguard rules [enable|disable <rule-id>]")
			os.Exit(1)
		}
	case "list":
		runList()
	case "enable":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: aiguard enable <tool name | all>")
			os.Exit(1)
		}
		runSetKill(os.Args[2], true)
	case "disable":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: aiguard disable <tool name | all>")
			os.Exit(1)
		}
		runSetKill(os.Args[2], false)
	default:
		printUsage()
		os.Exit(1)
	}
}

// runDaemon starts the full daemon: process watcher + filesystem monitor + REST API.
func runDaemon() {
	log.Println("[aiguard] starting daemon...")

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("[aiguard] failed to load config: %v", err)
	}
	log.Printf("[aiguard] loaded %d tools from config (%s)", len(cfg.Tools), config.ConfigPath())

	alertStore := alerts.New()

	// --- Suspicious activity monitor (multi-layer: FS + process + network) ---
	susmon, err := suspicious.New(alertStore, cfg)
	if err != nil {
		log.Fatalf("[aiguard] failed to create suspicious monitor: %v", err)
	}
	susmon.WatchDirs(getWatchDirs())
	susmon.Start()
	log.Printf("[aiguard] suspicious activity monitor started (FS + process + network)")

	// --- Process watcher ---
	procCh := make(chan []watcher.AIProcess, 10)
	stopCh := make(chan struct{})
	go watcher.Watch(cfg, scanInterval, procCh, stopCh)

	go func() {
		lastKnown := map[int32]bool{}
		for procs := range procCh {
			for _, p := range procs {
				if !lastKnown[p.PID] {
					killTag := "monitor"
					if p.WillKill {
						killTag = "WILL KILL"
					}
					log.Printf("[watcher] new AI process: %s (PID %d) [%s] [%s]",
						p.Name, p.PID, p.ToolName, killTag)
					// Fire a macOS notification for newly detected killable processes.
					if p.WillKill {
						suspicious.Notify("AI Process Detected",
							p.ToolName+" is running (PID "+fmt.Sprintf("%d", p.PID)+")")
					}
					lastKnown[p.PID] = true
				}
			}
			current := map[int32]bool{}
			for _, p := range procs {
				current[p.PID] = true
			}
			for pid := range lastKnown {
				if !current[pid] {
					delete(lastKnown, pid)
				}
			}
		}
	}()

	// --- REST API ---
	srv := api.New(alertStore, cfg, apiPort)
	go srv.Start()

	// --- Graceful shutdown ---
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	log.Println("[aiguard] shutting down...")
	close(stopCh)
	susmon.Stop()
}

// runStatus prints currently detected AI processes.
func runStatus(full bool) {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}
	procs, err := watcher.DetectAIProcesses(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if len(procs) == 0 {
		fmt.Println("No AI processes detected.")
		return
	}

	fmt.Printf("%-8s %-10s %-28s %-22s %s\n", "PID", "ACTION", "TOOL", "NAME", "CMDLINE")
	fmt.Println(repeat("-", 100))
	for _, p := range procs {
		action := "monitor"
		if p.WillKill {
			action = "KILL"
		}
		cmdline := p.Cmdline
		if !full && len(cmdline) > 60 {
			cmdline = cmdline[:60] + "..."
		}
		fmt.Printf("%-8d %-10s %-28s %-22s %s\n",
			p.PID, action,
			truncate(p.ToolName, 27),
			truncate(p.Name, 21),
			cmdline)
	}
}

// runKill immediately kills all processes marked kill=true.
func runKill() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Terminating all AI processes marked for termination...")
	killed, err := watcher.KillAll(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if len(killed) == 0 {
		fmt.Println("No AI processes found to terminate.")
		return
	}
	fmt.Printf("Terminated %d process(es):\n", len(killed))
	for _, p := range killed {
		fmt.Printf("  - %s (PID %d)\n", p.ToolName, p.PID)
	}
}

// runKillPID kills a single AI process by PID.
func runKillPID(pid int32) {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}
	killed, err := watcher.KillByPID(pid, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ Terminated %s (PID %d)\n", killed.ToolName, killed.PID)
}

// runAlerts fetches alerts from the running daemon via its local API.
func runAlerts() {
	fmt.Printf("Fetching alerts from daemon at http://127.0.0.1:%s/alerts\n\n", apiPort)
	fmt.Println("(Daemon must be running. Use: aiguard daemon)")
}

// runRulesList shows all configurable alert rules and their current state.
func runRulesList() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	enabledCount := 0
	for _, r := range cfg.AlertRules {
		if r.Enabled {
			enabledCount++
		}
	}

	fmt.Printf("aiguard — alert rules (%d total, %d enabled)\n\n", len(cfg.AlertRules), enabledCount)
	fmt.Println(repeat("-", 90))
	fmt.Printf("  %-3s  %-22s  %-8s  %s\n", "#", "RULE ID", "STATUS", "DESCRIPTION")
	fmt.Println(repeat("-", 90))

	for i, r := range cfg.AlertRules {
		status := "[ off ]"
		marker := " "
		if r.Enabled {
			status = "[ON]   "
			marker = ">"
		}
		fmt.Printf("%s %-3d  %-22s  %-8s  %s\n", marker, i+1, r.ID, status, r.Description)
	}

	fmt.Println(repeat("-", 90))
	fmt.Println("Commands:")
	fmt.Println("  aiguard rules enable  <rule-id | all>   — activate a detection rule")
	fmt.Println("  aiguard rules disable <rule-id | all>   — silence a detection rule")
}

// runSetRule enables or disables a named alert rule, or all rules if id == "all".
func runSetRule(id string, enable bool) {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	action := "enabled"
	if !enable {
		action = "disabled"
	}

	if id == "all" {
		count := cfg.SetAllAlertRules(enable)
		if err := config.Save(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "save error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("✓ ALL %d rules %s\n", count, action)
		fmt.Println("Run 'aiguard rules' to confirm.")
		return
	}

	if err := cfg.SetAlertRule(id, enable); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if err := config.Save(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "save error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ Rule %q %s\n", id, action)
	fmt.Println("Run 'aiguard rules' to confirm.")
}

// runList shows all configured AI tools.
func runList() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	killCount := 0
	for _, t := range cfg.Tools {
		if t.Kill {
			killCount++
		}
	}

	fmt.Printf("aiguard — known AI tools (%d total, %d will be terminated)\n\n", len(cfg.Tools), killCount)
	fmt.Println(repeat("-", 100))
	fmt.Printf("  %-3s  %-30s  %-8s  %-8s  %s\n", "#", "TOOL NAME", "STATUS", "ACTION", "NOTE")
	fmt.Println(repeat("-", 100))

	for i, t := range cfg.Tools {
		status := "[ off ]"
		action := "monitor"
		marker := " "
		if t.Kill {
			status = "[ON]   "
			action = "KILL"
			marker = ">"
		}
		fmt.Printf("%s %-3d  %-30s  %-8s  %-8s  %s\n",
			marker, i+1,
			truncate(t.Name, 29),
			status, action, t.Note)
	}

	fmt.Println(repeat("-", 100))
	fmt.Printf("Config: %s\n", config.ConfigPath())
	fmt.Println("Commands:")
	fmt.Println("  aiguard enable  \"<tool name>\"   — turn ON  (will terminate)")
	fmt.Println("  aiguard disable \"<tool name>\"   — turn OFF (monitor only)")
	fmt.Println("  aiguard enable  all             — arm every tool")
	fmt.Println("  aiguard disable all             — disarm every tool")
	fmt.Println("  aiguard kill                    — terminate ALL enabled processes")
}

// runSetKill enables or disables kill for a named tool, or all tools if name == "all".
func runSetKill(name string, kill bool) {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	action := "enabled (KILL)"
	if !kill {
		action = "disabled (monitor only)"
	}

	if name == "all" {
		count := cfg.SetAllKill(kill)
		if err := config.Save(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "save error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("✓ ALL %d tools %s\n", count, action)
		fmt.Println("Run 'aiguard list' to confirm.")
		return
	}

	if err := cfg.SetKill(name, kill); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if err := config.Save(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "save error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ %q %s\n", name, action)
	fmt.Println("Run 'aiguard list' to confirm.")
}

func truncate(s string, max int) string {
	if len(s) > max {
		return s[:max-1] + "…"
	}
	return s
}

func repeat(s string, n int) string {
	result := ""
	for i := 0; i < n; i++ {
		result += s
	}
	return result
}

func printUsage() {
	fmt.Println(`aiguard — AI process kill-switch

Usage:
  aiguard daemon                        Start the background daemon
  aiguard status                        List detected AI processes
  aiguard status --full                 List with full cmdline paths
  aiguard kill                          Kill ALL processes marked KILL
  aiguard kill --pid <PID>              Kill one specific process by PID
  aiguard list                          Show all tools and their kill status
  aiguard enable  <name>                Mark a single tool for termination
  aiguard enable  all                   Mark ALL tools for termination
  aiguard disable <name>                Exclude a single tool from termination
  aiguard disable all                   Set ALL tools to monitor only
  aiguard alerts                        Show recent suspicious activity alerts
  aiguard rules                         List all detection rules and their status
  aiguard rules enable  <rule-id>       Activate a detection rule
  aiguard rules enable  all             Activate ALL detection rules
  aiguard rules disable <rule-id>       Silence a detection rule
  aiguard rules disable all             Silence ALL detection rules

Examples:
  aiguard kill
  aiguard kill --pid 74752
  aiguard enable  all
  aiguard disable all
  aiguard enable  "Claude Code CLI"
  aiguard disable "Claude Desktop App"
  aiguard rules enable  all
  aiguard rules disable network.outbound

REST API (when daemon is running on localhost:7474):
  GET  /status         — list detected AI processes
  POST /kill           — kill all marked KILL
  GET  /alerts         — list alerts
  GET  /tools          — list all tools and kill settings
  POST /alerts/clear   — clear alerts
  GET  /usage          — token usage and cost estimates`)
}
