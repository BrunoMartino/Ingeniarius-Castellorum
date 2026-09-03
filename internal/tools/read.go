package tools

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"coolify-mcp/internal/coolify"
)

type emptyInput struct{}

type uuidInput struct {
	UUID string `json:"uuid" jsonschema:"uuid of the application, database or service"`
}

type searchInput struct {
	Query       string `json:"query,omitempty" jsonschema:"free text matched against name, uuid, description and fqdn"`
	Kind        string `json:"kind,omitempty" jsonschema:"restrict to application, database or service"`
	Project     string `json:"project,omitempty" jsonschema:"project uuid"`
	Environment string `json:"environment,omitempty" jsonschema:"environment name"`
	Status      string `json:"status,omitempty" jsonschema:"raw status, or the coarse state active, inactive or unknown"`
}

type listServersInput struct {
	UUID string `json:"uuid,omitempty" jsonschema:"server uuid; omit to list every server"`
}

type listProjectsInput struct {
	UUID        string `json:"uuid,omitempty" jsonschema:"project uuid; omit to list every project"`
	Environment string `json:"environment,omitempty" jsonschema:"environment name or uuid; requires uuid, returns that environment's detail"`
}

type listDeploymentsInput struct {
	UUID            string `json:"uuid,omitempty" jsonschema:"deployment uuid; returns that deployment's detail"`
	ApplicationUUID string `json:"application_uuid,omitempty" jsonschema:"application uuid; returns that application's deployment history"`
}

type logsInput struct {
	UUID      string `json:"uuid" jsonschema:"uuid of the application, database or service"`
	Lines     int    `json:"lines,omitempty" jsonschema:"how many trailing lines to return, default 200"`
	Container string `json:"container,omitempty" jsonschema:"services only: one container by name or uuid; omit to get every container in the service"`
}

type scheduledTasksInput struct {
	UUID     string `json:"uuid" jsonschema:"uuid of the application or service"`
	TaskUUID string `json:"task_uuid,omitempty" jsonschema:"scheduled task uuid; when set, returns that task's executions instead of the task list"`
}

// Overview is the agent's entry point: what exists, where, and how healthy.
type Overview struct {
	CoolifyURL string             `json:"coolify_url"`
	Servers    []coolify.Server   `json:"servers"`
	Projects   []coolify.Project  `json:"projects"`
	Totals     map[string]int     `json:"totals"`
	Health     map[string]int     `json:"health"`
	Unhealthy  []coolify.Resource `json:"unhealthy"`
}

func (r *Runtime) overview(ctx context.Context, _ *mcp.CallToolRequest, _ emptyInput) (*mcp.CallToolResult, any, error) {
	servers, err := r.client.ListServers(ctx)
	if err != nil {
		r.record(false, "get_infrastructure_overview", "", err)
		return fail(err)
	}
	projects, err := r.client.ListProjects(ctx)
	if err != nil {
		r.record(false, "get_infrastructure_overview", "", err)
		return fail(err)
	}
	resources, err := r.client.Inventory(ctx, true)
	if err != nil {
		r.record(false, "get_infrastructure_overview", "", err)
		return fail(err)
	}

	totals := map[string]int{"servers": len(servers), "projects": len(projects), "resources": len(resources)}
	health := map[string]int{}
	unhealthy := make([]coolify.Resource, 0)
	for _, res := range resources {
		totals[string(res.Kind)]++
		health[res.State]++
		if res.State == "unknown" || containsAny(res.Status, "unhealthy", "degraded") {
			unhealthy = append(unhealthy, res)
		}
	}

	r.record(false, "get_infrastructure_overview", "", nil)
	return ok(Overview{
		CoolifyURL: r.cfg.URL,
		Servers:    servers,
		Projects:   projects,
		Totals:     totals,
		Health:     health,
		Unhealthy:  unhealthy,
	})
}

func (r *Runtime) searchResources(ctx context.Context, _ *mcp.CallToolRequest, in searchInput) (*mcp.CallToolResult, any, error) {
	res, err := r.client.Search(ctx, coolify.SearchOptions{
		Query:       in.Query,
		Kind:        in.Kind,
		Project:     in.Project,
		Environment: in.Environment,
		Status:      in.Status,
	})
	r.record(false, "search_resources", in.Query, err)
	return items(res, err)
}

func (r *Runtime) listUnhealthy(ctx context.Context, _ *mcp.CallToolRequest, _ emptyInput) (*mcp.CallToolResult, any, error) {
	res, err := r.client.Unhealthy(ctx)
	r.record(false, "list_unhealthy_resources", "", err)
	return items(res, err)
}

