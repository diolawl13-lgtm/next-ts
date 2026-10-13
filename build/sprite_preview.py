"""Desenha o aventureiro chorando (pixel art original) e salva PNG para conferir."""
import json
import sys
from PIL import Image

PAL = {
    "K": "#1a120c",  # contorno
    "H": "#7a5230", "h": "#55371d",  # capuz
    "S": "#f0be92", "s": "#c98d63",  # pele
    "E": "#2a1a10",  # olho fechado
    "T": "#4fc3ff", "t": "#c8f0ff",  # lágrimas
    "M": "#5a1616", "m": "#d9534f",  # boca aberta / língua
    "R": "#8e2b2b", "r": "#5e1a1a",  # capa
    "C": "#3f7d3a", "c": "#2a5727",  # túnica
    "B": "#6b4423", "b": "#e2b33c",  # cinto / fivela
    "P": "#5b4a3b", "p": "#41352a",  # calça
    "O": "#3b2a1b",  # bota
    "W": "#d7dde2", "w": "#8f9aa3", "G": "#d9a43c", "g": "#8a6420",  # espada
}

# Metade esquerda (12 colunas); o lado direito é espelhado.
HALF = [
    "........KKKK",
    "......KKHHHH",
    ".....KHHHHHH",
    "....KHHhHHHH",
    "....KHhHHHHH",
    "...KHHhKKKKK",
    "...KHhKSSSSS",
    "...KHhKSSSSS",
    "...KHKSEEESS",
    "...KHKSsTSSS",
    "...KHKSSTSSK",
    "...KHKSsTSKM",
    "...KHhKSTSKm",
    "....KhKsTssK",
    "....KhKKKKKK",
    "...KRKKCCCCC",
    "..KRRKCCCCCC",
    "..KRKCCcCCCC",
    "..KRKCKcCCCC",
    "..KRKSKCCCCC",
    "..KRKKBBBBBB",
    "..KRRKCCCCCb",
    "..KRrKCcCCCC",
    "...KrKPPPPPP",
    "...KrKPPPPPK",
    "....KKPPPpPK",
    ".....KPPPpK.",
    ".....KOOOOK.",
    "....KOOOOOK.",
    "....KKKKKKK.",
]


def build():
    rows = [h + h[::-1] for h in HALF]
    w = 40
    off = 8
    grid = [list("." * w) for _ in range(len(rows) + 4)]
    for y, r in enumerate(rows):
        for x, ch in enumerate(r):
            if ch != ".":
                grid[y][x + off] = ch
    # lágrimas voando para os lados
    for (y, x) in [(8, off + 1), (9, off - 1), (7, off - 2), (8, off + 23), (9, off + 25), (7, off + 26)]:
        grid[y][x] = "T"
    for (y, x) in [(7, off - 1), (7, off + 24)]:
        grid[y][x] = "t"
    # poça de lágrimas
    for x in range(off + 3, off + 21):
        grid[len(rows)][x] = "T" if x % 3 else "t"
    # espada caída no chão, à esquerda
    sy = len(rows) + 1
    sword = "gGGKWWWWWWWWWWWWw"
    for i, ch in enumerate(sword):
        grid[sy][i + 1] = ch
    grid[sy - 1][3] = "G"
    grid[sy + 1][3] = "G"
    return ["".join(r) for r in grid]


if __name__ == "__main__":
    g = build()
    if len(sys.argv) > 2 and sys.argv[2] == "json":
        print(json.dumps(g))
        sys.exit()
    scale = 10
    im = Image.new("RGBA", (len(g[0]) * scale, len(g) * scale), (40, 36, 30, 255))
    for y, r in enumerate(g):
        for x, ch in enumerate(r):
            if ch in PAL:
                c = PAL[ch]
                rgb = tuple(int(c[i:i + 2], 16) for i in (1, 3, 5))
                for dy in range(scale):
                    for dx in range(scale):
                        im.putpixel((x * scale + dx, y * scale + dy), rgb + (255,))
    im.save(sys.argv[1])
    print("ok", len(g[0]), "x", len(g))
