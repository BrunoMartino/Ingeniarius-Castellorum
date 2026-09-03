# Decisões de implementação

Complemento a [`contextCoolify.md`](contextCoolify.md). Data: 2026-09-03.

---

## 1. Secção 8 do contexto — pontos fechados

| # | Pergunta | Decisão | Efeito no código |
|---|----------|---------|------------------|
| 8.1 | R2 é restritivo por design; bloquear `upsert_env` num app a correr obriga a downtime. | **Hot-edit com `confirm: true`.** R2 continua a bloquear por omissão; `confirm=true` permite editar em quente. | `guard.OnAirGuard.AssertMutable` aceita `confirmed`. As três tools de config (`update_application_config`, `upsert_env`, `update_domains`) expõem `confirm`. O resultado traz `hot_edited: true` e uma nota a dizer que a alteração aplica no próximo deploy. |
| 8.2 | `repair_resource` é ciclo de vida (permitido on air) ou cai sob R2? | **Sujeito a R2, sem escape.** | `guard.OnAirGuard.AssertStopped` — só permite estado provadamente inativo. `confirm=true` não contorna, e o erro diz isso explicitamente. |
| 8.3 | `read:sensitive` expõe passwords no contexto do agente. `mask=true` deve ser o default? | **Sim, `mask=true` por omissão.** | `secretInput.Mask` é `*bool`: omitido e `true` mascaram, só `false` explícito revela. Uma leitura em claro é auditada sob `get_env_values(unmasked)` / `get_database_credentials(unmasked)`. |

---

## 2. Ajustes impostos pela API real do Coolify

Verificado contra o OpenAPI oficial (`coollabsio/coolify@main/openapi.json`).

| Ponto do contexto | Realidade da API | O que ficou |
|-------------------|------------------|-------------|
| `deploy` com `branch?` | `POST /deploy` aceita `tag`, `uuid`, `force`, `pr`, `pull_request_id`, `docker_tag`. **Não há `branch`.** | `deploy` expõe `force` e `docker_tag`. Para mudar de branch: `update_application_config(git_branch=...)` e depois `deploy`. A descrição da tool diz isto. |
| `control` mapeado para GET | Start/stop/restart são **POST** `/{tipo}/{uuid}/{start\|stop\|restart}`. | Implementado como POST. |
| `create_application` com `source: docker_compose` | Não existe endpoint próprio para compose. | Mapeado para `POST /applications/public` com `build_pack=dockercompose`. |
| `repair_resource` como operação distinta | Não existe endpoint de "repair" no Coolify. | Implementado como `POST /{tipo}/{uuid}/start` com `force=true` em aplicações — recria o container a partir da configuração atual sem tocar em volumes. Combinado com 8.2, aplica-se a um recurso já parado. |

`update_domains` usa `domains` (string separada por vírgulas) em aplicações e `urls` (array) em serviços — as duas formas que a API expõe. Bases de dados são recusadas: não são alcançáveis por domínio.

---

## 3. Catálogo: 30 → 25 tools

A secção 4 do contexto enumera 30 tools, mas a decisão **A3** fixa o teto em ≤ 25. Cinco pares `list_*`/`get_*` foram fundidos, sem perder capacidade — o `get` passou a ser o caso do `list` com `uuid` preenchido:

| Contexto | Implementado |
|----------|--------------|
| `list_servers` + `get_server` | `list_servers(uuid?)` |
| `list_projects` + `get_project` | `list_projects(uuid?, environment?)` |
| `list_deployments` + `get_deployment` | `list_deployments(uuid?, application_uuid?)` |
| `list_scheduled_tasks` + `list_scheduled_task_executions` | `list_scheduled_tasks(uuid, task_uuid?)` |
| `list_resources` + `search_resources` | `search_resources(query?, kind?, project?, environment?, status?)` |

`TestCatalogueStaysUnderTheBudget` falha se o catálogo passar de 25, para que uma tool nova seja uma decisão deliberada.

---

## 4. Escolhas menores, para registo

- **Allowlist de rotas em vez de denylist.** R1, R3 e R4 ficam numa estrutura só, deny-by-default. Um endpoint novo do Coolify é inalcançável até ser adicionado de propósito.
- **`DELETE` verificado antes do allowlist**, para se manter válido mesmo que uma rota fosse acrescentada por engano com esse método.
- **Denylist explícita para `/servers/{import,hetzner,digitalocean,vultr}`**, que o padrão `{uuid}` de `/servers/{uuid}` apanharia.
- **`instant_deploy` removido à força** de toda a criação e de `update_application_config`: sem isso, uma alteração de config podia disparar um deployment sem o agente pedir.
- **Boot falha se o audit log não for escrevível.** Descobrir isso na primeira mutação é tarde demais.
- **`exec.Command` com ambiente mínimo** (só `PATH`): um diagnóstico não deve conseguir ler o `COOLIFY_API_TOKEN` do processo.
- **Cache de inventário de 15 s, só em memória (A6)**, invalidado a cada mutação. R2 nunca decide sobre um status em cache: `mutateConfig` relê o estado ao vivo antes de aplicar o guard.
- **`update_domains` não aceita lista vazia** — limpar domínios é destrutivo e fica para a UI.
- **`COOLIFY_MCP_ALLOW_PRIVATE_KEYS`** é lido e validado mas nenhuma tool o consome: chaves privadas continuam fora do catálogo, como o contexto pede. A flag existe para o dia em que isso mude.

