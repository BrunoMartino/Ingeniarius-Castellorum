package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type runCLIInput struct {
	Command string `json:"command" jsonschema:"docker_ps, docker_stats, docker_inspect, docker_logs, docker_images, docker_system_df, docker_network_ls, disk_usage, memory_usage, load_average, coolify_version"`
	Target  string `json:"target,omitempty" jsonschema:"container id/name for docker_inspect and docker_logs"`
	Lines   int    `json:"lines,omitempty" jsonschema:"docker_logs lines, 1-5000, default 200"`
}

func (r *Runtime) runCLI(ctx context.Context, _ *mcp.CallToolRequest, in runCLIInput) (*mcp.CallToolResult, any, error) {
	res, err := r.runner.Run(ctx, in.Command, in.Target, in.Lines)
	// Diagnostics are read-only, but a refusal is worth recording: it is the
	// signal that something tried to run outside the allowlist.
	if err != nil {
		r.record(true, "run_cli("+in.Command+")", in.Target, err)
		return fail(err)
	}
	r.record(false, "run_cli("+in.Command+")", in.Target, nil)
	return ok(res)
}