func (r *Runtime) listServers(ctx context.Context, _ *mcp.CallToolRequest, in listServersInput) (*mcp.CallToolResult, any, error) {
	if in.UUID != "" {
		detail, err := r.client.GetServer(ctx, in.UUID)
		r.record(false, "list_servers", in.UUID, err)
		if err != nil {
			return fail(err)
		}
		return ok(detail)
	}
	servers, err := r.client.ListServers(ctx)
	r.record(false, "list_servers", "", err)
	return items(servers, err)
}

func (r *Runtime) listProjects(ctx context.Context, _ *mcp.CallToolRequest, in listProjectsInput) (*mcp.CallToolResult, any, error) {
	switch {
	case in.UUID != "" && in.Environment != "":
		env, err := r.client.GetEnvironment(ctx, in.UUID, in.Environment)
		r.record(false, "list_projects", in.UUID, err)
		if err != nil {
			return fail(err)
		}
		return ok(map[string]any{"project_uuid": in.UUID, "environment": env})
	case in.UUID != "":
		project, err := r.client.GetProject(ctx, in.UUID)
		r.record(false, "list_projects", in.UUID, err)
		if err != nil {
			return fail(err)
		}
		return ok(project)
	default:
		projects, err := r.client.ListProjects(ctx)
		r.record(false, "list_projects", "", err)
		return items(projects, err)
	}
}

func (r *Runtime) getResource(ctx context.Context, _ *mcp.CallToolRequest, in uuidInput) (*mcp.CallToolResult, any, error) {
	res, raw, err := r.client.Detail(ctx, in.UUID)
	r.record(false, "get_resource", in.UUID, err)
	if err != nil {
		return fail(err)
	}
	return ok(map[string]any{"summary": res, "detail": raw})
}

func (r *Runtime) listDeployments(ctx context.Context, _ *mcp.CallToolRequest, in listDeploymentsInput) (*mcp.CallToolResult, any, error) {
	raw, err := r.client.ListDeployments(ctx, in.UUID, in.ApplicationUUID)
	target := in.UUID
	if target == "" {
		target = in.ApplicationUUID
	}
	r.record(false, "list_deployments", target, err)
	if err != nil {
		return fail(err)
	}
	return ok(map[string]any{"deployments": raw})
}

func (r *Runtime) getLogs(ctx context.Context, _ *mcp.CallToolRequest, in logsInput) (*mcp.CallToolResult, any, error) {
	lines := in.Lines
	if lines <= 0 {
		lines = 200
	}
	containers, err := r.client.Logs(ctx, in.UUID, lines, in.Container)
	r.record(false, "get_logs", in.UUID, err)
	if err != nil {
		return fail(err)
	}
	return ok(map[string]any{
		"uuid":       in.UUID,
		"lines":      lines,
		"containers": containers,
	})
}

func (r *Runtime) listEnvKeys(ctx context.Context, _ *mcp.CallToolRequest, in uuidInput) (*mcp.CallToolResult, any, error) {
	keys, err := r.client.ListEnvKeys(ctx, in.UUID)
	r.record(false, "list_env_keys", in.UUID, err)
	return items(keys, err)
}

func (r *Runtime) listStorages(ctx context.Context, _ *mcp.CallToolRequest, in uuidInput) (*mcp.CallToolResult, any, error) {
	raw, err := r.client.Storages(ctx, in.UUID)
	r.record(false, "list_storages", in.UUID, err)
	if err != nil {
		return fail(err)
	}
	return ok(map[string]any{"uuid": in.UUID, "storages": raw})
}

func (r *Runtime) listScheduledTasks(ctx context.Context, _ *mcp.CallToolRequest, in scheduledTasksInput) (*mcp.CallToolResult, any, error) {
	raw, err := r.client.ScheduledTasks(ctx, in.UUID, in.TaskUUID)
	r.record(false, "list_scheduled_tasks", in.UUID, err)
	if err != nil {
		return fail(err)
	}
	key := "tasks"
	if in.TaskUUID != "" {
		key = "executions"
	}
	return ok(map[string]any{"uuid": in.UUID, key: raw})
}

func containsAny(haystack string, needles ...string) bool {
	haystack = strings.ToLower(haystack)
	for _, n := range needles {
		if n != "" && strings.Contains(haystack, n) {
			return true
		}
	}
	return false
}
