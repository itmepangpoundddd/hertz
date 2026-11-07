# Build the HERTZ desktop installer (dist\HERTZ-Setup-<version>.exe).
#
# Requires Inno Setup 6 or 7 (ISCC.exe). The default search covers the common
# per-user and Program Files locations; pass -ISCC if yours is elsewhere.
#
# Examples:
#   .\scripts\build-installer.ps1
#   .\scripts\build-installer.ps1 -SkipAppBuild
#   .\scripts\build-installer.ps1 -ISCC "$env:LOCALAPPDATA\Programs\Inno Setup 6\ISCC.exe"

[CmdletBinding()]
param(
    [string]$ISCC = "",
    [switch]$SkipAppBuild
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
Push-Location $root
try {
    if (-not $SkipAppBuild) {
        Write-Host "[1/2] Building HERTZ.exe..." -ForegroundColor Cyan
        & (Join-Path $PSScriptRoot "build-admin.ps1") -NoZip
        if ($LASTEXITCODE -ne 0) { throw "HERTZ.exe build failed" }
    } else {
        Write-Host "[1/2] Skipping app build (requested)" -ForegroundColor DarkGray
    }

    if (-not (Test-Path "dist\HERTZ.exe")) {
        throw "dist\HERTZ.exe not found. Run scripts\build-admin.ps1 first, or omit -SkipAppBuild."
    }

    if (-not $ISCC) {
        $candidates = @(
            (Join-Path $env:LOCALAPPDATA "Programs\Inno Setup 6\ISCC.exe"),
            (Join-Path $env:LOCALAPPDATA "Programs\Inno Setup 7\ISCC.exe"),
            (Join-Path $env:ProgramFiles "Inno Setup 6\ISCC.exe"),
            (Join-Path ${env:ProgramFiles(x86)} "Inno Setup 6\ISCC.exe")
        )
        $cmd = Get-Command ISCC.exe -ErrorAction SilentlyContinue
        if ($cmd) { $candidates = @($cmd.Source) + $candidates }
        $ISCC = $candidates | Where-Object { $_ -and (Test-Path $_) } | Select-Object -First 1
    }
    if (-not $ISCC -or -not (Test-Path $ISCC)) {
        throw "ISCC.exe not found. Install Inno Setup 6/7 or pass -ISCC C:\path\to\ISCC.exe"
    }

    $rc = Get-Content "cmd\hertz-admin\winres\hertz.rc" -Raw
    if ($rc -notmatch 'FILEVERSION\s+(\d+),(\d+),(\d+),(\d+)') {
        throw "Cannot read FILEVERSION from cmd\hertz-admin\winres\hertz.rc"
    }
    $version = "$($matches[1]).$($matches[2]).$($matches[3])"

    Write-Host "[2/2] Building installer with $ISCC ..." -ForegroundColor Cyan
    & $ISCC "/DMyAppVersion=$version" "installer\HERTZ.iss"
    if ($LASTEXITCODE -ne 0) { throw "Inno Setup failed" }

    $out = Get-Item "dist\HERTZ-Setup-$version.exe"
    $hash = (Get-FileHash $out.FullName -Algorithm SHA256).Hash
    Write-Host ""
    Write-Host "Built: $($out.FullName)" -ForegroundColor Green
    Write-Host ("Size : {0:N0} bytes" -f $out.Length)
    Write-Host "SHA256: $hash"
} finally {
    Pop-Location
}
