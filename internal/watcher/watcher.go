// Package watcher detects and kills known AI tool processes.
package watcher

import (
	"fmt"
	"log"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/shirou/gopsutil/v3/process"
	"github.com/saasscaleup/ai-guard/internal/config"
)

// systemPathPrefixes lists executable path prefixes that are always OS-owned
// and should never be matched as AI processes, regardless of name keywords.
var systemPathPrefixes = func() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{"/System/Library/", "/usr/libexec/"}
	case "linux":
		return []string{"/usr/lib/systemd/", "/lib/systemd/", "/usr/libexec/"}
	default:
		return nil
	}
}()

// neverKillNames lists process names that must never be killed — not even as
// part of a process-group kill. If a targeted AI process shares a process group
// with any of these (e.g. it was launched from an interactive SSH session),
// AIGuard falls back to killing only the specific PID rather than the group.
//
// Without this guard, running `claude` or `aider` in an SSH terminal and then
// triggering a kill would also terminate the SSH session, because the AI process
// and the SSH shell share the same process group.
var neverKillNames = map[string]bool{
	// Remote-access daemons and clients
	"sshd": true, "ssh": true, "scp": true, "sftp": true,
	// Interactive shells — killing the shell ends the whole terminal session
	"bash": true, "sh": true, "zsh": true, "fish": true,
	"dash": true, "csh": true, "tcsh": true, "ksh": true,
	// Login / privilege infrastructure
	"login": true, "su": true, "sudo": true,
	// AIGuard itself — must never be detected or killed
	"aiguard": true,
}

// ── Process scan cache ────────────────────────────────────────────────────────
// DetectAIProcesses is called from 4 independent places (watcher goroutine,
// process monitor, network monitor, HTTP handler). Without caching, each caller
// does a full OS process list scan independently — up to 86 times/min.
//
// CachedDetectAIProcesses shares one result across all callers. The real scan
// runs at most once per cacheTTL (2 s). Every other caller gets the stored
// answer instantly, at zero syscall cost.

const procCacheTTL = 2 * time.Second

var (
	procCacheMu  sync.Mutex
	procCacheRes []AIProcess
	procCacheAt  time.Time
)

// CachedDetectAIProcesses returns the most recent process scan, re-scanning
// only when the cached result is older than procCacheTTL.
// Safe to call from multiple goroutines concurrently.
func CachedDetectAIProcesses(cfg *config.Config) ([]AIProcess, error) {
	procCacheMu.Lock()
	defer procCacheMu.Unlock()

	if procCacheRes != nil && time.Since(procCacheAt) < procCacheTTL {
		return procCacheRes, nil // cache hit — free
	}

	procs, err := DetectAIProcesses(cfg) // cache miss — real scan
	if err == nil {
		procCacheRes = procs
		procCacheAt = time.Now()
	}
	return procs, err
}

// AIProcess represents a detected AI process.
type AIProcess struct {
	PID        int32   `json:"pid"`
	Name       string  `json:"name"`
	Cmdline    string  `json:"cmdline"`
	ToolName   string  `json:"tool_name"`   // matched tool name from config
	WillKill   bool    `json:"will_kill"`    // true if this tool is marked kill=true
	CPUPercent float64 `json:"cpu_percent"`  // % CPU since last measurement (0 on first poll)
	MemBytes   uint64  `json:"mem_bytes"`    // RSS in bytes
}

