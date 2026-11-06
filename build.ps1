$ErrorActionPreference = "Stop"
Write-Host "building TeaCat..."
go build -trimpath -ldflags "-s -w" -o teacat.exe ./cmd/teacat
Write-Host "built teacat.exe"
