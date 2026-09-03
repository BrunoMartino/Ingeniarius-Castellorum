// Package tools exposes the Coolify operations as MCP tools.
//
// Nothing here enforces policy on its own: R1/R3/R4 live in the route
// allowlist inside the HTTP client, R2 lives in the on-air guard, and the CLI
// allowlist lives in package cli. This layer wires arguments, records the
// audit entry, and shapes the result.
package tools

import (
	"log/slog"
	"net/http"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"coolify-mcp/internal/cli"
	"coolify-mcp/internal/config"
	"coolify-mcp/internal/coolify"
	"coolify-mcp/internal/guard"
)

const (
	serverName    = "ingeniarius-castellorum"
	serverVersion = "1.0.0"
)

// Runtime holds the wired dependencies and the MCP server.
type Runtime struct {
	cfg    *config.Config
	client *coolify.Client
	onAir  *guard.OnAirGuard
	runner *cli.Runner
	audit  *guard.Auditor
	logger *slog.Logger
	server *mcp.Server
}

func New(cfg *config.Config, client *coolify.Client, audit *guard.Auditor, runner *cli.Runner, logger *slog.Logger) *Runtime {
	if logger == nil {
		logger = slog.Default()
	}
	r := &Runtime{
		cfg:    cfg,
		client: client,
		onAir:  guard.NewOnAirGuard(cfg.StrictOnAir),
		runner: runner,
		audit:  audit,
		logger: logger,
	}
	r.server = mcp.NewServer(&mcp.Implementation{
		Name:    serverName,
		Version: serverVersion,
	}, &mcp.ServerOptions{Instructions: instructions, Logger: logger})
	r.register()
	return r
}

func (r *Runtime) Server() *mcp.Server { return r.server }

// HTTPHandler wraps the server for the streamable HTTP transport, behind the
// bearer token the config insists on.
func (r *Runtime) HTTPHandler() http.Handler {
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return r.server
	}, nil)
	return r.requireBearer(handler)
}

func (r *Runtime) requireBearer(next http.Handler) http.Handler {
	expected := "Bearer " + r.cfg.HTTPToken
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if subtleCompare(req.Header.Get("Authorization"), expected) {
			next.ServeHTTP(w, req)
			return
		}
		w.Header().Set("WWW-Authenticate", `Bearer realm="`+serverName+`"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	})
}

// record writes the audit entry for a call. Read-only tools pass audit=false;
// mutations and sensitive reads always audit, including their refusals.
func (r *Runtime) record(audit bool, tool, target string, err error) {
	if audit {
		r.audit.Record(r.cfg.User, tool, target, "", "", err)
	}
	if err != nil {
		r.logger.Warn("tool failed", "tool", tool, "target", target, "code", guard.Code(err), "err", err)
		return
	}
	r.logger.Info("tool", "tool", tool, "target", target)
}

// inputSchema derives the JSON schema for a tool's arguments.
func inputSchema[T any]() *jsonschema.Schema {
	s, err := jsonschema.For[T](nil)
	if err != nil {
		panic(err)
	}
	s.AdditionalProperties = &jsonschema.Schema{}
	return s
}

func ok(v any) (*mcp.CallToolResult, any, error) {
	return nil, v, nil
}

func fail(err error) (*mcp.CallToolResult, any, error) {
	return nil, nil, err
}

func items[T any](v []T, err error) (*mcp.CallToolResult, any, error) {
	if err != nil {
		return fail(err)
	}
	if v == nil {
		v = []T{}
	}
	return ok(map[string]any{"items": v, "count": len(v)})
}

// subtleCompare is a constant-time equality check for the HTTP bearer token.
func subtleCompare(got, want string) bool {
	if len(got) != len(want) {
		return false
	}
	var diff byte
	for i := 0; i < len(got); i++ {
		diff |= got[i] ^ want[i]
	}
	return diff == 0
}
