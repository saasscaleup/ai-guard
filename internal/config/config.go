// Package config manages the aiguard tool list and kill preferences.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Category constants for grouping tools in kill operations.
const (
	CategoryAgent   = "agent"   // Autonomous agents — CLI tools, agentic IDEs, agentic extensions
	CategoryInfra   = "infra"   // AI Infrastructure — LLM servers, MCP servers, API processes
	CategoryDesktop = "desktop" // AI Desktop Apps — passive UIs (Claude Desktop, ChatGPT)
	CategoryEditor  = "editor"  // In-Editor AI — extension hosts, editor-native AI
)

// Tool defines a known AI tool and whether it should be killed.
type Tool struct {
	Name      string   `json:"name"`
	Keywords  []string `json:"keywords"`
	Kill      bool     `json:"kill"`
	Category  string   `json:"category"`
	Note      string   `json:"note,omitempty"`
	Protected bool     `json:"protected,omitempty"` // never killed; immune to enable/enable-all
}

// AlertRule defines a toggleable suspicious-activity detection rule.
type AlertRule struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
}

// Config is the top-level config structure.
type Config struct {
	Tools      []Tool      `json:"tools"`
	AlertRules []AlertRule `json:"alert_rules"`
}

// DefaultAlertRules returns the full set of built-in detection rules, all enabled.
func DefaultAlertRules() []AlertRule {
	return []AlertRule{
		{ID: "fs.protected-write",  Enabled: true,  Description: "Write to protected system dir (/etc, /usr, /bin, /System…)"},
		{ID: "fs.mass-delete",      Enabled: true,  Description: "Mass file deletion — 10+ files deleted in 5 seconds"},
		{ID: "fs.sensitive-file",   Enabled: true,  Description: "Write/create inside credential files (.env, .aws, .ssh, *.pem, *.key)"},
		{ID: "fs.ssh-key",          Enabled: true,  Description: "New SSH key created in ~/.ssh/"},
		{ID: "fs.cron-launchd",     Enabled: true,  Description: "Cron job or launchd agent added/modified"},
		{ID: "process.cpu-spike",   Enabled: true,  Description: "AI process CPU spike above 85%"},
		{ID: "process.fork",        Enabled: true,  Description: "AI process spawning 40+ child processes"},
		{ID: "network.outbound",    Enabled: true,  Description: "Unexpected outbound connection from high-risk AI process"},
	}
}

// IsAlertEnabled returns true if the given rule ID is enabled in config.
// Defaults to true if the rule is not found (safe default).
func (c *Config) IsAlertEnabled(ruleID string) bool {
	for _, r := range c.AlertRules {
		if r.ID == ruleID {
			return r.Enabled
		}
	}
	return true
}

// SetAllAlertRules enables or disables every alert rule at once.
// Returns the number of rules updated.
func (c *Config) SetAllAlertRules(enabled bool) int {
	for i := range c.AlertRules {
		c.AlertRules[i].Enabled = enabled
	}
	return len(c.AlertRules)
}

// SetAlertRule enables or disables a rule by ID. Returns error if not found.
func (c *Config) SetAlertRule(id string, enabled bool) error {
	for i, r := range c.AlertRules {
		if r.ID == id {
			c.AlertRules[i].Enabled = enabled
			return nil
		}
	}
	return fmt.Errorf("alert rule %q not found — use 'aiguard rules' to see available rules", id)
}

