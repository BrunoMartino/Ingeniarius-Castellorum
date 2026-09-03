# Ingeniarius Castellorum

<p align="center">
  <img src="docs/assets/castellorum.jpg" alt="Castellorum" />
</p>

<p align="center"><em>John Ruskin</em></p>

<p align="center"><em>"When we build, let us think that we build forever"</em></p>

MCP em Go para uma instância Coolify v4 (`/api/v1`). Binário: `coolify-mcp`.

---

## Requisitos

- Go 1.22+ (só na primeira compilação)
- Token da API Coolify com `read`, `read:sensitive`, `write` e `deploy` — nunca `root`

---

## Instalação

```bash
git clone https://github.com/BrunoMartino/Ingeniarius-Castellorum.git
cd Ingeniarius-Castellorum
cp .env.example .env
```

Edita `.env` e preenche:

| Variável | Obrigatório | Função |
|----------|-------------|--------|
| `COOLIFY_URL` | sim | URL da instância Coolify |
| `COOLIFY_API_TOKEN` | sim | Token da API |
| `COOLIFY_USER` | sim | Identidade no audit log |
| `COOLIFY_MCP_TRANSPORT` | não | `stdio` (default) ou `http` |
| `COOLIFY_MCP_HTTP_ADDR` | não | Endereço em modo http (ex. `:8788`) |
| `COOLIFY_MCP_HTTP_TOKEN` | em http | Bearer exigido aos clientes |
| `COOLIFY_MCP_AUDIT_PATH` | não | JSONL de auditoria (default `~/.coolify-mcp/audit.jsonl`) |

Compila (o wrapper faz isto na primeira execução):

```bash
CGO_ENABLED=0 go build -o bin/coolify-mcp ./cmd/coolify-mcp
```

O ficheiro de audit tem de ser escrevível; o processo recusa arrancar se não for.

---

## Cursor (stdio)

Configuração a nível de utilizador, em `~/.cursor/mcp.json`, com caminho absoluto:

```json
{
  "mcpServers": {
    "ingeniarius-castellorum": {
      "command": "/ABS/PATH/Ingeniarius-Castellorum/bin/coolify-mcp",
      "env": {
        "DOTENV_PATH": "/ABS/PATH/Ingeniarius-Castellorum/.env"
      }
    }
  }
}
```

Substitui `/ABS/PATH` pelo caminho real do clone. Reinicia o Cursor (ou recarrega os MCPs).

Alternativa: aponta `command` para `ingeniarius-castellorum.sh` no mesmo diretório — o script compila `bin/coolify-mcp` se ainda não existir e lê o `.env` ao lado.

---

## HTTP

```bash
coolify-mcp --transport=http --addr=:8788 --env-file=/ABS/PATH/Ingeniarius-Castellorum/.env
```

Define `COOLIFY_MCP_HTTP_TOKEN` no `.env`. Os clientes enviam `Authorization: Bearer <token>`.

---

## Uso

Prefixo das tools: `coolify_`.

Ponto de entrada: `get_infrastructure_overview`. Depois `search_resources` ou `list_unhealthy_resources` para localizar uuids.

| Grupo | Tools |
|-------|--------|
| Leitura | `get_infrastructure_overview`, `search_resources`, `list_unhealthy_resources`, `list_servers`, `list_projects`, `get_resource`, `list_deployments`, `get_logs`, `list_env_keys`, `list_storages`, `list_scheduled_tasks` |
| Sensível | `get_env_values`, `get_database_credentials` (`mask=true` por omissão) |
| Ciclo de vida | `control` (`start` \| `stop` \| `restart`), `deploy`, `cancel_deployment` |
| Escrita | `create_project`, `create_application`, `create_database`, `create_service`, `update_application_config`, `upsert_env`, `update_domains`, `repair_resource` |
| CLI local | `run_cli` (enum fechado: docker/ps/stats/inspect/logs/images, disco, memória, load, versão Coolify) |

Recursos criados nascem **parados**. Deploy é sempre um passo à parte (`deploy`).

Mutação de config num recurso a correr é recusada (`DENIED_ONAIR`), excepto com `confirm=true` (aplica no próximo deploy). Alternativa: `control(stop)` → alterar → `deploy`. `repair_resource` exige o recurso parado; `confirm` não contorna.

Não há tools de delete.

Códigos de recusa: `DENIED_DELETE` · `DENIED_ONAIR` · `DENIED_CLI` · `DENIED_SCOPE` · `UPSTREAM_ERROR` · `NOT_FOUND` · `BAD_INPUT` · `CONFIG_ERROR`

---

## Testes

```bash
go test ./...
```
