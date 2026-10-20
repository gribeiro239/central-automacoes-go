# Primeira entrega: central de automações

Registro histórico da primeira entrega. O escopo atual foi ampliado para agendamento e controles operacionais em [scheduling-scope.md](scheduling-scope.md); a execução Docker já foi validada em [validation.md](validation.md).

Projeto novo, local, Go 1.27 e PostgreSQL. Pasta final: `C:\Users\Jetta\Desktop\meus projetos\Go Lang 2`.

## Escopo e inventário

- API JSON local: health, cadastro/listagem/consulta/edição de automações HTTP; execução manual; listagem/consulta de execuções e tentativas.
- Configuração validada, congelada por execução; automação desabilitada não aceita novas execuções. Trabalhos já enfileirados continuam.
- Worker pool configurável, chamadas HTTP com timeout, resposta resumida limitada, retries de falhas transitórias e encerramento controlado.
- Persistência intercambiável: memória para demonstração e PostgreSQL para durabilidade. Não há fallback silencioso para memória.
- PostgreSQL com migrações, reserva transacional e prazo de posse; recuperação de tentativa abandonada e proteção contra conclusão de reserva antiga.
- README em português, exemplos PowerShell, Compose com volume persistente e testes reproduzíveis.

## Limites desta entrega

Agendamento automático é a próxima etapa, conforme a ordem discutida. Não inclui painel, autenticação multiusuário, execução de shell ou publicação GitHub. Servidor vinculado ao loopback por padrão; uso local de desenvolvimento. As chamadas podem ser repetidas após falhas: garantia pelo menos uma vez, sem promessa de efeito externo único. Para POST/PUT/PATCH/DELETE usar idempotência suportada pelo destino.

## Ambiente e validação

Go e Git confirmados. Docker ausente. PostgreSQL 18.6 tem executáveis disponíveis; tentar cluster temporário isolado na porta 55438, sem acessar instâncias existentes. Banco real deve provar migração, concorrência, reservas vencidas, snapshots e reinício. Testes HTTP usam servidor local com sucesso, falha transitória, falha permanente e atraso. Compilar e executar o binário para comprovar o fluxo por HTTP.

## Organização

Módulos: domain (contrato), memory e postgres (armazenamento), executor (HTTP), worker (processamento), httpapi (API), config e cmd/central (composição). Implementação pode ocorrer em paralelo após revisão do contrato. Runtime submit_plan/advance_project indisponível nesta sessão: usar arquivos de escopo, revisão independente e comandos reais como trilha local de execução.