// DetectAIProcesses scans running processes and returns those matching known AI tools.
//
// Matching uses a two-pass strategy so that "safe" tools (kill: false — desktop
// apps, IDE extensions) always claim their own processes before any "kill" tool
// can grab them by a broad keyword like "mcp" or "node".
//
// Example: Claude Desktop spawns Node processes whose cmdline contains
// "@modelcontextprotocol". Without priority matching those would be detected as
// "MCP Servers (kill:true)" and terminated when the user kills the MCP category,
// which crashes Claude Desktop. With this fix they are claimed by "Claude Desktop
// App (kill:false)" first and are never eligible for killing.
func DetectAIProcesses(cfg *config.Config) ([]AIProcess, error) {
	procs, err := process.Processes()
	if err != nil {
		return nil, fmt.Errorf("failed to list processes: %w", err)
	}

	// Split tools into two priority tiers based on CATEGORY, not kill setting.
	//
	// Tier 1 — desktop apps and protected tools: these have precise, app-specific
	// keywords (e.g. "Claude.app", "Claude Helper") and must always claim their
	// own processes first — regardless of whether the user has enabled kill for
	// them. Without this, enabling kill on a desktop app moves it to tier 2,
	// letting a broad CLI keyword (e.g. "claude" in Claude Code CLI) steal the
	// desktop's main process in tier 1 and mark it WillKill=false.
	//
	// Tier 2 — everything else: agents, infra, editors.
	var priorityTools, normalTools []config.Tool
	for _, t := range cfg.Tools {
		if t.Protected || t.Category == config.CategoryDesktop {
			priorityTools = append(priorityTools, t)
		} else {
			normalTools = append(normalTools, t)
		}
	}

	var found []AIProcess
	seen := map[int32]bool{}

	matchProcs := func(tools []config.Tool) {
		for _, p := range procs {
			if seen[p.Pid] {
				continue
			}
			name, err := p.Name()
			if err != nil {
				continue
			}
			cmdline, _ := p.Cmdline()

			// Skip OS-owned system processes — they should never be matched
			// even if the process name contains an AI keyword.
			exe, _ := p.Exe()
			systemProc := false
			for _, prefix := range systemPathPrefixes {
				if strings.HasPrefix(exe, prefix) {
					systemProc = true
					break
				}
			}
			if systemProc {
				continue
			}

			// Skip SSH, shells, and login infrastructure unconditionally.
			// These must never appear as AI processes — a keyword like "ssh"
			// appearing in an MCP server cmdline could otherwise cause the
			// SSH daemon itself to be matched and killed.
			if neverKillNames[strings.ToLower(name)] {
				continue
			}

			nameLower := strings.ToLower(name)
			cmdLower := strings.ToLower(cmdline)
			exeLower := strings.ToLower(exe) // exe is already fetched above for system path exclusion

			for _, tool := range tools {
				matched := false
				for _, kw := range tool.Keywords {
					kwLower := strings.ToLower(kw)
					if strings.Contains(nameLower, kwLower) ||
						strings.Contains(cmdLower, kwLower) ||
						strings.Contains(exeLower, kwLower) {
						matched = true
						break
					}
				}
				if matched {
					seen[p.Pid] = true
					ap := AIProcess{
						PID:      p.Pid,
						Name:     name,
						Cmdline:  cmdline,
						ToolName: tool.Name,
						WillKill: tool.Kill,
					}
					if cpu, err := p.CPUPercent(); err == nil {
						ap.CPUPercent = cpu
					}
					if mem, err := p.MemoryInfo(); err == nil && mem != nil {
						ap.MemBytes = mem.RSS
					}
					found = append(found, ap)
					break
				}
			}
		}
	}

	matchProcs(priorityTools) // pass 1 — desktop + protected tools claim their processes first
	matchProcs(normalTools)   // pass 2 — agents, infra, editors get whatever is left

	return found, nil
}

// groupContainsSafeProcess returns true if any process sharing pgid is in the
// neverKillNames set. When true, a group-wide SIGKILL would also terminate SSH
// sessions or interactive shells, so the caller must fall back to PID-only kill.
// Returns true on scan error as a fail-safe (better to leave a process alive
// than to accidentally kill an SSH session).
func groupContainsSafeProcess(pgid int) bool {
	procs, err := process.Processes()
	if err != nil {
		return true // fail-safe: don't group-kill if we cannot audit the group
	}
	for _, p := range procs {
		pg, err := syscall.Getpgid(int(p.Pid))
		if err != nil || pg != pgid {
			continue
		}
		name, err := p.Name()
		if err != nil {
			continue
		}
		if neverKillNames[strings.ToLower(name)] {
			log.Printf("[watcher] group %d contains protected process %q (PID %d) — skipping group kill",
				pgid, name, p.Pid)
			return true
		}
	}
	return false
}

// groupContainsProtectedPID returns true if any process sharing pgid has a PID
// in the protectedPIDs set. Used by KillByPID to avoid collaterally killing
// other AI processes that happen to share the same process group (e.g. multiple
// tools launched from the same terminal session).
func groupContainsProtectedPID(pgid int, protectedPIDs map[int32]bool) bool {
	if len(protectedPIDs) == 0 {
		return false
	}
	procs, err := process.Processes()
	if err != nil {
		return true // fail-safe
	}
	for _, p := range procs {
		if !protectedPIDs[p.Pid] {
			continue
		}
		pg, err := syscall.Getpgid(int(p.Pid))
		if err != nil || pg != pgid {
			continue
		}
		log.Printf("[watcher] group %d contains other AI process PID %d — skipping group kill to avoid collateral kill",
			pgid, p.Pid)
		return true
	}
	return false
}

