// Package suspicious detects suspicious behaviour by AI processes.
//
// Detection layers:
//   1. Filesystem  — mass deletes, writes to system/protected dirs
//   2. Process     — CPU spikes, excessive child-process forking
//   3. Network     — unexpected outbound connections from AI processes
package suspicious

import (
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	gnet "github.com/shirou/gopsutil/v3/net"
	"github.com/shirou/gopsutil/v3/process"
	"github.com/saasscaleup/ai-guard/internal/alerts"
	"github.com/saasscaleup/ai-guard/internal/config"
	"github.com/saasscaleup/ai-guard/internal/watcher"
)

// Thresholds — tune these as needed.
const (
	MassDeleteThreshold  = 10             // files deleted within the window
	MassDeleteWindow     = 5 * time.Second
	CPUSpikeThreshold    = 85.0           // percent CPU on a single AI process
	ChildForkThreshold   = 40             // child processes spawned by one AI PID
	                                      // (raised from 15 — VS Code spawns 20-30 helpers legitimately)
	NetScanInterval      = 10 * time.Second
	FSAlertDedupTTL      = 60 * time.Second // same path+rule won't re-alert within this window
)

// highChildProcessNames are process names that legitimately spawn many children
// and should be excluded from the fork threshold check.
var highChildProcessNames = []string{
	"code helper",   // VS Code extension host helper
	"electron",      // generic Electron apps
}

// protectedDirs are directories that should never be written to by AI tools.
// /Applications is intentionally excluded — app auto-updates write there legitimately.
var protectedDirs = []string{
	"/etc", "/usr", "/bin", "/sbin",
	"/System", "/private/etc",
}

// knownSafeDomainSuffixes lists domain suffixes whose IPs are always considered
// safe. Instead of maintaining a fragile static IP list (CDN IPs change constantly),
// we do a reverse DNS lookup on unknown IPs and check the hostname against this list.
//
// To add a new safe provider: just add its domain suffix here.
var knownSafeDomainSuffixes = []string{
	// AI providers
	"anthropic.com",
	"openai.com",
	"api.openai.com",
	"gemini.google.com",

	// CDNs used by AI providers
	"cloudfront.net",       // AWS CloudFront (Anthropic, many others)
	"fastly.net",           // Fastly CDN
	"cloudflare.com",
	"cloudflaressl.com",

	// Cloud infrastructure
	"amazonaws.com",
	"azure.com",
	"azure-api.net",
	"microsoft.com",        // VS Code telemetry, Copilot
	"microsoftonline.com",
	"office.com",           // Office 365 — many MS IPs resolve here
	"office365.com",
	"outlook.com",
	"windows.com",
	"1drv.com",
	"live.com",
	"msftconnecttest.com",
	"msecnd.net",
	"skype.com",

	// Developer tools
	"github.com",
	"githubusercontent.com",
	"npmjs.com",
	"registry.npmjs.org",
	"google.com",
	"googleapis.com",
	"gstatic.com",
}

// fallbackSafeIPPrefixes covers IPs that rarely have PTR records but are well-known
// safe infrastructure. Used as a last resort when reverse DNS returns nothing.
var fallbackSafeIPPrefixes = []string{
	// Microsoft / Azure (Office 365, VS Code, Copilot backend)
	// These IPs often have no PTR record despite being official Microsoft infra.
	"13.107.", "13.64.", "13.65.", "13.66.", "13.67.", "13.89.", "13.91.",
	"20.50.", "20.42.", "20.45.", "20.112.", "20.190.", "20.195.", "20.199.",
	"40.64.", "40.74.", "40.112.", "40.114.",
	"52.96.", "52.97.", "52.108.", "52.109.", "52.113.", "52.114.",

	// Cloudflare (npm registry, many AI APIs)
	"104.16.", "104.17.", "104.18.", "104.19.", "104.20.", "104.21.",
	"172.64.", "172.65.", "172.66.", "172.67.",

	// AWS CloudFront ranges (Anthropic, OpenAI)
	"13.224.", "13.225.", "13.226.", "13.227.", "13.228.",
	"54.230.", "54.239.", "99.86.", "205.251.",

	// Fastly
	"151.101.", "199.232.",

	// GitHub
	"140.82.", "143.55.", "185.199.", "192.30.",
}

