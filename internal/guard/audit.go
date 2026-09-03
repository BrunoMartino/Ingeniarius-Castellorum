package guard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// AuditEntry is one JSONL line. It records what was attempted, by whom, against
// what, and how it ended — including refusals. It never records secret values.
type AuditEntry struct {
	Timestamp string `json:"ts"`
	User      string `json:"user"`
	Tool      string `json:"tool"`
	Target    string `json:"target,omitempty"`
	Method    string `json:"method,omitempty"`
	Path      string `json:"path,omitempty"`
	Result    string `json:"result"`
	Code      string `json:"code,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

const (
	ResultOK     = "ok"
	ResultDenied = "denied"
	ResultError  = "error"
)

// Auditor appends AuditEntry lines to a JSONL file. A write failure never
// blocks the operation; it is surfaced through LastErr for the boot check.
type Auditor struct {
	mu      sync.Mutex
	path    string
	LastErr error
}

func NewAuditor(path string) *Auditor {
	return &Auditor{path: path}
}

// Path is the file the auditor appends to. Empty means auditing is disabled.
func (a *Auditor) Path() string {
	if a == nil {
		return ""
	}
	return a.path
}

func (a *Auditor) Write(e AuditEntry) {
	if a == nil || a.path == "" {
		return
	}
	if e.Timestamp == "" {
		e.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	}
	line, err := json.Marshal(e)
	if err != nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(a.path), 0o700); err != nil {
		a.LastErr = err
		return
	}
	f, err := os.OpenFile(a.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		a.LastErr = err
		return
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		a.LastErr = err
	}
}

// Record is the common shape: derive result and code from err.
func (a *Auditor) Record(user, tool, target, method, path string, err error) {
	e := AuditEntry{User: user, Tool: tool, Target: target, Method: method, Path: path}
	switch code := Code(err); {
	case err == nil:
		e.Result = ResultOK
	case strings.HasPrefix(code, "DENIED_"):
		e.Result = ResultDenied
		e.Code = code
		e.Detail = err.Error()
	default:
		e.Result = ResultError
		e.Code = code
		e.Detail = err.Error()
	}
	a.Write(e)
}
