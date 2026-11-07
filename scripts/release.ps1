# Builds, signs and publishes a TapperVPN release. Every device updates from
# the release this makes (github.com/userxx09-afk/VPN/releases).
#
# Usually you don't need this: pushing a new VERSION to main makes GitHub do
# the same (.github/workflows/release.yml). This is for releasing from the PC
# that has the keys. Keep the two in step when changing what a release holds.
#
#   1. Put the new version in VERSION (e.g. 0.3.0) and commit.
#   2. scripts\release.ps1                 # build + sign into bin\release\<version>
#      scripts\release.ps1 -Publish        # ...and upload it as a GitHub release
#
# -Publish needs a GitHub token with "contents: write" on the repo in
# $env:GITHUB_TOKEN. Without -Publish, upload the files in bin\release\<version>
# yourself: GitHub > Releases > Draft a new release, tag v<version>.
#
# Signing uses %USERPROFILE%\.tappervpn\release.key (tappervpn-release keygen).
# The Android APK is signed with this PC's Android debug key
# (%USERPROFILE%\.android\debug.keystore); Android only installs an update
# signed with the same key, so back both up.
param(
    [switch]$Publish,
    [switch]$SkipAndroid,
    [switch]$SkipRouter
)
$ErrorActionPreference = "Stop"
$root = Split-Path $PSScriptRoot -Parent
$repo = "userxx09-afk/VPN"
$version = (Get-Content "$root\VERSION" -Raw).Trim()
if ($version -notmatch '^\d+\.\d+\.\d+$') { throw "VERSION must look like 1.2.3, got '$version'" }
$out = "$root\bin\release\$version"
$stage = "$root\bin\release\stage"
Remove-Item -Recurse -Force $out, $stage -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force $out, "$stage\windows", "$stage\server" | Out-Null

$env:PATH = "C:\Program Files\Go\bin;$env:PATH"
$env:GOTOOLCHAIN = "local"
$commit = (& git -C $root rev-parse HEAD).Trim()
$engine = (Get-Content "$root\tappervpn\VERSION.txt" -Raw).Trim()
$ldflags = "-s -w -X tappervpn.com/updater.Version=$version " +
    "-X tappervpn.com/version.longStamp=$engine-t$($commit.Substring(0, 9)) " +
    "-X tappervpn.com/version.shortStamp=$engine " +
    "-X tappervpn.com/version.gitCommitStamp=$commit"

function GoBuild($dir, $goos, $outFile, $pkg, $extraLdflags = "") {
    $env:GOOS = $goos; $env:GOARCH = "amd64"; $env:CGO_ENABLED = "0"
    Push-Location $dir
    try {
        & go build -trimpath -ldflags "$ldflags $extraLdflags".Trim() -o $outFile $pkg
        if ($LASTEXITCODE) { throw "go build $pkg ($goos) failed" }
    } finally {
        Pop-Location
        Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED
    }
}

Write-Host "== TapperVPN $version"

# --- Windows: tappervpn-windows-amd64.zip ---
foreach ($cmd in "tappervpn", "tappervpnd", "tappervpn-updater") {
    GoBuild "$root\tappervpn" "windows" "$stage\windows\$cmd.exe" "./cmd/$cmd"
}
# The TapperVPN app (window + taskbar icon), a GUI exe. The updater replaces it like
# every file in the zip; the running app restarts itself from the new file.
GoBuild "$root\tappervpn" "windows" "$stage\windows\tappervpn-gui.exe" "./cmd/tappervpn-gui" "-H windowsgui"
# wintun.dll: WireGuard LLC's signed tunnel driver, redistributable unmodified.
$wintunHash = "E5DA8447DC2C320EDC0FC52FA01885C103DE8C118481F683643CACC3220DAFCE"
$wintun = "$root\bin\wintun.dll"
if (-not (Test-Path $wintun)) {
    $zip = "$env:TEMP\wintun-0.14.1.zip"
    Invoke-WebRequest "https://www.wintun.net/builds/wintun-0.14.1.zip" -OutFile $zip
    Expand-Archive $zip "$env:TEMP\wintun" -Force
    Copy-Item "$env:TEMP\wintun\wintun\bin\amd64\wintun.dll" $wintun
}
if ((Get-FileHash $wintun).Hash -ne $wintunHash) { throw "bin\wintun.dll doesn't match the pinned SHA-256" }
Copy-Item $wintun "$stage\windows\"
Compress-Archive "$stage\windows\*" "$out\tappervpn-windows-amd64.zip"

# --- Control server: tappervpn-server-linux-amd64.tar.gz ---
GoBuild "$root\headscale" "linux" "$stage\server\headscale" "./cmd/headscale"
foreach ($cmd in "tappervpn", "tappervpnd", "tappervpn-updater") {
    GoBuild "$root\tappervpn" "linux" "$stage\server\$cmd" "./cmd/$cmd"
}
Copy-Item "$root\server\install.sh", "$root\server\policy.hujson" "$stage\server\"
& tar -czf "$out\tappervpn-server-linux-amd64.tar.gz" -C "$stage\server" .
if ($LASTEXITCODE) { throw "tar failed" }

# --- GL.iNet routers: tappervpn-glinet-<cpu>.ipk ---
if (-not $SkipRouter) {
    & python "$root\glinet\build.py"
    if ($LASTEXITCODE) { throw "router build failed" }
    foreach ($ipk in Get-ChildItem "$root\bin\glinet\tappervpn_$version-*_*.ipk") {
        $cpu = $ipk.BaseName -replace '^.*_', ''
        Copy-Item $ipk.FullName "$out\tappervpn-glinet-$cpu.ipk"
    }
}

# --- Android / Fire TV: tappervpn-android.apk ---
if (-not $SkipAndroid) {
    & "$root\scripts\build-android.ps1" -Restamp
    Copy-Item "$root\bin\tappervpn-debug.apk" "$out\tappervpn-android.apk"
}

# --- Sign ---
Push-Location "$root\tappervpn"
try {
    & go run ./cmd/tappervpn-release sign $out
    if ($LASTEXITCODE) { throw "signing failed" }
} finally { Pop-Location }
Remove-Item -Recurse -Force $stage
Get-ChildItem $out | Format-Table Name, Length

if (-not $Publish) {
    Write-Host "Release files are in $out. Run again with -Publish to upload them."
    return
}

# --- Publish to GitHub Releases ---
if (-not $env:GITHUB_TOKEN) { throw "Set `$env:GITHUB_TOKEN (contents: write on $repo) to publish." }
$headers = @{ Authorization = "Bearer $env:GITHUB_TOKEN"; Accept = "application/vnd.github+json" }
$body = @{ tag_name = "v$version"; target_commitish = $commit; name = "TapperVPN $version"; draft = $true } | ConvertTo-Json
$rel = Invoke-RestMethod -Method Post -Headers $headers -Body $body "https://api.github.com/repos/$repo/releases"
foreach ($f in Get-ChildItem $out) {
    Write-Host "uploading $($f.Name)"
    $url = "https://uploads.github.com/repos/$repo/releases/$($rel.id)/assets?name=$($f.Name)"
    Invoke-RestMethod -Method Post -Headers $headers -ContentType "application/octet-stream" -InFile $f.FullName $url | Out-Null
}
# Publish only once every file is up, so no device sees a half-uploaded release.
Invoke-RestMethod -Method Patch -Headers $headers -Body (@{ draft = $false } | ConvertTo-Json) `
    "https://api.github.com/repos/$repo/releases/$($rel.id)" | Out-Null
Write-Host "Published $($rel.html_url -replace '/tag/untagged-.*', "/tag/v$version")"
