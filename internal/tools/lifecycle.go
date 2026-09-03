package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"coolify-mcp/internal/coolify"
)

type controlInput struct {
	UUID   string `json:"uuid" jsonschema:"uuid of the application, database or service"`
	Action string `json:"action" jsonschema:"start, stop or restart"`
}

type deployInput struct {
	UUID      string `json:"uuid" jsonschema:"uuid of the application or service to deploy"`
	Force     bool   `json:"force,omitempty" jsonschema:"rebuild without the build cache"`
	DockerTag string `json:"docker_tag,omitempty" jsonschema:"image tag to deploy, for docker-image applications only"`
}

type cancelInput struct {
	DeploymentUUID string `json:"deployment_uuid" jsonschema:"uuid of the running deployment, from list_deployments"`
}

func (r *Runtime) control(ctx context.Context, _ *mcp.CallToolRequest, in controlInput) (*mcp.CallToolResult, any, error) {
	raw, err := r.client.Control(ctx, in.UUID, in.Action)
	r.record(true, "control("+in.Action+")", in.UUID, err)
	if err != nil {
		return fail(err)
	}
	out := map[string]any{"uuid": in.UUID, "action": in.Action, "result": raw}
	if in.Action != coolify.ActionStop {
		out["next"] = coolify.PostDeployWarning
	}
	return ok(out)
}

func (r *Runtime) deploy(ctx context.Context, _ *mcp.CallToolRequest, in deployInput) (*mcp.CallToolResult, any, error) {
	raw, err := r.client.Deploy(ctx, in.UUID, coolify.DeployOptions{Force: in.Force, DockerTag: in.DockerTag})
	r.record(true, "deploy", in.UUID, err)
	if err != nil {
		return fail(err)
	}
	return ok(map[string]any{
		"uuid":     in.UUID,
		"result":   raw,
		"deployed": false,
		"next":     coolify.PostDeployWarning,
	})
}

func (r *Runtime) cancelDeployment(ctx context.Context, _ *mcp.CallToolRequest, in cancelInput) (*mcp.CallToolResult, any, error) {
	raw, err := r.client.CancelDeployment(ctx, in.DeploymentUUID)
	r.record(true, "cancel_deployment", in.DeploymentUUID, err)
	if err != nil {
		return fail(err)
	}
	return ok(map[string]any{"deployment_uuid": in.DeploymentUUID, "result": raw})
}