// DefaultConfig returns the built-in list of known AI tools.
func DefaultConfig() *Config {
	return &Config{
		Tools: []Tool{
			// ── Autonomous Agents (high risk — kill by default) ──────────────────
			// Anything that can take actions on its own: CLI agents, agentic IDEs,
			// and in-editor agents that go beyond autocomplete.
			{Name: "Claude Code CLI",       Keywords: []string{"claude --resume", "claude"},                   Kill: true,  Category: CategoryAgent, Note: "Autonomous CLI agent — high risk"},
			{Name: "OpenAI Codex CLI",      Keywords: []string{"codex"},                                       Kill: true,  Category: CategoryAgent, Note: "OpenAI terminal agent — high risk"},
			{Name: "OpenCode",              Keywords: []string{"opencode"},                                     Kill: true,  Category: CategoryAgent, Note: "Open-source terminal agent, 75+ LLM providers"},
			{Name: "Aider",                 Keywords: []string{"aider"},                                       Kill: true,  Category: CategoryAgent, Note: "Autonomous CLI agent — high risk"},
			{Name: "AutoGPT",               Keywords: []string{"autogpt"},                                     Kill: true,  Category: CategoryAgent, Note: "Autonomous agent — high risk"},
			{Name: "Devin",                 Keywords: []string{"devin"},                                       Kill: true,  Category: CategoryAgent, Note: "Fully autonomous coding agent — high risk"},
			{Name: "Goose (Block)",         Keywords: []string{"goose"},                                       Kill: true,  Category: CategoryAgent, Note: "Open-source autonomous agent by Block/Square"},
			{Name: "OpenHands",             Keywords: []string{"openhands"},                                   Kill: true,  Category: CategoryAgent, Note: "Open-source autonomous software developer"},
			{Name: "Plandex",               Keywords: []string{"plandex"},                                     Kill: true,  Category: CategoryAgent, Note: "Long-context terminal agent — high risk"},
			{Name: "Amazon Q Developer",    Keywords: []string{"amazon-q", "amazonq", "q developer"},         Kill: true,  Category: CategoryAgent, Note: "AWS autonomous coding agent"},
			{Name: "Sourcegraph Amp",       Keywords: []string{"sourcegraph-amp", "sg amp", "amp-workbench"},  Kill: true,  Category: CategoryAgent, Note: "Amp CLI agent with deep research mode"},
			{Name: "Kilo Code",             Keywords: []string{"kilocode", "kilo-code"},                      Kill: true,  Category: CategoryAgent, Note: "Emerging structured-mode agent"},
			{Name: "Gemini CLI",            Keywords: []string{"gemini"},                                      Kill: true,  Category: CategoryAgent, Note: "Google Gemini CLI — autonomous agent"},
			{Name: "Cursor IDE",            Keywords: []string{"cursor"},                                      Kill: true,  Category: CategoryAgent, Note: "AI code editor with autonomous agent mode"},
			{Name: "Windsurf (Codeium)",    Keywords: []string{"windsurf"},                                    Kill: true,  Category: CategoryAgent, Note: "Autonomous multi-repo orchestration IDE"},
			{Name: "Kiro",                  Keywords: []string{"kiro"},                                        Kill: true,  Category: CategoryAgent, Note: "Spec-driven autonomous dev tool (AWS)"},
			{Name: "Cline (VS Code)",       Keywords: []string{"cline"},                                       Kill: true,  Category: CategoryAgent, Note: "VS Code autonomous coding assistant"},
			{Name: "OpenClaw",              Keywords: []string{"openclaw"},                                    Kill: true,  Category: CategoryAgent, Note: "Autonomous agent via messaging platforms; executes shell commands, browses web"},
			{Name: "Roo Code (VS Code)",    Keywords: []string{"roo-code", "roo"},                             Kill: true,  Category: CategoryAgent, Note: "Open-source multi-agent VS Code extension — Code/Architect/Debug modes"},

			// Newer / emerging agents (2025–2026)
			{Name: "Hermes Agent",          Keywords: []string{"hermes-agent", "hermes_agent"},               Kill: true,  Category: CategoryAgent, Note: "NousResearch open-source agent with persistent memory — runs as 'hermes-agent'"},
			{Name: "ZeroClaw",              Keywords: []string{"zeroclaw"},                                    Kill: true,  Category: CategoryAgent, Note: "Rust-based ultra-lightweight autonomous agent runtime"},
			{Name: "Open Interpreter",      Keywords: []string{"open-interpreter", "interpreter --"},         Kill: true,  Category: CategoryAgent, Note: "Runs code locally, can control computer — high risk"},
			{Name: "SWE-agent",             Keywords: []string{"sweagent", "swe-agent"},                      Kill: true,  Category: CategoryAgent, Note: "Princeton autonomous software engineering agent"},
			{Name: "MetaGPT",               Keywords: []string{"metagpt"},                                    Kill: true,  Category: CategoryAgent, Note: "Multi-agent software development framework"},
			{Name: "AutoGen (Microsoft)",   Keywords: []string{"autogen", "pyautogen"},                       Kill: true,  Category: CategoryAgent, Note: "Microsoft multi-agent orchestration framework"},
			{Name: "CrewAI",                Keywords: []string{"crewai", "crew-ai"},                          Kill: true,  Category: CategoryAgent, Note: "Role-based multi-agent framework"},
			{Name: "LangGraph",             Keywords: []string{"langgraph"},                                   Kill: true,  Category: CategoryAgent, Note: "LangChain stateful multi-agent graph framework"},
			{Name: "GPT Pilot",             Keywords: []string{"gpt-pilot", "gpt_pilot"},                     Kill: true,  Category: CategoryAgent, Note: "Autonomous coding agent — writes full apps from scratch"},
			{Name: "GPT-Engineer",          Keywords: []string{"gpt-engineer"},                               Kill: true,  Category: CategoryAgent, Note: "Prompt-to-codebase generator"},
			{Name: "Mentat",                Keywords: []string{"mentat"},                                      Kill: true,  Category: CategoryAgent, Note: "Terminal coding agent with context-aware edits"},
			{Name: "BabyAGI",               Keywords: []string{"babyagi", "baby-agi"},                        Kill: true,  Category: CategoryAgent, Note: "Task-driven autonomous agent loop"},
			{Name: "SuperAGI",              Keywords: []string{"superagi"},                                    Kill: true,  Category: CategoryAgent, Note: "Open-source autonomous general agent platform"},
			{Name: "Smol Developer",        Keywords: []string{"smol-developer", "smol_dev"},                 Kill: true,  Category: CategoryAgent, Note: "Scaffold entire codebases from a single prompt"},

			// ── AI Infrastructure (medium-high risk — kill by default) ────────────
			// Backend servers and subprocesses that agents connect to or spawn.
			{Name: "Ollama",                Keywords: []string{"ollama"},                                      Kill: true,  Category: CategoryInfra, Note: "Local LLM server — backbone for many agents"},
			{Name: "LM Studio",             Keywords: []string{"lmstudio"},                                    Kill: false, Category: CategoryInfra, Note: "Local LLM UI — monitor by default"},
			{Name: "MCP Servers (Node)",    Keywords: []string{"context7-mcp", "@modelcontextprotocol", "mcp"}, Kill: true, Category: CategoryInfra, Note: "MCP subprocesses spawned by agents"},
			{Name: "OpenAI API Processes",  Keywords: []string{"openai"},                                      Kill: true,  Category: CategoryInfra, Note: "Direct API script — medium risk"},
			{Name: "llama.cpp Server",      Keywords: []string{"llama-server", "llama-cpp", "llama.cpp"},     Kill: true,  Category: CategoryInfra, Note: "High-performance local LLM inference server"},
			{Name: "Text Gen WebUI",        Keywords: []string{"text-generation-webui"},                      Kill: true,  Category: CategoryInfra, Note: "Oobabooga web-based local LLM server"},
			{Name: "koboldcpp",             Keywords: []string{"koboldcpp", "kobold"},                        Kill: true,  Category: CategoryInfra, Note: "Local KoboldAI LLM server"},
			{Name: "LocalAI",               Keywords: []string{"localai", "local-ai"},                        Kill: true,  Category: CategoryInfra, Note: "Self-hosted OpenAI-compatible API server"},
			{Name: "Flowise",               Keywords: []string{"flowise"},                                     Kill: false, Category: CategoryInfra, Note: "Low-code LLM app builder — monitor by default"},
			{Name: "Open WebUI",            Keywords: []string{"open-webui"},                                  Kill: false, Category: CategoryInfra, Note: "ChatGPT-like frontend for local LLMs — monitor by default"},

			// ── AI Desktop Apps (low risk — monitor only) ─────────────────────────
			// Passive UIs — user-facing only, not autonomous.
			// "Claude.app" matches exe path on macOS even when Cmdline() is empty (sandboxed).
			// "Claude Helper" matches all Electron helper sub-processes.
			// The process name "Claude" alone is NOT used as a keyword because it
			// would also match the `claude` CLI binary — exe path is precise enough.
			{Name: "Claude Desktop App",    Keywords: []string{"Claude Helper", "Claude.app", "Claude.app/Contents"}, Kill: false, Category: CategoryDesktop, Note: "Desktop UI — low risk"},
			{Name: "ChatGPT Desktop",       Keywords: []string{"chatgpt", "ChatGPTHelper"},                   Kill: false, Category: CategoryDesktop, Note: "Desktop UI — low risk"},
			{Name: "Warp Terminal",         Keywords: []string{"warp"},                                        Kill: false, Category: CategoryDesktop, Note: "AI-powered terminal — monitor by default"},
			{Name: "Jan",                   Keywords: []string{"jan-ai", "jan"},                               Kill: false, Category: CategoryDesktop, Note: "Local-first LLM desktop app — monitor by default"},
			{Name: "AnythingLLM",           Keywords: []string{"anythingllm"},                                 Kill: false, Category: CategoryDesktop, Note: "All-in-one local AI platform — monitor by default"},
			{Name: "GPT4All",               Keywords: []string{"gpt4all"},                                     Kill: false, Category: CategoryDesktop, Note: "Local LLM desktop chat — monitor by default"},
			{Name: "Msty",                  Keywords: []string{"msty"},                                        Kill: false, Category: CategoryDesktop, Note: "Local + remote LLM desktop UI — monitor by default"},

			// ── In-Editor AI (monitor by default) ────────────────────────────────
			// Extensions and editor-native AI. Note: killing an extension host or
			// editor process terminates ALL plugins in that editor simultaneously —
			// there is no way to target a single extension.
			//
			// VS Code the editor is NOT listed here — it is a generic code editor,
			// not an AI tool. Its AI capabilities come entirely from extensions
			// (Copilot, Codeium, Continue, Cody etc.) which are tracked separately
			// below. Listing VS Code itself would catch GPU renderers, spell-checkers,
			// language servers, and other non-AI infrastructure processes.
			{Name: "VS Code Extension Host", Keywords: []string{"--type=extensionHost"},                      Kill: false, Category: CategoryEditor, Note: "⚠️  Kills ALL VS Code extensions at once — enable for hard stop"},
			{Name: "GitHub Copilot",        Keywords: []string{"copilot"},                                     Kill: false, Category: CategoryEditor, Note: "Autocomplete + Copilot Workspace agent"},
			{Name: "Codeium Extension",     Keywords: []string{"codeium", "language_server_macos"},            Kill: false, Category: CategoryEditor, Note: "Autocomplete only — low risk"},
			{Name: "Continue.dev",          Keywords: []string{"continue"},                                    Kill: false, Category: CategoryEditor, Note: "VS Code AI chat extension"},
			{Name: "Cody (Sourcegraph)",    Keywords: []string{"cody-agent", "@sourcegraph/cody-agent"},       Kill: false, Category: CategoryEditor, Note: "Sourcegraph codebase-aware AI assistant"},
			{Name: "Tabby",                 Keywords: []string{"tabby"},                                       Kill: false, Category: CategoryEditor, Note: "Self-hosted autocomplete — low risk"},
			{Name: "JetBrains AI",          Keywords: []string{"idea", "pycharm", "webstorm", "goland", "phpstorm", "clion", "rider", "rubymine", "datagrip"}, Kill: false, Category: CategoryEditor, Note: "⚠️  Kills entire JetBrains IDE — AI Assistant has no separate process"},
			{Name: "Zed",                   Keywords: []string{"zed"},                                         Kill: false, Category: CategoryEditor, Note: "Built-in Claude-powered AI — killing Zed closes the editor"},
			{Name: "Neovim (AI plugins)",   Keywords: []string{"nvim"},                                        Kill: false, Category: CategoryEditor, Note: "Hosts Avante.nvim, copilot.vim etc — killing closes the editor"},
			{Name: "Tabnine",               Keywords: []string{"tabnine"},                                     Kill: false, Category: CategoryEditor, Note: "AI autocomplete extension — low risk"},
			{Name: "Supermaven",            Keywords: []string{"supermaven"},                                  Kill: false, Category: CategoryEditor, Note: "Fast AI autocomplete — low risk"},
		},
	}
}

