# Draws installer/tappervpn.ico: the TapperVPN "T ->" gate-sign logo, neon
# green on black. Same shapes and colors as the Android launcher icon
# (tappervpn-android/.../drawable/ic_launcher_foreground.xml).
#
#   python installer/make-icon.py      (needs Pillow: pip install pillow)
from pathlib import Path

from PIL import Image, ImageDraw, ImageFilter

BLACK = (0, 0, 0, 255)
PLATE = (0x0A, 0x0A, 0x0A, 255)
EDGE = (0x1F, 0x6B, 0x2E, 255)
GREEN = (0x39, 0xFF, 0x14, 255)

SIZE = 1024  # drawn large, then scaled down for each icon size
# The launcher's plate spans x 27..81, y 41..67 of a 108 viewport. Shift it
# by 24 so it sits centered in a 60x60 square.
S = SIZE / 60.0


def p(x, y):
    return ((x - 24) * S, (y - 24) * S)


def strokes(d, width, color):
    d.line([p(33, 48), p(45, 48)], fill=color, width=width)
    d.line([p(39, 48), p(39, 61)], fill=color, width=width)
    d.line([p(50, 54), p(73, 54)], fill=color, width=width)
    d.line([p(65.5, 46.5), p(73, 54), p(65.5, 61.5)], fill=color, width=width, joint="curve")
    r = width / 2
    for x, y in [(33, 48), (45, 48), (39, 61), (50, 54), (65.5, 46.5), (65.5, 61.5), (73, 54)]:
        cx, cy = p(x, y)
        d.ellipse([cx - r, cy - r, cx + r, cy + r], fill=color)


img = Image.new("RGBA", (SIZE, SIZE), (0, 0, 0, 0))
d = ImageDraw.Draw(img)
d.rounded_rectangle([0, 0, SIZE - 1, SIZE - 1], radius=SIZE * 0.18, fill=BLACK)
x0, y0 = p(27, 41)
x1, y1 = p(81, 67)
d.rounded_rectangle([x0, y0, x1, y1], radius=3 * S, fill=PLATE, outline=EDGE, width=int(1.5 * S))

glow = Image.new("RGBA", (SIZE, SIZE), (0, 0, 0, 0))
strokes(ImageDraw.Draw(glow), int(7 * S), (0x39, 0xFF, 0x14, 90))
glow = glow.filter(ImageFilter.GaussianBlur(S * 1.5))
img = Image.alpha_composite(img, glow)
strokes(ImageDraw.Draw(img), int(4 * S), GREEN)

out = Path(__file__).with_name("tappervpn.ico")
img.save(out, sizes=[(16, 16), (24, 24), (32, 32), (48, 48), (64, 64), (128, 128), (256, 256)])
print("wrote", out)
