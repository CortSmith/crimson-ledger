# Runs on the Windows executor only. Builds the Windows binaries natively into dist\windows-amd64 (CGO_ENABLED=0,
# the same targets and flags as the windows block of build-all.sh). Needs Go on PATH. Usage: build-windows.ps1 <version>
param([Parameter(Mandatory = $true)][string]$Version)
$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $true
Set-Location (Split-Path -Parent $PSScriptRoot)
$ld = "-s -w -X main.version=$Version"
$out = 'dist\windows-amd64'
Remove-Item -Recurse -Force $out -ErrorAction SilentlyContinue
New-Item -ItemType Directory "$out\lib" | Out-Null
$env:CGO_ENABLED = '0'; $env:GOOS = 'windows'; $env:GOARCH = 'amd64'
# name|package|tags|extra ldflags
$targets = @(
    @('crimson-ledger.exe', './viewer/cmd/crimson-ledger', '', ''),
    @('crimson-scout.exe', './logger/cmd/crimson-scout', '', ''),
    @('crimson-api.exe', './api/cmd/crimson-api', '', ''),
    @('crimson-db.exe', './database/cmd/crimson-db', '', ''),
    @('lib\crimson-ledger-ui.exe', './viewer/cmd/crimson-ledger-ui', 'webview', '-H=windowsgui '),
    @('lib\crimson-scout-ui.exe', './logger/cmd/crimson-scout-ui', 'webview', '-H=windowsgui ')
)
foreach ($t in $targets) {
    $name, $pkg, $tags, $extra = $t
    $args_ = @('build', '-trimpath')
    if ($tags) { $args_ += @('-tags', $tags) }
    $args_ += @("-ldflags=$extra$ld", '-o', "$out\$name", $pkg)
    & go @args_
    if ($LASTEXITCODE -ne 0) { throw "go build $pkg failed" }
    Write-Host "built $name"
}
