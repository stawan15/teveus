# teveus installer for Windows
#
#   irm https://raw.githubusercontent.com/stawan15/teveus/main/install.ps1 | iex
#
# Options (environment variables):
#   TEVEUS_INSTALL_DIR   where to put teveus.exe          (default: %LOCALAPPDATA%\Programs\teveus)
#   TEVEUS_VERSION       a release tag, e.g. v0.10.0       (default: latest)
#   TEVEUS_BASE_URL      download location override       (for testing mirrors)

param(
    [string]$InstallDir = $env:TEVEUS_INSTALL_DIR,
    [string]$Version = $env:TEVEUS_VERSION,
    [string]$BaseUrl = $env:TEVEUS_BASE_URL
)

$ErrorActionPreference = "Stop"
$Repo = "stawan15/teveus"

if (-not $InstallDir) {
    $InstallDir = Join-Path $env:LOCALAPPDATA "Programs\teveus"
}
if (-not $Version) {
    $Version = "latest"
}

if ($PSVersionTable.PSVersion.Major -lt 6) {
    [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
}

$arch = $env:PROCESSOR_ARCHITECTURE
if ($arch -eq "x86") {
    throw "teveus needs 64-bit Windows."
}
$archive = "teveus_windows_amd64.zip"

if ($BaseUrl) {
    $Base = $BaseUrl
} elseif ($Version -eq "latest") {
    $Base = "https://github.com/$Repo/releases/latest/download"
} else {
    $Base = "https://github.com/$Repo/releases/download/$Version"
}

function Step($Message) {
    Write-Host "> $Message"
}

Write-Host ""
Write-Host "✦ teveus installer"
Step "downloading $archive ($Version)"

$tmp = Join-Path ([IO.Path]::GetTempPath()) ("teveus-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
    $zip = Join-Path $tmp $archive
    $checksums = Join-Path $tmp "checksums.txt"
    Invoke-WebRequest -Uri "$Base/$archive" -OutFile $zip -UseBasicParsing
    Invoke-WebRequest -Uri "$Base/checksums.txt" -OutFile $checksums -UseBasicParsing

    Step "verifying checksum"
    $expected = $null
    foreach ($line in Get-Content -LiteralPath $checksums) {
        $parts = $line -split "\s+", 2
        if ($parts.Count -eq 2 -and $parts[1].Trim() -eq $archive) {
            $expected = $parts[0].ToLowerInvariant()
        }
    }
    if (-not $expected) {
        throw "$archive is not listed in checksums.txt"
    }
    $actual = (Get-FileHash -LiteralPath $zip -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($actual -ne $expected) {
        throw "checksum mismatch for $archive (expected $expected, got $actual)"
    }

    Expand-Archive -LiteralPath $zip -DestinationPath $tmp -Force
    $source = Join-Path $tmp "teveus.exe"
    if (-not (Test-Path -LiteralPath $source)) {
        throw "teveus.exe is missing from $archive"
    }

    New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
    $target = Join-Path $InstallDir "teveus.exe"
    if (Test-Path -LiteralPath $target) {
        # Windows won't overwrite a running program, but it can be renamed.
        # The old copy is cleaned up by the next install after it exits.
        $old = Join-Path $InstallDir "teveus.exe.old"
        Remove-Item -LiteralPath $old -Force -ErrorAction SilentlyContinue
        try {
            Move-Item -LiteralPath $target -Destination $old -Force
        } catch {
            throw "can't replace $target while teveus is running; quit teveus and run this again"
        }
        Copy-Item -LiteralPath $source -Destination $target -Force
        Remove-Item -LiteralPath $old -Force -ErrorAction SilentlyContinue
    } else {
        Copy-Item -LiteralPath $source -Destination $target -Force
    }
    Step "installed teveus to $target"

    $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
    $onPath = $false
    foreach ($dir in ($userPath -split ";")) {
        if ($dir.TrimEnd("\") -ieq $InstallDir.TrimEnd("\")) {
            $onPath = $true
        }
    }
    if (-not $onPath) {
        $newPath = if ($userPath) { "$userPath;$InstallDir" } else { $InstallDir }
        [Environment]::SetEnvironmentVariable("Path", $newPath, "User")
        Write-Host ""
        Write-Host "Added $InstallDir to your PATH. Open a new terminal to use it."
    }

    Write-Host ""
    Write-Host "Done. Run teveus inside any project folder." -ForegroundColor Green
    $installed = & $target -version 2>$null
    if ($installed) {
        Write-Host $installed
    }
} finally {
    Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue
}
