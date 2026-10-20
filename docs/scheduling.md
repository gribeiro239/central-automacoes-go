# Agendamento e controles operacionais

O backend inicia agendador, API e workers no mesmo processo. O agendador consulta horários vencidos a cada `SCHEDULER_INTERVAL` (padrão 500ms) e somente insere trabalhos na fila. No PostgreSQL, a geração de uma ocorrência e a atualização do próximo horário pertencem à mesma transação. Uma restrição de unicidade protege automação/horário. Transações curtas de controle usam um advisory lock por banco/schema para coordenar admissões, reservas e cancelamentos entre processos; o lock é liberado antes de qualquer chamada HTTP.

## Criar agenda por intervalo

```json
{
  "name": "Consultar integracao",
  "type": "http_request",
  "config": {"method": "GET", "url": "http://127.0.0.1:19091/health"},
  "timeout_seconds": 15,
  "max_attempts": 3,
  "overlap_policy": "forbid",
  "retry_policy": {"base_delay_seconds": 2, "max_delay_seconds": 30},
  "schedule": {
    "kind": "interval",
    "interval_seconds": 300,
    "paused": false,
    "misfire_policy": "run_once",
    "misfire_grace_seconds": 60
  }
}
```

Envie com POST `/automations`. Intervalo: de 1 a 31536000 segundos. O primeiro horário é calculado a partir do cadastro; a execução não é disparada imediatamente. `next_run_at` retorna o próximo instante em UTC. Os intervalos seguintes mantêm a fase original mesmo quando o tick chega atrasado. Execuções pontuais continuam disponíveis em POST `/automations/{id}/runs`.

## Cron com fuso

Use o objeto abaixo em `schedule` no cadastro ou PATCH da automação:

```json
{
  "kind": "cron",
  "cron": "0 8 * * 1-5",
  "timezone": "America/Sao_Paulo",
  "misfire_policy": "skip",
  "misfire_grace_seconds": 60
}
```

Isso agenda 08:00 de segunda a sexta no fuso de São Paulo. Cron tem cinco campos: minuto, hora, dia do mês, mês e dia da semana. Segundos, macros como `@daily` e prefixos `TZ=` dentro da expressão são recusados. O fuso é obrigatório em cron. Expressões impossíveis e fusos inválidos retornam 400. O banco armazena instantes UTC; a interpretação da expressão usa o fuso informado. As regras IANA estão embutidas no programa para funcionamento no Windows.