// ConfigPath returns the path to the user's config file.
func ConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".aiguard", "config.json")
}

// Load reads the config from disk, creating default if missing.
func Load() (*Config, error) {
	path := ConfigPath()

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		cfg := DefaultConfig()
		if saveErr := Save(cfg); saveErr != nil {
			return cfg, nil // return default even if save fails
		}
		return cfg, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	// Migration: rebuild the tool list from DefaultConfig on every load.
	//
	// This ensures keywords, notes, categories, and ordering are ALWAYS in sync
	// with the current codebase — fixes cases where a saved config has stale
	// keywords (e.g. old "extensionhostprocess" vs new "--type=extensionHost"),
	// wrong tool ordering (VS Code before Extension Host), or removed tools
	// (Slack, Postman).
	//
	// Only the user's Kill preference per tool is preserved. Everything else
	// (keywords, note, category, order) comes from DefaultConfig.
	defaults := DefaultConfig()

	// Capture user's Kill settings by tool name before rebuilding.
	userKill := map[string]bool{}
	for _, t := range cfg.Tools {
		userKill[t.Name] = t.Kill
	}

	// Rebuild from defaults, restoring each tool's Kill preference.
	cfg.Tools = make([]Tool, len(defaults.Tools))
	copy(cfg.Tools, defaults.Tools)
	for i, t := range cfg.Tools {
		if kill, ok := userKill[t.Name]; ok {
			cfg.Tools[i].Kill = kill
		}
	}

	// Sync alert rules: preserve existing Enabled settings, append any new rules.
	existingRules := map[string]AlertRule{}
	for _, r := range cfg.AlertRules {
		existingRules[r.ID] = r
	}
	syncedRules := make([]AlertRule, len(DefaultAlertRules()))
	copy(syncedRules, DefaultAlertRules())
	for i, r := range syncedRules {
		if existing, ok := existingRules[r.ID]; ok {
			syncedRules[i].Enabled = existing.Enabled
		}
	}
	cfg.AlertRules = syncedRules

	// Always persist the rebuilt config.
	_ = Save(&cfg)

	return &cfg, nil
}

// Save writes the config to disk.
func Save(cfg *Config) error {
	path := ConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("serializing config: %w", err)
	}
	return os.WriteFile(path, data, 0600)
}

// SetKill enables or disables kill for a tool by name. Returns error if not
// found or if the tool is protected.
func (c *Config) SetKill(name string, kill bool) error {
	for i, t := range c.Tools {
		if t.Name == name {
			if t.Protected {
				return fmt.Errorf("tool %q is protected and cannot be set as a kill target", name)
			}
			c.Tools[i].Kill = kill
			return nil
		}
	}
	return fmt.Errorf("tool %q not found — use 'aiguard list' to see available tools", name)
}

// SetAllKill enables or disables kill for every non-protected tool at once.
// Returns the number of tools updated.
func (c *Config) SetAllKill(kill bool) int {
	count := 0
	for i, t := range c.Tools {
		if !t.Protected {
			c.Tools[i].Kill = kill
			count++
		}
	}
	return count
}
