#!/usr/bin/env python3
"""重新生成 NovaVei 图标集: 4角星芒 + 双弧线, 与 logo.svg 同源同色。
纯标准库实现(多边形扫描填充 + 超采样抗锯齿 + 手写 PNG 编码)。"""
import struct, zlib, math

COLOR = (70, 101, 81)  # #466551
STAR = [(50,12),(56.5,41.5),(85,48),(56.5,54.5),(50,84),(43.5,54.5),(15,48),(43.5,41.5)]
ARC1 = [(16,68),(33,82),(50,82),(67,82),(84,68)]   # 两段二次贝塞尔: 控制点串联
ARC1_CTRL = [None,(33,82),None,(67,82),None]
ARC2 = [(26,80),(38,90),(50,90),(62,90),(74,80)]
SS = 4  # 超采样倍数

def point_in_poly(x, y, poly):
    inside = False
    n = len(poly)
    for i in range(n):
        x1,y1 = poly[i]; x2,y2 = poly[(i+1)%n]
        if (y1 > y) != (y2 > y):
            xin = x1 + (y - y1) * (x2 - x1) / (y2 - y1)
            if x < xin:
                inside = not inside
    return inside

def quad_points(p0, p1, p2, steps=24):
    pts=[]
    for i in range(steps+1):
        t=i/steps; mt=1-t
        pts.append((mt*mt*p0[0]+2*mt*t*p1[0]+t*t*p2[0],
                    mt*mt*p0[1]+2*mt*t*p1[1]+t*t*p2[1]))
    return pts

def render(size, with_arcs=True):
    S = size * SS
    scale = S / 100.0
    buf = [[0]*S for _ in range(S)]

    def stamp(cx, cy, radius):
        cx*=scale; cy*=scale; r=radius*scale
        x0,x1 = max(0,int(cx-r-1)), min(S-1,int(cx+r+1))
        y0,y1 = max(0,int(cy-r-1)), min(S-1,int(cy+r+1))
        rr=r*r
        for yy in range(y0,y1+1):
            for xx in range(x0,x1+1):
                dx=xx-cx; dy=yy-cy
                if dx*dx+dy*dy <= rr:
                    buf[yy][xx]=255

    # 星形填充
    xs=[p[0] for p in STAR]; ys=[p[1] for p in STAR]
    for yy in range(int(min(ys)*scale), int(max(ys)*scale)+1):
        for xx in range(int(min(xs)*scale), int(max(xs)*scale)+1):
            if point_in_poly((xx+0.5)/scale, (yy+0.5)/scale, STAR):
                buf[yy][xx]=255

    if with_arcs and size >= 32:
        for i in range(1, len(ARC1), 2):
            for px,py in quad_points(ARC1[i-1], ARC1[i], ARC1[i+1]):
                stamp(px, py, 3.0)          # stroke-width 6 → 半径 3
        alpha2 = 0.55
        for i in range(1, len(ARC2), 2):
            for px,py in quad_points(ARC2[i-1], ARC2[i], ARC2[i+1]):
                stamp(px, py, 2.0)          # stroke-width 4 → 半径 2

    # 超采样降采样 → RGBA
    rgba = bytearray()
    for y in range(size):
        for x in range(size):
            acc=0
            for dy in range(SS):
                row=buf[y*SS+dy]
                for dx in range(SS):
                    acc += row[x*SS+dx]
            cov = acc // (SS*SS)  # 0..255
            rgba += bytes((*COLOR, cov))
    return bytes(rgba)

def png_bytes(size, rgba):
    def chunk(t, d):
        c = struct.pack('>I', len(d)) + t + d
        return c + struct.pack('>I', zlib.crc32(t + d) & 0xffffffff)
    ihdr = struct.pack('>IIBBBBB', size, size, 8, 6, 0, 0, 0)
    raw = b''.join(b'\x00' + rgba[y*size*4:(y+1)*size*4] for y in range(size))
    return (b'\x89PNG\r\n\x1a\n' + chunk(b'IHDR', ihdr)
            + chunk(b'IDAT', zlib.compress(raw, 9)) + chunk(b'IEND', b''))

def write_png(path, size, rgba):
    open(path,'wb').write(png_bytes(size, rgba))

# favicon.ico: 16/32/48 三帧 PNG 打包
sizes = [16, 32, 48]
blobs = [png_bytes(s, render(s, with_arcs=(s>=32))) for s in sizes]
ico = struct.pack('<HHH', 0, 1, len(blobs))
offset = 6 + 16*len(blobs)
for s, p in zip(sizes, blobs):
    ico += struct.pack('<BBBBHHII', s, s, 0, 0, 1, 32, len(p), offset)
    offset += len(p)
ico += b''.join(blobs)
open('web/public/favicon.ico','wb').write(ico)

write_png('web/public/apple-icon.png', 180, render(180))
write_png('web/public/web-app-manifest-192x192.png', 192, render(192))
write_png('web/public/web-app-manifest-512x512.png', 512, render(512))
print("generated:", [f"{s}x{s}" for s in sizes], "+180/192/512")
