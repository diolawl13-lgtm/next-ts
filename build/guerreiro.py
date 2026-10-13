"""Guerreiro de armadura de ferro (pixel art original): chorando e comemorando.

Uso: python3 guerreiro.py <saida.png>   -> imagem de conferência
     python3 guerreiro.py json         -> grades para o app
"""
import json
import math
import sys
from PIL import Image

PAL = {
    "K": "#140f0b",                                   # contorno
    "I": "#aab4bd", "i": "#6c7680", "J": "#3e464d",   # ferro claro / sombra / escuro
    "j": "#7d868e", "q": "#59626a",                   # cota de malha
    "F": "#d8342b", "f": "#8f1f19",                   # pena vermelha
    "S": "#f0be92", "s": "#c98d63",                   # pele
    "E": "#2a1a10",                                   # olhos
    "T": "#4fc3ff", "t": "#c8f0ff",                   # lágrimas
    "M": "#5a1616", "m": "#d9534f",                   # boca
    "B": "#5b3a1e", "b": "#e2b33c",                   # cinto / ouro
    "Z": "#9a6531", "z": "#6b4220",                   # madeira do escudo
    "O": "#2e2a26",                                   # bota
    "W": "#d7dde2", "w": "#8f9aa3", "G": "#d9a43c", "g": "#8a6420",  # espada
}

HALF = [
    "..........KF",
    ".........KFF",
    ".........KfF",
    ".......KKKKf",
    ".....KKIIIII",
    "....KIIIIIII",
    "....KIiIIIII",
    "...KIIiJJJJJ",
    "...KIiJSSSSS",
    "...KIiJSEESS",
    "...KIiJSsTSS",
    "...KIiJSSTSK",
    "...KIiJSSTKM",
    "...KIIiJJJJJ",
    "....KIiIIIII",
    "..KKIIIiIIII",
    ".KIIIKJjqjqj",
    ".KIiIKjqjqjq",
    ".KIiKqjqjqjq",
    ".KSSKjqjqjqj",
    "..KKKBBBBBBb",
    "....KjqjqjqK",
    "....KqjqjqjK",
    ".....KIIIIK.",
    ".....KIiIIK.",
    ".....KIiIIK.",
    "....KOOOOOK.",
    "....KKKKKKK.",
]

W, OFF = 44, 8


def blank(h):
    return [list("." * W) for _ in range(h)]


def put(g, y, x, c):
    if 0 <= y < len(g) and 0 <= x < W:
        g[y][x] = c


def body(g):
    for y, h in enumerate(HALF):
        for x, ch in enumerate(h + h[::-1]):
            if ch != ".":
                g[y][x + OFF] = ch
    # a pena cai um pouco para trás (assimétrica)
    for (y, x) in [(0, OFF + 14), (1, OFF + 15), (1, OFF + 14), (2, OFF + 16), (2, OFF + 15), (3, OFF + 16)]:
        put(g, y, x, "F" if y < 2 else "f")


def shield(g, cy, cx, r=6.0):
    for y in range(int(cy - r - 1), int(cy + r + 2)):
        for x in range(int(cx - r - 1), int(cx + r + 2)):
            d = math.hypot(y - cy, x - cx)
            if d > r + 0.5:
                continue
            if d > r - 0.4:
                c = "K"
            elif d > r - 1.4:
                c = "I" if (y - cy) < 0 else "i"
            elif d <= 1.3:
                c = "b"
            elif d <= 2.0:
                c = "K"
            else:
                c = "Z" if (x - int(cx)) % 3 else "z"
            put(g, y, x, c)


def cry():
    g = blank(len(HALF) + 4)
    body(g)
    shield(g, 19, OFF + 26)
    # lágrimas voando e escorrendo
    for (y, x, c) in [(9, OFF + 4, "T"), (10, OFF + 2, "T"), (8, OFF + 1, "t"), (9, OFF + 19, "T"), (10, OFF + 21, "T"), (8, OFF + 22, "t")]:
        put(g, y, x, c)
    base = len(HALF)
    for x in range(OFF + 4, OFF + 20):
        put(g, base, x, "T" if x % 3 else "t")
    # espada caída
    sy = base + 1
    for i, ch in enumerate("gGGKWWWWWWWWWWWWw"):
        put(g, sy, i + 1, ch)
    put(g, sy - 1, 3, "G")
    put(g, sy + 1, 3, "G")
    return g


def happy():
    g = blank(len(HALF) + 4)
    body(g)
    # rosto feliz: olhos ^ ^, sorriso, sem lágrimas
    for x in range(OFF + 7, OFF + 17):
        for y in range(8, 13):
            if g[y][x] in "TtEMK":
                g[y][x] = "S"
    for (y, x) in [(9, OFF + 8), (8, OFF + 9), (9, OFF + 10), (9, OFF + 13), (8, OFF + 14), (9, OFF + 15)]:
        put(g, y, x, "E")
    for (y, x) in [(11, OFF + 9), (12, OFF + 10), (12, OFF + 11), (12, OFF + 12), (12, OFF + 13), (11, OFF + 14)]:
        put(g, y, x, "M")
    for (y, x) in [(11, OFF + 8), (11, OFF + 15)]:
        put(g, y, x, "m")
    # escudo erguido acima do ombro
    shield(g, 9, OFF + 27)
    for y in range(14, 17):
        put(g, y, OFF + 23, "I")
        put(g, y, OFF + 24, "i")
    # espada erguida na outra mão
    for y in range(3, 17):
        put(g, y, OFF - 4, "W")
        put(g, y, OFF - 3, "w")
    put(g, 2, OFF - 4, "W")
    for x in range(OFF - 6, OFF): put(g, 17, x, "G")
    put(g, 18, OFF - 4, "g"); put(g, 18, OFF - 3, "g")
    put(g, 19, OFF - 4, "S"); put(g, 19, OFF - 3, "S"); put(g, 19, OFF - 2, "S")
    # brilho de comemoração
    for (y, x) in [(1, OFF + 33), (3, OFF + 35), (0, OFF - 1), (5, OFF + 31)]:
        put(g, y, x, "b")
    return g


def to_rows(g):
    return ["".join(r) for r in g]


if __name__ == "__main__":
    a, b = to_rows(cry()), to_rows(happy())
    if sys.argv[1] == "json":
        print(json.dumps({"cry": a, "happy": b}))
        sys.exit()
    sc = 10
    im = Image.new("RGB", (W * sc * 2 + 40, len(a) * sc), (40, 36, 30))
    for k, g in enumerate((a, b)):
        for y, r in enumerate(g):
            for x, ch in enumerate(r):
                if ch in PAL:
                    c = PAL[ch]
                    rgb = tuple(int(c[i:i + 2], 16) for i in (1, 3, 5))
                    x0 = k * (W * sc + 40) + x * sc
                    for dy in range(sc):
                        for dx in range(sc):
                            im.putpixel((x0 + dx, y * sc + dy), rgb)
    im.save(sys.argv[1])
    print("ok")
