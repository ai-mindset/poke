$ErrorActionPreference = "Stop"

$ARCH = if ([Environment]::Is64BitProcess) { "x86_64" } else { "x86" }
$BINARY_NAME = "ping-windows-${ARCH}.exe"
$INSTALL_DIR = "$env:USERPROFILE\.local\bin"
$CONFIG_DIR = "$env:USERPROFILE\AppData\Local\ping"
$LATEST_URL = "https://api.github.com/repos/ai-mindset/ping/releases/latest"

Write-Host "Installing ping for windows-$ARCH..."

# Get latest release info
$releaseInfo = Invoke-RestMethod -Uri $LATEST_URL
$downloadUrl = $releaseInfo.assets | Where-Object { $_.name -eq $BINARY_NAME } | Select-Object -ExpandProperty browser_download_url

if (-not $downloadUrl) {
    Write-Error "Could not find binary for windows-$ARCH"
}

# Create install directory
New-Item -ItemType Directory -Force -Path $INSTALL_DIR | Out-Null
New-Item -ItemType Directory -Force -Path $CONFIG_DIR | Out-Null

# Download and install
Write-Host "Downloading from: $downloadUrl"
Invoke-WebRequest -Uri $downloadUrl -OutFile "$INSTALL_DIR\ping.exe"

Write-Host "✅ ping installed to $INSTALL_DIR\ping.exe"
Write-Host ""
Write-Host "Add to PATH if needed (run as Administrator):"
Write-Host "[Environment]::SetEnvironmentVariable('Path', [Environment]::GetEnvironmentVariable('Path', 'User') + ';$INSTALL_DIR', 'User')"
Write-Host ""
Write-Host "Create config file with your GitHub settings:"
Write-Host "New-Item -ItemType Directory -Force -Path '$CONFIG_DIR' | Out-Null"
Write-Host @"
Set-Content -Path "$CONFIG_DIR\.env" -Value @"
GITHUB_TOKEN=ghp_your_token
GITHUB_USERNAME=your_username
WORK_ORGS=Your-Organization
WORK_TEAMS=team1,team2,team3
"@
"@
