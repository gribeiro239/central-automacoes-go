# Validação realizada — 20/10/2026

## Ambiente

Windows amd64, Go 1.27.2 e PostgreSQL 18.6. Docker ausente. Foi criado um cluster PostgreSQL temporário, separado das instalações existentes, com acesso apenas por loopback na porta 55438. Testes usaram schemas exclusivos removidos automaticamente; smoke do binário usou um banco exclusivo desse cluster. Nenhum banco operacional foi acessado.

O sandbox bloqueou conexões TCP locais e apresentou falhas de acesso ao cache Go. A validação definitiva usou execução autorizada fora desse sandbox; falhas ambientais anteriores não foram contadas como sucesso.

## Comandos e resultados

```text
go mod tidy                                      OK
go vet ./...                                     OK
go build -o bin/central.exe ./cmd/central         OK
go build -o bin/fixture.exe ./cmd/fixture         OK
TEST_DATABASE_URL=<banco temporário isolado>
go test -count=1 -p 2 -timeout 120s ./...          OK
```

Na suíte final: config 1.580s, executor 3.572s, httpapi 3.362s, memory 1.539s, postgres 7.964s e worker 4.842s. Pacotes de composição, domínio, migrações e testes comuns também compilaram. Não foi executado `-race`: ambiente Go com CGO desabilitado e sem compilador C disponível.

Revisão independente do contrato foi aprovada após separar os critérios. Revisão independente do executor/API encontrou classificação incorreta de HTTP permanente com corpo incompleto e cabeçalhos com controle inválido; ambos foram corrigidos com regressões. Previews binários passaram a ser normalizados antes de persistir em JSONB. Uma suposição de ordem entre timestamps iguais no teste de shutdown foi substituída por sincronização explícita da fixture, mantendo a prova de parada de novas reservas.

## Evidências por critério

| Critério | Veredito | Evidência |
|---|---|---|
| VAL-01 | Aprovado | Health do binário retornou status ok e storage postgres; modo memory também iniciado explicitamente. |
| VAL-02 | Aprovado | TestAPIWorkflow: cadastro, consulta, lista e PATCH preservando campos omitidos. Demonstração cadastra automação no binário. |
| VAL-03 | Aprovado | TestInvalidInput cobre JSON, desconhecidos, tamanho, configuração e NUL/DEL em POST/PATCH; binário respondeu 400 para inválido. |
| VAL-04 | Aprovado | Contrato comum nos dois stores e binário: enqueue 202, desabilitada 409. |
| VAL-05 | Aprovado | snapshot_and_copy_isolation executado nos dois stores após edição da automação. |
| VAL-06 | Aprovado | API retorna runs e attempts; exemplos consultaram resultados reais e tentativas 503/200. |
| VAL-07 | Aprovado | Executor real e smoke /large: succeeded, uma tentativa, preview 4096 bytes, truncated true. |
| VAL-08 | Aprovado | TestHTTPTimeoutAndCancellation e TestWorkerAppliesAttemptTimeout atingem fixture real. |
| VAL-09 | Aprovado | Classificação por status/transporte e regressões para corpo incompleto HTTP 400 permanente / HTTP 200 transitório. |
| VAL-10 | Aprovado | Worker: delays crescentes e chave estável; contrato comum: retry indisponível antes do prazo e orçamento respeitado. Binário /flaky: 503 seguido de 200, duas tentativas. |
| VAL-11 | Aprovado | TestWorkerConcurrencyIsBoundedAndQueueDrains mede pico em destino bloqueante e verifica conclusão da fila. |
| VAL-12 | Aprovado | exclusive_concurrent_claim no store em memória e no PostgreSQL real. |
| VAL-13 | Aprovado | Contrato comum altera retornos, mapas, resultados e timestamps sem alterar armazenamento. |
| VAL-14 | Aprovado | Contrato comum e teste de reopen rejeitam token vencido e token substituído. |
| VAL-15 | Aprovado | Contrato comum comprova retomada por expiração e falha terminal por orçamento esgotado. |
| VAL-16 | Aprovado | TestMigrationRepeatabilityPreservesData, PostgreSQL real, segunda migração mantém automação e run. |
| VAL-17 | Aprovado | TestReopenRecoversAbandonedLeaseAndKeepsHistory fecha/reabre store com tentativa running abandonada, recupera reserva e preserva resultados/tentativas. Smoke também reiniciou processo real mantendo dados. |
| VAL-18 | Aprovado | Testes de configuração; binário com banco em porta indisponível saiu 1 com erro; WORKERS=0 saiu 1; modo memory funcionou separadamente. |
| VAL-19 | Aprovado | Testes de drenagem/cancelamento e Ctrl+C no binário real: prazo de um segundo, quatro chamadas canceladas persistidas e quinta tarefa intocada na fila. Após reiniciar, todas concluíram. |
| VAL-20 | Aprovado para artefatos; Docker pendente | README, .env.example, SQL e Compose revisados; exemplos PowerShell executados em memory e postgres. Não houve execução de Docker/Compose. |

