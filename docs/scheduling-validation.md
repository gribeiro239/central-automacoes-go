# Validação: agendador e controles operacionais

Concluída em 20/10/2026. Código em `Go Lang 2`, Go 1.27.2, Windows amd64, PostgreSQL 18 pelo Docker Compose. Os testes foram executados com autorização fora do sandbox para permitir TCP local. Não houve publicação no GitHub.

## Resultado dos comandos

```text
go mod tidy                                      OK
go test -count=1 -p 2 -timeout 180s ./...          OK
go vet ./...                                     OK
go build -o bin/central.exe ./cmd/central         OK
go build -o bin/fixture.exe ./cmd/fixture         OK
gofmt -l cmd internal migrations                 sem arquivos pendentes
```

TEST_DATABASE_URL apontou para `central_validation`, com schemas exclusivos criados e removidos pelos testes. Resultado final: config 1.282s, domain 1.368s, executor 3.074s, httpapi 3.118s, memory 1.874s, postgres 9.852s, scheduler 1.758s e worker 5.579s. Os testes anteriores continuam passando. Detector `-race` não executado: ambiente com CGO desabilitado e sem compilador C configurado.

O contrato passou por duas revisões sequenciais. A revisão de implementação identificou query strings malformadas silenciosamente ignoradas por URL.Query; o parser foi substituído por url.ParseQuery com tratamento de erro. Regressões de percent escape e ponto e vírgula passaram. A revisão também exigiu a prova concorrente do primeiro Claim após mudar allow para forbid: oito callers simultâneos, uma única reserva protegida e processamento de outra automação. O cenário passou em memória e PostgreSQL. Revisão final sem bloqueios nos pontos atribuídos.

## Vereditos

| ID | Veredito | Evidência |
|---|---|---|
| VAL-S01 | Aprovado | Domínio e API: intervalo válido, limites e tipo inválidos; próximo horário. |
| VAL-S02 | Aprovado | Testes com cron válido/impossível, fuso explícito e API real retornando próxima ocorrência. |
| VAL-S03 | Aprovado | Datas fixas em DST: horário ausente pulado e horário repetido em dois UTC distintos. |
| VAL-S04 | Aprovado | Contrato comum e API: schedule editado/remove_schedule, próximo horário visível/limpo. |
| VAL-S05 | Aprovado | Binário real gerou execução automática; exemplo público concluiu scheduled e manual. |
| VAL-S06 | Aprovado | Ticks concorrentes nas duas implementações; SQL unique23505 e dedupe após reabrir. |
| VAL-S07 | Aprovado | Contrato comum: atraso longo run_once/skip/grace, limite do tick, fase e próximo futuro; processo reiniciado recuperou uma ocorrência. |
| VAL-S08 | Aprovado | Pausa/retomada idempotente; exemplo executou manualmente durante pausa. |
| VAL-S09 | Aprovado | Desabilitada/arquivada não gera horários; reabilitada não arquivada volta no futuro. |
| VAL-S10 | Aprovado | Admissão concorrente manual/rerun/tick, busy tick avança; cancel pedido mantém slot até finalização. HTTP manual adicional retornou409. |
| VAL-S11 | Aprovado | Fila anterior sob forbid: casos sequencial e oito Claims simultâneos, outra automação prossegue. |
| VAL-O01 | Aprovado | Contrato comum e HTTP: automação/status/período inclusivo antes de limit/offset e desempate estável. |
| VAL-O02 | Aprovado | API: status/datas/limites/repetidos/desconhecidos e query malformada retornam400. |
| VAL-O03 | Aprovado | HTTP: falha404, edição para health, retry continua no snapshot404 com novo ID/parent; original preservado; manual usa configuração nova e tem sucesso. |
| VAL-O04 | Aprovado | Stores/API: estados indevidos ou automação desabilitada/arquivada/ocupada retornam409 sem run extra. |
| VAL-O05 | Aprovado | HTTP com quatro workers ocupados: quinta tarefa cancelada pending, zero tentativas, idempotente. |
| VAL-O06 | Aprovado | Destino HTTP lento: running cancel202, depois canceled, uma tentativa e sem retry. Worker tests observam contexto HTTP interrompido. |
| VAL-O07 | Aprovado | Stores: cancel antes de sucesso/falha permanente/transitória vence; ordem inversa conflita; corrida concorrente termina consistente. |
| VAL-O08 | Aprovado | Stores: lease cancelada expira sem retry, tentativa fecha e token antigo é recusado. |
| VAL-O09 | Aprovado | HTTP: archive preserva histórico, lista oculta/opt-in inclui, novas admissões/alterações bloqueadas. |
| VAL-O10 | Aprovado | Policy validada por API/domínio; worker usa snapshot, mantém fallback global e limita jitter pelo teto configurado. |
| VAL-X01 | Aprovado | Upgrade de001 com dados,002 e reaplicação; banco aplicativo atualizado após backup e contagens/digests intactos. |
| VAL-X02 | Aprovado | Binário inicia scheduler; Ctrl+C encerrou às12:34:15 sem novos ticks; reinício do processo preservou agenda/histórico e recuperou ocorrência atrasada. |
| VAL-X03 | Aprovado | README/guia/exemplo atualizados; exemplo executado; suíte antiga e nova, vet e build passam. |
| VAL-X04 | Aprovado | HTTP: archive com chamadas lentas ativas preservou sua conclusão; teste worker com running e pending também observou ambas concluindo. |
| VAL-X05 | Aprovado | Triggers reais de falha em avanço e inserção: rollback sem run parcial/horário consumido; tick saudável gerou exatamente uma ocorrência. |

