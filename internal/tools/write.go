package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"coolify-mcp/internal/coolify"
	"coolify-mcp/internal/guard"
)

type createProjectInput struct {
	Name        string `json:"name" jsonschema:"project name"`
	Description string `json:"description,omitempty"`
}

type createApplicationInput struct {
	Source          string         `json:"source" jsonschema:"public_repo, private_repo_github, dockerfile, docker_compose or docker_image"`
	ProjectUUID     string         `json:"project_uuid" jsonschema:"target project uuid"`
	ServerUUID      string         `json:"server_uuid" jsonschema:"target server uuid"`
	EnvironmentName string         `json:"environment_name" jsonschema:"target environment name, usually production"`
	EnvironmentUUID string         `json:"environment_uuid,omitempty" jsonschema:"target environment uuid; Coolify requires it alongside environment_name"`
	Name            string         `json:"name,omitempty"`
	Fields          map[string]any `json:"fields,omitempty" jsonschema:"source-specific fields, passed to Coolify verbatim: git_repository, git_branch, build_pack, dockerfile, docker_registry_image_name, ports_exposes, domains and so on"`
}

type createDatabaseInput struct {
	Engine          string         `json:"engine" jsonschema:"postgresql, mysql, mariadb, mongodb, redis, keydb, clickhouse or dragonfly"`
	ProjectUUID     string         `json:"project_uuid"`
	ServerUUID      string         `json:"server_uuid"`
	EnvironmentName string         `json:"environment_name"`
	EnvironmentUUID string         `json:"environment_uuid,omitempty"`
	Name            string         `json:"name,omitempty"`
	Fields          map[string]any `json:"fields,omitempty" jsonschema:"engine-specific fields passed verbatim, such as postgres_user, postgres_db or image"`
}

type createServiceInput struct {
	Type            string         `json:"type,omitempty" jsonschema:"one-click service type; required unless docker_compose_raw is given"`
	ProjectUUID     string         `json:"project_uuid"`
	ServerUUID      string         `json:"server_uuid"`
	EnvironmentName string         `json:"environment_name"`
	EnvironmentUUID string         `json:"environment_uuid,omitempty"`
	Name            string         `json:"name,omitempty"`
	Fields          map[string]any `json:"fields,omitempty" jsonschema:"extra fields passed verbatim, such as docker_compose_raw or description"`
}

type updateAppConfigInput struct {
	UUID    string         `json:"uuid" jsonschema:"application uuid"`
	Fields  map[string]any `json:"fields" jsonschema:"settings to patch: build_pack, build_command, install_command, start_command, ports_exposes, health_check_*, limits_*, git_branch and so on"`
	Confirm bool           `json:"confirm,omitempty" jsonschema:"edit in place while the application is on air; without it an active application is refused"`
}

type upsertEnvInput struct {
	UUID      string             `json:"uuid" jsonschema:"uuid of the application, database or service"`
	Variables []coolify.EnvInput `json:"variables" jsonschema:"variables to create or update; existing keys are overwritten and nothing is ever removed"`
	Confirm   bool               `json:"confirm,omitempty" jsonschema:"edit in place while the resource is on air; without it an active resource is refused"`
}

type updateDomainsInput struct {
	UUID    string   `json:"uuid" jsonschema:"application or service uuid"`
	Domains []string `json:"domains" jsonschema:"the full FQDN list for the resource; this replaces the current list"`
	Confirm bool     `json:"confirm,omitempty" jsonschema:"edit in place while the resource is on air; without it an active resource is refused"`
}

// placement copies the four fields every creation endpoint requires into the
// verbatim field map, so the agent does not have to know both shapes.
func placement(fields map[string]any, project, server, envName, envUUID, name string) (map[string]any, error) {
	out := map[string]any{}
	for k, v := range fields {
		out[k] = v
	}
	if project == "" || server == "" || envName == "" {
		return nil, guard.NewError(guard.CodeBadInput,
			"project_uuid, server_uuid and environment_name are required; find them with list_projects and list_servers")
	}
	out["project_uuid"] = project
	out["server_uuid"] = server
	out["environment_name"] = envName
	if envUUID != "" {
		out["environment_uuid"] = envUUID
	}
	if name != "" {
		out["name"] = name
	}
	return out, nil
}

