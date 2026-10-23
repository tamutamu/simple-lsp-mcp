# Install official Windows release under the current user, without admin rights.
# Emits the absolute binary path; does not edit the user's PATH or execute it.
$ErrorActionPreference = 'Stop'
$repo = 'https://github.com/tamutamu/simple-lsp-mcp'
$architecture = if ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture -eq 'Arm64') { 'arm64' } elseif ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture -eq 'X64') { 'amd64' } else { throw 'Unsupported architecture' }
$release = Invoke-RestMethod -Uri 'https://api.github.com/repos/tamutamu/simple-lsp-mcp/releases/latest' -Headers @{ 'User-Agent' = 'simple-lsp-mcp-setup' }
if ($release.tag_name -notmatch '^v[0-9]+\.[0-9]+\.[0-9]+(?:[-.][a-zA-Z0-9.-]+)?$') { throw 'Unexpected release tag' }
$version = $release.tag_name.Substring(1)
$archiveName = "simple-lsp-mcp_${version}_windows_${architecture}.zip"
$archiveAsset = $release.assets | Where-Object { $_.name -eq $archiveName } | Select-Object -First 1
$checksumAsset = $release.assets | Where-Object { $_.name -eq 'checksums.txt' } | Select-Object -First 1
if (-not $archiveAsset -or -not $checksumAsset) { throw "Missing official archive or checksums: $archiveName" }
$temp = Join-Path ([IO.Path]::GetTempPath()) ("simple-lsp-mcp-" + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $temp | Out-Null
try {
    $archive = Join-Path $temp $archiveName
    $checksums = Join-Path $temp 'checksums.txt'
    Invoke-WebRequest -Uri $archiveAsset.browser_download_url -OutFile $archive
    Invoke-WebRequest -Uri $checksumAsset.browser_download_url -OutFile $checksums
    $entry = Get-Content $checksums | Where-Object { $_ -match ('^([0-9a-fA-F]{64})\s+\*?' + [regex]::Escape($archiveName) + '$') } | Select-Object -First 1
    if (-not $entry) { throw "No checksum for $archiveName" }
    $match = [regex]::Match($entry, '^([0-9a-fA-F]{64})')
    $expected = $match.Groups[1].Value
    $actual = (Get-FileHash -Path $archive -Algorithm SHA256).Hash
    if ($actual -ne $expected) { throw "SHA-256 verification failed for $archiveName" }
    Expand-Archive -LiteralPath $archive -DestinationPath $temp -Force
    $binary = Join-Path $temp 'simple-lsp-mcp.exe'
    if (-not (Test-Path -LiteralPath $binary -PathType Leaf)) { throw 'Binary absent from release' }
    $destination = if ($env:SIMPLE_LSP_INSTALL_DIR) { $env:SIMPLE_LSP_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'simple-lsp-mcp\bin' }
    New-Item -ItemType Directory -Path $destination -Force | Out-Null
    $target = Join-Path $destination 'simple-lsp-mcp.exe'
    if ((Test-Path -LiteralPath $target) -and ((Get-Item -LiteralPath $target).Attributes -band [IO.FileAttributes]::ReparsePoint)) { throw 'Refusing to replace a symlink' }
    Copy-Item -LiteralPath $binary -Destination $target -Force
    Write-Output $target
} finally {
    Remove-Item -LiteralPath $temp -Recurse -Force -ErrorAction SilentlyContinue
}
