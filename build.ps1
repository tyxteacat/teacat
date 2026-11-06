param(
    [switch]$All,
    [string]$Out = "dist"
)

$ErrorActionPreference = "Stop"
$env:CGO_ENABLED = "0"

if (-not $All) {
    Write-Host "building TeaCat for the local platform..."
    go build -trimpath -ldflags "-s -w" -o teacat.exe ./cmd/teacat
    Write-Host "built teacat.exe"
    exit 0
}

$targets = @(
    @{ GOOS = "windows"; GOARCH = "amd64"; Ext = ".exe" },
    @{ GOOS = "windows"; GOARCH = "arm64"; Ext = ".exe" },
    @{ GOOS = "linux";   GOARCH = "amd64"; Ext = "" },
    @{ GOOS = "linux";   GOARCH = "arm64"; Ext = "" },
    @{ GOOS = "darwin";  GOARCH = "amd64"; Ext = "" },
    @{ GOOS = "darwin";  GOARCH = "arm64"; Ext = "" }
)

New-Item -ItemType Directory -Force -Path $Out | Out-Null
$built = @()

foreach ($t in $targets) {
    $name = "teacat-$($t.GOOS)-$($t.GOARCH)$($t.Ext)"
    $path = Join-Path $Out $name
    $env:GOOS = $t.GOOS
    $env:GOARCH = $t.GOARCH
    Write-Host "building $name ..."
    go build -trimpath -ldflags "-s -w" -o $path ./cmd/teacat
    $built += $path
}

Remove-Item Env:GOOS -ErrorAction SilentlyContinue
Remove-Item Env:GOARCH -ErrorAction SilentlyContinue

$checksums = Join-Path $Out "checksums.txt"
$built | ForEach-Object {
    $hash = (Get-FileHash -Algorithm SHA256 $_).Hash.ToLower()
    "$hash  $(Split-Path $_ -Leaf)"
} | Set-Content -Encoding ascii $checksums

Write-Host "built $($built.Count) binaries in $Out/ (see $checksums)"
