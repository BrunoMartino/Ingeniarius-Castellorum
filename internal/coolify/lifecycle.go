package coolify

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"

	"coolify-mcp/internal/guard"
)

// Lifecycle actions. R2 deliberately does not apply here: stopping, starting or
// restarting a running resource is the whole point.
const (
	ActionStart   = "start"
	ActionStop    = "stop"
	ActionRestart = "restart"
)

// Control starts, stops or restarts a resource of any kind.
func (c *Client) Control(ctx context.Context, uuid, action string) (json.RawMessage, error) {
	action = strings.ToLower(strings.TrimSpace(action))
	switch action {
	case ActionStart, ActionStop, ActionRestart:
	default:
		return nil, guard.NewError(guard.CodeBadInput,
			"action must be one of start, stop, restart (got "+quoteEmpty(action)+")")
	}
	res, err := c.Resolve(ctx, uuid)
	if err != nil {
		return nil, err
	}
	var out json.RawMessage
	err = c.Post(ctx, res.Kind.segment()+"/"+uuid+"/"+action, nil, nil, &out)
	c.cache.invalidate()
	if err != nil {
		return nil, err
	}
	return out, nil
}

// DeployOptions are the query parameters Coolify's POST /deploy accepts.
//
// Note: the API deploys the branch already configured on the application. To
// deploy a different branch, change git_branch through update_application_config
// first, then deploy. DockerTag applies to docker-image applications.
type DeployOptions struct {
	Force     bool
	DockerTag string
}

func (c *Client) Deploy(ctx context.Context, uuid string, opts DeployOptions) (json.RawMessage, error) {
	if strings.TrimSpace(uuid) == "" {
		return nil, guard.NewError(guard.CodeBadInput, "uuid is required")
	}
	q := url.Values{}
	q.Set("uuid", uuid)
	if opts.Force {
		q.Set("force", "true")
	}
	if tag := strings.TrimSpace(opts.DockerTag); tag != "" {
		q.Set("docker_tag", tag)
	}
	var out json.RawMessage
	if err := c.Post(ctx, "/deploy", q, nil, &out); err != nil {
		return nil, err
	}
	c.cache.invalidate()
	return out, nil
}

func (c *Client) CancelDeployment(ctx context.Context, deploymentUUID string) (json.RawMessage, error) {
	if strings.TrimSpace(deploymentUUID) == "" {
		return nil, guard.NewError(guard.CodeBadInput, "deployment_uuid is required")
	}
	var out json.RawMessage
	if err := c.Post(ctx, "/deployments/"+deploymentUUID+"/cancel", nil, nil, &out); err != nil {
		return nil, err
	}
	c.cache.invalidate()
	return out, nil
}

// ListDeployments returns running deployments, one deployment by uuid, or the
// deployment history of one application.
func (c *Client) ListDeployments(ctx context.Context, deploymentUUID, applicationUUID string) (json.RawMessage, error) {
	switch {
	case strings.TrimSpace(deploymentUUID) != "":
		return c.GetRaw(ctx, "/deployments/"+deploymentUUID, nil)
	case strings.TrimSpace(applicationUUID) != "":
		return c.GetRaw(ctx, "/deployments/applications/"+applicationUUID, nil)
	default:
		return c.GetRaw(ctx, "/deployments", nil)
	}
}

func (c *inventoryCache) invalidate() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.items = nil
	c.mu.Unlock()
}

func quoteEmpty(s string) string {
	if s == "" {
		return `""`
	}
	return s
}
