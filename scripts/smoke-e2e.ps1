param(
  [string]$BaseUrl = "http://127.0.0.1:8080",
  [string]$Username = "admin",
  [string]$Password = "admin",
  [string]$CallbackToken = "maas-box-callback-token"
)

$ErrorActionPreference = "Stop"

function Invoke-MBApi {
  param(
    [Parameter(Mandatory = $true)][string]$Method,
    [Parameter(Mandatory = $true)][string]$Path,
    [object]$Body = $null,
    [hashtable]$Headers = @{}
  )

  $uri = "$BaseUrl$Path"
  $params = @{
    Method  = $Method
    Uri     = $uri
    Headers = $Headers
  }
  if ($null -ne $Body) {
    $params.ContentType = "application/json"
    $params.Body = ($Body | ConvertTo-Json -Depth 20 -Compress)
  }

  $resp = Invoke-RestMethod @params
  if ($null -eq $resp) {
    throw "empty response: $Method $Path"
  }
  if ($null -ne $resp.code -and $resp.code -ne 0) {
    throw "api failed [$($resp.code)] $($resp.msg): $Method $Path"
  }
  return $resp.data
}

$timestamp = Get-Date -Format "yyyyMMddHHmmss"
$deviceId = ""
$algorithmId = ""
$taskId = ""
$eventId = ""
$recordingName = ""

