package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// secretInput carries an explicit Mask pointer so an omitted field and an
// explicit false are distinguishable: omitted means masked.
type secretInput struct {
	UUID string `json:"uuid" jsonschema:"uuid of the resource"`
	Mask *bool  `json:"mask,omitempty" jsonschema:"default true; pass false only when the real value is genuinely needed, which is recorded in the audit log"`
}

// masked resolves the default. Omitting mask means masked.
func (in secretInput) masked() bool {
	return in.Mask == nil || *in.Mask
}

func (r *Runtime) getEnvValues(ctx context.Context, _ *mcp.CallToolRequest, in secretInput) (*mcp.CallToolResult, any, error) {
	mask := in.masked()
	vars, err := r.client.GetEnvValues(ctx, in.UUID, mask)
	// Sensitive reads are always audited, and an unmasked read is called out
	// as its own tool name so it stands out when the log is reviewed.
	tool := "get_env_values"
	if !mask {
		tool = "get_env_values(unmasked)"
	}
	r.record(true, tool, in.UUID, err)
	if err != nil {
		return fail(err)
	}
	return ok(map[string]any{"uuid": in.UUID, "masked": mask, "count": len(vars), "variables": vars})
}

func (r *Runtime) getDatabaseCredentials(ctx context.Context, _ *mcp.CallToolRequest, in secretInput) (*mcp.CallToolResult, any, error) {
	mask := in.masked()
	creds, err := r.client.GetDatabaseCredentials(ctx, in.UUID, mask)
	tool := "get_database_credentials"
	if !mask {
		tool = "get_database_credentials(unmasked)"
	}
	r.record(true, tool, in.UUID, err)
	if err != nil {
		return fail(err)
	}
	return ok(creds)
}
