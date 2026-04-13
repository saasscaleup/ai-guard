// Package api provides the local REST API for the aiguard daemon.
package api

import (
	_ "embed"
	"encoding/json"
	"log"
	"net/http"
	"strconv"

	"github.com/saasscaleup/ai-guard/internal/alerts"
	"github.com/saasscaleup/ai-guard/internal/config"
	"github.com/saasscaleup/ai-guard/internal/watcher"
)

//go:embed dashboard.html
var dashboardHTML []byte

// Server holds the API dependencies.
type Server struct {
	alertStore *alerts.Store
	cfg        *config.Config
	port       string
}

// New creates an API server.
func New(store *alerts.Store, cfg *config.Config, port string) *Server {
	return &Server{alertStore: store, cfg: cfg, port: port}
}

// Start registers routes and begins listening.
func (s *Server) Start() {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleDashboard)
	mux.HandleFunc("/status", s.handleStatus)
	mux.HandleFunc("/kill", s.handleKill)
	mux.HandleFunc("/kill/pid", s.handleKillPID)
	mux.HandleFunc("/alerts", s.handleAlerts)
	mux.HandleFunc("/alerts/clear", s.handleClearAlerts)
	mux.HandleFunc("/tools", s.handleTools)
	mux.HandleFunc("/tools/set", s.handleToolsSet)
addr := "127.0.0.1:" + s.port
	log.Printf("[api] listening on http://%s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("[api] server error: %v", err)
	}
}

// freshConfig reloads the config from disk on every call so that changes made
// via the CLI (enable/disable/category/rules) are immediately visible to the
// daemon without a restart. Falls back to the startup config on read error.
func (s *Server) freshConfig() *config.Config {
	cfg, err := config.Load()
	if err != nil {
		log.Printf("[api] config reload error (using cached): %v", err)
		return s.cfg
	}
	return cfg
}

// handleStatus returns currently detected AI processes.
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	procs, err := watcher.CachedDetectAIProcesses(s.freshConfig())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]interface{}{
		"ai_processes": procs,
		"count":        len(procs),
	})
}

// handleKill terminates all AI processes marked kill=true.
func (s *Server) handleKill(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	killed, err := watcher.KillAll(s.freshConfig())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]interface{}{
		"killed": killed,
		"count":  len(killed),
	})
}

// handleKillPID terminates a single AI process by PID.
// Accepts both JSON body {"pid": 1234} and query string ?pid=1234.
// The PID must belong to a currently detected AI process — arbitrary PIDs
// are rejected to prevent the endpoint being used as a general process killer.
func (s *Server) handleKillPID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}

	var pid int64

	// Try JSON body first: {"pid": 1234}
	var body struct {
		PID int64 `json:"pid"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err == nil && body.PID > 0 {
		pid = body.PID
	} else if q := r.URL.Query().Get("pid"); q != "" {
		// Fall back to query string: ?pid=1234
		var err error
		pid, err = strconv.ParseInt(q, 10, 32)
		if err != nil {
			http.Error(w, "invalid pid query param", http.StatusBadRequest)
			return
		}
	}

	if pid <= 0 {
		http.Error(w, `missing pid — send {"pid": 1234} or ?pid=1234`, http.StatusBadRequest)
		return
	}

	killed, err := watcher.KillByPID(int32(pid), s.freshConfig())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]interface{}{
		"killed": killed,
		"count":  1,
	})
}

// handleAlerts returns all recorded alerts.
func (s *Server) handleAlerts(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.alertStore.All())
}

// handleClearAlerts wipes the alert log.
func (s *Server) handleClearAlerts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	s.alertStore.Clear()
	writeJSON(w, map[string]string{"status": "cleared"})
}

// handleTools returns the full tool list with kill settings.
func (s *Server) handleTools(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, s.freshConfig().Tools)
}

// handleDashboard serves the web dashboard at /.
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(dashboardHTML)
}

// handleToolsSet toggles the kill flag for a single tool and persists the change.
func (s *Server) handleToolsSet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Name string `json:"name"`
		Kill bool   `json:"kill"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	cfg := s.freshConfig()
	if err := cfg.SetKill(body.Name, body.Kill); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := config.Save(cfg); err != nil {
		http.Error(w, "save failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("[api] json encode error: %v", err)
	}
}