Na transição de horário de verão, horários locais inexistentes são pulados e horários repetidos podem executar duas vezes em instantes UTC diferentes, seguindo o [parser robfig/cron](https://github.com/robfig/cron). Não há promessa de precisão de tempo real: disparos dependem do intervalo de consulta, disponibilidade do banco e capacidade dos workers.

## Pausar, retomar e lidar com atraso

| Operação | Rota/alteração | Resultado |
|---|---|---|
| Pausar agenda | POST `/automations/{id}/schedule/pause` | Sem novas ocorrências; execução manual permitida |
| Retomar agenda | POST `/automations/{id}/schedule/resume` | Recalcula próximo horário futuro |
| Desabilitar automação | PATCH `{"enabled":false}` | Bloqueia manual/reexecução e suspende agenda |
| Reabilitar | PATCH `{"enabled":true}` | Agenda habilitada retoma no futuro |
| Remover agenda | PATCH `{"remove_schedule":true}` | Mantém a automação para uso manual |

Pausa e retomada são idempotentes. Uma agenda já retomada não tem sua fase deslocada por um segundo pedido de retomada. Trabalhos já enfileirados ou ativos continuam em todos esses casos. Para interrompê-los, cancele os runs individualmente. Alterar o objeto `schedule` substitui a definição completa e recalcula o próximo horário.

Quando o processo retorna após ficar desligado:

- `run_once` (padrão) gera no máximo uma execução de recuperação por automação; não reproduz cada horário perdido.
- `skip` descarta a ocorrência se o atraso ultrapassa `misfire_grace_seconds` (padrão 60; 1–3600). Dentro dessa tolerância, a ocorrência ainda é gerada.

Após tratar o atraso, o próximo horário fica no futuro. O campo `scheduled_for` identifica o horário original da ocorrência recuperada; `created_at` indica quando ela foi inserida na fila.

## Sobreposição

`overlap_policy` aceita:

- `allow` (padrão compatível com a primeira versão): permite múltiplos runs da automação.
- `forbid`: manual e reexecução retornam 409 enquanto há um run pending/running. Uma ocorrência agendada nesse período é pulada e o próximo horário avança. Runs antigos já na fila são processados em sequência.

O slot também permanece ocupado enquanto um run running aguarda confirmação de cancelamento. Retry automático pertence ao mesmo run e não consome outro slot.

## Histórico, filtros e tentativas

GET `/runs` aceita parâmetros combináveis:

| Parâmetro | Regra |
|---|---|
| `automation_id` | ID exato da automação |
| `status` | pending, running, succeeded, failed ou canceled |
| `from` / `to` | Intervalo inclusivo sobre created_at, formato RFC3339 com fuso |
| `limit` | 1–500, padrão 100 |
| `offset` | Inteiro >=0, padrão 0 |

Filtros são aplicados antes da paginação. A ordem é created_at decrescente e ID decrescente como desempate. Filtros desconhecidos, vazios ou repetidos são recusados. Exemplo:

```powershell
$query = 'automation_id=ID&status=failed&from=2026-10-01T00:00:00Z&to=2026-10-31T23:59:59Z&limit=25&offset=0'
Invoke-RestMethod "http://127.0.0.1:18081/runs?$query"
Invoke-RestMethod 'http://127.0.0.1:18081/runs/ID/attempts'
```

Cada tentativa expõe número, worker, início, fim, erro, status HTTP e preview. Nenhuma nova tentativa sobrescreve as anteriores.

## Reexecutar falha

POST `/runs/{id}/retry` aceita runs failed ou canceled, devolvendo 202 e um novo run com `origin=retry` e `parent_run_id`. Usa a configuração congelada do run original, mesmo que a automação tenha sido editada. O orçamento de tentativas começa novamente em zero; resultado e tentativas do original não são alterados. Cada novo run tem nova chave de idempotência.

Para executar a configuração atualizada, use POST `/automations/{id}/runs`. Reexecução de pending/running/succeeded retorna 409, assim como automação atualmente desabilitada, arquivada ou ocupada com overlap forbid.

## Cancelar

POST `/runs/{id}/cancel` tem dois comportamentos:

- Pending: retorna 200 com canceled, sem criar tentativa nem chamar o destino.
- Running: retorna 202 com `cancel_requested_at`. O worker observa o pedido, cancela o contexto HTTP e registra canceled sem retry.

Se o cancelamento for registrado antes da conclusão, ele prevalece sobre sucesso, falha permanente ou retry. Se a conclusão terminal já venceu a corrida, o cancelamento retorna 409. Repetir um cancelamento já solicitado ou concluído é idempotente. Caso um worker morra, a expiração da reserva finaliza o cancelamento pendente sem reexecutar.

Cancelar uma chamada não desfaz um efeito já realizado pelo destino. O sistema mantém a garantia pelo menos uma vez, não exatamente uma vez.

## Arquivar

POST `/automations/{id}/archive` marca `archived_at`, desabilita novas admissões e limpa o próximo horário. É idempotente e não apaga automações, runs ou tentativas. Runs pending/running anteriores continuam executando. Não há desarquivamento nesta entrega.

A listagem padrão oculta arquivadas. GET `/automations?include_archived=true` as inclui; consulta por ID e histórico permanecem acessíveis. Alterações e novos disparos/reexecuções/agendas da arquivada retornam 409.

## Política de retry

Timeout e max_attempts continuam editáveis. `retry_policy` opcional define `base_delay_seconds` (1–3600) e `max_delay_seconds` (entre base e 86400). A espera cresce exponencialmente com jitter e é limitada pelo máximo depois de aplicar a variação aleatória.

Sem política explícita, usa `RETRY_BASE` do processo, preservando o comportamento anterior. A política explícita é congelada no snapshot; alterar uma automação não altera runs existentes.

## Executar o exemplo

Com a central e `go run ./cmd/fixture` em execução:

```powershell
.\examples\scheduling.ps1
```

O exemplo cria agenda curta, espera execução automática, pausa, executa manualmente e arquiva a automação. O exemplo arquiva somente a automação que acabou de criar.