---

## 5. Correcções pós-incidente (2026-09-03)

Origem: ao pôr um healthcheck no `beholder-contabo` (service Glances), a sonda falhou, o Traefik retirou o único backend do balanceador e o site passou a devolver `no available server`. Duas falhas do MCP ficaram expostas.

### 5.1 `get_logs` dava 404 em services

O Coolify **não** serve `/services/{uuid}/logs` — 404. Os logs existem por container, em `/services/{uuid}/applications/{app_uuid}/logs` e `/services/{uuid}/databases/{db_uuid}/logs`. O `get_logs` chamava a rota morta e devolvia o 404 ao agente, precisamente no momento em que os logs eram necessários para diagnosticar.

Agora, para um service, o `get_logs` lê a lista de membros e faz fan-out por todos os containers. Um parâmetro opcional `container` filtra por nome ou uuid. O resultado é sempre uma lista `containers`, mesmo para recursos de um só container, para a forma não mudar conforme o tipo. Um container ilegível não afunda a chamada: fica com `error` preenchido e os restantes devolvem logs — o que falha é normalmente o que interessa ler.

Rotas acrescentadas ao allowlist: as duas acima, **só GET** (o OpenAPI também expõe POST em applications/logs; não foi concedido).

### 5.2 Estado lido logo após um deploy não valia nada

`deploy`, `control(start|restart)` e `repair_resource` devolvem quando o Coolify **aceita** o pedido, não quando o recurso está de pé. Pior: durante o `start_period` de um healthcheck, o Docker reporta o container como saudável independentemente do que a sonda diria. Ler o status nessa janela e chamar-lhe sucesso é um falso positivo garantido — foi exactamente o que aconteceu.

Duas medidas:

- **`deployTracker`** — o client regista em memória (A6) quando cada uuid foi deployed/started. Durante `settleWindow` (3 min), qualquer leitura de estado — `Resolve`, `Detail`, `Search`, `Inventory`, e portanto `get_resource`, `search_resources` e o overview — devolve `status_provisional: true` e uma nota a dizer para não reportar sucesso. A anotação é recalculada a cada leitura e nunca entra na cache de inventário, porque depende do tempo.
- **`PostDeployWarning`** — devolvido no campo `next` por `deploy` e por `control(start|restart)`, junto com `deployed: false`. `control(stop)` não o devolve: parar não reinicia healthchecks.

As instruções do servidor passaram a dizer as duas coisas explicitamente, incluindo que **validar que o compose é YAML válido não prova que a sonda funciona** — a confusão entre as duas foi a causa-raiz do incidente.

Nada disto muda o catálogo: continuam 25 tools, `get_logs` só ganhou um parâmetro opcional.

### 5.3 R2 passa a bloqueio duro: o `confirm` foi removido

A decisão 8.1 (hot-edit com `confirm: true`) foi **revertida**. Foi precisamente esse escape que permitiu gravar um healthcheck partido num service que estava a servir tráfego.

Agora, se um recurso estiver a correr, `update_application_config`, `upsert_env`, `update_domains` e `repair_resource` são recusados com `DENIED_ONAIR` e **não existe forma de contornar**. O parâmetro `confirm` desapareceu das assinaturas do client, dos inputs das tools e dos schemas — não é ignorado em silêncio, deixou de existir.

O que a recusa diz mudou também: em vez de sugerir `control(stop)` à IA, manda-a devolver o problema ao humano e **pedir-lhe** que faça o stop. Derrubar um serviço é decisão do humano, não de um agente a meio de uma tarefa. As instruções do servidor e as descrições das quatro tools dizem isto explicitamente.

`COOLIFY_MCP_STRICT_ONAIR` continua a governar apenas o caso `unknown`. Ciclo de vida (`control`, `deploy`, `cancel_deployment`) continua isento de R2.

Testes: a tabela de estados exige agora recusa em todos os estados activos e em ambos os modos strict; `TestNothingAdvertisesConfirm` garante que nenhuma mensagem de recusa volta a mencionar o escape; e no limite do MCP, `TestNoToolOffersAConfirmEscapeHatch` inspecciona o schema tal como o modelo o vê.
