param(
    [string]$BaseURL = 'http://127.0.0.1:18081',
    [string]$DestinationURL = 'http://127.0.0.1:19091/health'
)
$ErrorActionPreference = 'Stop'
$payload = @{
    name = 'Demonstracao de agendamento'
    type = 'http_request'
    config = @{ method = 'GET'; url = $DestinationURL }
    schedule = @{ kind = 'interval'; interval_seconds = 2; misfire_policy = 'run_once' }
    overlap_policy = 'forbid'
    timeout_seconds = 5
    max_attempts = 2
} | ConvertTo-Json -Depth 5
$automation = Invoke-RestMethod "$BaseURL/automations" -Method Post -ContentType 'application/json' -Body $payload
$deadline = [DateTime]::UtcNow.AddSeconds(20)
do {
    Start-Sleep -Milliseconds 200
    $runs = Invoke-RestMethod "$BaseURL/runs?automation_id=$($automation.id)&status=succeeded"
    $scheduled = @($runs | Where-Object { $_.origin -eq 'scheduled' })
    if ([DateTime]::UtcNow -gt $deadline) { throw 'A execucao automatica nao concluiu no prazo.' }
} while ($scheduled.Count -eq 0)
Invoke-RestMethod "$BaseURL/automations/$($automation.id)/schedule/pause" -Method Post | Out-Null
# Wait for any last automatically admitted run before testing manual admission.
do {
    $runs = Invoke-RestMethod "$BaseURL/runs?automation_id=$($automation.id)"
    $active = @($runs | Where-Object { $_.status -in @('pending', 'running') })
    if ([DateTime]::UtcNow -gt $deadline) { throw 'A fila nao drenou no prazo.' }
    if ($active.Count -gt 0) { Start-Sleep -Milliseconds 100 }
} while ($active.Count -gt 0)
$manual = Invoke-RestMethod "$BaseURL/automations/$($automation.id)/runs" -Method Post
do {
    Start-Sleep -Milliseconds 100
    $manual = Invoke-RestMethod "$BaseURL/runs/$($manual.id)"
    if ([DateTime]::UtcNow -gt $deadline) { throw 'A execucao manual nao concluiu no prazo.' }
} while ($manual.status -in @('pending', 'running'))
if ($manual.status -ne 'succeeded') { throw "Execucao manual terminou em $($manual.status)." }
$archived = Invoke-RestMethod "$BaseURL/automations/$($automation.id)/archive" -Method Post
[pscustomobject]@{
    automation_id = $automation.id
    scheduled_run_id = $scheduled[0].id
    manual_run_id = $manual.id
    archived = ($null -ne $archived.archived_at)
} | ConvertTo-Json
