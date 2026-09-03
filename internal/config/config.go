package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"coolify-mcp/internal/guard"
)

const (
	TransportStdio = "stdio"
	TransportHTTP  = "http"
)

// Config is the immutable product of Load for one MCP process (one Coolify
// instance, one logical user).
type Config struct {
	URL              string
	Token            string
	User             string
	Transport        string
	HTTPAddr         string
	HTTPToken        string
	StrictOnAir      bool
	AllowCLI         bool
	AllowPrivateKeys bool
	AuditPath        string
	Timeout          time.Duration
}

// Overrides carries command-line flags, which win over the environment.
type Overrides struct {
	Transport string
	Addr      string
}

// Load builds and validates the config, failing fast (A1: a misconfigured
// process must not start and then refuse every call).
func Load(get func(string) string, ov Overrides) (*Config, error) {
	cfg := &Config{
		URL:       strings.TrimRight(strings.TrimSpace(get("COOLIFY_URL")), "/"),
		Token:     strings.TrimSpace(get("COOLIFY_API_TOKEN")),
		User:      strings.TrimSpace(get("COOLIFY_USER")),
		Transport: firstNonEmpty(ov.Transport, get("COOLIFY_MCP_TRANSPORT"), TransportStdio),
		HTTPAddr:  firstNonEmpty(ov.Addr, get("COOLIFY_MCP_HTTP_ADDR"), ":8788"),
		HTTPToken: strings.TrimSpace(get("COOLIFY_MCP_HTTP_TOKEN")),
	}

	if cfg.URL == "" {
		return nil, guard.NewError(guard.CodeConfig, "COOLIFY_URL is required")
	}
	u, err := url.Parse(cfg.URL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, guard.NewError(guard.CodeConfig, "COOLIFY_URL must be an absolute http(s) URL")
	}
	if cfg.Token == "" {
		return nil, guard.NewError(guard.CodeConfig, "COOLIFY_API_TOKEN is required")
	}
	if cfg.User == "" {
		return nil, guard.NewError(guard.CodeConfig, "COOLIFY_USER is required (it identifies the caller in the audit log)")
	}

	cfg.Transport = strings.ToLower(cfg.Transport)
	if cfg.Transport != TransportStdio && cfg.Transport != TransportHTTP {
		return nil, guard.NewError(guard.CodeConfig,
			"COOLIFY_MCP_TRANSPORT must be "+TransportStdio+" or "+TransportHTTP)
	}
	// The HTTP transport exposes write and read:sensitive tools over the
	// network; refusing to boot without a bearer token is not negotiable.
	if cfg.Transport == TransportHTTP && cfg.HTTPToken == "" {
		return nil, guard.NewError(guard.CodeConfig,
			"COOLIFY_MCP_HTTP_TOKEN is required when the transport is http")
	}

	cfg.StrictOnAir = boolEnv(get("COOLIFY_MCP_STRICT_ONAIR"), true)
	cfg.AllowCLI = boolEnv(get("COOLIFY_MCP_ALLOW_CLI"), true)
	cfg.AllowPrivateKeys = boolEnv(get("COOLIFY_MCP_ALLOW_PRIVATE_KEYS"), false)

	cfg.AuditPath, err = expandHome(firstNonEmpty(get("COOLIFY_MCP_AUDIT_PATH"), "~/.coolify-mcp/audit.jsonl"))
	if err != nil {
		return nil, guard.NewError(guard.CodeConfig, "COOLIFY_MCP_AUDIT_PATH: "+err.Error())
	}

	cfg.Timeout, err = durationEnv(get("COOLIFY_MCP_TIMEOUT"), 30*time.Second)
	if err != nil {
		return nil, guard.NewError(guard.CodeConfig, "COOLIFY_MCP_TIMEOUT: "+err.Error())
	}

	return cfg, nil
}

// Redacted is a log-safe view of the config. Token and HTTPToken never appear.
func (c *Config) Redacted() map[string]any {
	return map[string]any{
		"url":                c.URL,
		"user":               c.User,
		"token_len":          len(c.Token),
		"transport":          c.Transport,
		"http_addr":          c.HTTPAddr,
		"strict_onair":       c.StrictOnAir,
		"allow_cli":          c.AllowCLI,
		"allow_private_keys": c.AllowPrivateKeys,
		"audit_path":         c.AuditPath,
		"timeout":            c.Timeout.String(),
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func boolEnv(raw string, def bool) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return def
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return def
	}
	return v
}

func durationEnv(raw string, def time.Duration) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return def, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%q is not a duration (try 30s)", raw)
	}
	if d <= 0 {
		return 0, fmt.Errorf("must be positive")
	}
	return d, nil
}

func expandHome(path string) (string, error) {
	if !strings.HasPrefix(path, "~") {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~")), nil
}
