# Critérios da primeira entrega

Cada linha tem veredito independente. Memória não comprova PostgreSQL. Evidências finais em docs/validation.md. Docker runtime permanece pendente.

| ID | Dono | Surface | Needs | Behavior | Evidence |
|---|---|---|---|---|---|
| VAL-01 | API | API | servidor | Health 200 identifica armazenamento | teste HTTP e smoke binário |
| VAL-02 | API | API | store | Cadastro/listagem/consulta preservam campos; PATCH altera só campos fornecidos | httptest |
| VAL-03 | API | API | servidor | JSON inválido, campos desconhecidos, tamanho e configuração inválida retornam 400; inexistente 404 | httptest |
| VAL-04 | store | API/data | automação | Enqueue retorna pending; desabilitada não aceita execução | testes comuns memória/PG e smoke 202/409 |
| VAL-05 | store | data | execução criada | Edição posterior não altera snapshot | teste comum memória/PG |
| VAL-06 | API | API | execução | Consulta/listagem de runs e attempts mostram resultado e histórico | httptest e smoke |
| VAL-07 | executor | HTTP | destino local | 2xx sucesso e preview limitado a 4096 bytes | httptest real |
| VAL-08 | executor | HTTP | destino lento | Timeout interrompe chamada | httptest real e worker |
| VAL-09 | executor | HTTP | respostas controladas | Transporte,408,429,5xx transitórios; outros não-2xx permanentes | httptest real |
| VAL-10 | worker | job | destino transitório | Retry respeita available_at e espera crescente; para no orçamento | timestamps, store e worker tests |
| VAL-11 | worker | job | mais runs que workers | Pico simultâneo nunca excede WORKERS | contador em destino bloqueante |
| VAL-12 | store | data | claims concorrentes | Só uma posse ativa por run; transação curta | testes concorrentes memória/PG |
| VAL-13 | store | data | consulta | Alterar mapas/ponteiros retornados não altera estado persistido | testes comuns |
| VAL-14 | store | data | lease vencida | Finish de token antigo/vencido é rejeitado | testes comuns |
| VAL-15 | store | data | run running abandonado | Expiração fecha tentativa e recupera run; esgotamento encerra falha | testes comuns memória/PG |
| VAL-16 | postgres | data | PG isolado | Migração repetida preserva esquema e dados | integração real |
| VAL-17 | postgres | data/job | PG isolado | Reabrir store preserva runs/attempts; novo worker recupera reserva abandonada após expiração | integração real com DB fechado/reaberto e tokens antigos |
| VAL-18 | config | CLI | binário | STORAGE explícito; PG indisponível encerra com erro e sem fallback; env inválida recusada | unit tests e processo real |
| VAL-19 | lifecycle | CLI/job | processo e tarefa lenta | Sinal para novas reservas, aguarda prazo configurado, cancela ativos e preserva trabalho recuperável | teste pool e processo real/sinal |
| VAL-20 | docs | artifact | fontes | README/exemplos executáveis e Compose com volume/loopback | revisão, exemplos, Compose real saudável e resultado preservado após reiniciar contêiner |

## Revisão e execução

Revisão independente inicial identificou granularidade, teto de concorrência, pacing de retry, ausência de fallback e fechamento limitado. Itens separados acima. Trabalho agrupado: domain/store (VAL-04,05,12–15), postgres (VAL-16,17 e paridade), API/config/lifecycle (VAL-01–03,06,18–19), executor/worker (VAL-07–11), documentação (VAL-20). Testes reais e revisão final independente decidem os vereditos.
