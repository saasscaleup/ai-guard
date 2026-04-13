// Package alerts manages the in-memory alert log.
package alerts

import (
	"sync"
	"time"
)

// Alert represents a single suspicious activity record.
type Alert struct {
	ID        int       `json:"id"`
	Time      time.Time `json:"time"`
	Severity  string    `json:"severity"` // "INFO" | "WARN" | "CRITICAL"
	Source    string    `json:"source"`   // "filesystem" | "process" | "network"
	Message   string    `json:"message"`
	Path      string    `json:"path,omitempty"`
	ProcessID int32     `json:"pid,omitempty"`
}

// Store is a thread-safe in-memory alert store.
type Store struct {
	mu     sync.RWMutex
	alerts []Alert
	nextID int
}

// New creates a new alert Store.
func New() *Store {
	return &Store{nextID: 1}
}

// Add appends a new alert and returns it.
func (s *Store) Add(source, message, path string, pid int32) Alert {
	return s.AddWithSeverity(source, "INFO", message, path, pid)
}

// AddWithSeverity appends an alert with an explicit severity level.
func (s *Store) AddWithSeverity(source, severity, message, path string, pid int32) Alert {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := Alert{
		ID:        s.nextID,
		Time:      time.Now(),
		Severity:  severity,
		Source:    source,
		Message:   message,
		Path:      path,
		ProcessID: pid,
	}
	s.alerts = append(s.alerts, a)
	s.nextID++
	return a
}

// All returns a copy of all alerts.
func (s *Store) All() []Alert {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]Alert, len(s.alerts))
	copy(result, s.alerts)
	return result
}

// Clear removes all alerts.
func (s *Store) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.alerts = nil
}
