"""Generate the Mihomo-fpk application icons (flat, anti-aliased, no deps)."""
import math
import os
import struct
import zlib

OUT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

# logical design canvas is 48x48, matching the in-app SVG logo
CANVAS = 48.0
GRAD_A = (0x63, 0x66, 0xF1)
GRAD_B = (0x0E, 0xA5, 0xE9)
RECT_X0, RECT_Y0, RECT_X1, RECT_Y1 = 2.0, 2.0, 46.0, 46.0
RECT_R = 12.0
STROKE = 3.4
POLYLINE = [(12.0, 33.0), (12.0, 19.0), (20.0, 27.0), (28.0, 19.0), (28.0, 33.0)]
DOT_C = (34.0, 33.0)
DOT_R = 3.4


def clamp01(v):
    return 0.0 if v < 0.0 else (1.0 if v > 1.0 else v)


def sd_round_rect(px, py, x0, y0, x1, y1, r):
    cx, cy = (x0 + x1) / 2.0, (y0 + y1) / 2.0
    hw, hh = (x1 - x0) / 2.0, (y1 - y0) / 2.0
    qx = abs(px - cx) - (hw - r)
    qy = abs(py - cy) - (hh - r)
    return math.hypot(max(qx, 0.0), max(qy, 0.0)) + min(max(qx, qy), 0.0) - r


def sd_segment(px, py, ax, ay, bx, by):
    vx, vy = bx - ax, by - ay
    wx, wy = px - ax, py - ay
    denom = vx * vx + vy * vy
    t = 0.0 if denom == 0.0 else (wx * vx + wy * vy) / denom
    t = 0.0 if t < 0.0 else (1.0 if t > 1.0 else t)
    return math.hypot(px - (ax + t * vx), py - (ay + t * vy))


def coverage(d, half):
    """1px wide analytic anti-aliasing around the given half-thickness."""
    return clamp01(0.5 - (d - half))


def gradient(px, py):
    t = ((px - RECT_X0) + (py - RECT_Y0)) / ((RECT_X1 - RECT_X0) + (RECT_Y1 - RECT_Y0))
    t = clamp01(t)
    return tuple(GRAD_A[i] + (GRAD_B[i] - GRAD_A[i]) * t for i in range(3))


def render(size):
    scale = size / CANVAS
    pixels = bytearray(size * size * 4)
    half = size / 2.0
    for y in range(size):
        ly = (y + 0.5) / scale
        for x in range(size):
            lx = (x + 0.5) / scale

            rect_a = coverage(sd_round_rect(lx, ly, RECT_X0, RECT_Y0, RECT_X1, RECT_Y1, RECT_R), 0.0)
            if rect_a <= 0.0:
                continue

            r, g, b = gradient(lx, ly)

            # white glyph: polyline + dot
            d = 1e9
            for i in range(len(POLYLINE) - 1):
                ax, ay = POLYLINE[i]
                bx, by = POLYLINE[i + 1]
                dd = sd_segment(lx, ly, ax, ay, bx, by)
                if dd < d:
                    d = dd
            glyph_a = coverage(d, STROKE / 2.0)

            dot_d = math.hypot(lx - DOT_C[0], ly - DOT_C[1])
            dot_a = coverage(dot_d, DOT_R)
            if dot_a > glyph_a:
                glyph_a = dot_a

            if glyph_a > 0.0:
                r = r + (255.0 - r) * glyph_a
                g = g + (255.0 - g) * glyph_a
                b = b + (255.0 - b) * glyph_a

            idx = (y * size + x) * 4
            pixels[idx] = int(r + 0.5)
            pixels[idx + 1] = int(g + 0.5)
            pixels[idx + 2] = int(b + 0.5)
            pixels[idx + 3] = int(rect_a * 255.0 + 0.5)
    return pixels


def write_png(path, size, pixels):
    raw = bytearray()
    stride = size * 4
    for y in range(size):
        raw.append(0)
        raw += pixels[y * stride:(y + 1) * stride]

    def chunk(tag, data):
        out = struct.pack('>I', len(data)) + tag + data
        out += struct.pack('>I', zlib.crc32(tag + data) & 0xFFFFFFFF)
        return out

    png = b'\x89PNG\r\n\x1a\n'
    png += chunk(b'IHDR', struct.pack('>IIBBBBB', size, size, 8, 6, 0, 0, 0))
    png += chunk(b'IDAT', zlib.compress(bytes(raw), 9))
    png += chunk(b'IEND', b'')
    with open(path, 'wb') as f:
        f.write(png)
    print('wrote', path, size, 'x', size, len(png), 'bytes')


def main():
    targets = [
        (512, ['ICON_512.PNG']),
        (256, ['ICON_256.PNG', os.path.join('app', 'ui', 'images', 'icon_256.png')]),
        (128, ['ICON.PNG']),
        (64, [os.path.join('app', 'ui', 'images', 'icon_64.png')]),
    ]
    cache = {}
    for size, names in targets:
        if size not in cache:
            cache[size] = render(size)
        for name in names:
            path = os.path.join(OUT, name)
            os.makedirs(os.path.dirname(path), exist_ok=True)
            write_png(path, size, cache[size])


if __name__ == '__main__':
    main()
