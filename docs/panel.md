# Guia do painel local

[Prévia do painel](images/panel-desktop.png) · [Registro de validação](panel-validation.md)

O painel está disponível em [http://127.0.0.1:18081/](http://127.0.0.1:18081/) enquanto a central está em execução. Usa o mesmo endereço da API e funciona com armazenamento em memória ou PostgreSQL. No modo em memória, os dados são perdidos ao encerrar o programa.

Para a configuração PostgreSQL local existente, inicie a central na pasta do projeto:

```powershell
.\Start-Central.ps1
```

O script pressupõe que o banco esteja iniciado. Para experimentar em memória:

```powershell
$env:STORAGE = 'memory'
go run ./cmd/central
```

Se alterar `HTTP_ADDR`, use o endereço configurado. A aplicação aceita apenas loopback e ainda não possui autenticação multiusuário.

## Navegação

| Tela | Endereço | Uso |
|---|---|---|
| Visão geral | `/#/` | Métricas, atividade, próximas agendas e execuções recentes |
| Automações | `/#/automations` | Buscar, cadastrar e controlar rotinas |
| Detalhes da automação | `/#/automations/{id}` | Consultar configuração, agenda e histórico recente |
| Execuções | `/#/runs` | Filtrar e percorrer o histórico |
| Detalhes da execução | `/#/runs/{id}` | Resultado, tentativas, snapshot e vínculo de reexecução |

Os endereços após `#` identificam telas do navegador; os endpoints JSON existentes continuam disponíveis sem mudança de prefixo.

## Criar, editar e executar

Selecione **Nova automação** e informe nome, método e URL HTTP(S). Em configurações avançadas, os cabeçalhos usam um objeto JSON com valores de texto, por exemplo `{"Accept":"application/json"}`. O corpo é texto livre de até 64 KiB.

Configure o timeout entre 1 e 300 segundos e o máximo de tentativas entre 1 e 10, incluindo a primeira. Em **Execuções simultâneas**, permitir sobreposição mantém o comportamento padrão; impedir sobreposição bloqueia novas admissões enquanto houver uma execução pendente ou ativa da mesma automação. Isso inclui a espera por retry e um cancelamento ainda em andamento.

Salve e selecione **Executar**. O painel abre a execução criada e acompanha o resultado. A seção de tentativas mostra início, fim, duração, worker e resposta de cada tentativa. O preview da resposta é limitado a 4096 bytes; uma indicação informa quando houve truncamento.

Ao editar, os valores existentes são carregados no formulário. Salvar somente nome ou configuração HTTP preserva o horário já calculado da agenda. Alterar a definição da agenda recalcula o próximo horário. Erros de validação são apresentados no formulário, mantendo os campos preenchidos para correção.

## Agenda e política de tentativas

| Opção | Comportamento |
|---|---|
| Somente manualmente | Não cria execuções automaticamente; ao editar, remove uma agenda existente |
| Intervalos regulares | Aceita segundos, minutos, horas ou dias; intervalo total entre 1 segundo e 365 dias |
| Expressão cron | Usa cinco campos e fuso IANA explícito, como `0 8 * * *` com `America/Sao_Paulo` |
| Agendamento pausado | Suspende somente a agenda; a execução manual continua disponível |
| Automação desabilitada | Impede novas execuções manuais e agendadas; o trabalho já enfileirado continua |

Pausar e retomar também estão disponíveis nas ações da automação. Retomar ou reabilitar calcula um próximo horário futuro, sem reproduzir o período suspenso. O próximo horário é exibido conforme a formatação local do navegador; o cron é interpretado no fuso informado na agenda.

Para horários perdidos, **Executar uma vez ao recuperar** cria no máximo uma execução de recuperação. **Ignorar após a tolerância** descarta ocorrências além do atraso permitido, entre 1 e 3600 segundos. Nenhuma dessas opções cria uma fila ilimitada de horários passados.

Marque **Personalizar espera entre tentativas** para definir espera inicial e máxima. Desmarque para herdar novamente o padrão da central. A mudança vale para novas execuções; snapshots já registrados continuam com sua política original. O máximo de tentativas inclui a primeira chamada, e falhas permanentes não consomem todas as tentativas por repetição automática. Consulte o [guia operacional](scheduling.md) para a política completa de falhas e retries.

## Interpretar a visão geral

As métricas de atividade abrangem **sete dias de calendário, incluindo hoje**, no fuso do navegador: da meia-noite de seis dias atrás até a geração dos dados. O dia atual está incompleto. O agrupamento usa a data de **criação** da execução; uma tarefa antiga finalizada hoje continua associada ao dia em que foi criada.

| Indicador | Abrangência |
|---|---|
| Automações no workspace | Automações atuais não arquivadas |
| Automações ativas | Automações habilitadas e não arquivadas; a agenda pode estar pausada |
| Agendas ativas | Automações habilitadas, não arquivadas, com agenda não pausada |
| Execuções, sucessos, falhas e gráfico | Execuções criadas no período de sete dias |
| Fila e em execução | Todo trabalho `pending`/`running`, mesmo anterior ao período ou de automação arquivada |

**Taxa de sucesso = sucessos ÷ (sucessos + falhas)**. Canceladas, pendentes e em execução não entram no denominador. Sem sucessos ou falhas, o painel mostra um travessão, em vez de inventar uma taxa de 100%.

Os contadores são calculados pelo backend sobre os registros pertinentes, sem se limitar à página do histórico. As listas de **execuções recentes** e **próximas execuções** são resumos de até oito e cinco itens, respectivamente. Uma falha de conexão é exibida como erro; dados anteriores podem estar desatualizados.

## Histórico, filtros e atualização

A tela de execuções permite filtrar automação, estado e datas de criação. Os campos de data usam o horário local do navegador. O histórico é ordenado da criação mais recente para a mais antiga, com desempate por ID, e mostra **25 resultados por página**. A indicação de próxima página vem de um item adicional consultado; não há total calculado de páginas.

Ao entrar ou aplicar filtros, o painel fixa um **instante de corte**. As páginas seguintes mantêm esse corte, evitando que novas execuções desloquem os resultados. **Atualizar** redefine o corte e retorna à primeira página, mantendo os filtros. Filtros, página e corte ficam no endereço do histórico para permitir recarregamento.

O histórico continua ao vivo: os estados dos registros existentes podem mudar. Portanto, um filtro de estado não representa um snapshot imutável; uma execução pode deixar uma página quando muda de estado. O corte limita novas criações, sem congelar essas mudanças.

A atualização automática ocorre aproximadamente a cada três segundos enquanto a página está visível e nenhum formulário ou campo está sendo editado. Detalhes da automação mostram apenas as oito execuções mais recentes; **Ver todas** abre o histórico filtrado daquela rotina.

## Cancelar, reexecutar e arquivar

- **Cancelar execução:** exige confirmação. Uma execução pendente é cancelada sem iniciar outra tentativa. Se estiver ativa, aparece como **Cancelando** até o worker interromper a chamada e registrar o estado final. Cancelar não desfaz efeitos externos que já ocorreram.
- **Reexecutar:** disponível para falhas e canceladas quando a automação atual permite novas admissões. Cria outro ID, vinculado ao original, e mantém o histórico anterior. Reutiliza o snapshot original, incluindo URL, headers, timeout e retry, mesmo após editar a automação.
- **Arquivar:** exige confirmação, desabilita a automação e remove o próximo horário. O histórico continua acessível, e trabalhos já na fila ou ativos continuam. Marque **Incluir arquivadas** na lista para consultar essas rotinas; novas execuções e edições ficam indisponíveis.

Cada execução mantém sua **configuração preservada** nos detalhes. A duração de uma tentativa mede a chamada correspondente; **Tempo total na central** também inclui fila e esperas por retry. Conflitos de estado e erros da API são apresentados na tela; ações dependem da resposta do servidor.

## Desenvolvimento do painel

HTML, CSS e módulos JavaScript são incorporados ao executável Go. **Não é necessário Node.js, npm ou um servidor frontend para usar ou compilar a central.** Os arquivos não dependem de CDN.

Somente para desenvolver o JavaScript, com Node.js e npm instalados, execute na pasta do projeto:

```powershell
npm.cmd run check
npm.cmd test
```

O primeiro comando verifica sintaxe; o segundo executa os testes de lógica do frontend. Não é necessário instalar pacotes npm. Esses comandos não comprovam sozinhos os fluxos no navegador. Depois de editar assets incorporados, reinicie `go run` ou recompile o binário.
