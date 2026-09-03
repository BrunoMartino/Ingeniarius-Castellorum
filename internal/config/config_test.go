package config

import (
	"testing"
	"time"

	"coolify-mcp/internal/guard"
)

func env(pairs map[string]string) func(string) string {
	return func(k string) string { return pairs[k] }
}

func base() map[string]string {
	return map[string]string{
		"COOLIFY_URL":            "https://coolify.example.com",
		"COOLIFY_API_TOKEN":      "token",
		"COOLIFY_USER":           "bruno",
		"COOLIFY_MCP_AUDIT_PATH": "/tmp/audit.jsonl",
	}
}

func TestDefaults(t *testing.T) {
	cfg, err := Load(env(base()), Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Transport != TransportStdio {
		t.Errorf("transport = %q, want stdio", cfg.Transport)
	}
	if !cfg.StrictOnAir {
		t.Error("COOLIFY_MCP_STRICT_ONAIR must default to true (fail closed)")
	}
	if !cfg.AllowCLI {
		t.Error("COOLIFY_MCP_ALLOW_CLI must default to true")
	}
	if cfg.AllowPrivateKeys {
		t.Error("COOLIFY_MCP_ALLOW_PRIVATE_KEYS must default to false")
	}
	if cfg.Timeout != 30*time.Second {
		t.Errorf("timeout = %v, want 30s", cfg.Timeout)
	}
}

// The http transport exposes write tools over the network; booting without a
// bearer token must be impossible.
func TestHTTPTransportRequiresAToken(t *testing.T) {
	e := base()
	e["COOLIFY_MCP_TRANSPORT"] = "http"
	if _, err := Load(env(e), Overrides{}); !guard.IsCode(err, guard.CodeConfig) {
		t.Fatalf("want CONFIG_ERROR, got %v", err)
	}
	// The --transport flag must not be a way around it either.
	if _, err := Load(env(base()), Overrides{Transport: "http"}); !guard.IsCode(err, guard.CodeConfig) {
		t.Fatalf("flag path: want CONFIG_ERROR, got %v", err)
	}
	e["COOLIFY_MCP_HTTP_TOKEN"] = "secret"
	cfg, err := Load(env(e), Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Transport != TransportHTTP || cfg.HTTPAddr != ":8788" {
		t.Errorf("got transport=%q addr=%q", cfg.Transport, cfg.HTTPAddr)
	}
}

func TestRequiredFields(t *testing.T) {
	for _, key := range []string{"COOLIFY_URL", "COOLIFY_API_TOKEN", "COOLIFY_USER"} {
		e := base()
		delete(e, key)
		if _, err := Load(env(e), Overrides{}); !guard.IsCode(err, guard.CodeConfig) {
			t.Errorf("missing %s: want CONFIG_ERROR, got %v", key, err)
		}
	}
	for _, bad := range []string{"coolify.example.com", "ftp://x", "/not/a/url", "https://"} {
		e := base()
		e["COOLIFY_URL"] = bad
		if _, err := Load(env(e), Overrides{}); !guard.IsCode(err, guard.CodeConfig) {
			t.Errorf("COOLIFY_URL=%q: want CONFIG_ERROR, got %v", bad, err)
		}
	}
}

func TestStrictOnAirCanBeTurnedOff(t *testing.T) {
	e := base()
	e["COOLIFY_MCP_STRICT_ONAIR"] = "false"
	cfg, err := Load(env(e), Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StrictOnAir {
		t.Error("COOLIFY_MCP_STRICT_ONAIR=false was not honoured")
	}
}

// Redacted is what gets logged at boot; the two secrets must never appear.
func TestRedactedHidesSecrets(t *testing.T) {
	e := base()
	e["COOLIFY_MCP_TRANSPORT"] = "http"
	e["COOLIFY_MCP_HTTP_TOKEN"] = "http-secret"
	e["COOLIFY_API_TOKEN"] = "api-secret"
	cfg, err := Load(env(e), Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range cfg.Redacted() {
		if s, ok := value.(string); ok && (s == "api-secret" || s == "http-secret") {
			t.Errorf("Redacted() leaked a secret in %q", key)
		}
	}
}

func TestParseDotEnv(t *testing.T) {
	got := parseDotEnv([]byte("# comment\n\nexport COOLIFY_URL=https://x\nCOOLIFY_USER=\"bruno\"\nQUOTED='v'\nnot a pair\n"))
	want := map[string]string{"COOLIFY_URL": "https://x", "COOLIFY_USER": "bruno", "QUOTED": "v"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if len(got) != len(want) {
		t.Errorf("parsed %d entries, want %d: %v", len(got), len(want), got)
	}
}