// privateIPPrefixes are always safe (loopback, LAN, link-local).
var privateIPPrefixes = []string{
	"127.", "10.", "192.168.", "::1", "fd", "fe80:",
	"172.16.", "172.17.", "172.18.", "172.19.", "172.20.", "172.21.",
	"172.22.", "172.23.", "172.24.", "172.25.", "172.26.", "172.27.",
	"172.28.", "172.29.", "172.30.", "172.31.",
}

// rdnsCache caches reverse DNS results so we only look up each IP once.
var rdnsCache sync.Map // map[string]bool — true = safe, false = suspicious

// Monitor runs all suspicious activity detectors and emits alerts.
type Monitor struct {
	alertStore    *alerts.Store
	cfg           *config.Config
	fsWatcher     *fsnotify.Watcher
	stop          chan struct{}
	deleteMu      sync.Mutex
	recentDeletes []time.Time        // sliding window for mass-delete detection
	seenNetMu     sync.Mutex
	seenNetConns  map[string]bool    // dedup: "pid:ip:port" already alerted
	fsDedupMu     sync.Mutex
	fsDedupSeen   map[string]time.Time // dedup: "rule:path" → last alert time
}

// New creates and returns a Monitor. Call Start() to begin watching.
func New(store *alerts.Store, cfg *config.Config) (*Monitor, error) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("fsnotify: %w", err)
	}
	return &Monitor{
		alertStore:   store,
		cfg:          cfg,
		fsWatcher:    fw,
		stop:         make(chan struct{}),
		seenNetConns: make(map[string]bool),
		fsDedupSeen:  make(map[string]time.Time),
	}, nil
}

// sensitivePathPatterns are substrings that, when found in a file path being
// written or created, indicate a potentially sensitive credential operation.
var sensitivePathPatterns = []string{
	".env",
	".aws/credentials", ".aws/config",
	".ssh/",
	".netrc",
	".npmrc",
	".pypirc",
	".docker/config",
	"id_rsa", "id_ed25519", "id_ecdsa", "id_dsa",
	".pem", ".key", ".p12", ".pfx",
	"token", "secret", "api_key", "apikey", "credentials",
}

// cronAndLaunchdPaths are paths that indicate persistence mechanism modification.
var cronAndLaunchdPaths = []string{
	"/etc/crontab",
	"/var/spool/cron",
	"/etc/cron.",
	"Library/LaunchAgents",
	"Library/LaunchDaemons",
}

// WatchDirs adds directories to the filesystem monitor and also watches
// high-value sensitive paths (SSH dir, AWS config, launchd agents).
func (m *Monitor) WatchDirs(dirs []string) {
	for _, d := range dirs {
		if err := m.fsWatcher.Add(d); err != nil {
			log.Printf("[suspicious] could not watch %s: %v", d, err)
		}
	}

	// Additionally watch specific high-value directories.
	home, _ := os.UserHomeDir()
	extraDirs := []string{
		filepath.Join(home, ".ssh"),
		filepath.Join(home, ".aws"),
		filepath.Join(home, "Library", "LaunchAgents"),
		"/etc",
	}
	for _, d := range extraDirs {
		if err := m.fsWatcher.Add(d); err != nil {
			// Non-fatal — directory may not exist on all systems.
			log.Printf("[suspicious] could not watch %s: %v", d, err)
		}
	}
}

// Start launches all detectors in background goroutines.
func (m *Monitor) Start() {
	go m.runFSMonitor()
	go m.runProcessMonitor()
	go m.runNetworkMonitor()
	log.Println("[suspicious] all detectors started")
}

// Stop shuts down all detectors.
func (m *Monitor) Stop() {
	select {
	case <-m.stop:
	default:
		close(m.stop)
	}
	m.fsWatcher.Close()
}

// ── Layer 1: Filesystem ───────────────────────────────────────────────────────

func (m *Monitor) runFSMonitor() {
	for {
		select {
		case <-m.stop:
			return
		case event, ok := <-m.fsWatcher.Events:
			if !ok {
				return
			}
			m.evaluateFSEvent(event)
		case err, ok := <-m.fsWatcher.Errors:
			if !ok {
				return
			}
			log.Printf("[suspicious/fs] watcher error: %v", err)
		}
	}
}

