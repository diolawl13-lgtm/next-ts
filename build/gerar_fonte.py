"""Gera fonte_dados.go: as letras usadas na faixa do vídeo da gravação.

Desenha cada letra da fonte DejaVu Sans Mono (licença livre) numa grade de
tamanho fixo e guarda a transparência de cada ponto (0 a 255).
Uso: python3 gerar_fonte.py <pasta_do_projeto>
"""
import base64
import sys
from PIL import Image, ImageDraw, ImageFont

ROOT = sys.argv[1]
FONT = "/usr/share/fonts/truetype/dejavu/DejaVuSansMono.ttf"
SIZE = 15

chars = [chr(c) for c in range(0x20, 0x7F)] + [chr(c) for c in range(0xA0, 0x100)]
chars += list("–—‘’“”•…→")

f = ImageFont.truetype(FONT, SIZE)
asc, desc = f.getmetrics()
W = round(f.getlength("M"))
H = asc + desc + 2

data = bytearray()
for ch in chars:
    im = Image.new("L", (W, H), 0)
    ImageDraw.Draw(im).text((0, 1), ch, font=f, fill=255)
    data += im.tobytes()

runes = "".join(chars).replace("\\", "\\\\").replace('"', '\\"')
b64 = base64.b64encode(bytes(data)).decode()
lines = [b64[i:i + 100] for i in range(0, len(b64), 100)]
out = [
    "// Código gerado por build/gerar_fonte.py. NÃO EDITE À MÃO.",
    "// Letras da fonte DejaVu Sans Mono (licença livre), usadas na faixa do vídeo.",
    "",
    "package main",
    "",
    f"const fontW, fontH = {W}, {H}",
    "",
    f'const fontRunes = "{runes}"',
    "",
    "const fontAlphaB64 = \"\" +",
]
out += [f'\t"{ln}" +' for ln in lines[:-1]] + [f'\t"{lines[-1]}"', ""]
open(f"{ROOT}/fonte_dados.go", "w", encoding="utf-8").write("\n".join(out))
print(f"fonte: {len(chars)} letras de {W}x{H} pontos")
