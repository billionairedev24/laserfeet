# Laser Feet installer for Windows.
#
#   irm https://billionairedev24.github.io/laserfeet/install.ps1 | iex
#
# Downloads lf from the latest GitHub release, checks its SHA-256 checksum
# against the release's SHA256SUMS file, and only then installs it.
# Options: set $env:LF_VERSION = "v0.1.0" to pick a version.
$ErrorActionPreference = "Stop"

$Repo = "billionairedev24/laserfeet"
$Version = if ($env:LF_VERSION) { $env:LF_VERSION } else { "latest" }
$Dir = if ($env:LF_INSTALL_DIR) { $env:LF_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA "laserfeet\bin" }
$File = "lf-windows-amd64.exe"

if ($env:LF_BASE_URL) { $Base = $env:LF_BASE_URL }
elseif ($Version -eq "latest") { $Base = "https://github.com/$Repo/releases/latest/download" }
else { $Base = "https://github.com/$Repo/releases/download/$Version" }

[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
$Tmp = Join-Path ([IO.Path]::GetTempPath()) ("laserfeet-" + [Guid]::NewGuid())
New-Item -ItemType Directory -Path $Tmp | Out-Null
try {
    Write-Host "Downloading $File ($Version)..."
    Invoke-WebRequest -UseBasicParsing -Uri "$Base/$File" -OutFile (Join-Path $Tmp $File)
    Invoke-WebRequest -UseBasicParsing -Uri "$Base/SHA256SUMS" -OutFile (Join-Path $Tmp "SHA256SUMS")

    $Line = Get-Content (Join-Path $Tmp "SHA256SUMS") | Where-Object { ($_ -split '\s+')[1] -eq $File }
    if (-not $Line) { throw "$File is not listed in SHA256SUMS" }
    $Expected = ($Line -split '\s+')[0].ToLower()
    $Actual = (Get-FileHash -Algorithm SHA256 (Join-Path $Tmp $File)).Hash.ToLower()
    if ($Actual -ne $Expected) { throw "checksum mismatch for $File (expected $Expected, got $Actual). Nothing was installed." }
    Write-Host "Checksum verified: $Actual"

    New-Item -ItemType Directory -Force -Path $Dir | Out-Null
    Move-Item -Force (Join-Path $Tmp $File) (Join-Path $Dir "lf.exe")
    Write-Host "Installed lf to $Dir\lf.exe"

    $UserPath = [Environment]::GetEnvironmentVariable("Path", "User")
    if (($UserPath -split ';') -notcontains $Dir) {
        [Environment]::SetEnvironmentVariable("Path", "$UserPath;$Dir", "User")
        Write-Host "Added $Dir to your PATH. Open a new terminal, then try: lf new my-app"
    } else {
        Write-Host "Try it: lf new my-app"
    }
} finally {
    Remove-Item -Recurse -Force $Tmp -ErrorAction SilentlyContinue
}