## Smoke de reinício e encerramento

API real em `127.0.0.1:18081`, quatro workers, destino HTTP local em `127.0.0.1:19091`. Dois resultados iniciais: /flaky com duas tentativas e /large com uma tentativa. Ambos sobreviveram ao reinício.

Cinco chamadas /slow foram solicitadas com timeout de 30 segundos. Antes de Ctrl+C: quatro running com attempt_count=1 e uma pending com attempt_count=0. O log registrou encerramento solicitado às 10:25:48 e cancelamento após o prazo às 10:25:49, no horário de São Paulo. Consulta SQL após o encerramento confirmou quatro resultados de tentativa `context canceled`, quatro runs pending com uma tentativa e um run pending sem tentativa.

Reinício às 10:26:26: os cinco runs chegaram a succeeded, quatro com duas tentativas e um com uma. Isso comprova persistência entre processos e retomada de fila. A recuperação por lease vencida após abandono abrupto é comprovada separadamente pelo teste real PostgreSQL, com lease curta controlada; o lease do produto permanece em 330 segundos.

O wrapper do terminal reportou código 1 ao receber Ctrl+C; os logs da aplicação confirmaram a sequência de encerramento e `central encerrada`. O comportamento não foi inferido a partir do código de saída do wrapper.

## Limites

Na primeira validação, Compose ainda estava pendente; essa etapa foi concluída na validação adicional abaixo. Agendamento automático e painel não fazem parte desta primeira entrega. Garantia de processamento pelo menos uma vez; idempotência de efeitos externos depende do destino. O modo memory é volátil. Serviços e cluster temporários da primeira validação foram encerrados após o smoke.

## Validação adicional com Docker — 20/10/2026

Após a instalação e configuração inicial do Docker Desktop pelo usuário, o contexto `desktop-linux` respondeu normalmente. Docker Engine 29.9.1 e Compose v5.6.0 foram confirmados. A sessão do terminal anterior à instalação não tinha o diretório do auxiliar de credenciais no PATH; ele foi adicionado somente aos processos desta validação. Nenhum login adicional foi realizado, e nenhum dado da conta Docker foi gravado no projeto.

- `.env` local criado com senha aleatória, sem exibir seu conteúdo. Arquivo ignorado pelo `.gitignore`.
- `docker compose config --quiet`: aprovado.
- `docker compose up -d --wait`: aprovado; `central-automacoes-postgres-1` saudável, porta `127.0.0.1:55432`, volume `central-automacoes_postgres_data`.
- `Start-Central.ps1 -MigrateOnly`: aprovado; migrações aplicadas e variáveis do terminal restauradas.
- `go test -count=1 ./internal/postgres`, com TEST_DATABASE_URL em banco exclusivo `central_validation` no contêiner: aprovado em 4.444s, incluindo migração, reserva concorrente, recuperação, snapshots e persistência de preview binário.
- Binário da API iniciado temporariamente em `127.0.0.1:18083`, pois a instância existente em `18081` estava em modo memory e foi preservada.
- Automação HTTP real criada e executada: run `333W6EN2P7ETAM777AELIRLDQ5`, succeeded, uma tentativa. O destino foi `/health` da própria API, retornando storage postgres.
- `docker compose restart postgres`, seguido de `docker compose up -d --wait`: aprovado. Após reiniciar o contêiner, `/health` retornou ok/postgres e o resultado do mesmo run continuou succeeded com uma tentativa.
- Instância temporária em 18083 encerrada com Ctrl+C; banco Docker mantido em execução para desenvolvimento. Instância pré-existente em 18081 preservada.

VAL-20 agora está aprovado também na superfície Docker/Compose real. O inicializador `Start-Central.ps1` lê apenas valores simples CHAVE=valor do arquivo local, monta a URL sem exibir a senha e usa PostgreSQL por padrão. Para mudar a instância existente de memory para postgres, encerre-a no terminal correspondente e execute o inicializador; dados voláteis anteriores não são migrados automaticamente.
