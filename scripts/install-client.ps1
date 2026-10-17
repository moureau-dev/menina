# Menina client installer for Windows.
#
# Installs the cross-platform client binary and adds it to the user's PATH.
# It does not install the host control plane (that is Linux-only).
#
# Usage:
#   irm https://menina.moureau.dev/install-client.ps1 | iex
#
# Optional environment variables:
#   MENINA_CLIENT_VERSION       release tag (default: latest)
#   MENINA_CLIENT_DIR           install directory
#   MENINA_CLIENT_LOCAL_BINARY  install a local build
#   MENINA_REPO                 GitHub repository

param(
    [string]$Version = $env:MENINA_CLIENT_VERSION,
    [string]$InstallDir = $env:MENINA_CLIENT_DIR,
    [switch]$NoPathUpdate
)

$ErrorActionPreference = "Stop"
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

$Repo = if ($env:MENINA_REPO) { $env:MENINA_REPO } else { "moureau-dev/menina" }
if (-not $Version) { $Version = "latest" }
if (-not $InstallDir) { $InstallDir = Join-Path $env:LOCALAPPDATA "Menina\bin" }

function Info($Message) { Write-Host $Message }
function Die($Message) { Write-Error $Message; exit 1 }

switch ($env:PROCESSOR_ARCHITECTURE) {
    "AMD64" { $Arch = "amd64" }
    "ARM64" { $Arch = "arm64" }
    default { Die "unsupported architecture '$($env:PROCESSOR_ARCHITECTURE)'; supported: amd64, arm64" }
}

New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
$Target = Join-Path $InstallDir "menina-client.exe"

if ($env:MENINA_CLIENT_LOCAL_BINARY) {
    if (-not (Test-Path $env:MENINA_CLIENT_LOCAL_BINARY)) {
        Die "MENINA_CLIENT_LOCAL_BINARY=$($env:MENINA_CLIENT_LOCAL_BINARY) does not exist"
    }
    Info "installing local binary $($env:MENINA_CLIENT_LOCAL_BINARY) (no download, no checksum)"
    Copy-Item -Force $env:MENINA_CLIENT_LOCAL_BINARY $Target
    $InstalledVersion = "local"
}
else {
    if ($Version -eq "latest") {
        $Release = Invoke-RestMethod -UseBasicParsing "https://api.github.com/repos/$Repo/releases/latest"
        $Version = $Release.tag_name
    }
    if (-not $Version) { Die "could not determine the latest release" }

    $Asset = "menina-client-windows-$Arch.exe"
    $Base = "https://github.com/$Repo/releases/download/$Version"
    $Tmp = Join-Path ([IO.Path]::GetTempPath()) ("menina-client-" + [guid]::NewGuid().ToString("N"))
    New-Item -ItemType Directory -Force -Path $Tmp | Out-Null

    try {
        $AssetPath = Join-Path $Tmp $Asset
        $ChecksumsPath = Join-Path $Tmp "checksums.txt"

        Info "downloading $Asset $Version"
        Invoke-WebRequest -UseBasicParsing -Uri "$Base/$Asset" -OutFile $AssetPath
        Invoke-WebRequest -UseBasicParsing -Uri "$Base/checksums.txt" -OutFile $ChecksumsPath

        $Line = Get-Content $ChecksumsPath | Where-Object { $_ -match "\s$([regex]::Escape($Asset))$" } | Select-Object -First 1
        if (-not $Line) { Die "checksum for $Asset not found in checksums.txt" }
        $Expected = ($Line -split '\s+')[0].ToLowerInvariant()
        $Actual = (Get-FileHash -Algorithm SHA256 -Path $AssetPath).Hash.ToLowerInvariant()
        if ($Expected -ne $Actual) { Die "checksum mismatch for $Asset" }

        Info "checksum verified"
        Copy-Item -Force $AssetPath $Target
        $InstalledVersion = $Version
    }
    finally {
        Remove-Item -Recurse -Force $Tmp -ErrorAction SilentlyContinue
    }
}

if (-not $NoPathUpdate -and $env:MENINA_CLIENT_NO_MODIFY_PATH -ne "1") {
    $UserPath = [Environment]::GetEnvironmentVariable("Path", "User")
    $Parts = @()
    if ($UserPath) { $Parts = $UserPath -split ';' | Where-Object { $_ } }
    if ($Parts -notcontains $InstallDir) {
        $NewPath = (@($Parts) + $InstallDir) -join ';'
        [Environment]::SetEnvironmentVariable("Path", $NewPath, "User")
        Info "added $InstallDir to the user PATH; restart your shell"
    }
    if (($env:Path -split ';') -notcontains $InstallDir) {
        $env:Path = $env:Path + ";" + $InstallDir
    }
}
else {
    Info "PATH not modified. Add this directory manually: $InstallDir"
}

Info ""
Info "Menina client $InstalledVersion installed to $Target"
Info "Next:"
Info "  menina-client init          # scaffold .menina.yml"
Info "  menina-client validate      # check it"
Info "  menina-client apply --host user@vps.example.com"
Info "  menina-client build --push  # build and push images"