try {
  $login = Invoke-MBApi -Method "Post" -Path "/api/v1/auth/login" -Body @{
    username = $Username
    password = $Password
  }
  $token = [string]$login.token
  if ([string]::IsNullOrWhiteSpace($token)) {
    throw "login succeeded but token is empty"
  }

  $authHeaders = @{ Authorization = "Bearer $token" }
  $callbackHeaders = @{ Authorization = $CallbackToken }

  $device = Invoke-MBApi -Method "Post" -Path "/api/v1/devices" -Headers $authHeaders -Body @{
    name             = "smoke-device-$timestamp"
    area_id          = "root"
    protocol         = "rtsp"
    transport        = "tcp"
    stream_url       = "rtsp://127.0.0.1:8554/live"
  }
  $deviceId = [string]$device.id

  $repoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
  $recordingDir = Join-Path (Join-Path $repoRoot "configs\\recordings") $deviceId
  New-Item -ItemType Directory -Force -Path $recordingDir | Out-Null
  $recordingName = "smoke-$timestamp.mp4"
  $recordingFile = Join-Path $recordingDir $recordingName
  Set-Content -Path $recordingFile -Value "smoke-recording-bytes" -NoNewline

  $recordingsResp = Invoke-MBApi -Method "Get" -Path "/api/v1/devices/$deviceId/recordings?page=1&page_size=20" -Headers $authHeaders
  $recordings = @($recordingsResp.items)
  $matched = @($recordings | Where-Object { $_.name -eq $recordingName })
  if ($matched.Count -le 0) {
    throw "recording list missing test file: $recordingName"
  }

  $downloadTarget = Join-Path $env:TEMP $recordingName
  $downloadURL = "$BaseUrl/api/v1/devices/$deviceId/recordings/file/$recordingName"
  Invoke-WebRequest -Method "Get" -Uri $downloadURL -Headers $authHeaders -OutFile $downloadTarget | Out-Null
  if ((Get-Item $downloadTarget).Length -le 0) {
    throw "downloaded recording file is empty"
  }
  Remove-Item -Force $downloadTarget

  $deleteRecordingResp = Invoke-MBApi -Method "Delete" -Path "/api/v1/devices/$deviceId/recordings" -Headers $authHeaders -Body @{
    paths = @($recordingName)
  }
  if ([int]$deleteRecordingResp.summary.removed -lt 1) {
    throw "recording delete failed for $recordingName"
  }

  $algorithm = Invoke-MBApi -Method "Post" -Path "/api/v1/algorithms" -Headers $authHeaders -Body @{
    name              = "smoke-algorithm-$timestamp"
    description       = "smoke e2e check"
    scene             = "security"
    category          = "smoke"
    mode              = "small"
    enabled           = $true
    small_model_label = "person"
  }
  $algorithmId = [string]$algorithm.id

  $taskResp = Invoke-MBApi -Method "Post" -Path "/api/v1/tasks" -Headers $authHeaders -Body @{
    name            = "smoke-task-$timestamp"
    notes           = "smoke e2e check"
    device_configs  = @(
      @{
        device_id              = $deviceId
        algorithm_configs      = @(
          @{
            algorithm_id        = $algorithmId
            alert_cycle_seconds = 60
          }
        )
        frame_rate_mode        = "fps"
        frame_rate_value       = 5
        small_confidence       = 0.5
        large_confidence       = 0.8
        small_iou              = 0.8
        recording_policy       = "none"
        recording_pre_seconds  = 8
        recording_post_seconds = 12
      }
    )
  }
  $taskId = [string]$taskResp.task.id
  if ([string]::IsNullOrWhiteSpace($taskId)) {
    throw "task id is empty"
  }

  $startResp = Invoke-MBApi -Method "Post" -Path "/api/v1/tasks/$taskId/start" -Headers $authHeaders
  $stopResp = Invoke-MBApi -Method "Post" -Path "/api/v1/tasks/$taskId/stop" -Headers $authHeaders

  $nowMs = [DateTimeOffset]::UtcNow.ToUnixTimeMilliseconds()
  $eventResp = Invoke-MBApi -Method "Post" -Path "/ai/events" -Headers $callbackHeaders -Body @{
    camera_id       = $deviceId
    timestamp       = $nowMs
    detect_mode     = 1
    detections      = @(
      @{
        label      = "person"
        confidence = 0.95
        box        = @{
          x_min = 16
          y_min = 20
          x_max = 320
          y_max = 520
        }
      }
    )
    llm_result      = ""
    snapshot        = ""
    snapshot_width  = 1920
    snapshot_height = 1080
  }
  $eventIDs = @($eventResp.created_event_ids)
  if ($eventIDs.Count -le 0) {
    throw "no event created from callback"
  }
  $eventId = [string]$eventIDs[0]

  $review = Invoke-MBApi -Method "Put" -Path "/api/v1/events/$eventId/review" -Headers $authHeaders -Body @{
    status      = "valid"
    review_note = "smoke-e2e"
  }

  Write-Host "Smoke e2e passed"
  Write-Host "Recording API check: ok"
  Write-Host "Task start status: $($startResp.status)"
  Write-Host "Task stop status: $($stopResp.status)"
  Write-Host "Event reviewed: $($review.id) -> $($review.status)"
}
finally {
  $cleanupHeaders = @{}
  try {
    if ([string]::IsNullOrWhiteSpace($token) -eq $false) {
      $cleanupHeaders = @{ Authorization = "Bearer $token" }
    }
  }
  catch {
    $cleanupHeaders = @{}
  }

  if ([string]::IsNullOrWhiteSpace($taskId) -eq $false -and $cleanupHeaders.Count -gt 0) {
    try { Invoke-MBApi -Method "Delete" -Path "/api/v1/tasks/$taskId" -Headers $cleanupHeaders | Out-Null } catch {}
  }
  if ([string]::IsNullOrWhiteSpace($algorithmId) -eq $false -and $cleanupHeaders.Count -gt 0) {
    try { Invoke-MBApi -Method "Delete" -Path "/api/v1/algorithms/$algorithmId" -Headers $cleanupHeaders | Out-Null } catch {}
  }
  if ([string]::IsNullOrWhiteSpace($deviceId) -eq $false -and $cleanupHeaders.Count -gt 0) {
    try { Invoke-MBApi -Method "Delete" -Path "/api/v1/devices/$deviceId" -Headers $cleanupHeaders | Out-Null } catch {}
  }
}
