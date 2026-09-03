package tools

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"coolify-mcp/internal/cli"
	"coolify-mcp/internal/config"
	"coolify-mcp/internal/coolify"
	"coolify-mcp/internal/guard"
)

func newRuntime(t *testing.T) (*Runtime, *guard.Auditor) {
	t.Helper()
	cfg := &config.Config{
		URL: "https://coolify.example.com", Token: "t", User: "tester",
		Transport: config.TransportStdio, StrictOnAir: true, AllowCLI: true,
		AuditPath: t.TempDir() + "/audit.jsonl", Timeout: 5 * time.Second,
	}
	audit := guard.NewAuditor(cfg.AuditPath)
	client := coolify.NewClient(cfg.URL, cfg.Token, cfg.User, guard.NewRoutePolicy(), audit, &http.Client{})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(cfg, client, audit, cli.NewRunner(cfg.AllowCLI), logger), audit
}

// connect drives the server through a real in-memory MCP session, so the
// registered schemas and handlers are exercised the way a client sees them.
func connect(t *testing.T, r *Runtime) *mcp.ClientSession {
	t.Helper()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	go func() { _ = r.Server().Run(context.Background(), serverTransport) }()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func TestCatalogueMatchesTheDeclaredNames(t *testing.T) {
	r, _ := newRuntime(t)
	res, err := connect(t, r).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(res.Tools))
	for _, tool := range res.Tools {
		got = append(got, tool.Name)
		if strings.TrimSpace(tool.Description) == "" {
			t.Errorf("%s has no description", tool.Name)
		}
		if tool.InputSchema == nil {
			t.Errorf("%s has no input schema", tool.Name)
		}
	}
	want := append([]string(nil), ToolNames...)
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("registered tools differ from ToolNames\n got: %v\nwant: %v", got, want)
	}
}

// A3: the catalogue stays small. A new tool needs a deliberate change here.
func TestCatalogueStaysUnderTheBudget(t *testing.T) {
	if len(ToolNames) > 25 {
		t.Fatalf("the catalogue has %d tools; the architectural budget is 25", len(ToolNames))
	}
}

// R1/R4 read as a naming contract too: nothing in the catalogue offers to
// delete, or to touch tokens, teams, keys or servers.
func TestNoToolOffersDeletionOrEscalation(t *testing.T) {
	forbidden := []string{"delete", "remove", "destroy", "purge", "token", "team", "private_key", "api_settings", "create_server"}
	for _, name := range ToolNames {
		for _, word := range forbidden {
			if strings.Contains(name, word) {
				t.Errorf("%s must not exist: %q is out of scope", name, word)
			}
		}
	}
}

func TestSecretToolsDefaultToMasked(t *testing.T) {
	// An omitted mask and an explicit true both mean masked; only an explicit
	// false unmasks.
	cases := []struct {
		in   secretInput
		want bool
	}{
		{secretInput{UUID: "x"}, true},
		{secretInput{UUID: "x", Mask: ptr(true)}, true},
		{secretInput{UUID: "x", Mask: ptr(false)}, false},
	}
	for _, c := range cases {
		if got := c.in.masked(); got != c.want {
			t.Errorf("masked() = %v, want %v for %+v", got, c.want, c.in)
		}
	}
}

// The http transport must reject a request without the right bearer token.
func TestHTTPTransportRequiresTheBearerToken(t *testing.T) {
	r, _ := newRuntime(t)
	r.cfg.Transport = config.TransportHTTP
	r.cfg.HTTPToken = "s3cret"
	srv := httptest.NewServer(r.HTTPHandler())
	t.Cleanup(srv.Close)

	for _, header := range []string{"", "Bearer wrong", "s3cret", "Basic s3cret"} {
		req, _ := http.NewRequest(http.MethodPost, srv.URL, strings.NewReader("{}"))
		if header != "" {
			req.Header.Set("Authorization", header)
		}
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("Authorization=%q: got %d, want 401", header, resp.StatusCode)
		}
	}
}

// run_cli refusals are recorded: an attempt outside the allowlist is exactly
// what the audit log exists to surface.
func TestDeniedCLICallIsAudited(t *testing.T) {
	r, audit := newRuntime(t)
	session := connect(t, r)
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "coolify_run_cli",
		Arguments: map[string]any{"command": "rm", "target": "everything"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("a command outside the allowlist must fail")
	}
	data := readAudit(t, audit.Path())
	if !strings.Contains(data, guard.CodeDeniedCLI) || !strings.Contains(data, `"user":"tester"`) {
		t.Errorf("audit log did not record the refusal:\n%s", data)
	}
}

func TestCLIRefusalIsNotAuditedAsSuccess(t *testing.T) {
	r, audit := newRuntime(t)
	if _, err := r.runner.Run(context.Background(), "sudo", "", 0); err == nil {
		t.Fatal("expected a refusal")
	}
	r.record(true, "run_cli(sudo)", "", guard.NewError(guard.CodeDeniedCLI, "x"))
	if strings.Contains(readAudit(t, audit.Path()), `"result":"ok"`) {
		t.Error("a refusal was logged as ok")
	}
}

func ptr[T any](v T) *T { return &v }