func (r *Runtime) createProject(ctx context.Context, _ *mcp.CallToolRequest, in createProjectInput) (*mcp.CallToolResult, any, error) {
	raw, err := r.client.CreateProject(ctx, in.Name, in.Description)
	r.record(true, "create_project", in.Name, err)
	if err != nil {
		return fail(err)
	}
	return ok(map[string]any{"project": raw})
}

func (r *Runtime) createApplication(ctx context.Context, _ *mcp.CallToolRequest, in createApplicationInput) (*mcp.CallToolResult, any, error) {
	fields, err := placement(in.Fields, in.ProjectUUID, in.ServerUUID, in.EnvironmentName, in.EnvironmentUUID, in.Name)
	if err != nil {
		r.record(true, "create_application", in.Name, err)
		return fail(err)
	}
	raw, err := r.client.CreateApplication(ctx, in.Source, fields)
	r.record(true, "create_application", in.Name, err)
	if err != nil {
		return fail(err)
	}
	return ok(map[string]any{"application": raw, "note": createdStoppedNote})
}

func (r *Runtime) createDatabase(ctx context.Context, _ *mcp.CallToolRequest, in createDatabaseInput) (*mcp.CallToolResult, any, error) {
	fields, err := placement(in.Fields, in.ProjectUUID, in.ServerUUID, in.EnvironmentName, in.EnvironmentUUID, in.Name)
	if err != nil {
		r.record(true, "create_database", in.Name, err)
		return fail(err)
	}
	raw, err := r.client.CreateDatabase(ctx, in.Engine, fields)
	r.record(true, "create_database", in.Name, err)
	if err != nil {
		return fail(err)
	}
	return ok(map[string]any{"database": raw, "note": createdStoppedNote})
}

func (r *Runtime) createService(ctx context.Context, _ *mcp.CallToolRequest, in createServiceInput) (*mcp.CallToolResult, any, error) {
	fields, err := placement(in.Fields, in.ProjectUUID, in.ServerUUID, in.EnvironmentName, in.EnvironmentUUID, in.Name)
	if err != nil {
		r.record(true, "create_service", in.Name, err)
		return fail(err)
	}
	if in.Type != "" {
		fields["type"] = in.Type
	}
	raw, err := r.client.CreateService(ctx, fields)
	r.record(true, "create_service", in.Name, err)
	if err != nil {
		return fail(err)
	}
	return ok(map[string]any{"service": raw, "note": createdStoppedNote})
}

const createdStoppedNote = "the resource was created stopped; call deploy(uuid) when you are ready to bring it up"

func (r *Runtime) updateApplicationConfig(ctx context.Context, _ *mcp.CallToolRequest, in updateAppConfigInput) (*mcp.CallToolResult, any, error) {
	m, err := r.client.UpdateApplicationConfig(ctx, r.onAir, in.UUID, in.Fields, in.Confirm)
	r.record(true, "update_application_config", in.UUID, err)
	if err != nil {
		return fail(err)
	}
	return ok(m)
}

func (r *Runtime) upsertEnv(ctx context.Context, _ *mcp.CallToolRequest, in upsertEnvInput) (*mcp.CallToolResult, any, error) {
	m, err := r.client.UpsertEnv(ctx, r.onAir, in.UUID, in.Variables, in.Confirm)
	r.record(true, "upsert_env", in.UUID, err)
	if err != nil {
		return fail(err)
	}
	// The values were secrets on the way in; do not echo them back.
	keys := make([]string, 0, len(in.Variables))
	for _, v := range in.Variables {
		keys = append(keys, v.Key)
	}
	return ok(map[string]any{"mutation": m, "keys": keys})
}

func (r *Runtime) updateDomains(ctx context.Context, _ *mcp.CallToolRequest, in updateDomainsInput) (*mcp.CallToolResult, any, error) {
	m, err := r.client.UpdateDomains(ctx, r.onAir, in.UUID, in.Domains, in.Confirm)
	r.record(true, "update_domains", in.UUID, err)
	if err != nil {
		return fail(err)
	}
	return ok(m)
}

func (r *Runtime) repairResource(ctx context.Context, _ *mcp.CallToolRequest, in uuidInput) (*mcp.CallToolResult, any, error) {
	m, err := r.client.RepairResource(ctx, r.onAir, in.UUID)
	r.record(true, "repair_resource", in.UUID, err)
	if err != nil {
		return fail(err)
	}
	return ok(m)
}
