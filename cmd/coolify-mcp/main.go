// Command coolify-mcp is Ingeniarius Castellorum: an MCP server that operates a
// Coolify v4 instance under hard guardrails.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"coolify-mcp/internal/cli"
	"coolify-mcp/internal/config"
	"coolify-mcp/internal/coolify"
	"coolify-mcp/internal/guard"
	"coolify-mcp/internal/tools"
)

func main() {
	transport := flag.String("transport", "", "stdio (default) or http")
	addr := flag.String("addr", "", "listen address for the http transport, e.g. :8788")
	dotenv := flag.String("env-file", os.Getenv("DOTENV_PATH"), "path to a .env file; the process environment always wins")
	flag.Parse()

	// stdio carries the MCP protocol on stdout, so every log line goes to stderr.
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	if err := run(logger, *transport, *addr, *dotenv); err != nil {
		logger.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger, transport, addr, dotenvPath string) error {
	cfg, err := config.Load(
		config.Getenv(config.LoadDotEnv(dotenvPath)),
		config.Overrides{Transport: transport, Addr: addr},
	)
	if err != nil {
		return err
	}

	audit := guard.NewAuditor(cfg.AuditPath)
	// Prove the audit log is writable before serving: A8 is not optional, and
	// discovering it at the first mutation is too late.
	audit.Write(guard.AuditEntry{User: cfg.User, Tool: "boot", Result: guard.ResultOK})
	if audit.LastErr != nil {
		return fmt.Errorf("audit log %s is not writable: %w", cfg.AuditPath, audit.LastErr)
	}

	client := coolify.NewClient(
		cfg.URL, cfg.Token, cfg.User,
		guard.NewRoutePolicy(), audit,
		&http.Client{Timeout: cfg.Timeout},
		logger,
	)
	runtime := tools.New(cfg, client, audit, cli.NewRunner(cfg.AllowCLI), logger)

	logger.Info("ingeniarius-castellorum starting", slogArgs(cfg.Redacted())...)

	if cfg.Transport == config.TransportHTTP {
		server := &http.Server{
			Addr:              cfg.HTTPAddr,
			Handler:           runtime.HTTPHandler(),
			ReadHeaderTimeout: 10 * time.Second,
		}
		logger.Info("listening", "addr", cfg.HTTPAddr)
		return server.ListenAndServe()
	}
	// A client closing the pipe is a normal shutdown, not a failure.
	if err := runtime.Server().Run(context.Background(), &mcp.StdioTransport{}); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	logger.Info("client disconnected, shutting down")
	return nil
}

func slogArgs(m map[string]any) []any {
	args := make([]any, 0, len(m)*2)
	for k, v := range m {
		args = append(args, k, v)
	}
	return args
}
