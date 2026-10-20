# Central de Automações em Go

Painel web e API local para cadastrar rotinas HTTP, agendar execuções e acompanhar resultados.

## Funcionalidades

- Agendamento por intervalo ou cron com fuso horário, pausa e retomada.
- Workers concorrentes, timeout e retry com espera exponencial.
- Histórico de execuções e tentativas, com cancelamento e reexecução.
- Controle de sobreposição e arquivamento de automações.
- Armazenamento em memória ou PostgreSQL, com migrações automáticas.

O painel é incorporado ao executável Go e não depende de Node.js para funcionar.

## Executar

Requisito: **Go 1.27+**. Os exemplos abaixo usam PowerShell.

### Sem banco de dados

```powershell
go mod download
$env:STORAGE = 'memory'
go run ./cmd/central
```

Acesse **[http://127.0.0.1:18081](http://127.0.0.1:18081)**. Neste modo, os dados são perdidos ao encerrar.

### Com PostgreSQL

Com o Docker Desktop em execução, crie o arquivo de configuração sem substituir um `.env` existente:

```powershell
if (!(Test-Path .env)) { Copy-Item .env.example .env }
notepad .env
```

Preencha `POSTGRES_PASSWORD`, salve e inicie:

```powershell
docker compose up -d --wait
powershell.exe -NoProfile -ExecutionPolicy Bypass -File ".\Start-Central.ps1"
```

O launcher carrega o `.env` e inicia a central com PostgreSQL. A liberação de scripts vale somente para esse processo. O volume Docker preserva os dados entre reinícios. Use **Ctrl+C** para encerrar a central.

## Testar e compilar

```powershell
go test ./...
go vet ./...
go build -o bin/central.exe ./cmd/central
```

Para incluir os testes PostgreSQL, defina `TEST_DATABASE_URL` com a conexão de um **banco exclusivo de teste**. Sem essa variável, esses testes são ignorados; com ela, criam e removem schemas próprios.

Para verificar o JavaScript, com Node.js instalado e sem instalar pacotes:

```powershell
npm.cmd run check
npm.cmd test
```

Para experimentar uma automação HTTP local, execute `go run ./cmd/fixture` em outro terminal e cadastre a URL `http://127.0.0.1:19091/health` no painel.

## Documentação

- [Uso do painel](docs/panel.md)
- [Agendamento e operação da API](docs/scheduling.md)
- [Validação do painel](docs/panel-validation.md)
- [Variáveis de configuração](.env.example)

## Limites atuais

Aplicação local, sem autenticação multiusuário, com acesso restrito a loopback. As execuções têm garantia de pelo menos uma vez; evitar efeitos duplicados depende de o destino respeitar o cabeçalho `Idempotency-Key`.
