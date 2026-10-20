# Contrato: tópicos 1 e 2

Cada linha é um veredito independente. Needs comuns: fonte atual, Go, destino httptest real; para PG, TEST_DATABASE_URL exclusivo em Docker. Implementação só será concluída com evidências em scheduling-validation.md. As regras detalhadas e limites são parte de scheduling-scope.md.

| ID | Surface | Needs | Behavior | Evidence |
|---|---|---|---|---|
| VAL-S01 | library/API | relógio fixo | Intervalo válido calcula próximo horário; tipo/limites inválidos 400 | testes domínio e HTTP |
| VAL-S02 | library/API | tzdata | Cron 5 campos, fuso explícito válido, expressão impossível/inválida recusada | datas esperadas, HTTP 400 |
| VAL-S03 | library | datas DST | Cron respeita offset e transição de horário de verão no fuso | testes com horários UTC esperados |
| VAL-S04 | data/API | automação agendada | next_run_at visível; PATCH agenda recalcula; remove_schedule limpa | contrato comum e API |
| VAL-S05 | job/data | due automação | Tick gera run scheduled com snapshot e scheduled_for, avança próximo horário | store real + binário destino HTTP |
| VAL-S06 | data | ticks concorrentes | Uma execução por automação/horário, incluindo após reabrir store | concorrência PG/memória e unique SQL |
| VAL-S07 | data | backlog | run_once cria só uma recuperação; skip respeita grace; próximo futuro | relógio controlado PG/memória |
| VAL-S08 | API/data | agenda ativa | Pausa/resume não impede manual; não recupera período pausado | contrato comum e HTTP |
| VAL-S09 | data | disabled/archived | Suspender não gera horários; reabilitar calcula futuro | contrato comum |
| VAL-S10 | data | forbid | Enqueue/rerun concorrentes não criam trabalho sobreposto; schedule ocupado avança sem gerar; cancel_requested mantém slot até Finish/expiração | concorrência PG/memória |
| VAL-S11 | job/data | fila anterior + forbid | Claim não inicia segundo run da mesma automação enquanto há running; outros prosseguem | testes stores concorrentes |
| VAL-O01 | API/data | runs diversos | Filtros automação/status/período aplicados antes de limit/offset, ordem estável | contrato comum e HTTP real |
| VAL-O02 | API | query inválida | Rejeitar status/datas/intervalo/limit/offset inválidos com 400 | testes HTTP |
| VAL-O03 | API/data | run failed/canceled | Retry cria novo ID, vínculo e snapshot original; original preservado | ambos stores e HTTP |
| VAL-O04 | API/data | run terminal/ativo | Retry de pending/running/succeeded ou automação desabilitada/arquivada conflita | testes 409 e sem run extra |
| VAL-O05 | API/data | pending | Cancel fica terminal sem tentativa, idempotente e não é reservado | contrato comum e HTTP |
| VAL-O06 | job/API | HTTP bloqueante | Cancel running marca pedido e worker interrompe chamada, registra canceled sem retry | fixture real + estado persistido |
| VAL-O07 | data | corrida cancel/Finish | Cancel antes de Finish vence sucesso, falha permanente e retry transitório; sem requeue e uma tentativa fechada; Finish antes de cancel terminal conflita | ambos stores e testes concorrentes |
| VAL-O08 | data | cancel + lease vencida | Expiração fecha canceled, sem retry e token antigo recusado | ambos stores |
| VAL-O09 | API/data | arquivada com histórico | Archive preserva histórico/filas, oculta lista padrão, inclui opt-in e recusa novas operações | contrato comum e HTTP |
| VAL-O10 | job/API | política por automação | Editar timeout/max_attempts/retry_policy valida limites e usa snapshot/espera limitada; política omitida usa RETRY_BASE existente, substituição não altera snapshots antigos | worker timestamps e HTTP |
| VAL-X01 | migration | esquema 001 populado | Migração 002 preserva dados, marca versões e é repetível | PG real antes/depois + reaplicação |
| VAL-X02 | CLI/job | binário + Docker | Scheduler inicia junto ao backend e para em shutdown; dados agendados persistem no reinício | processo, HTTP, DB e logs |
| VAL-X03 | artifact | implementação final | README/exemplos explicam API, pausa, atraso, overlap, cancel, arquivo e retry; suíte anterior passa | testes regressão, vet/build, exemplo executado |
| VAL-X04 | job | fixture bloqueante + archive | Runs running e pending continuam e concluem após arquivar; novas admissões seguem bloqueadas | chamadas HTTP observadas e estados/tentativas |
| VAL-X05 | data | PG exclusivo + falha controlada | Falha real durante tick não deixa run parcial nem próximo horário avançado; recuperação gera exatamente um run | trigger de falha isolada, before/after e tick saudável |
