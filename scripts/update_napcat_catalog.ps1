param(
    [string]$Revision = "f2ea3354f8bbe8672cbb4ae6a2b4e1fbeff44faf",
    [string]$InputPath = ""
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
$source = "https://raw.githubusercontent.com/SnowLuma/SnowLuma/$Revision/packages/mcp/src/generated/catalog.json"
if ($InputPath) {
    $raw = Get-Content -LiteralPath $InputPath -Raw -Encoding UTF8
} else {
    $raw = & curl.exe --location --max-time 25 --fail $source
    if ($LASTEXITCODE -ne 0) { throw "Catalog download failed" }
    $raw = $raw -join "`n"
}
$catalog = $raw | ConvertFrom-Json
if ($catalog.actions.Count -lt 1) { throw "Catalog contains no actions" }
$actions = @($catalog.actions | Sort-Object name | Select-Object name, aliases,
    summary, category, readOnly, inputSchema, returns, returnsSchema, examples, invariants)
$output = [ordered]@{
    source = $source
    revision = $Revision
    actions = $actions
} | ConvertTo-Json -Depth 100 -Compress
$utf8 = New-Object System.Text.UTF8Encoding($false)
[System.IO.File]::WriteAllText((Join-Path $root "agent/napcat_catalog.json"), $output + "`n", $utf8)
Write-Output "Generated $($actions.Count) actions. New actions remain disabled until locally reviewed."
