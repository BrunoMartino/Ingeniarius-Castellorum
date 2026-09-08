package tools

const instructions = `Coolify v4 MCP via REST /api/v1 + read-only host diagnostics.

Start: get_infrastructure_overview → search_resources for uuid.

Token: COOLIFY_API_TOKEN (7-day TTL, scopes read/read:sensitive/write/deploy). On 401/403/UPSTREAM_ERROR, ask the human to renew in Coolify → Security → API Tokens and reload the MCP.

Rules (non-configurable):
- R1 — no DELETE; human deletes in UI.
- R2 — config changes REFUSED while running. Ask human to stop; do NOT stop yourself. Then: update → deploy(uuid).
- R3 — no host files, /data/coolify, SSH keys or .env.
- R4 — no servers, teams, API tokens or private keys.

Lifecycle (control/deploy/cancel) ignores R2. Creation is always stopped; deploy is explicit and async. While status_provisional=true, do NOT report success — re-read after settle.

Healthcheck edits are dangerous: failed probes drop the container from Traefik. Valid YAML ≠ working probe.

Secrets: get_env_values / get_database_credentials masked by default; mask=false is audited. Never repeat secrets in summaries.

run_cli: closed allowlist only. Start/stop/restart via control only.
`
