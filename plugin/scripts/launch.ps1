# Resolves, downloads and verifies the whatsapp-unofficial binary on Windows.
# Called by launch.cmd. Writes ONLY the verified binary path to stdout;
# every diagnostic goes to stderr. Never returns an unverified file.
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

# GitHub repository that publishes the release assets.
$RepoSlug = 'Beg1nner4ever/whatsapp-unofficial'
$Name = 'whatsapp-unofficial'

function Fail([string]$Message) {
    [Console]::Error.WriteLine("$Name launcher: error: $Message")
    exit 1
}

function Note([string]$Message) {
    [Console]::Error.WriteLine("$Name launcher: $Message")
}

# .NET hashing instead of Get-FileHash: the Utility module may fail to load
# when Windows PowerShell inherits a PowerShell 7 PSModulePath.
function Get-Sha256([string]$Path) {
    $sha = [Security.Cryptography.SHA256]::Create()
    $stream = [IO.File]::OpenRead($Path)
    try {
        return ([BitConverter]::ToString($sha.ComputeHash($stream)) -replace '-', '').ToLowerInvariant()
    } finally {
        $stream.Dispose()
        $sha.Dispose()
    }
}

$tmp = $null

try {
    $pluginRoot = Split-Path -Parent $PSScriptRoot

    $versionFile = Join-Path $pluginRoot 'VERSION'
    if (-not (Test-Path -LiteralPath $versionFile)) { Fail "missing $versionFile" }
    $version = (Get-Content -Raw -LiteralPath $versionFile).Trim()
    if ($version -notmatch '^[0-9A-Za-z.+-]+$') { Fail "invalid version '$version' in $versionFile" }

    $arch = $env:PROCESSOR_ARCHITEW6432
    if (-not $arch) { $arch = $env:PROCESSOR_ARCHITECTURE }
    switch ($arch) {
        'AMD64' { $goArch = 'amd64' }
        # Windows on ARM runs x64 binaries through emulation.
        'ARM64' { $goArch = 'amd64' }
        default { Fail "unsupported platform: windows/$arch. Supported: Windows x86_64 (and ARM64 via emulation). Build from source and set WHATSAPP_UNOFFICIAL_BIN." }
    }
    $asset = "${Name}_windows_$goArch.exe"

    $checksums = Join-Path $pluginRoot 'checksums.txt'
    if (-not (Test-Path -LiteralPath $checksums)) { Fail "missing $checksums" }
    $expected = $null
    foreach ($line in Get-Content -LiteralPath $checksums) {
        $parts = $line.Trim() -split '\s+'
        if ($parts.Count -ge 2 -and ($parts[1] -eq $asset -or $parts[1] -eq "*$asset")) {
            $expected = $parts[0].ToLowerInvariant()
            break
        }
    }
    if (-not $expected) { Fail "no checksum for $asset in $checksums; this plugin build is incomplete" }
    if ($expected -notmatch '^[0-9a-f]{64}$') { Fail "malformed checksum for $asset in $checksums" }

    if ($env:CLAUDE_PLUGIN_DATA) { $dataRoot = $env:CLAUDE_PLUGIN_DATA }
    elseif ($env:PLUGIN_DATA) { $dataRoot = $env:PLUGIN_DATA }
    elseif ($env:LOCALAPPDATA) { $dataRoot = Join-Path $env:LOCALAPPDATA $Name }
    else { $dataRoot = Join-Path $HOME ".cache\$Name" }
    $cacheDir = Join-Path (Join-Path $dataRoot 'bin') $version
    $target = Join-Path $cacheDir $asset

    if (Test-Path -LiteralPath $target) {
        if ((Get-Sha256 $target) -eq $expected) {
            Write-Output $target
            exit 0
        }
        Note 'cached binary failed verification; downloading again'
        Remove-Item -Force -LiteralPath $target
    }

    $base = $env:WHATSAPP_UNOFFICIAL_DOWNLOAD_BASE
    if (-not $base) { $base = "https://github.com/$RepoSlug/releases/download/v$version" }
    $base = $base.TrimEnd('/')
    if (-not ($base.StartsWith('https://') -or $base.StartsWith('file://'))) {
        Fail "refusing download base '$base': only https:// URLs are allowed"
    }
    $url = "$base/$asset"

    New-Item -ItemType Directory -Force -Path $cacheDir | Out-Null
    # Unique temp name without .exe so it cannot be run before verification;
    # concurrent first runs each use their own temp file.
    $tmp = Join-Path $cacheDir (".$asset." + [Guid]::NewGuid().ToString('N') + '.download')

    Note "downloading $asset v$version from $url"
    try {
        if ($url.StartsWith('file://')) {
            Copy-Item -LiteralPath ([Uri]$url).LocalPath -Destination $tmp
        } else {
            [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
            Invoke-WebRequest -UseBasicParsing -Uri $url -OutFile $tmp
        }
    } catch {
        Remove-Item -Force -ErrorAction SilentlyContinue -LiteralPath $tmp
        Fail "download failed: $url ($($_.Exception.Message))"
    }

    $actual = Get-Sha256 $tmp
    if ($actual -ne $expected) {
        Remove-Item -Force -ErrorAction SilentlyContinue -LiteralPath $tmp
        Fail "checksum mismatch for $asset (expected $expected, got $actual); refusing to run it"
    }

    try {
        [IO.File]::Move($tmp, $target)
    } catch {
        # Another session installed it first; keep theirs only if it verifies.
        Remove-Item -Force -ErrorAction SilentlyContinue -LiteralPath $tmp
        if (-not ((Test-Path -LiteralPath $target) -and ((Get-Sha256 $target) -eq $expected))) {
            Fail "cannot move binary into ${target}: $($_.Exception.Message)"
        }
    }
    Write-Output $target
    exit 0
} catch {
    # Never leave an unverified download behind.
    if ($tmp) { Remove-Item -Force -ErrorAction SilentlyContinue -LiteralPath $tmp }
    Fail $_.Exception.Message
}
