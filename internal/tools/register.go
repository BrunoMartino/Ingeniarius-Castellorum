package tools

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ToolNames is the complete catalogue, in registration order. It is the
// contract the tests assert against: 25 tools, no delete, no escalation.
var ToolNames = []string{
	// read
	"coolify_get_infrastructure_overview",
	"coolify_search_resources",
	"coolify_list_unhealthy_resources",
	"coolify_list_servers",
	"coolify_list_projects",
	"coolify_get_resource",
	"coolify_list_deployments",
	"coolify_get_logs",
	"coolify_list_env_keys",
	"coolify_list_storages",
	"coolify_list_scheduled_tasks",
	// read:sensitive
	"coolify_get_env_values",
	"coolify_get_database_credentials",
	// deploy
	"coolify_control",
	"coolify_deploy",
	"coolify_cancel_deployment",
	// write
	"coolify_create_project",
	"coolify_create_application",
	"coolify_create_database",
	"coolify_create_service",
	"coolify_update_application_config",
	"coolify_upsert_env",
	"coolify_update_domains",
	"coolify_repair_resource",
	// local diagnostics
	"coolify_run_cli",
}

func (r *Runtime) register() {
	// --- read ---
	mcp.AddTool(r.server, &mcp.Tool{
		Name:        "coolify_get_infrastructure_overview",
		Description: "Instance snapshot: servers, projects, resource counts and health. Start here.",
		InputSchema: inputSchema[emptyInput](),
	}, r.overview)

	mcp.AddTool(r.server, &mcp.Tool{
		Name:        "coolify_search_resources",
		Description: "Find apps, databases or services by name/uuid/domain; filter by kind, project, environment or status.",
		InputSchema: inputSchema[searchInput](),
	}, r.searchResources)

	mcp.AddTool(r.server, &mcp.Tool{
		Name:        "coolify_list_unhealthy_resources",
		Description: "Degraded, unhealthy or unknown-status resources. Cleanly stopped ones are excluded.",
		InputSchema: inputSchema[emptyInput](),
	}, r.listUnhealthy)

	mcp.AddTool(r.server, &mcp.Tool{
		Name:        "coolify_list_servers",
		Description: "List servers, or one server with resources and domains (read-only; R4).",
		InputSchema: inputSchema[listServersInput](),
	}, r.listServers)

	mcp.AddTool(r.server, &mcp.Tool{
		Name:        "coolify_list_projects",
		Description: "List projects, one with environments, or one environment detail.",
		InputSchema: inputSchema[listProjectsInput](),
	}, r.listProjects)

	mcp.AddTool(r.server, &mcp.Tool{
		Name:        "coolify_get_resource",
		Description: "Full detail of one app, database or service. No env values.",
		InputSchema: inputSchema[uuidInput](),
	}, r.getResource)

	mcp.AddTool(r.server, &mcp.Tool{
		Name:        "coolify_list_deployments",
		Description: "Running deployments, one by uuid, or an app's history. List responses omit build logs.",
		InputSchema: inputSchema[listDeploymentsInput](),
	}, r.listDeployments)

	mcp.AddTool(r.server, &mcp.Tool{
		Name:        "coolify_get_logs",
		Description: "Runtime logs (default 200 lines). Services fan out per container; use container to narrow.",
		InputSchema: inputSchema[logsInput](),
	}, r.getLogs)

	mcp.AddTool(r.server, &mcp.Tool{
		Name:        "coolify_list_env_keys",
		Description: "Env variable names only, no values.",
		InputSchema: inputSchema[uuidInput](),
	}, r.listEnvKeys)

	mcp.AddTool(r.server, &mcp.Tool{
		Name:        "coolify_list_storages",
		Description: "Persistent volumes and file storages of a resource.",
		InputSchema: inputSchema[uuidInput](),
	}, r.listStorages)

	mcp.AddTool(r.server, &mcp.Tool{
		Name:        "coolify_list_scheduled_tasks",
		Description: "Scheduled tasks of an app or service, or one task's executions. Databases: none.",
		InputSchema: inputSchema[scheduledTasksInput](),
	}, r.listScheduledTasks)

	// --- read:sensitive ---
	mcp.AddTool(r.server, &mcp.Tool{
		Name:        "coolify_get_env_values",
		Description: "Env variables with values. Masked by default; mask=false is audited.",
		InputSchema: inputSchema[secretInput](),
	}, r.getEnvValues)

	mcp.AddTool(r.server, &mcp.Tool{
		Name:        "coolify_get_database_credentials",
		Description: "Database connection URLs and credentials. Masked by default; mask=false is audited.",
		InputSchema: inputSchema[secretInput](),
	}, r.getDatabaseCredentials)

	// --- deploy ---
	mcp.AddTool(r.server, &mcp.Tool{
		Name:        "coolify_control",
		Description: "Start, stop or restart a resource. Allowed while running (not R2). Returns before containers are up.",
		InputSchema: inputSchema[controlInput](),
	}, r.control)

	mcp.AddTool(r.server, &mcp.Tool{
		Name:        "coolify_deploy",
		Description: "Trigger deploy. Returns on accept, not when up (deployed:false). Set git_branch via update_application_config first.",
		InputSchema: inputSchema[deployInput](),
	}, r.deploy)

	mcp.AddTool(r.server, &mcp.Tool{
		Name:        "coolify_cancel_deployment",
		Description: "Cancel an in-progress deployment by deployment uuid.",
		InputSchema: inputSchema[cancelInput](),
	}, r.cancelDeployment)

	// --- write ---
	mcp.AddTool(r.server, &mcp.Tool{
		Name:        "coolify_create_project",
		Description: "Create an empty project.",
		InputSchema: inputSchema[createProjectInput](),
	}, r.createProject)

	mcp.AddTool(r.server, &mcp.Tool{
		Name:        "coolify_create_application",
		Description: "Create an app (repo/Dockerfile/compose/image). Created STOPPED; deploy separately.",
		InputSchema: inputSchema[createApplicationInput](),
	}, r.createApplication)

	mcp.AddTool(r.server, &mcp.Tool{
		Name:        "coolify_create_database",
		Description: "Provision a managed database. Created STOPPED; deploy separately.",
		InputSchema: inputSchema[createDatabaseInput](),
	}, r.createDatabase)

	mcp.AddTool(r.server, &mcp.Tool{
		Name:        "coolify_create_service",
		Description: "Provision a one-click or custom compose service. Created STOPPED; deploy separately.",
		InputSchema: inputSchema[createServiceInput](),
	}, r.createService)

	mcp.AddTool(r.server, &mcp.Tool{
		Name:        "coolify_update_application_config",
		Description: "Patch app, service or database settings (compose, build, image, limits, etc.). REFUSED while running — ask human to stop; do not stop it yourself. Then update → deploy.",
		InputSchema: inputSchema[updateAppConfigInput](),
	}, r.updateApplicationConfig)

	mcp.AddTool(r.server, &mcp.Tool{
		Name:        "coolify_upsert_env",
		Description: "Create/update env vars in batch; never removes keys. REFUSED while running — ask human to stop; do not stop it yourself.",
		InputSchema: inputSchema[upsertEnvInput](),
	}, r.upsertEnv)

	mcp.AddTool(r.server, &mcp.Tool{
		Name:        "coolify_update_domains",
		Description: "Replace FQDN list of an app or service. REFUSED while running — ask human to stop; do not stop it yourself. Not for databases.",
		InputSchema: inputSchema[updateDomainsInput](),
	}, r.updateDomains)

	mcp.AddTool(r.server, &mcp.Tool{
		Name:        "coolify_repair_resource",
		Description: "Recreate container from current config; volumes untouched. Requires STOPPED — ask human to stop; do not stop it yourself.",
		InputSchema: inputSchema[uuidInput](),
	}, r.repairResource)

	// --- local diagnostics ---
	mcp.AddTool(r.server, &mcp.Tool{
		Name:        "coolify_run_cli",
		Description: "Read-only host diagnostic from allowlist (docker ps/stats/inspect/logs, df, free, uptime). No shell.",
		InputSchema: inputSchema[runCLIInput](),
	}, r.runCLI)
}
