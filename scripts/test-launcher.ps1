# Tests for plugin/scripts/launch.cmd + launch.ps1 on Windows (run in CI on
# windows-latest). Uses a file:// download base and a copy of a system .exe as
# the fake release asset.
$ErrorActionPreference = 'Stop'
$repo = Split-Path -Parent $PSScriptRoot
$version = '9.8.7'
$asset = 'whatsapp-unofficial_windows_amd64.exe'
$work = Join-Path ([IO.Path]::GetTempPath()) ("wau-test-" + [Guid]::NewGuid().ToString('N'))
$script:failures = 0

function Check([string]$Name, [bool]$Condition) {
    if ($Condition) { Write-Host "ok   - $Name" } else { Write-Host "FAIL - $Name"; $script:failures++ }
}

function New-Plugin([string]$Name, [string]$Checksum) {
    $root = Join-Path $work "$Name\plugin"
    New-Item -ItemType Directory -Force -Path (Join-Path $root 'scripts') | Out-Null
    Copy-Item (Join-Path $repo 'plugin\scripts\launch.cmd') (Join-Path $root 'scripts')
    Copy-Item (Join-Path $repo 'plugin\scripts\launch.ps1') (Join-Path $root 'scripts')
    Set-Content -NoNewline -Path (Join-Path $root 'VERSION') -Value $version
    Set-Content -Path (Join-Path $root 'checksums.txt') -Value "$Checksum  $asset"
    return $root
}

function Invoke-Launcher([string]$Root, [string]$Data, [string[]]$LauncherArgs) {
    $env:CLAUDE_PLUGIN_DATA = $Data
    $env:WHATSAPP_UNOFFICIAL_DOWNLOAD_BASE = "file:///" + ($releases -replace '\\', '/')
    $errFile = Join-Path $work ([Guid]::NewGuid().ToString('N') + '.stderr')
    $out = & cmd.exe /d /c (Join-Path $Root 'scripts\launch.cmd') @LauncherArgs 2>$errFile
    $code = $LASTEXITCODE
    # Show launcher diagnostics so CI failures are debuggable.
    if (Test-Path $errFile) { Get-Content $errFile | ForEach-Object { Write-Host "   # $_" } }
    return @{ Code = $code; Out = ($out -join "`n") }
}

try {
    $releases = Join-Path $work 'releases'
    New-Item -ItemType Directory -Force -Path $releases | Out-Null
    # where.exe prints the path of a named program: a deterministic fake binary.
    Copy-Item "$env:SystemRoot\System32\where.exe" (Join-Path $releases $asset)
    $good = (Get-FileHash -Algorithm SHA256 (Join-Path $releases $asset)).Hash.ToLowerInvariant()

    $root = New-Plugin 'success' $good
    $data = Join-Path $work 'success\data'
    $r = Invoke-Launcher $root $data @('cmd.exe')
    Check 'success: exit 0' ($r.Code -eq 0)
    Check 'success: stdout is the binary output only' ($r.Out -match '^[A-Za-z]:\\.*cmd\.exe' -and $r.Out -notmatch 'launcher')
    Check 'success: binary cached' (Test-Path (Join-Path $data "bin\$version\$asset"))

    Remove-Item (Join-Path $releases $asset) -Force
    $r = Invoke-Launcher $root $data @('cmd.exe')
    Check 'cache: works without download source' ($r.Code -eq 0)
    Copy-Item "$env:SystemRoot\System32\where.exe" (Join-Path $releases $asset)

    $root = New-Plugin 'mismatch' ('0' * 64)
    $data = Join-Path $work 'mismatch\data'
    $r = Invoke-Launcher $root $data @('cmd.exe')
    Check 'mismatch: non-zero exit' ($r.Code -ne 0)
    Check 'mismatch: stdout empty' ([string]::IsNullOrEmpty($r.Out))
    Check 'mismatch: nothing cached' (-not (Test-Path (Join-Path $data "bin\$version\$asset")))
    Check 'mismatch: no leftover files' ((Get-ChildItem -Recurse -File $data -ErrorAction SilentlyContinue | Measure-Object).Count -eq 0)

    $root = New-Plugin 'override' $good
    $env:WHATSAPP_UNOFFICIAL_BIN = "$env:SystemRoot\System32\where.exe"
    $r = Invoke-Launcher $root (Join-Path $work 'override\data') @('cmd.exe')
    Remove-Item Env:\WHATSAPP_UNOFFICIAL_BIN
    Check 'override: runs given binary' ($r.Code -eq 0 -and $r.Out -match 'cmd\.exe')
    Check 'override: no cache created' (-not (Test-Path (Join-Path $work 'override\data')))

    $root = New-Plugin 'insecure' $good
    $env:CLAUDE_PLUGIN_DATA = Join-Path $work 'insecure\data'
    $env:WHATSAPP_UNOFFICIAL_DOWNLOAD_BASE = 'http://example.com'
    $out = & cmd.exe /d /c (Join-Path $root 'scripts\launch.cmd') 'cmd.exe' 2>$null
    Check 'insecure base: refused' ($LASTEXITCODE -ne 0 -and -not $out)
} finally {
    Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $work
}

if ($script:failures -gt 0) { Write-Host "$script:failures test(s) failed"; exit 1 }
Write-Host 'windows launcher tests passed'
