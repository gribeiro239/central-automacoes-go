param(
    [ValidateSet('postgres', 'memory')]
    [string]$Storage = 'postgres',
    [switch]$MigrateOnly
)

$ErrorActionPreference = 'Stop'
$settings = @{}
$envPath = Join-Path $PSScriptRoot '.env'
if (Test-Path -LiteralPath $envPath) {
    foreach ($line in Get-Content -LiteralPath $envPath) {
        if ($line -match '^\s*([A-Z_]+)=(.*)$') {
            $value = $Matches[2].Trim()
            if ($value.Length -ge 2 -and (($value.StartsWith('"') -and $value.EndsWith('"')) -or ($value.StartsWith("'") -and $value.EndsWith("'")))) {
                $value = $value.Substring(1, $value.Length - 2)
            }
            $settings[$Matches[1]] = $value
        }
    }
}

$changes = @{ STORAGE = $Storage }
foreach ($key in @('HTTP_ADDR', 'WORKERS', 'POLL_INTERVAL', 'SCHEDULER_INTERVAL', 'RETRY_BASE', 'SHUTDOWN_TIMEOUT')) {
    if ($settings.ContainsKey($key) -and $settings[$key] -ne '') { $changes[$key] = $settings[$key] }
}

if ($Storage -eq 'postgres') {
    if (-not $settings.ContainsKey('POSTGRES_PASSWORD') -or $settings['POSTGRES_PASSWORD'] -eq '') {
        throw 'Preencha POSTGRES_PASSWORD no arquivo .env antes de iniciar com PostgreSQL.'
    }
    $pgUser = if ($settings['POSTGRES_USER']) { $settings['POSTGRES_USER'] } else { 'central' }
    $pgDatabase = if ($settings['POSTGRES_DB']) { $settings['POSTGRES_DB'] } else { 'central' }
    $pgPort = if ($settings['POSTGRES_PORT']) { $settings['POSTGRES_PORT'] } else { '55432' }
    if ($pgPort -notmatch '^\d+$' -or [int]$pgPort -lt 1 -or [int]$pgPort -gt 65535) { throw 'POSTGRES_PORT inválida no .env.' }
    $changes['DATABASE_URL'] = 'postgres://' + [uri]::EscapeDataString($pgUser) + ':' + [uri]::EscapeDataString($settings['POSTGRES_PASSWORD']) + '@127.0.0.1:' + $pgPort + '/' + [uri]::EscapeDataString($pgDatabase) + '?sslmode=disable'
}
if ($MigrateOnly -and $Storage -ne 'postgres') { throw 'MigrateOnly exige armazenamento postgres.' }

$previous = @{}
Push-Location $PSScriptRoot
try {
    foreach ($key in $changes.Keys) {
        $previous[$key] = [Environment]::GetEnvironmentVariable($key, 'Process')
        [Environment]::SetEnvironmentVariable($key, $changes[$key], 'Process')
    }
    $appArguments = @()
    if ($MigrateOnly) { $appArguments += 'migrate' }
    & go run ./cmd/central @appArguments
    if ($LASTEXITCODE -ne 0) { throw "A central encerrou com código $LASTEXITCODE." }
} finally {
    foreach ($key in $previous.Keys) { [Environment]::SetEnvironmentVariable($key, $previous[$key], 'Process') }
    Pop-Location
}
