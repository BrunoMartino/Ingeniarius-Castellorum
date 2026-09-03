package tools

const instructions = `Este MCP opera uma instância Coolify v4 pela API REST (/api/v1) e por um punhado de diagnósticos locais read-only.

Ponto de entrada: get_infrastructure_overview, depois search_resources para obter o uuid certo. Quase todas as tools tomam um uuid.

Regras invioláveis, não configuráveis:
- R1 — este MCP nunca apaga nada. Não existe tool de exclusão e o cliente HTTP recusa DELETE. Para apagar um recurso, o humano usa a UI do Coolify.
- R2 — mutações de configuração (update_application_config, upsert_env, update_domains) são bloqueadas quando o recurso está no ar. Passar confirm=true edita em quente: a alteração fica gravada e aplica-se no próximo deploy. Sem confirm, a sequência é control(stop) -> alteração -> deploy(uuid).
- R3 — nenhum acesso a ficheiros do host, /data/coolify, chaves SSH ou .env.
- R4 — nunca cria/roda tokens de API, não mexe em equipas, servidores, chaves privadas nem em definições da API.

Ciclo de vida (control, deploy, cancel_deployment) não é afetado por R2: reiniciar ou fazer deploy de um recurso ativo é o comportamento esperado.

Criação (create_application, create_database, create_service) nasce parada. O deploy é sempre um passo separado e explícito.

Segredos: get_env_values e get_database_credentials devolvem valores mascarados por omissão. Só passar mask=false quando o valor real for mesmo necessário; a leitura em claro fica registada no audit log. Nunca imprimir segredos em resumos nem repeti-los sem necessidade.

run_cli executa apenas comandos de um enum fechado, sem shell. Start/stop/restart só existem via control, onde há audit e verificação de estado.

Não inventar tools nem endpoints. Se um guardrail recusar, a mensagem de erro traz o código e o caminho alternativo — seguir esse caminho ou reportar ao humano.
`
