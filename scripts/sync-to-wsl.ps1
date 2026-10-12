[CmdletBinding()]
param(
    [switch] $Apply,
    [string] $Distribution
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$repositoryRoot = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..')).Path
$repositoryName = Split-Path -Leaf $repositoryRoot
$gitRoot = (& git -C $repositoryRoot rev-parse --show-toplevel).Trim()
if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($gitRoot)) {
    throw "The authoritative Windows directory is not a Git repository: $repositoryRoot"
}

$resolvedGitRoot = (Resolve-Path -LiteralPath $gitRoot).Path
if (-not $resolvedGitRoot.Equals($repositoryRoot, [StringComparison]::OrdinalIgnoreCase)) {
    throw "The script must be inside the authoritative Git repository root: $repositoryRoot"
}

$wslArguments = @()
if ($Distribution) {
    $wslArguments += @('--distribution', $Distribution)
}

$portableRepositoryRoot = $repositoryRoot.Replace('\', '/')
$wslSource = (& wsl.exe @wslArguments -- wslpath -a -u $portableRepositoryRoot).Trim()
if ($LASTEXITCODE -ne 0 -or -not $wslSource.StartsWith('/mnt/', [StringComparison]::Ordinal)) {
    throw "Could not resolve the Windows checkout to a WSL /mnt path: $repositoryRoot"
}

$wslHome = (& wsl.exe @wslArguments -- bash -lc 'printf %s "$HOME"').Trim()
if ($LASTEXITCODE -ne 0 -or -not $wslHome.StartsWith('/', [StringComparison]::Ordinal)) {
    throw 'Could not resolve the WSL home directory.'
}

$wslDestination = "$wslHome/code/$repositoryName"
& wsl.exe @wslArguments -- test -d $wslDestination
if ($LASTEXITCODE -ne 0) {
    throw "The WSL mirror does not exist: $wslDestination"
}

& wsl.exe @wslArguments -- test -e "$wslDestination/.git"
if ($LASTEXITCODE -eq 0) {
    throw "The WSL destination must be a plain mirror, not a Git repository: $wslDestination"
}
if ($LASTEXITCODE -ne 1) {
    throw "Could not inspect the WSL mirror: $wslDestination"
}

$rsyncArguments = @(
    '--',
    'rsync',
    '-rlc',
    '--delete-delay',
    '--itemize-changes',
    '--exclude=/.git/',
    '--exclude=/.gocache/',
    '--exclude=/web/node_modules/',
    '--exclude=/web/test-results/',
    '--exclude=/web/playwright-report/',
    '--include=/backend/internal/ui/dist/',
    '--include=/backend/internal/ui/dist/index.html',
    '--exclude=/backend/internal/ui/dist/*',
    '--exclude=/dist/',
    '--exclude=*.log',
    '--exclude=coverage.out',
    '--exclude=*.test',
    '--exclude=*.sock',
    '--exclude=*.lock.tmp'
)

if (-not $Apply) {
    $rsyncArguments += '--dry-run'
}

$rsyncArguments += "$wslSource/"
$rsyncArguments += "$wslDestination/"

$mode = if ($Apply) { 'APPLY' } else { 'DRY RUN' }
Write-Host "${mode}: $repositoryRoot -> $wslDestination"
& wsl.exe @wslArguments @rsyncArguments
if ($LASTEXITCODE -ne 0) {
    throw "rsync failed with exit code $LASTEXITCODE."
}

if (-not $Apply) {
    Write-Host 'No files were changed. Review the list, then rerun with -Apply.'
}
