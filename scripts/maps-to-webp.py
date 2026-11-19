"""Convert the zone PNGs in maps to WebP in viewer/maps (Pillow). Usage: python3 -I scripts/maps-to-webp.py [quality]"""
import pathlib
import sys

from PIL import Image

root = pathlib.Path(__file__).resolve().parent.parent
quality = int(sys.argv[1]) if len(sys.argv) > 1 else 80
out = root / "viewer" / "maps"
total_in = total_out = 0
for png in sorted((root.parent / "maps").glob("*.png")):
    dest = out / (png.stem + ".webp")
    Image.open(png).save(dest, "WEBP", quality=quality, method=6)
    total_in += png.stat().st_size
    total_out += dest.stat().st_size
print(f"{total_in / 1e6:.1f} MB png -> {total_out / 1e6:.1f} MB webp at q{quality}")
