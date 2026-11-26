<#
.SYNOPSIS
  Checks the test install's .env against the live install's .env before the test app is first
  started (docs/TEST_INSTALL.md). Prints setting names only, never a value.

.DESCRIPTION
  Stops (exit code 1) when the test install could disturb the live system or would be refused
  by the app:
    - the Telegram bot token is the live one, or belongs to the same bot;
    - any Twilio (WhatsApp) setting is present;
    - a setting the app now refuses is still there;
    - a setting would point the test install at the live project, a live tunnel or an outside
      database, queue or cache;
    - the private backup key is still in the test install's backups folder.
  Exit code 0 means the checks passed; lines marked [note] are worth reading but do not stop.

.EXAMPLE
  powershell -ExecutionPolicy Bypass -File D:\MyPA-test\deploy\test-install\check-test-env.ps1 -LiveDir D:\MyPA -TestDir D:\MyPA-test
#>
param(
    [Parameter(Mandatory = $true)][string]$LiveDir,
    [Parameter(Mandatory = $true)][string]$TestDir
)

$ErrorActionPreference = 'Stop'
$stops = New-Object System.Collections.Generic.List[string]
$notes = New-Object System.Collections.Generic.List[string]

# Reads a .env file the way docker compose does for simple KEY=VALUE lines: blank lines and
# comments are skipped, "export " is allowed, one pair of surrounding quotes is removed, and an
# unquoted value ends at " #". Keys are compared without regard to case.
function Read-EnvFile([string]$Path) {
    $map = @{}
    foreach ($line in [System.IO.File]::ReadAllLines($Path)) {
        $t = $line.Trim()
        if ($t -eq '' -or $t.StartsWith('#')) { continue }
        if ($t.StartsWith('export ')) { $t = $t.Substring(7).TrimStart() }
        $i = $t.IndexOf('=')
        if ($i -lt 1) { continue }
        $key = $t.Substring(0, $i).Trim()
        $val = $t.Substring($i + 1).Trim()
        $quoted = $false
        if ($val.Length -ge 2) {
            $first = $val.Substring(0, 1)
            $last = $val.Substring($val.Length - 1, 1)
            if (($first -eq '"' -and $last -eq '"') -or ($first -eq "'" -and $last -eq "'")) {
                $val = $val.Substring(1, $val.Length - 2)
                $quoted = $true
            }
        }
        if (-not $quoted) {
            $c = $val.IndexOf(' #')
            if ($c -ge 0) { $val = $val.Substring(0, $c).TrimEnd() }
        }
        $map[$key] = $val
    }
    return $map
}

function Test-Set($envMap, [string]$key) {
    return $envMap.ContainsKey($key) -and $envMap[$key].Trim() -ne ''
}

function Test-Placeholder([string]$v) {
    return $v.Trim().ToLower().StartsWith('your-') -or $v.Contains('<YOUR_')
}

