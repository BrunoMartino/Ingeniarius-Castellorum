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
		Description: "Snapshot of the whole instance: servers, projects, resource counts by kind and aggregated health. Start here, then use coolify_search_resources to get the uuid you need.",
		InputSchema: inputSchema[emptyInput](),
	}, r.overview)

	mcp.AddTool(r.server, &mcp.Tool{
		Name:        "coolify_search_resources",
		Description: "Find applications, databases and services by name, uuid, description or domain, filtered by kind, project, environment or status. Returns uuid, kind and status. Also serves as the environment inventory when you pass project and environment.",
		InputSchema: inputSchema[searchInput](),
	}, r.searchResources)

	mcp.AddTool(r.server, &mcp.Tool{
		Name:        "coolify_list_unhealthy_resources",
		Description: "Resources that are degraded, unhealthy, or in a status this server does not recognise. Cleanly stopped resources are not listed: being stopped is not a failure.",
		InputSchema: inputSchema[emptyInput](),
	}, r.listUnhealthy)

	mcp.AddTool(r.server, &mcp.Tool{
		Name:        "coolify_list_servers",
		Description: "List servers, or one server with its resources and domains when uuid is given. Read only: this server never creates, edits or removes servers (R4).",
		InputSchema: inputSchema[listServersInput](),
	}, r.listServers)

	mcp.AddTool(r.server, &mcp.Tool{
		Name:        "coolify_list_projects",
		Description: "List projects, one project with its environments when uuid is given, or one environment's detail when uuid and environment are both given.",
		InputSchema: inputSchema[listProjectsInput](),
	}, r.listProjects)

	mcp.AddTool(r.server, &mcp.Tool{
		Name:        "coolify_get_resource",
		Description: "Full detail of one application, database or service by uuid, with the kind resolved for you. Never returns environment variable values.",
		InputSchema: inputSchema[uuidInput](),
	}, r.getResource)

	mcp.AddTool(r.server, &mcp.Tool{
		Name:        "coolify_list_deployments",
		Description: "Currently running deployments, one deployment's detail by uuid, or one application's deployment history by application_uuid.",
		InputSchema: inputSchema[listDeploymentsInput](),
	}, r.listDeployments)

	mcp.AddTool(r.server, &mcp.Tool{
		Name:        "coolify_get_logs",
		Description: "Runtime logs of an application, database or service. Defaults to the last 200 lines. A service has no logs of its own, so this returns one entry per container in it; pass container to narrow it to one by name or uuid. Always returns a containers list, even for a single-container resource.",
		InputSchema: inputSchema[logsInput](),
	}, r.getLogs)

	mcp.AddTool(r.server, &mcp.Tool{
		Name:        "coolify_list_env_keys",
		Description: "Names of a resource's environment variables, without any values. Use this whenever you only need to know which variables exist.",
		InputSchema: inputSchema[uuidInput](),
	}, r.listEnvKeys)

	mcp.AddTool(r.server, &mcp.Tool{
		Name:        "coolify_list_storages",
		Description: "Persistent volumes and file storages attached to a resource.",
		InputSchema: inputSchema[uuidInput](),
	}, r.listStorages)

	mcp.AddTool(r.server, &mcp.Tool{
		Name:        "coolify_list_scheduled_tasks",
		Description: "Scheduled tasks of an application or service, or one task's execution history when task_uuid is given. Managed databases have no scheduled tasks.",
		InputSchema: inputSchema[scheduledTasksInput](),
	}, r.listScheduledTasks)

	// --- read:sensitive ---
	mcp.AddTool(r.server, &mcp.Tool{
		Name:        "coolify_get_env_values",
		Description: "Environment variables of a resource WITH their values. Masked by default: pass mask=false only when the real value is genuinely needed. Every call is written to the audit log, and unmasked reads are flagged there. Do not repeat secret values in your summaries.",
		InputSchema: inputSchema[secretInput](),
	}, r.getEnvValues)

	mcp.AddTool(r.server, &mcp.Tool{
		Name:        "coolify_get_database_credentials",
		Description: "Connection URLs, user and password of a managed database. Masked by default: pass mask=false only when the real value is genuinely needed. Audited like coolify_get_env_values. SSH private keys are never exposed by this server.",
		InputSchema: inputSchema[secretInput](),
	}, r.getDatabaseCredentials)

	// --- deploy ---
	mcp.AddTool(r.server, &mcp.Tool{
		Name:        "coolify_control",
		Description: "Start, stop or restart an application, database or service. The only place start/stop/restart exists. Allowed on a running resource: this is a lifecycle operation, not a configuration change. start and restart return before the containers are actually up.",
		InputSchema: inputSchema[controlInput](),
	}, r.control)

	mcp.AddTool(r.server, &mcp.Tool{
		Name:        "coolify_deploy",
		Description: "Trigger a deployment. Returns once Coolify ACCEPTS the request, not once the resource is up: the result carries deployed:false and the containers are still starting. Deploys the branch already configured on the application; to deploy a different branch, set git_branch with coolify_update_application_config first. docker_tag applies to docker-image applications only.",
		InputSchema: inputSchema[deployInput](),
	}, r.deploy)

	mcp.AddTool(r.server, &mcp.Tool{
		Name:        "coolify_cancel_deployment",
		Description: "Cancel a deployment that is in progress. Takes the deployment uuid from coolify_list_deployments, not the resource uuid.",
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
		Description: "Create an application from a public repo, a private GitHub repo, a Dockerfile, a Docker Compose file or a Docker image. The application is created STOPPED; deploying is a separate, explicit call to coolify_deploy.",
		InputSchema: inputSchema[createApplicationInput](),
	}, r.createApplication)

	mcp.AddTool(r.server, &mcp.Tool{
		Name:        "coolify_create_database",
		Description: "Provision a managed database (postgresql, mysql, mariadb, mongodb, redis, keydb, clickhouse, dragonfly). Created STOPPED; deploying is a separate, explicit call.",
		InputSchema: inputSchema[createDatabaseInput](),
	}, r.createDatabase)

	mcp.AddTool(r.server, &mcp.Tool{
		Name:        "coolify_create_service",
		Description: "Provision a one-click service by type, or a custom one from docker_compose_raw. Created STOPPED; deploying is a separate, explicit call.",
		InputSchema: inputSchema[createServiceInput](),
	}, r.createService)

	mcp.AddTool(r.server, &mcp.Tool{
		Name:        "coolify_update_application_config",
		Description: "Patch an application's build/runtime settings, or a service's docker_compose_raw and urls. REFUSED while the resource is running, with no override: you cannot change or repair the definition of something that is up. Report the refusal to the human and ask them to stop it; do not stop it yourself. Then: the update, then coolify_deploy.",
		InputSchema: inputSchema[updateAppConfigInput](),
	}, r.updateApplicationConfig)

	mcp.AddTool(r.server, &mcp.Tool{
		Name:        "coolify_upsert_env",
		Description: "Create or update environment variables in one batch. Never removes a variable: removal is done by hand in the Coolify UI. REFUSED while the resource is running, with no override. Report the refusal to the human and ask them to stop it; do not stop it yourself. Then: the update, then coolify_deploy.",
		InputSchema: inputSchema[upsertEnvInput](),
	}, r.upsertEnv)

	mcp.AddTool(r.server, &mcp.Tool{
		Name:        "coolify_update_domains",
		Description: "Set the full FQDN list of an application or service, replacing the current one. REFUSED while the resource is running, with no override. Report the refusal to the human and ask them to stop it; do not stop it yourself. Managed databases are not reachable by domain.",
		InputSchema: inputSchema[updateDomainsInput](),
	}, r.updateDomains)

	mcp.AddTool(r.server, &mcp.Tool{
		Name:        "coolify_repair_resource",
		Description: "Recreate a resource's container from its current configuration, image and commit. Does not touch volumes, files or the database. Requires the resource to be STOPPED. Report the refusal to the human and ask them to stop it; do not stop it yourself.",
		InputSchema: inputSchema[uuidInput](),
	}, r.repairResource)

	// --- local diagnostics ---
	mcp.AddTool(r.server, &mcp.Tool{
		Name:        "coolify_run_cli",
		Description: "Run one read-only host diagnostic from a closed allowlist (docker ps/stats/inspect/logs/images/system df/network ls, df -h, free -m, uptime, coolify version). No shell, no arbitrary arguments, 20s timeout, output truncated at 64 KB. Start/stop/restart are not here: use coolify_control.",
		InputSchema: inputSchema[runCLIInput](),
	}, r.runCLI)
}