func (m *Monitor) evaluateFSEvent(event fsnotify.Event) {
	path := event.Name
	pathLower := strings.ToLower(path)
	isWriteOrCreate := event.Op&fsnotify.Write != 0 || event.Op&fsnotify.Create != 0
	isRemoveOrRename := event.Op&fsnotify.Remove != 0 || event.Op&fsnotify.Rename != 0

	// ── Rule: fs.protected-write ─────────────────────────────────────────────
	if isWriteOrCreate && m.cfg.IsAlertEnabled("fs.protected-write") {
		for _, dir := range protectedDirs {
			if strings.HasPrefix(path, dir) {
				if !m.fsAlertDedup("fs.protected-write", path) {
					m.alert("filesystem", "CRITICAL",
						fmt.Sprintf("Write to protected system directory: %s", filepath.Base(path)), path, 0)
				}
				return
			}
		}
	}

	// ── Rule: fs.sensitive-file ──────────────────────────────────────────────
	// Fires when a credential or secret file is written or created.
	if isWriteOrCreate && m.cfg.IsAlertEnabled("fs.sensitive-file") {
		for _, pattern := range sensitivePathPatterns {
			if strings.Contains(pathLower, strings.ToLower(pattern)) {
				// Exclude SSH key detection here — handled separately below.
				if !strings.Contains(pathLower, ".ssh/id_") {
					if !m.fsAlertDedup("fs.sensitive-file", path) {
						m.alert("filesystem", "CRITICAL",
							fmt.Sprintf("Sensitive file written: %s", filepath.Base(path)), path, 0)
					}
					return
				}
			}
		}
	}

	// ── Rule: fs.ssh-key ────────────────────────────────────────────────────
	// Fires when a new SSH private key is created in ~/.ssh/.
	if event.Op&fsnotify.Create != 0 && m.cfg.IsAlertEnabled("fs.ssh-key") {
		home, _ := os.UserHomeDir()
		sshDir := filepath.Join(home, ".ssh")
		base := filepath.Base(path)
		if strings.HasPrefix(path, sshDir) &&
			(strings.HasPrefix(base, "id_") || strings.HasSuffix(base, ".pem")) {
			if !m.fsAlertDedup("fs.ssh-key", path) {
				m.alert("filesystem", "CRITICAL",
					fmt.Sprintf("New SSH key created: %s", base), path, 0)
			}
			return
		}
	}

	// ── Rule: fs.cron-launchd ────────────────────────────────────────────────
	// Fires when a cron job or launchd agent is added or modified.
	if isWriteOrCreate && m.cfg.IsAlertEnabled("fs.cron-launchd") {
		for _, p := range cronAndLaunchdPaths {
			if strings.Contains(path, p) {
				if !m.fsAlertDedup("fs.cron-launchd", path) {
					m.alert("filesystem", "CRITICAL",
						fmt.Sprintf("Persistence mechanism modified: %s", filepath.Base(path)), path, 0)
				}
				return
			}
		}
	}

	// ── Rule: fs.mass-delete ─────────────────────────────────────────────────
	if isRemoveOrRename {
		if m.cfg.IsAlertEnabled("fs.mass-delete") {
			m.checkMassDelete(path)
		}
	}
}

func (m *Monitor) checkMassDelete(path string) {
	now := time.Now()
	m.deleteMu.Lock()
	defer m.deleteMu.Unlock()

	// Add current delete
	m.recentDeletes = append(m.recentDeletes, now)

	// Prune entries outside the window
	cutoff := now.Add(-MassDeleteWindow)
	fresh := m.recentDeletes[:0]
	for _, t := range m.recentDeletes {
		if t.After(cutoff) {
			fresh = append(fresh, t)
		}
	}
	m.recentDeletes = fresh

	if len(m.recentDeletes) >= MassDeleteThreshold {
		m.alert("filesystem", "CRITICAL",
			fmt.Sprintf("Mass delete detected: %d files removed in %s",
				len(m.recentDeletes), MassDeleteWindow), path, 0)
		m.recentDeletes = nil // reset after firing
	}
}