// killProcAndGroup sends SIGKILL to the process and its entire process group.
// Killing the group catches all child processes the target spawned, which
// p.Kill() alone would miss.
//
// Safety guards — the group kill is skipped (falling back to PID-only kill) if:
//   - the group contains an SSH session or interactive shell (prevents self-termination)
//   - the group contains another AI process PID listed in protectedPIDs (prevents
//     collateral kills when multiple AI tools share a process group, e.g. when
//     launched from the same terminal session)
//
// Pass nil for protectedPIDs when all AI processes in the group should be killed
// (i.e. KillAll).
func killProcAndGroup(ap AIProcess, reason string, protectedPIDs map[int32]bool) bool {
	// Kill the process group first (negative PID = entire group).
	// This catches spawned children even if the parent already exited.
	pgid, pgErr := syscall.Getpgid(int(ap.PID))
	if pgErr == nil && pgid > 1 &&
		!groupContainsSafeProcess(pgid) &&
		!groupContainsProtectedPID(pgid, protectedPIDs) {
		if err := syscall.Kill(-pgid, syscall.SIGKILL); err == nil {
			log.Printf("[watcher] killed process group %d (%s, PID %d) [%s] — %s",
				pgid, ap.Name, ap.PID, ap.ToolName, reason)
			return true
		}
	}
	// Fallback: kill just the individual PID.
	p, err := process.NewProcess(ap.PID)
	if err != nil {
		log.Printf("[watcher] could not find process %d: %v", ap.PID, err)
		return false
	}
	if err := p.Kill(); err != nil {
		log.Printf("[watcher] failed to kill %s (PID %d): %v", ap.Name, ap.PID, err)
		return false
	}
	log.Printf("[watcher] killed %s (PID %d) [%s] — %s", ap.Name, ap.PID, ap.ToolName, reason)
	return true
}

// KillByPID terminates a single AI process by its PID.
// The process must be present in the current detected AI process list —
// this prevents the API from being used as an arbitrary process killer.
// Returns an error if the PID is not a recognised AI process.
func KillByPID(pid int32, cfg *config.Config) (AIProcess, error) {
	procs, err := DetectAIProcesses(cfg)
	if err != nil {
		return AIProcess{}, fmt.Errorf("process scan failed: %w", err)
	}
	// Build a set of all OTHER AI process PIDs so that killProcAndGroup won't
	// collaterally kill them if they share the same process group as the target
	// (e.g. multiple AI tools launched from the same terminal session).
	otherAIPIDs := make(map[int32]bool, len(procs))
	for _, ap := range procs {
		if ap.PID != pid {
			otherAIPIDs[ap.PID] = true
		}
	}
	for _, ap := range procs {
		if ap.PID == pid {
			if killProcAndGroup(ap, "kill by pid", otherAIPIDs) {
				return ap, nil
			}
			return AIProcess{}, fmt.Errorf("failed to kill PID %d (%s)", pid, ap.Name)
		}
	}
	return AIProcess{}, fmt.Errorf("PID %d is not a recognised AI process — use 'aiguard status' to list valid targets", pid)
}

// KillAll terminates all AI processes marked kill=true.
//
// Runs up to 3 scan+kill passes (300 ms apart) to catch processes that
// were spawning mid-kill or that respawn immediately.
//
// Kills the entire process group of each matched PID so that child
// processes (worker threads, sub-agents) are also terminated.
func KillAll(cfg *config.Config) ([]AIProcess, error) {
	protectedByName := map[string]bool{}
	for _, t := range cfg.Tools {
		protectedByName[t.Name] = t.Protected
	}

	killedPIDs := map[int32]bool{}
	var killed []AIProcess

	const maxPasses = 3
	for pass := 0; pass < maxPasses; pass++ {
		if pass > 0 {
			time.Sleep(300 * time.Millisecond)
		}

		procs, err := DetectAIProcesses(cfg)
		if err != nil {
			return killed, err
		}

		newKills := 0
		for _, ap := range procs {
			if killedPIDs[ap.PID] {
				continue
			}
			if !ap.WillKill || protectedByName[ap.ToolName] {
				continue
			}
			if killProcAndGroup(ap, "kill all", nil) {
				killedPIDs[ap.PID] = true
				killed = append(killed, ap)
				newKills++
			}
		}

		if newKills == 0 {
			break
		}
	}

	return killed, nil
}

// Watch continuously scans for AI processes and sends them to the out channel.
func Watch(cfg *config.Config, interval time.Duration, out chan<- []AIProcess, stop <-chan struct{}) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			procs, err := DetectAIProcesses(cfg)
			if err != nil {
				log.Printf("[watcher] scan error: %v", err)
				continue
			}
			out <- procs
		}
	}
}
