"""One-shot generator for the MINTI favicon PNGs (the ▚ quadrant glyph).

Pure stdlib (struct+zlib) so it runs anywhere Python does. Emits 8-bit RGB
PNGs: cyan upper-left + lower-right quadrant squares on the brand background,
matching assets/favicon.svg.
"""
import struct
import zlib

CYAN = (0x5F, 0xD7, 0xFF)
BG = (0x0A, 0x0A, 0x0A)


def make_png(path, size):
    margin = round(size * 0.125)   # same proportions as favicon.svg (4/32)
    quad = round(size * 0.375)     # (12/32)
    lo = size - margin - quad      # lower-right quadrant start
    rows = []
    for y in range(size):
        row = bytearray(b"\x00")   # filter type 0 per scanline
        for x in range(size):
            in_ul = margin <= x < margin + quad and margin <= y < margin + quad
            in_lr = lo <= x < size - margin and lo <= y < size - margin
            row += bytes(CYAN if (in_ul or in_lr) else BG)
        rows.append(bytes(row))

    def chunk(tag, data):
        return (struct.pack(">I", len(data)) + tag + data
                + struct.pack(">I", zlib.crc32(tag + data) & 0xFFFFFFFF))

    ihdr = struct.pack(">IIBBBBB", size, size, 8, 2, 0, 0, 0)
    png = (b"\x89PNG\r\n\x1a\n"
           + chunk(b"IHDR", ihdr)
           + chunk(b"IDAT", zlib.compress(b"".join(rows), 9))
           + chunk(b"IEND", b""))
    with open(path, "wb") as f:
        f.write(png)
    print(f"{path}: {len(png)} bytes")


if __name__ == "__main__":
    import os
    assets = os.path.join(os.path.dirname(os.path.abspath(__file__)),
                          "..", "site", "assets")
    make_png(os.path.join(assets, "favicon-32.png"), 32)
    make_png(os.path.join(assets, "apple-touch-icon.png"), 180)
