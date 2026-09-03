# contextCoolify.md

Documento de contexto e decisões arquiteturais do **coolify-mcp** (implementação própria, Go).

- **Status:** especificação inicial
- **Data:** 2026-08-31
- **Owner:** Bruno / Buskar
- **Base:** Coolify v4 REST API (`/api/v1`) + CLI local em allowlist

---

## 1. Por que um MCP próprio

O MCP oficial do Coolify (`https://<dominio>/mcp`, streamable HTTP) cobre leitura ampla e um grupo de ciclo de vida (`control`, `deploy`, `cancel_deployment`), mas **não escreve**: não cria recursos, não edita variáveis de ambiente e nunca retorna valores sensíveis. A API REST, por outro lado, continua com escrita completa (permissões `read`, `read:sensitive`, `write`, `deploy`, `root`).

O `@felixallistar/coolify-mcp` — referência do artigo de out/2025 — foi **arquivado em 17/01/2026** (último publish npm: v1.0.5, 30/06/2025) e expunha ~100 tools de uma vez, o que degrada a escolha de ferramenta do agente.

Este MCP existe para preencher a lacuna com **superfície mínima e guardrails duros**.

---

## 2. Decisões arquiteturais

| # | Decisão | Racional |
|---|---------|----------|
| A1 | **Linguagem: Go** | Binário único, sem runtime, cross-compile trivial, startup instantâneo para stdio. `CGO_ENABLED=0`. |
| A2 | **SDK: `github.com/modelcontextprotocol/go-sdk`** | SDK oficial; evita reimplementar o protocolo. |
| A3 | **Pequeno e sucinto** | Alvo: **≤ 25 tools** e **~2.000 LOC**. Cada tool nova exige justificativa. Sem gerador de código a partir do OpenAPI inteiro. |
| A4 | **Transporte duplo** | `stdio` é o padrão (uso local). `--transport=http` habilita streamable HTTP para uso em nuvem. Mesmo core de tools nos dois modos. |
| A5 | **Auth: usuário + API key** | Igual ao oficial: `Authorization: Bearer <token>`. O "usuário" é a identidade lógica (`COOLIFY_USER`) usada em audit log e em multi-perfil — a autorização real vem do escopo do token no Coolify. |
| A6 | **Sem estado persistente** | Nenhum DB. Cache só em memória (TTL curto) para inventário. |
| A7 | **Guardrails no transporte, não só na tool** | O HTTP client recusa `DELETE` e paths destrutivos **antes** de sair da máquina. Uma tool mal escrita não consegue burlar. |
| A8 | **Audit log estruturado** | Toda chamada mutante em JSONL: timestamp, usuário, tool, alvo (uuid), verbo HTTP, resultado. |
| A9 | **CLI apenas via allowlist literal** | Sem shell, sem `sh -c`, sem pipes, sem interpolação. `exec.Command` com argv fixo e argumentos validados por regex. |

---

## 3. Modelo de permissões

### 3.1 Token do Coolify

Escopos concedidos ao token usado pelo MCP:

- `read`
- `read:sensitive`
- `write`
- `deploy`

**Nunca `root`.**

### 3.2 Regras invioláveis (hard deny — não configuráveis)

| Regra | Implementação |
|-------|---------------|
| **R1 — Zero delete** | O client HTTP rejeita o método `DELETE` incondicionalmente. Nenhuma tool de exclusão existe no catálogo. Vale para qualquer recurso: app, DB, service, projeto, servidor, chave, domínio, env, backup, deployment. |
| **R2 — Nada de alterar recurso "on air"** | Antes de qualquer mutação de configuração, o MCP consulta o status do recurso. Se estiver **ativo**, a chamada é recusada com erro explicativo. |
| **R3 — Sem toque em arquivos de sistema** | O CLI não acessa `/data/coolify/**`, `docker-compose.yml` de instalações, `.env` do próprio Coolify, chaves SSH, nem nada fora do allowlist de leitura. |
| **R4 — Sem escalonamento** | O MCP nunca cria/rotaciona tokens de API, nunca altera permissões de time, nunca habilita/desabilita a API. |

### 3.3 Definição de "on air" (R2)

Estados considerados **ATIVOS** (bloqueiam mutação de config):

```
running · running:healthy · running:unhealthy · degraded · starting · restarting · deploying
```

Estados considerados **INATIVOS** (mutação de config permitida):

```
exited · stopped · created · paused · not_deployed · unknown(*)
```

`unknown` só é tratado como inativo se `COOLIFY_MCP_STRICT_ONAIR=false`. Por padrão, **`unknown` bloqueia** (fail-closed).

Operações de **ciclo de vida** (`control`, `deploy`, `cancel_deployment`) **não** são afetadas por R2 — reiniciar ou fazer deploy de um recurso ativo é o comportamento esperado.

Fluxo canônico para editar algo que está no ar:

```
control(stop) → update_* → deploy()
```

