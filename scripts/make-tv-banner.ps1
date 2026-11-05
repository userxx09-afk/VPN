# Draws the Fire TV / Android TV banner (320x180): the TapperVPN gate-sign
# logo, neon green on black, plus the name. Same shapes and colors as
# ic_launcher_foreground.xml and TapperVPNLogoView.kt.
#
#   scripts\make-tv-banner.ps1
Add-Type -AssemblyName System.Drawing
$root = Split-Path $PSScriptRoot -Parent
$out = Join-Path $root "tappervpn-android\android\src\main\res\drawable-xhdpi\tv_banner.png"

$black = [Drawing.Color]::FromArgb(0x00, 0x00, 0x00)
$plate = [Drawing.Color]::FromArgb(0x0A, 0x0A, 0x0A)
$edge  = [Drawing.Color]::FromArgb(0x1F, 0x6B, 0x2E)
$green = [Drawing.Color]::FromArgb(0x39, 0xFF, 0x14)

$bmp = New-Object Drawing.Bitmap 320, 180
$g = [Drawing.Graphics]::FromImage($bmp)
$g.SmoothingMode = "AntiAlias"
$g.TextRenderingHint = "AntiAliasGridFit"
$g.Clear($black)

function RoundRect($x, $y, $w, $h, $r) {
    $p = New-Object Drawing.Drawing2D.GraphicsPath
    $p.AddArc($x, $y, 2 * $r, 2 * $r, 180, 90)
    $p.AddArc($x + $w - 2 * $r, $y, 2 * $r, 2 * $r, 270, 90)
    $p.AddArc($x + $w - 2 * $r, $y + $h - 2 * $r, 2 * $r, 2 * $r, 0, 90)
    $p.AddArc($x, $y + $h - 2 * $r, 2 * $r, 2 * $r, 90, 90)
    $p.CloseFigure()
    $p
}

# The sign plate (the launcher icon's 54x26 plate, scaled 2x).
$sx = 2.0; $ox = 18 - 27 * $sx; $oy = 64 - 41 * $sx
function P($x, $y) { New-Object Drawing.PointF ([float]($ox + $x * $sx)), ([float]($oy + $y * $sx)) }
$plateRect = RoundRect (18) (64) (54 * $sx) (26 * $sx) (3 * $sx)
$g.FillPath((New-Object Drawing.SolidBrush $plate), $plateRect)
$g.DrawPath((New-Object Drawing.Pen $edge, ([float](1.5 * $sx))), $plateRect)

# Neon glow under the strokes, then the strokes.
function Strokes($pen) {
    $g.DrawLine($pen, (P 33 48), (P 45 48))
    $g.DrawLine($pen, (P 39 48), (P 39 61))
    $g.DrawLine($pen, (P 50 54), (P 73 54))
    $g.DrawLines($pen, [Drawing.PointF[]]@((P 65.5 46.5), (P 73 54), (P 65.5 61.5)))
}
foreach ($w in 18, 13) {
    $glow = New-Object Drawing.Pen ([Drawing.Color]::FromArgb(40, $green)), ([float]$w)
    $glow.StartCap = $glow.EndCap = "Round"; $glow.LineJoin = "Round"
    Strokes $glow
}
$pen = New-Object Drawing.Pen $green, ([float](4 * $sx))
$pen.StartCap = $pen.EndCap = "Round"; $pen.LineJoin = "Round"
Strokes $pen

$font = New-Object Drawing.Font "Arial", 22, ([Drawing.FontStyle]::Bold), ([Drawing.GraphicsUnit]::Pixel)
$fmt = New-Object Drawing.StringFormat; $fmt.LineAlignment = "Center"
$g.DrawString("TapperVPN", $font, [Drawing.Brushes]::White, (New-Object Drawing.RectangleF 144, 64, 170, (26 * $sx)), $fmt)

$g.Dispose()
$bmp.Save($out, [Drawing.Imaging.ImageFormat]::Png)
$bmp.Dispose()
Write-Host "Wrote $out"