// ── Layer 2: Process behaviour ────────────────────────────────────────────────

func (m *Monitor) runProcessMonitor() {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-m.stop:
			return
		case <-ticker.C:
			m.checkProcessBehaviour()
		}
	}
}

func (m *Monitor) checkProcessBehaviour() {
	aiProcs, err := watcher.CachedDetectAIProcesses(m.cfg)
	if err != nil {
		return
	}

	for _, ap := range aiProcs {
		p, err := process.NewProcess(ap.PID)
		if err != nil {
			continue
		}

		// ── Rule: process.cpu-spike ─────────────────────────────────────────
		if m.cfg.IsAlertEnabled("process.cpu-spike") {
			cpu, err := p.CPUPercent()
			if err == nil && cpu > CPUSpikeThreshold {
				m.alert("process", "WARN",
					fmt.Sprintf("CPU spike: %s (PID %d) using %.1f%% CPU", ap.Name, ap.PID, cpu),
					"", ap.PID)
			}
		}

		// ── Rule: process.fork ───────────────────────────────────────────────
		if m.cfg.IsAlertEnabled("process.fork") {
			children, err := p.Children()
			if err == nil && len(children) > ChildForkThreshold {
				nameLower := strings.ToLower(ap.Name)
				skip := false
				for _, known := range highChildProcessNames {
					if strings.Contains(nameLower, known) {
						skip = true
						break
					}
				}
				if !skip {
					m.alert("process", "WARN",
						fmt.Sprintf("Excessive forking: %s (PID %d) has %d child processes",
							ap.Name, ap.PID, len(children)),
						"", ap.PID)
				}
			}
		}
	}
}

// ── Layer 3: Network connections ─────────────────────────────────────────────

func (m *Monitor) runNetworkMonitor() {
	ticker := time.NewTicker(NetScanInterval)
	defer ticker.Stop()
	for {
		select {
		case <-m.stop:
			return
		case <-ticker.C:
			m.checkNetworkConnections()
		}
	}
}

func (m *Monitor) checkNetworkConnections() {
	aiProcs, err := watcher.CachedDetectAIProcesses(m.cfg)
	if err != nil {
		return
	}

	// Only monitor network connections from kill=true (high-risk) processes.
	// Safe/monitor-only tools (Claude Desktop, VS Code, etc.) connect to many
	// legitimate cloud IPs and would generate constant false positives.
	aiPIDs := map[int32]string{}
	for _, ap := range aiProcs {
		if ap.WillKill {
			aiPIDs[ap.PID] = fmt.Sprintf("%s (process: %s)", ap.ToolName, ap.Name)
		}
	}

	conns, err := gnet.Connections("tcp")
	if err != nil {
		return
	}

	for _, conn := range conns {
		if conn.Status != "ESTABLISHED" {
			continue
		}
		procName, isAI := aiPIDs[conn.Pid]
		if !isAI {
			continue
		}
		remoteIP := conn.Raddr.IP
		remotePort := conn.Raddr.Port

		// Skip known-safe cloud providers and private networks
		if isSafeConnection(remoteIP) {
			continue
		}

		// Only flag non-standard ports — port 443 (HTTPS) to unknown IPs
		// is suspicious; random high ports are even more so
		dedupKey := fmt.Sprintf("%d:%s:%d", conn.Pid, remoteIP, remotePort)
		m.seenNetMu.Lock()
		alreadySeen := m.seenNetConns[dedupKey]
		if !alreadySeen {
			m.seenNetConns[dedupKey] = true
		}
		m.seenNetMu.Unlock()

		if alreadySeen {
			continue // don't re-alert same connection every 10s
		}

		// ── Rule: network.outbound ──────────────────────────────────────────
		if !m.cfg.IsAlertEnabled("network.outbound") {
			continue
		}

		severity := "WARN"
		if remotePort != 443 && remotePort != 80 {
			severity = "CRITICAL" // non-HTTP/S port is more suspicious
		}

		m.alert("network", severity,
			fmt.Sprintf("Unexpected outbound connection from %s (PID %d) to %s:%d",
				procName, conn.Pid, remoteIP, remotePort),
			"", conn.Pid)
	}
}