O erro de R2 retorna exatamente essa instrução ao agente.

---

## 4. Catálogo de tools

Prefixo: `coolify_`. Total-alvo: 24.

### 4.1 Leitura (`read`)

| Tool | Descrição |
|------|-----------|
| `get_infrastructure_overview` | Snapshot: servidores, projetos, contagem de recursos, saúde agregada. Ponto de entrada do agente. |
| `search_resources` | Busca por nome/uuid/tag em apps, DBs e services. Retorna uuid + tipo + status. |
| `list_unhealthy_resources` | Recursos em estado degradado/parado inesperado. |
| `list_servers` / `get_server` | Servidores, incluindo recursos e domínios. |
| `list_projects` / `get_project` | Projetos e seus environments. |
| `list_resources` | Inventário de um environment (apps + DBs + services). |
| `get_resource` | Detalhe unificado de app/DB/service por uuid. |
| `list_deployments` / `get_deployment` | Histórico e detalhe de deployment. |
| `get_logs` | Logs de aplicação/serviço/deployment (com `lines`, default 200). |
| `list_env_keys` | **Apenas chaves**, sem valores. |
| `list_storages` | Volumes/persistent storages do recurso. |
| `list_scheduled_tasks` | Tarefas agendadas do recurso. |
| `list_scheduled_task_executions` | Execuções de uma tarefa agendada. |

### 4.2 Leitura sensível (`read:sensitive`)

| Tool | Descrição | Guardrail |
|------|-----------|-----------|
| `get_env_values` | Valores das variáveis de ambiente de um recurso. | Loga a leitura no audit. Suporta `mask=true` (default `false`). |
| `get_database_credentials` | Connection string / usuário / senha de um DB gerenciado. | Idem. |

> **Chaves privadas SSH ficam fora do catálogo.** Mesmo com `read:sensitive`, o MCP não expõe `private_keys`. Se necessário no futuro, entra atrás de flag `COOLIFY_MCP_ALLOW_PRIVATE_KEYS=true` (default `false`).

### 4.3 Ciclo de vida (`deploy`)

| Tool | Descrição |
|------|-----------|
| `control` | `action`: `start` \| `stop` \| `restart`. Alvo por uuid. Único ponto de start/stop/restart. |
| `deploy` | Dispara deployment. Params: `uuid`, `force?`, `branch?`. |
| `cancel_deployment` | Cancela deployment em andamento por `deployment_uuid`. |

### 4.4 Escrita / provisionamento (`write`) — sujeitas a R1 + R2

| Tool | Descrição | Notas |
|------|-----------|-------|
| `create_project` | Cria projeto. | — |
| `create_application` | Cria app. `source`: `public_repo` \| `private_repo_github` \| `dockerfile` \| `docker_compose` \| `docker_image`. | Recurso nasce parado; deploy é passo separado e explícito. |
| `create_database` | Provisiona DB gerenciado (postgres, mysql, mariadb, mongodb, redis, keydb, clickhouse, dragonfly). | Idem. |
| `create_service` | Provisiona serviço one-click por `type`. | Idem. |
| `update_application_config` | Ajusta build pack, comandos de build/start, portas, health check, limites de recurso. | **Bloqueado se on air (R2).** |
| `upsert_env` | Cria ou atualiza variáveis de ambiente (batch). Nunca remove. | **Bloqueado se on air (R2).** Para remover, o usuário usa a UI. |
| `update_domains` | Define/atualiza FQDNs do recurso. | **Bloqueado se on air (R2).** |
| `repair_resource` | Reparo guiado: revalida config, refaz o `docker compose up` do recurso, recria o container a partir da imagem/commit atual. Não altera arquivos, não apaga volumes. | Idempotente. Permitido em recurso ativo (é operação de ciclo de vida, não de config). |

Ausentes por decisão: `delete_*` (R1), `create_server`, `create_private_key`, `update_team*`, `update_api_settings` (R4).

### 4.5 CLI local (baixa criticidade, read-only)

Tool única: **`run_cli`**, com `command` restrito a um enum. Sem shell, argv fixo, timeout de 20s, saída truncada em 64 KB.

| `command` | Executa | Para quê |
|-----------|---------|----------|
| `docker_ps` | `docker ps --format json` | Containers e status. |
| `docker_stats` | `docker stats --no-stream --format json` | CPU/mem por container. |
| `docker_inspect` | `docker inspect <id>` | Config efetiva do container. |
| `docker_logs` | `docker logs --tail N <id>` | Logs quando a API não basta. |
| `docker_images` | `docker images --format json` | Imagens e espaço. |
| `docker_system_df` | `docker system df` | Uso de disco por camada/volume. |
| `docker_network_ls` | `docker network ls` | Diagnóstico de rede. |
| `disk_usage` | `df -h` | Espaço em disco do host. |
| `memory_usage` | `free -m` | Memória do host. |
| `load_average` | `uptime` | Carga do host. |
| `coolify_version` | `docker inspect coolify --format '{{.Config.Image}}'` | Versão em execução. |

