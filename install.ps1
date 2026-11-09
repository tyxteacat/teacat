#Requires -Version 5.1
<#
.SYNOPSIS
    Download and install TeaCat on Windows.

.DESCRIPTION
    Fetches a prebuilt teacat.exe from the official GitHub releases,
    verifies its SHA-256 checksum, installs it, and adds it to the user PATH.

.EXAMPLE
    irm https://raw.githubusercontent.com/tyxteacat/teacat/main/install.ps1 | iex

.EXAMPLE
    .\install.ps1 -Version v0.3.1 -InstallDir "$env:USERPROFILE\bin"

.EXAMPLE
    .\install.ps1 -NoPath -Force
#>
[CmdletBinding()]
param(
    [string]$Version = "latest",
    [string]$InstallDir = (Join-Path $env:LOCALAPPDATA "TeaCat\bin"),
    [switch]$NoPath,
    [switch]$Force
)

$ErrorActionPreference = "Stop"
$repo = "tyxteacat/teacat"
$headers = @{ "User-Agent" = "TeaCat-Installer" }

try {
    [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
} catch {
    # Older .NET runtimes may not expose Tls12; GitHub still negotiates TLS.
}

function Write-Step($message) { Write-Host "==> $message" }

$arch = switch -Regex ($env:PROCESSOR_ARCHITECTURE) {
    "ARM64" { "arm64" }
    default { "amd64" }
}
$asset = "teacat-windows-$arch.exe"

Write-Step "TeaCat installer for Windows ($arch)"

if ($Version -eq "latest") {
    try {
        $release = Invoke-RestMethod -Uri "https://api.github.com/repos/$repo/releases/latest" -Headers $headers
    } catch {
        throw "Could not query GitHub releases: $($_.Exception.Message)"
    }
    $Version = $release.tag_name
    $downloadUrl = ($release.assets | Where-Object { $_.name -eq $asset } | Select-Object -First 1).browser_download_url
    $checksumsUrl = ($release.assets | Where-Object { $_.name -eq "checksums.txt" } | Select-Object -First 1).browser_download_url
} else {
    $downloadUrl = "https://github.com/$repo/releases/download/$Version/$asset"
    $checksumsUrl = "https://github.com/$repo/releases/download/$Version/checksums.txt"
}

if (-not $downloadUrl) {
    throw "Release $Version does not contain $asset."
}

New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
$exePath = Join-Path $InstallDir "teacat.exe"
$tempExe = Join-Path ([IO.Path]::GetTempPath()) ("teacat-" + [Guid]::NewGuid().ToString("N") + ".exe")
$tempSums = Join-Path ([IO.Path]::GetTempPath()) ("teacat-checksums-" + [Guid]::NewGuid().ToString("N") + ".txt")

try {
    Write-Step "Downloading $asset ($Version)"
    Invoke-WebRequest -Uri $downloadUrl -OutFile $tempExe -Headers $headers -UseBasicParsing

    Write-Step "Verifying SHA-256 checksum"
    Invoke-WebRequest -Uri $checksumsUrl -OutFile $tempSums -Headers $headers -UseBasicParsing
    $line = Get-Content $tempSums | Where-Object { $_ -match "\s+$([regex]::Escape($asset))\s*$" } | Select-Object -First 1
    if (-not $line) {
        throw "checksums.txt does not list $asset."
    }
    $expected = ($line -split "\s+")[0].ToLowerInvariant()
    $actual = (Get-FileHash -Algorithm SHA256 -Path $tempExe).Hash.ToLowerInvariant()
    if ($expected -ne $actual) {
        throw "Checksum mismatch for ${asset}: expected $expected, got $actual."
    }
    Write-Host "    checksum ok"

    if ((Test-Path $exePath) -and -not $Force) {
        $answer = Read-Host "teacat.exe already exists at $exePath. Overwrite? [y/N]"
        if ($answer -notmatch "^(y|yes)$") {
            Write-Step "Install cancelled"
            return
        }
    }

    Move-Item -Force -Path $tempExe -Destination $exePath

    if (-not $NoPath) {
        $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
        $parts = @()
        if ($userPath) { $parts = $userPath -split ";" | Where-Object { $_ } }
        if ($parts -notcontains $InstallDir) {
            $newPath = (@($parts) + $InstallDir) -join ";"
            [Environment]::SetEnvironmentVariable("Path", $newPath, "User")
            Write-Step "Added $InstallDir to your user PATH (open a new terminal to use it)"
        }
        if (($env:Path -split ";") -notcontains $InstallDir) {
            $env:Path = "$env:Path;$InstallDir"
        }
    }

    Write-Step "Installed TeaCat $Version to $exePath"
    & $exePath version
    Write-Host ""
    Write-Host "Try it:"
    Write-Host "  teacat pull qwen2.5:0.5b"
    Write-Host "  teacat run qwen2.5:0.5b `"Explain goroutines in two sentences`""
    Write-Host "  teacat ui            # opens the dashboard in your browser"
    Write-Host "  teacat serve         # API + dashboard on http://127.0.0.1:11434"
} finally {
    Remove-Item -Force -ErrorAction SilentlyContinue $tempExe, $tempSums
}
