# Removes local build artefacts (P5-1). Build outputs belong in bin/ only.
# Usage: powershell -File scripts/clean.ps1
$root = Split-Path -Parent $PSScriptRoot
$targets = @(
    (Join-Path $root 'bin'),
    (Join-Path $root 'coverage'),
    (Join-Path $root 'coverage.out')
)
$targets += Get-ChildItem -LiteralPath $root -File |
    Where-Object { $_.Extension -in '.exe', '.test', '.out' } |
    ForEach-Object { $_.FullName }
foreach ($t in $targets) {
    if (Test-Path -LiteralPath $t) {
        Write-Host "removing $t"
        Remove-Item -LiteralPath $t -Recurse -Force -Confirm:$false
    }
}
