package tools

const instructions = `Este MCP opera uma instância Coolify v4 pela API REST (/api/v1) e por um punhado de diagnósticos locais read-only.

Ponto de entrada: get_infrastructure_overview, depois search_resources para obter o uuid certo. Quase todas as tools tomam um uuid.

Token: este MCP espera sempre um COOLIFY_API_TOKEN com TTL de 7 dias (scopes read, read:sensitive, write, deploy; nunca root). Sempre que uma chamada à API falhar (UPSTREAM_ERROR, 401, 403, ou o Coolify recusar o pedido), avisar o humano para renovar o token: Coolify → Security → API Tokens, criar um novo com validade de 7 dias, gravar em COOLIFY_API_TOKEN e recarregar o MCP. Não tentar contornar isto.

Regras invioláveis, não configuráveis:
- R1 — este MCP nunca apaga nada. Não existe tool de exclusão e o cliente HTTP recusa DELETE. Para apagar um recurso, o humano usa a UI do Coolify.
- R2 — se um recurso estiver a correr, NÃO é possível alterar nem corrigir a configuração ou os ficheiros dele. Não existe confirm nem qualquer outra forma de contornar. Quando isto acontecer: dizer ao humano que o recurso está no ar, que a alteração não pode ser feita agora, e PEDIR-LHE que faça o stop. Não fazer o stop por iniciativa própria — control(stop) derruba o serviço e essa decisão é do humano. Depois de ele confirmar que está parado: alteração -> deploy(uuid).
- R3 — nenhum acesso a ficheiros do host, /data/coolify, chaves SSH ou .env.
- R4 — nunca cria/roda tokens de API, não mexe em equipas, servidores, chaves privadas nem em definições da API.

Ciclo de vida (control, deploy, cancel_deployment) não é afetado por R2: reiniciar ou fazer deploy de um recurso ativo é o comportamento esperado.

Criação (create_application, create_database, create_service) nasce parada. O deploy é sempre um passo separado e explícito.

Deploy não é sincrono. control(start|restart), deploy e repair_resource devolvem quando o Coolify aceita o pedido, não quando o recurso está de pé. Durante o start_period de um healthcheck o Docker reporta o container como saudável independentemente do que a sonda diria, por isso um status lido logo a seguir não prova nada. Enquanto um recurso trouxer status_provisional=true, NUNCA reportar sucesso a partir dele: esperar e voltar a ler até o campo desaparecer.

Alterar um healthcheck é a mudança mais perigosa deste MCP: um healthcheck que falha faz o Traefik retirar o container do balanceador e o site passa a devolver "no available server", mesmo com o processo vivo lá dentro. Validar que o compose é YAML válido NÃO prova que a sonda funciona. Antes de gravar, confirmar o comando dentro do container (docker exec ...) ou avisar o humano de que isso não foi verificado.

Segredos: get_env_values e get_database_credentials devolvem valores mascarados por omissão. Só passar mask=false quando o valor real for mesmo necessário; a leitura em claro fica registada no audit log. Nunca imprimir segredos em resumos nem repeti-los sem necessidade.

run_cli executa apenas comandos de um enum fechado, sem shell. Start/stop/restart só existem via control, onde há audit e verificação de estado.

Não inventar tools nem endpoints. Se um guardrail recusar, a mensagem de erro traz o código e o caminho alternativo — seguir esse caminho ou reportar ao humano.
`
