# Builds the TapperVPN Android / Fire TV debug APK on Windows.
# Mirrors the tappervpn-android Makefile's `apk` target (which needs Linux/macOS),
# minus stripping debug symbols from libgojni.so.
#
# Output: bin	appervpn-debug.apk  (installs on phones and Fire TV)
param(
    [switch]$SkipAar,  # reuse the existing android\libs\libtappervpn.aar
    [switch]$Restamp,  # regenerate tappervpn.version (after go.mod changes)
    # Control server the app signs in to (our Headscale). Use an address every
    # device can reach; give this PC a DHCP reservation so it doesn't change.
    [string]$ControlURL = "http://192.168.5.207:8080"
)
$ErrorActionPreference = "Stop"
$root = Split-Path $PSScriptRoot -Parent
$app = Join-Path $root "tappervpn-android"

# --- Toolchain locations ---------------------------------------------------
if (-not $env:JAVA_HOME) {
    $jdk = Get-ChildItem "C:\Program Files\Microsoft" -Directory -Filter "jdk-17*" -ErrorAction SilentlyContinue |
        Sort-Object Name -Descending | Select-Object -First 1
    if (-not $jdk) { throw "JDK 17 not found. Install it: winget install Microsoft.OpenJDK.17" }
    $env:JAVA_HOME = $jdk.FullName
}
if (-not $env:ANDROID_HOME) { $env:ANDROID_HOME = Join-Path $root "toolchain\android-sdk" }
$ndk = Join-Path $env:ANDROID_HOME "ndk\23.1.7779620"
if (-not (Test-Path $ndk)) { throw "NDK 23.1.7779620 not found under $env:ANDROID_HOME. Run sdkmanager first." }
$env:ANDROID_NDK_HOME = $ndk
$env:PATH = "$env:JAVA_HOME\bin;$env:ANDROID_HOME\platform-tools;C:\Program Files\Go\bin;$env:PATH"

# Android builds need a Go with patches\go-android-time32.patch applied:
# stock Go uses futex_time64 on 5.x kernels, which Fire OS 7 / Android 9
# seccomp kills (SIGSYS). Make it with:
#   robocopy "C:\Program Files\Go" toolchain\go-android /E
#   then apply patches\go-android-time32.patch inside toolchain\go-android
$goAndroid = Join-Path $root "toolchain\go-android"
if (-not (Test-Path "$goAndroid\bin\go.exe")) { throw "Patched Go not found at $goAndroid (see comment above)." }
if (-not (Select-String -Quiet -Path "$goAndroid\src\runtime\os_linux32.go" -Pattern "meshwall patch")) {
    throw "$goAndroid is missing patches\go-android-time32.patch"
}
$env:GOROOT = $goAndroid
$env:PATH = "$goAndroid\bin;$env:PATH"
Write-Host "Go: $(& go version) from $goAndroid"
$env:GOTOOLCHAIN = "local"
$gobin = Join-Path $app "android\build\go\bin"
New-Item -ItemType Directory -Force $gobin | Out-Null
$env:GOBIN = $gobin
$env:PATH = "$gobin;$env:PATH"   # gomobile runs gobind from PATH
Write-Host "JAVA_HOME=$env:JAVA_HOME"
Write-Host "ANDROID_HOME=$env:ANDROID_HOME"

Push-Location $app
try {
    # --- Version stamp (tappervpn.version, read by Gradle and the ldflags) ---
    # The stamp only changes with go.mod, so reuse it unless -Restamp. (Skipping
    # `go run` also avoids Windows Application Control blocking the freshly
    # built mkversion.exe.)
    if ($Restamp -or -not (Test-Path tappervpn.version)) {
        # mkversion expects the engine in its own git repo; here it's a folder of
        # this repo, so hand it the version: tappervpn\VERSION.txt + our commit.
        $hash = (& git -C $root rev-parse HEAD).Trim()
        $engine = (Get-Content (Join-Path $root "tappervpn\VERSION.txt") -Raw).Trim()
        $env:TS_VERSION_LONG = "$engine-t$($hash.Substring(0, 9))"
        $env:TS_VERSION_GIT_HASH = $hash
        & go run tappervpn.com/cmd/mkversion | Set-Content -Encoding ascii tappervpn.version
        Remove-Item Env:TS_VERSION_LONG, Env:TS_VERSION_GIT_HASH
        if ($LASTEXITCODE) { throw "mkversion failed" }
    }
    $ver = @{}
    foreach ($line in Get-Content tappervpn.version) {
        if ($line -match '^([A-Z_]+)="?(.*?)"?$') { $ver[$matches[1]] = $matches[2] }
    }
    $tvVersion = (Get-Content (Join-Path $root "VERSION") -Raw).Trim()
    Write-Host "Version: TapperVPN $tvVersion (engine $($ver.VERSION_LONG))"

    if (-not $SkipAar) {
        # --- gomobile / gobind at the versions pinned in go.mod ---
        # Reuse existing binaries: rebuilt ones get new hashes, which Windows
        # Application Control may block.
        if (-not ((Test-Path "$gobin\gomobile.exe") -and (Test-Path "$gobin\gobind.exe"))) {
            & go install golang.org/x/mobile/cmd/gomobile golang.org/x/mobile/cmd/gobind
            if ($LASTEXITCODE) { throw "installing gomobile failed" }
        }

        # --- Go library -> AAR ---
        New-Item -ItemType Directory -Force "android\libs" | Out-Null
        $ldflags = "-linkmode=external -extldflags=-Wl,-z,max-page-size=16384 " +
            "-X tappervpn.com/version.longStamp=$($ver.VERSION_LONG) " +
            "-X tappervpn.com/version.shortStamp=$($ver.VERSION_SHORT) " +
            "-X tappervpn.com/version.gitCommitStamp=$($ver.VERSION_GIT_HASH) " +
            "-X tappervpn.com/version.extraGitCommitStamp=$($ver.VERSION_EXTRA_HASH) " +
            "-X tappervpn.com/updater.Version=$tvVersion"
        & "$gobin\gomobile.exe" bind -target android -androidapi 26 `
            -tags "not_tappervpn_go ts_omit_cachenetmap" `
            -ldflags $ldflags `
            -o (Join-Path $app "android\libs\libtappervpn.aar") ./libtappervpn
        if ($LASTEXITCODE) { throw "gomobile bind failed" }
    }

    # --- APK ---
    Push-Location android
    try {
        # --no-daemon: a lingering Gradle daemon keeps this script's process
        # (and its redirected log) open after the build finishes.
        Write-Host "Control server: $ControlURL"
        & .\gradlew.bat --no-daemon "-PcontrolURL=$ControlURL" assembleDebug
        if ($LASTEXITCODE) { throw "gradle assembleDebug failed" }
    } finally { Pop-Location }

    Copy-Item "android\build\outputs\apk\debug\android-debug.apk" (Join-Path $root "bin\tappervpn-debug.apk") -Force
    Write-Host "Built: $root\bin\tappervpn-debug.apk"
} finally { Pop-Location }
