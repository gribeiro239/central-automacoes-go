param(
    [string]$BaseURL = 'http://127.0.0.1:18081',
    [string]$DestinationURL = 'http://127.0.0.1:19091/flaky'
)
$ErrorActionPreference = 'Stop'

$payload = @{
    name = 'Demonstracao HTTP com retry'
    type = 'http_request'
    config = @{ method = 'GET'; url = $DestinationURL }
    timeout_seconds = 2
    max_attempts = 3
} | ConvertTo-Json -Depth 5

$automation = Invoke-RestMethod -Uri "$BaseURL/automations" -Method Post -ContentType 'application/json; charset=utf-8' -Body ([System.Text.Encoding]::UTF8.GetBytes($payload))
$run = Invoke-RestMethod -Uri "$BaseURL/automations/$($automation.id)/runs" -Method Post
$deadline = [DateTime]::UtcNow.AddSeconds(45)
do {
    Start-Sleep -Milliseconds 200
    $run = Invoke-RestMethod -Uri "$BaseURL/runs/$($run.id)"
    if ([DateTime]::UtcNow -gt $deadline) { throw "Tempo limite aguardando a execucao $($run.id)" }
} while ($run.status -in @('pending', 'running'))

$run | ConvertTo-Json -Depth 8
Invoke-RestMethod -Uri "$BaseURL/runs/$($run.id)/attempts" | ConvertTo-Json -Depth 8
if ($run.status -ne 'succeeded') { throw "Execucao terminou em $($run.status)" }
