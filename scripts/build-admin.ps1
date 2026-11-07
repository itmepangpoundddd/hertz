# Build the single-file Windows admin application (dist\HERTZ.exe).
#
# The admin binary embeds the HERTZ engine, so the engine must be built first
# into cmd\hertz-admin\assets\hertz-engine.exe. No CGO is required.
#
# Examples:
#   .\scripts\build-admin.ps1
#   .\scripts\build-admin.ps1 -Output dist\HERTZ.exe -NoZip
#   .\scripts\build-admin.ps1 -GoBin C:\tools\go\bin\go.exe
#
# If PowerShell blocks local scripts, run:
#   powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\build-admin.ps1

[CmdletBinding()]
param(
    [string]$Output = "dist\HERTZ.exe",
    [string]$GoBin = "",
    [switch]$NoZip,
    [switch]$SkipResource
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
Push-Location $root
try {
    if (-not $GoBin) {
        $cmd = Get-Command go -ErrorAction SilentlyContinue
        if ($cmd) { $GoBin = $cmd.Source }
    }
    if (-not $GoBin -or -not (Test-Path $GoBin)) {
        throw "go.exe not found. Add Go to PATH or pass -GoBin C:\path\to\go.exe"
    }

    $env:GOTOOLCHAIN = "local"
    $env:CGO_ENABLED = "0"

    Write-Host "[1/3] Building HERTZ engine..." -ForegroundColor Cyan
    New-Item -ItemType Directory -Force -Path "cmd\hertz-admin\assets" | Out-Null
    & $GoBin build -trimpath -ldflags "-s -w" -o "cmd\hertz-admin\assets\hertz-engine.exe" ".\cmd\hertz"
    if ($LASTEXITCODE -ne 0) { throw "engine build failed" }

    if (-not $SkipResource) {
        Write-Host "[2/3] Building Windows icon/version resource..." -ForegroundColor Cyan
        $windres = $null
        $cmd = Get-Command windres -ErrorAction SilentlyContinue
        if ($cmd) { $windres = $cmd.Source }
        if (-not $windres) {
            $candidate = Join-Path $env:TEMP "winlibs\mingw64\bin\windres.exe"
            if (Test-Path $candidate) { $windres = $candidate }
        }
        if ($windres) {
            Copy-Item "cmd\hertz-admin\icon.ico" "cmd\hertz-admin\winres\icon.ico" -Force
            & $windres -I "cmd\hertz-admin\winres" -i "cmd\hertz-admin\winres\hertz.rc" `
                -O coff -F pe-x86-64 -o "cmd\hertz-admin\rsrc_windows_amd64.syso"
            if ($LASTEXITCODE -ne 0) { throw "windres failed" }
        } elseif (-not (Test-Path "cmd\hertz-admin\rsrc_windows_amd64.syso")) {
            Write-Warning "windres not found; building without the custom exe icon"
        }
    } else {
        Write-Host "[2/3] Skipping resource (requested)" -ForegroundColor DarkGray
    }

    Write-Host "[3/3] Building HERTZ.exe..." -ForegroundColor Cyan
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $Output) | Out-Null
    & $GoBin build -trimpath -ldflags "-s -w" -o $Output ".\cmd\hertz-admin"
    if ($LASTEXITCODE -ne 0) { throw "admin build failed" }

    $file = Get-Item $Output
    $hash = (Get-FileHash $file.FullName -Algorithm SHA256).Hash
    Write-Host ""
    Write-Host "Built: $($file.FullName)" -ForegroundColor Green
    Write-Host ("Size : {0:N2} MB" -f ($file.Length / 1MB))
    Write-Host "SHA256: $hash"

    if (-not $NoZip) {
        $zipPath = Join-Path (Split-Path -Parent $file.FullName) "HERTZ-windows-x64.zip"
        $stage = Join-Path $env:TEMP ("hertz-package-" + [guid]::NewGuid().ToString("N"))
        try {
            New-Item -ItemType Directory -Force -Path $stage | Out-Null
            Copy-Item $file.FullName (Join-Path $stage "HERTZ.exe") -Force
            Copy-Item "docs\ADMIN.md" (Join-Path $stage "ADMIN.md") -Force
            Copy-Item "README.md" (Join-Path $stage "README.md") -Force
            Remove-Item $zipPath -Force -ErrorAction SilentlyContinue
            Compress-Archive -Path (Join-Path $stage "*") -DestinationPath $zipPath -CompressionLevel Optimal
            Write-Host "Package: $zipPath"
        } finally {
            Remove-Item $stage -Recurse -Force -ErrorAction SilentlyContinue
        }
    }
} finally {
    Pop-Location
}
