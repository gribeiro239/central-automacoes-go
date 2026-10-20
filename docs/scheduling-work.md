# Responsabilidades e validação

Contrato revisado em duas passagens independentes; segunda passagem aprovada. Implementação concorrente com arquivos separados, contrato de domínio comum e alterações coordenadas.

| Trabalho | Dono | Arquivos | Critérios principais |
|---|---|---|---|
| Domínio/integração/API | principal | domain implementação, httpapi, config, cmd/central, docs/exemplos | O02, X02, X03 e provas HTTP das demais regras |
| Estado em memória e contrato comum | memory_store | internal/memory, internal/storetest | S04–11, O01, O03–09 |
| Persistência e evolução de esquema | postgres_store | internal/postgres, migrations | X01, X05 e paridade real dos critérios de estado |
| Tempo, agendador e execução | execution_tests | testes domain, internal/scheduler, internal/worker | S01–03, O10, X04 e propagação real O06 |

Validação: suíte anterior e nova, banco PostgreSQL Docker com schemas exclusivos, API/binário reais com destino HTTP local, revisão independente de implementação e atualização dos documentos. Nunca encerrar instâncias do usuário ou apagar banco de desenvolvimento. Não publicar.