# ------------------------------------------------------------------ folders and files
$live = [System.IO.Path]::GetFullPath($LiveDir).TrimEnd('\')
$test = [System.IO.Path]::GetFullPath($TestDir).TrimEnd('\')
if ($live -ieq $test) {
    Write-Host "[STOP] -LiveDir and -TestDir are the same folder. The test install needs its own clone." -ForegroundColor Red
    exit 1
}
$liveEnvPath = Join-Path $live '.env'
$testEnvPath = Join-Path $test '.env'
foreach ($p in @($liveEnvPath, $testEnvPath)) {
    if (-not (Test-Path -LiteralPath $p -PathType Leaf)) {
        Write-Host "[STOP] $p does not exist." -ForegroundColor Red
        exit 1
    }
}

$override = Join-Path $test 'docker-compose.override.yml'
if (-not (Test-Path -LiteralPath $override -PathType Leaf)) {
    $stops.Add("docker-compose.override.yml is missing in $test. Copy deploy\test-install\docker-compose.test.yml to it (step 2).")
} elseif (-not (Select-String -LiteralPath $override -Pattern '^name:\s*mypa-test\s*$' -Quiet)) {
    $stops.Add("docker-compose.override.yml in $test does not set 'name: mypa-test'. Copy deploy\test-install\docker-compose.test.yml to it again.")
}
if (Test-Path -LiteralPath (Join-Path $live 'docker-compose.override.yml')) {
    $notes.Add("$live has a docker-compose.override.yml. If it is a copy of the test override, remove it from the live folder at once.")
}

$keyFile = Join-Path $test 'backups\backup-key.txt'
if (Test-Path -LiteralPath $keyFile) {
    $stops.Add("backups\backup-key.txt (the private backup key) is still in the test install. Delete it (step 7).")
}

$liveEnv = Read-EnvFile $liveEnvPath
$testEnv = Read-EnvFile $testEnvPath

# ------------------------------------------------------------------ Telegram
if (-not (Test-Set $testEnv 'TELEGRAM_BOT_TOKEN')) {
    $stops.Add("TELEGRAM_BOT_TOKEN is empty. Set the token of the test bot from BotFather (step 4).")
} elseif (Test-Placeholder $testEnv['TELEGRAM_BOT_TOKEN']) {
    $stops.Add("TELEGRAM_BOT_TOKEN still holds the .env.example placeholder. Set the test bot's token (step 4).")
} elseif (Test-Set $liveEnv 'TELEGRAM_BOT_TOKEN') {
    $t = $testEnv['TELEGRAM_BOT_TOKEN'].Trim()
    $l = $liveEnv['TELEGRAM_BOT_TOKEN'].Trim()
    if ($t -ceq $l) {
        $stops.Add("TELEGRAM_BOT_TOKEN is the LIVE bot's token. Starting the test app with it would take the live bot away from the family. Create a test bot with BotFather and use its token (step 4).")
    } elseif ($t.Split(':')[0] -eq $l.Split(':')[0]) {
        $stops.Add("TELEGRAM_BOT_TOKEN is a different token for the SAME bot as live (same bot ID before the colon). Create a separate test bot with BotFather /newbot (step 4).")
    }
}
if ($testEnv.ContainsKey('TELEGRAM_SECRET_TOKEN')) {
    $notes.Add("TELEGRAM_SECRET_TOKEN is present. A polling install does not use it; it is the live webhook's secret and can be removed.")
}

# ------------------------------------------------------------------ WhatsApp
$twilio = @($testEnv.Keys | Where-Object { $_.ToUpper().StartsWith('TWILIO_') } | Sort-Object)
if ($twilio.Count -gt 0) {
    $stops.Add("Twilio (WhatsApp) settings are present: $($twilio -join ', '). Remove every TWILIO_ line: the test install runs without WhatsApp (step 5).")
}

# ------------------------------------------------------------------ settings the app refuses
# config.RetiredEnvKeys (V4-35) and the settings PUBLIC_URL replaced (V4-40, config.replacedEnvKeys).
$refused = @('DASHBOARD_API_SECRET', 'DASHBOARD_FAMILY_GROUP', 'JWT_SECRET',
    'TELEGRAM_WEBHOOK_URL', 'TWILIO_STATUS_CALLBACK_URL', 'GOOGLE_REDIRECT_URL', 'NGROK_DOMAIN')
$present = @($refused | Where-Object { $testEnv.ContainsKey($_) })
if ($present.Count -gt 0) {
    $stops.Add("Settings the app now refuses are still there: $($present -join ', '). Remove these lines (step 5).")
}

# INGRESS=none: no public address, no bundled tunnel (deploy.Settings.Validate).
if ((Test-Set $testEnv 'INGRESS') -and $testEnv['INGRESS'].Trim().ToLower() -ne 'none') {
    $stops.Add("INGRESS is not none. Set INGRESS=none (step 5).")
}
if (Test-Set $testEnv 'PUBLIC_URL') {
    $stops.Add("PUBLIC_URL is set. With INGRESS=none the app refuses it; leave it empty (step 5).")
}
if (Test-Set $testEnv 'COMPOSE_PROFILES') {
    $other = @($testEnv['COMPOSE_PROFILES'].Split(',') | ForEach-Object { $_.Trim() } | Where-Object { $_ -ne '' -and $_ -ne 'tools' })
    if ($other.Count -gt 0) {
        $stops.Add("COMPOSE_PROFILES starts more than 'tools'. Leave it empty: the test install runs no tunnel or proxy (step 5).")
    }
}

# Required keys of the app that a copied file could still hold as placeholders.
foreach ($k in @('ENCRYPTION_KEY', 'GEMINI_API_KEY')) {
    if (-not (Test-Set $testEnv $k)) {
        $stops.Add("$k is empty. The app cannot start without it.")
    } elseif (Test-Placeholder $testEnv[$k]) {
        $stops.Add("$k still holds the .env.example placeholder.")
    }
}

# ------------------------------------------------------------------ the live system's own things
foreach ($k in @('COMPOSE_PROJECT_NAME', 'COMPOSE_FILE')) {
    if ($testEnv.ContainsKey($k)) {
        $stops.Add("$k is present. It could make compose use another project or other files than the test install's; remove the line (step 5).")
    }
}
$tunnels = @(@('NGROK_AUTHTOKEN', 'CLOUDFLARE_TUNNEL_TOKEN', 'TAILSCALE_AUTHKEY') | Where-Object { Test-Set $testEnv $_ })
if ($tunnels.Count -gt 0) {
    $stops.Add("Tunnel credentials are set: $($tunnels -join ', '). The test install runs no tunnel, and the live tunnel's credentials stay with live; remove these lines (step 5).")
}
$external = @(@('EXTERNAL_DATABASE_URL', 'EXTERNAL_REDIS_URL', 'EXTERNAL_RABBITMQ_URL') | Where-Object { Test-Set $testEnv $_ })
if ($external.Count -gt 0) {
    $stops.Add("Outside services are set: $($external -join ', '). The test install must use its own bundled containers; leave these empty.")
}

# ------------------------------------------------------------------ keys that must match or differ
if ((Test-Set $testEnv 'ENCRYPTION_KEY') -and (Test-Set $liveEnv 'ENCRYPTION_KEY') -and
    ($testEnv['ENCRYPTION_KEY'].Trim() -cne $liveEnv['ENCRYPTION_KEY'].Trim())) {
    $stops.Add("ENCRYPTION_KEY differs from live. The restored secrets only open with the live key; copy it unchanged.")
}
if (Test-Set $testEnv 'LINK_CODE_SECRET') {
    $s = $testEnv['LINK_CODE_SECRET'].Trim()
    if (Test-Placeholder $s) {
        $stops.Add("LINK_CODE_SECRET still holds a placeholder.")
    } elseif ($s.Length -lt 32) {
        $stops.Add("LINK_CODE_SECRET is shorter than 32 characters; the app refuses it (step 5 shows how to make one).")
    } elseif ((Test-Set $testEnv 'ENCRYPTION_KEY') -and $s -ceq $testEnv['ENCRYPTION_KEY'].Trim()) {
        $stops.Add("LINK_CODE_SECRET is the same as ENCRYPTION_KEY; the app refuses that.")
    }
} else {
    $notes.Add("LINK_CODE_SECRET is not set: /link will be refused. Add one to test /link (step 5).")
}

# ------------------------------------------------------------------ harmless leftovers
foreach ($k in @('GATEWAY_URL', 'ORCHESTRATOR_URL')) {
    if ($testEnv.ContainsKey($k)) { $notes.Add("$k is no longer read since V4-01 and can be removed.") }
}
if (Test-Set $testEnv 'BACKUP_AGE_IDENTITY_FILE') {
    $notes.Add("BACKUP_AGE_IDENTITY_FILE is set. It is only needed during a restore, passed with -e; leave it empty in .env.")
}
if (Test-Set $testEnv 'GEMINI_MODEL') {
    $notes.Add("GEMINI_MODEL is set. Remove it to test this release's default chat model.")
}

# ------------------------------------------------------------------ result
foreach ($n in $notes) { Write-Host "[note] $n" -ForegroundColor Yellow }
foreach ($s in $stops) { Write-Host "[STOP] $s" -ForegroundColor Red }
if ($stops.Count -gt 0) {
    Write-Host ""
    Write-Host "Do not start the test install. Fix the lines marked [STOP] in $testEnvPath and run this check again." -ForegroundColor Red
    exit 1
}
Write-Host ""
Write-Host "OK: the test .env passed every check. Continue with the next step of docs/TEST_INSTALL.md." -ForegroundColor Green
exit 0
