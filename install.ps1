Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

$osArchitecture = [Runtime.InteropServices.RuntimeInformation]::OSArchitecture
if ($osArchitecture -ne [Runtime.InteropServices.Architecture]::X64) {
    throw "Unsupported Windows architecture: $osArchitecture. Poke currently provides Windows x86-64 executables."
}

$binaryName = "poke-windows-x86_64.exe"
$installDirectory = Join-Path $HOME ".local\bin"
$installedPath = Join-Path $installDirectory "poke.exe"
$releaseBaseUrl = "https://github.com/ai-mindset/poke/releases/latest/download"
$temporaryDirectory = Join-Path ([IO.Path]::GetTempPath()) ("poke-install-" + [Guid]::NewGuid())
$temporaryInstallPath = $null

New-Item -ItemType Directory -Path $temporaryDirectory | Out-Null

try {
    $binaryPath = Join-Path $temporaryDirectory $binaryName
    $checksumsPath = Join-Path $temporaryDirectory "SHA256SUMS"

    Write-Host "Installing Poke for windows-x86_64..."
    Invoke-WebRequest -UseBasicParsing -Uri "$releaseBaseUrl/$binaryName" -OutFile $binaryPath
    Invoke-WebRequest -UseBasicParsing -Uri "$releaseBaseUrl/SHA256SUMS" -OutFile $checksumsPath

    $escapedBinaryName = [Regex]::Escape($binaryName)
    $checksumPattern = "^([0-9a-fA-F]{64})\s+\*?$escapedBinaryName$"
    $checksumLine = Get-Content $checksumsPath | Where-Object {
        [Regex]::IsMatch($_, $checksumPattern)
    } | Select-Object -First 1

    if (-not $checksumLine) {
        throw "SHA256SUMS has no valid entry for $binaryName"
    }

    $expectedHash = [Regex]::Match($checksumLine, $checksumPattern).Groups[1].Value
    $actualHash = (Get-FileHash -Algorithm SHA256 -Path $binaryPath).Hash
    if ($actualHash -ine $expectedHash) {
        throw "Checksum verification failed for $binaryName"
    }

    & $binaryPath --help *> $null
    if ($LASTEXITCODE -ne 0) {
        throw "The downloaded executable failed its smoke test"
    }

    New-Item -ItemType Directory -Force -Path $installDirectory | Out-Null
    $temporaryInstallPath = Join-Path $installDirectory (".poke-install-" + [Guid]::NewGuid() + ".exe")
    Copy-Item -Path $binaryPath -Destination $temporaryInstallPath
    if (Test-Path $installedPath) {
        [IO.File]::Replace($temporaryInstallPath, $installedPath, $null)
    }
    else {
        [IO.File]::Move($temporaryInstallPath, $installedPath)
    }
    $temporaryInstallPath = $null
}
finally {
    if ($temporaryInstallPath -and (Test-Path $temporaryInstallPath)) {
        Remove-Item -Force $temporaryInstallPath
    }
    if (Test-Path $temporaryDirectory) {
        Remove-Item -Recurse -Force $temporaryDirectory
    }
}

$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
$userPathEntries = @($userPath -split ";" | Where-Object { $_ })
if ($userPathEntries -notcontains $installDirectory) {
    $newUserPath = if ([String]::IsNullOrWhiteSpace($userPath)) {
        $installDirectory
    }
    else {
        "$userPath;$installDirectory"
    }
    [Environment]::SetEnvironmentVariable("Path", $newUserPath, "User")
}

$processPathEntries = @($env:Path -split ";" | Where-Object { $_ })
if ($processPathEntries -notcontains $installDirectory) {
    $env:Path = "$installDirectory;$env:Path"
}

Write-Host "Poke installed to $installedPath"
Write-Host "Run 'poke --help' to get started."