## Fluxo real

Smoke em banco exclusivo `central_scheduling_20261020_152912`, API temporária em18083 e destino local em19091. O exemplo público retornou uma execução scheduled, uma manual e archive=true. O roteiro adicional registrou203 respostas HTTP em `.local/scheduling/http-trace.json`, incluindo consultas de acompanhamento; não são203 testes independentes.

O roteiro comprovou falha permanente sem retry automático; reexecução com snapshot histórico; filtros; cancelamento pending e running; continuidade das outras chamadas após archive; bloqueio de overlap; cron/fuso, pausa/resume e remoção de agenda. Resumos e comandos estão em `.local/scheduling/`, ignorada pelo Git.

Prova de reinício: agenda de dois segundos, próximo horário `2026-10-20T15:35:57.6759722Z`; processo voltou às15:35:58.7856491Z. Gerou exatamente uma recuperação, succeeded, com scheduled_for `15:35:57.675972Z` e próximo futuro `15:35:59.6759722Z`. O run recuperado foi `TI66J2UUIF6YV4H7N3MOCB3VPI`. Horários de comparação foram normalizados à precisão de microssegundos do PostgreSQL; os valores Go/Windows têm precisão de100ns. Casos de backlog maior são cobertos por relógio controlado em ambos os stores.

## Migração no banco da central

Antes de aplicar002 no banco aplicativo, foi salvo backup local em `.local/backups/central-before-scheduling-20261020_153648.dump` (8344bytes). `Start-Central.ps1 -MigrateOnly` aplicou a migração. Comparação antes/depois: uma automação, um run e uma tentativa; digests do documento, snapshot/resultado e tentativas permaneceram iguais. Histórico de migrações agora contém versões1 e2.

Os bancos/schema de teste são separados do banco aplicativo. Instâncias e fixture criadas para validação foram encerradas; PostgreSQL Docker permanece disponível. Os dados do smoke foram mantidos no banco de teste para inspeção, sem agenda ativa. Não houve remoção de dados existentes.

## Limites operacionais

Cancelamento não desfaz efeitos externos e depende de comunicação com o banco e cooperação do executor. Ausência do processo é recuperada por lease; não há garantia exatamente uma vez. Intervalos não oferecem precisão de tempo real. Mutações de controle no PostgreSQL são serializadas por schema em transações curtas; execução HTTP permanece concorrente. Painel e autenticação continuam fora desta entrega.
