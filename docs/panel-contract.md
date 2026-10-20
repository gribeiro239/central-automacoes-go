# Critérios do painel

Needs comuns: binário Go + banco Docker exclusivo + fixture HTTP + navegador IAB real. Domínio/API anteriores continuam contrato de compatibilidade. Cada linha recebe veredito separado em panel-validation.md.

| ID | Surface | Needs | Behavior | Evidence |
|---|---|---|---|---|
| VAL-P01 | artifact/http | go:embed | GET / e assets funcionam a partir de qualquer cwd, sem Node/CDN; API JSON antiga intacta | testes Go MIME/status + binário real |
| VAL-P02 | http/security | assets | .env, caminhos arbitrários e assets ausentes não expõem arquivos; CSP local | requests reais e testes |
| VAL-P03 | API/data | >500 runs + datas | Totais/gráfico por created_at em sete dias de calendário no timezone, zero e sucesso succeeded/(succeeded+failed) corretos sem cap | teste contagens exatas em memória/PG e navegador |
| VAL-P04 | browser | quatro telas | Navegação overview/automations/runs/detail e links profundos/reload voltam ao lugar correto | navegação real e reload |
| VAL-P05 | browser/API | cadastro | Criar HTTP válido, validar campos/JSON, erro preserva draft; executar e acompanhar sucesso/tentativas | navegador + run persistido |
| VAL-P06 | browser/data | configuração existente | Editar nome/HTTP/timeouts/overlap sem apagar headers/body/retry/schedule nem deslocar fase sem alteração | browser e before/after API |
| VAL-P07 | browser/API | interval/cron | Criar/editar/remover agenda e timezone, mostrar next, pausar/resumir e manual durante pausa | browser + DB/API |
| VAL-P08 | browser/API | policy | Política herdada/custom, max_attempts/timeout; remove_retry_policy valida conflito, volta nil e preserva snapshot antigo nos dois stores | memória/PG, Node tests e browser |
| VAL-P09 | browser/API | automações | Busca/filtros/arquivadas, habilitar/desabilitar, archive confirmado preserva detalhe/histórico e bloqueia novas ações | browser + API |
| VAL-P10 | browser/API | >25 runs | Filtros e26-para25 no servidor com corte to persistido, novas criações não deslocam páginas; Atualizar reseta corte/página; não inventar total | browser dataset real criando novo run entre páginas |
| VAL-P11 | browser/job | run ativo | Atualização automática reflete conclusão sem perder filtro/draft; cancel pendente/ativo mostra pedido e depois canceled | fixture real + browser |
| VAL-P12 | browser/job | run failed/canceled | Retry cria novo run e link parent, original intacto; estado incompatível desabilita e409 é tratado honestamente | browser + API traces |
| VAL-P13 | browser | run detalhado | Resultado, erro/HTTP/preview/truncamento e todas tentativas reais com horários e duração | browser + fixture |
| VAL-P14 | browser/network | backend indisponível/erro | Loading/empty/error distintos, falha nunca vira sucesso/zero; atualizar recupera; duplo clique não duplica ação | browser erro + requests |
| VAL-P15 | browser/security | conteúdo adversarial | Nome/headers/body/preview HTML renderizam texto sem executar código | browser XSS inerte + testes escape |
| VAL-P16 | browser/a11y | teclado e viewport | Labels, foco, dialogs/Escape/restauração, toast acessível, desktop/mobile sem overflow global | interação real + screenshots |
| VAL-P17 | CLI/artifact | conjunto final | Go tests/vet/build, Node tests/syntax, docs e launcher funcional; nenhum dado operacional apagado | comandos e evidências |
| VAL-P18 | API/data | ativos antigos | Contagem pending/running global e active_by_automation completa sem cap controla forbid; próximas agendas elegíveis | snapshot teste >500 e ativo antigo, browser botões |
| VAL-P19 | data | stores concorrentes | Snapshot memória/PG coerente sob mutex/repeatable-read, retornos isolados e contagens fecham no mesmo estado | testes de concorrência/clone e revisão SQL |
| VAL-P20 | browser/network | proxy HTTP de teste | Resposta antiga atrasada não substitui rota/filtro/ações mais novos; draft/foco continuam íntegros | browser com ordem de respostas observada e conteúdo final correto |