// isSafeConnection returns true if the IP is private or resolves via reverse DNS
// to a known-safe domain suffix. Results are cached in rdnsCache.
func isSafeConnection(ip string) bool {
	// Fast path: private / loopback ranges never need a DNS lookup.
	for _, prefix := range privateIPPrefixes {
		if strings.HasPrefix(ip, prefix) {
			return true
		}
	}

	// Check the cache first.
	if v, ok := rdnsCache.Load(ip); ok {
		return v.(bool)
	}

	// Reverse DNS lookup. On failure, treat as unknown (not safe).
	safe := false
	hostnames, err := net.LookupAddr(ip)
	if err == nil {
		for _, h := range hostnames {
			h = strings.ToLower(strings.TrimSuffix(h, "."))
			for _, suffix := range knownSafeDomainSuffixes {
				if h == suffix || strings.HasSuffix(h, "."+suffix) {
					safe = true
					break
				}
			}
			if safe {
				break
			}
		}
	}

	// Fallback: if reverse DNS returned nothing, check known IP prefixes for
	// infrastructure (like Microsoft Azure) that rarely publishes PTR records.
	if !safe && err != nil {
		for _, prefix := range fallbackSafeIPPrefixes {
			if strings.HasPrefix(ip, prefix) {
				safe = true
				break
			}
		}
	}

	rdnsCache.Store(ip, safe)
	if safe {
		log.Printf("[suspicious/net] IP %s whitelisted (rdns or known prefix)", ip)
	}
	return safe
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// fsAlertDedup returns true if this rule+path combination was already alerted
// within FSAlertDedupTTL. Prevents the same file event flooding the alert log.
func (m *Monitor) fsAlertDedup(rule, path string) bool {
	key := rule + ":" + path
	now := time.Now()
	m.fsDedupMu.Lock()
	defer m.fsDedupMu.Unlock()
	if last, ok := m.fsDedupSeen[key]; ok && now.Sub(last) < FSAlertDedupTTL {
		return true // already alerted recently
	}
	m.fsDedupSeen[key] = now
	return false
}

// Notify fires a native desktop notification.
// On macOS it uses osascript; on Linux it uses notify-send.
// Exported so main.go can also call it for new-process events.
// Non-blocking — runs in a background goroutine and logs any error.
func Notify(subtitle, message string) {
	go func() {
		switch runtime.GOOS {
		case "darwin":
			// AppleScript requires plain double-quote escaping; Go's %q adds
			// backslash escapes that AppleScript does not understand.
			esc := func(s string) string {
				s = strings.ReplaceAll(s, `\`, `\\`)
				s = strings.ReplaceAll(s, `"`, `\"`)
				return s
			}
			script := fmt.Sprintf(
				`display notification "%s" with title "AIGuard" subtitle "%s" sound name "Basso"`,
				esc(message), esc(subtitle),
			)
			if out, err := exec.Command("osascript", "-e", script).CombinedOutput(); err != nil {
				log.Printf("[notify] osascript failed: %v — %s", err, strings.TrimSpace(string(out)))
			}
		case "linux":
			// notify-send is available on most Linux desktops (libnotify).
			// Falls back silently if not installed (headless servers).
			out, err := exec.Command("notify-send",
				"--icon=dialog-warning",
				"--urgency=normal",
				fmt.Sprintf("AIGuard — %s", subtitle),
				message,
			).CombinedOutput()
			if err != nil {
				log.Printf("[notify] notify-send failed: %v — %s", err, strings.TrimSpace(string(out)))
			}
		default:
			log.Printf("[notify] unsupported OS %q — skipping notification: %s: %s", runtime.GOOS, subtitle, message)
		}
	}()
}

func (m *Monitor) alert(source, severity, message, path string, pid int32) {
	a := m.alertStore.AddWithSeverity(source, severity, message, path, pid)
	pidStr := ""
	if pid > 0 {
		pidStr = fmt.Sprintf(" [PID %d]", pid)
	}
	log.Printf("🚨 ALERT #%-4d | %-8s | %-12s%s | %s", a.ID, severity, source, pidStr, message)

	// Send a native desktop notification for CRITICAL and WARN alerts.
	if severity == "CRITICAL" || severity == "WARN" {
		Notify(severity+" — "+source, message)
	}
}
