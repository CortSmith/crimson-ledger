# Runs on the Windows executor against the unpacked package. Usage: windows-smoke.ps1 <dist\windows-amd64>
# 1. The launcher starts the UI and --selftest prints SELFTEST OK (WebView2 is present on the executor).
# 2. The config file written by the run is readable only by its owner (icacls).
param([Parameter(Mandatory = $true)][string]$Dir)
$ErrorActionPreference = 'Stop'
$home_ = Join-Path $env:TEMP ("cl-smoke-" + [guid]::NewGuid())
New-Item -ItemType Directory $home_ | Out-Null
$env:CRIMSON_LEDGER_HOME = $home_
foreach ($app in 'crimson-ledger', 'crimson-scout') {
    $p = Start-Process -FilePath (Join-Path $Dir "$app.exe") -ArgumentList '--selftest' -Wait -PassThru `
        -RedirectStandardOutput "$home_\$app.out" -RedirectStandardError "$home_\$app.err"
    $out = Get-Content "$home_\$app.out" -Raw
    if ($p.ExitCode -ne 0 -or $out -notmatch 'SELFTEST OK') {
        Write-Error "$app selftest failed (exit $($p.ExitCode)): $out $(Get-Content "$home_\$app.err" -Raw)"
    }
    Write-Host "$app : SELFTEST OK"
}
foreach ($f in Get-ChildItem $home_ -Filter *.json) {
    $acl = (icacls $f.FullName) -join "`n"
    if ($acl -match 'Everyone|BUILTIN\\Users') { Write-Error "$($f.Name) is readable by others:`n$acl" }
}
Write-Host 'windows-smoke OK'