**Proibido no CLI, sem exceção:** `rm`, `rmi`, `prune`, `volume rm`, `network rm`, `down`, `kill`, `stop`, `restart`, `exec`, `cp`, `systemctl` (qualquer verbo), `sed`, `tee`, redirecionamento, `sudo`, e qualquer acesso a `/data/coolify`, `~/.ssh`, `.env`. Start/stop/restart só existem via `control` na API, onde há audit e checagem de estado.

Validação de argumentos: `<id>` deve casar `^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`; `N` deve ser inteiro 1–5000.

---

## 5. Configuração

```bash
# obrigatórios
COOLIFY_URL=https://coolify.exemplo.com
COOLIFY_API_TOKEN=<token com read, read:sensitive, write, deploy>
COOLIFY_USER=bruno              # identidade para audit log

# opcionais
COOLIFY_MCP_TRANSPORT=stdio     # stdio | http
COOLIFY_MCP_HTTP_ADDR=:8788     # quando transport=http
COOLIFY_MCP_HTTP_TOKEN=<token>  # bearer exigido dos clientes, modo http
COOLIFY_MCP_STRICT_ONAIR=true   # unknown = bloqueia
COOLIFY_MCP_ALLOW_CLI=true      # desliga o grupo run_cli inteiro
COOLIFY_MCP_ALLOW_PRIVATE_KEYS=false
COOLIFY_MCP_AUDIT_PATH=~/.coolify-mcp/audit.jsonl
COOLIFY_MCP_TIMEOUT=30s
```

Registro no cliente MCP (stdio):

```json
{
  "mcpServers": {
    "coolify": {
      "command": "coolify-mcp",
      "env": {
        "COOLIFY_URL": "https://coolify.exemplo.com",
        "COOLIFY_API_TOKEN": "...",
        "COOLIFY_USER": "bruno"
      }
    }
  }
}
```

Modo nuvem: `coolify-mcp --transport=http --addr=:8788`, atrás de TLS, com `COOLIFY_MCP_HTTP_TOKEN` obrigatório. Sem esse token o binário recusa subir em modo http.

---

## 6. Estrutura do projeto

```
coolify-mcp/
├── cmd/coolify-mcp/main.go      # flags, transporte, wiring
├── internal/
│   ├── config/config.go         # env + validação; falha rápido
│   ├── coolify/
│   │   ├── client.go            # HTTP client + guard R1 (nega DELETE)
│   │   ├── resources.go         # apps, DBs, services (leitura unificada)
│   │   ├── lifecycle.go         # control, deploy, cancel
│   │   └── write.go             # create_*, update_*, upsert_env
│   ├── guard/
│   │   ├── onair.go             # R2: mapa de estados + checagem
│   │   ├── deny.go              # R1/R4: verbos e paths proibidos
│   │   └── audit.go             # JSONL
│   ├── cli/
│   │   ├── allowlist.go         # enum → argv fixo
│   │   └── exec.go              # exec.Command, timeout, truncate
│   └── tools/
│       ├── read.go
│       ├── sensitive.go
│       ├── lifecycle.go
│       ├── write.go
│       └── cli.go
└── go.mod
```

**Convenção de erro:** toda recusa de guardrail retorna erro estruturado com `code` (`DENIED_DELETE`, `DENIED_ONAIR`, `DENIED_CLI`, `DENIED_SCOPE`), o motivo e — quando aplicável — o caminho alternativo (ex.: a sequência stop → update → deploy).

---

## 7. Testes mínimos

- Tabela de estados on-air: cada estado × cada tool mutante → permitido/negado.
- `DELETE` bloqueado no client, mesmo chamado diretamente.
- `run_cli` rejeita todo comando fora do enum e todo argumento fora do regex.
- Modo http recusa iniciar sem `COOLIFY_MCP_HTTP_TOKEN`.
- Audit log escrito para toda mutação, inclusive as negadas.

---

## 8. Pontos a confirmar

1. **R2 é restritivo por design.** Bloquear `upsert_env` em app rodando significa que trocar uma variável exige `stop → upsert_env → deploy`, com downtime. Se o objetivo for evitar mudança acidental e não downtime, a alternativa é permitir hot-edit exigindo `confirm: true` no argumento da tool. Decidir antes de implementar.
2. **`repair_resource`** foi classificado como ciclo de vida (permitido on air). Confirmar se recriar container de app ativo é aceitável ou se deve cair sob R2.
3. **`read:sensitive`** expõe senhas de banco em texto no contexto do agente. Confirmar se `mask=true` deve ser o default.

---

## 9. Fora de escopo (v1)

Exclusão de qualquer recurso · gestão de servidores e chaves SSH · gestão de times e tokens · terminal interativo em container · edição de arquivos no host · backups/restore · webhooks.
