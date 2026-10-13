"""Gera o ícone e o arquivo de recursos (rsrc_windows_amd64.syso) do Next TS.

O .syso é um objeto COFF com a seção .rsrc (ícone + informações de versão),
que o compilador Go embute automaticamente no .exe.
Uso: python3 gerar_recursos.py <pasta_do_projeto> <versao ex. 1.0.0>
"""
import io
import struct
import sys
from PIL import Image, ImageDraw, ImageFont

ROOT, VERSION = sys.argv[1], sys.argv[2]
FONT = "/usr/share/fonts/truetype/dejavu/DejaVuSerif-Bold.ttf"


def draw_icon(size=256):
    s = size * 4  # desenha grande e reduz (bordas suaves)
    im = Image.new("RGBA", (s, s), (0, 0, 0, 0))
    d = ImageDraw.Draw(im)
    r = int(s * 0.2)
    d.rounded_rectangle([0, 0, s - 1, s - 1], r, fill=(27, 30, 36, 255))
    w = int(s * 0.045)
    d.rounded_rectangle([w, w, s - 1 - w, s - 1 - w], r - w, outline=(217, 173, 69, 255), width=w)
    f = ImageFont.truetype(FONT, int(s * 0.66))
    bb = d.textbbox((0, 0), "N", font=f)
    tw, th = bb[2] - bb[0], bb[3] - bb[1]
    d.text(((s - tw) / 2 - bb[0], (s - th) / 2 - bb[1] - s * 0.02), "N", font=f, fill=(240, 200, 98, 255))
    # setinha "próximo" no canto
    a = s * 0.78
    d.polygon([(a - s * 0.07, s * 0.70), (a + s * 0.06, s * 0.78), (a - s * 0.07, s * 0.86)], fill=(61, 187, 114, 255))
    return im.resize((size, size), Image.LANCZOS)


SIZES = [16, 20, 24, 32, 40, 48, 64, 128, 256]


def icon_images():
    big = draw_icon(256)
    out = []
    for sz in SIZES:
        img = big if sz == 256 else draw_icon(sz) if sz <= 48 else big.resize((sz, sz), Image.LANCZOS)
        buf = io.BytesIO()
        img.save(buf, format="PNG")  # ícones PNG são aceitos desde o Windows Vista
        out.append((sz, buf.getvalue()))
    return out


def write_ico(path, imgs):
    hdr = struct.pack("<HHH", 0, 1, len(imgs))
    off = 6 + 16 * len(imgs)
    entries, data = b"", b""
    for sz, png in imgs:
        b = 0 if sz >= 256 else sz
        entries += struct.pack("<BBBBHHII", b, b, 0, 0, 1, 32, len(png), off + len(data))
        data += png
    open(path, "wb").write(hdr + entries + data)


# ---------- VS_VERSIONINFO ----------

def utf16z(s):
    return s.encode("utf-16-le") + b"\0\0"


def pad4(b):
    return b + b"\0" * ((4 - len(b) % 4) % 4)


def vblock(key, value=b"", vtype=0, children=(), value_len=None):
    body = struct.pack("<HHH", 0, 0, 0) + utf16z(key)
    body = pad4(body)
    body += value
    for ch in children:
        body = pad4(body) + ch
    vl = len(value) if value_len is None else value_len
    return struct.pack("<HHH", len(body), vl, vtype) + body[6:]


def version_info():
    parts = [int(x) for x in VERSION.split(".")] + [0] * 4
    ms, ls = (parts[0] << 16) | parts[1], (parts[2] << 16) | parts[3]
    fixed = struct.pack("<13I", 0xFEEF04BD, 0x00010000, ms, ls, ms, ls, 0x3F, 0, 0x00040004, 1, 0, 0, 0)
    strings = [("CompanyName", "Next TS"), ("FileDescription", "Next TS - next automático no TeamSpeak 3"),
               ("FileVersion", VERSION), ("InternalName", "NextTS"), ("LegalCopyright", "Uso pessoal"),
               ("OriginalFilename", "Next TS.exe"), ("ProductName", "Next TS"), ("ProductVersion", VERSION)]
    sblocks = [vblock(k, utf16z(v), 1, value_len=len(v) + 1) for k, v in strings]
    table = vblock("041604B0", b"", 1, sblocks)
    sfi = vblock("StringFileInfo", b"", 1, [table])
    var = vblock("Translation", struct.pack("<HH", 0x0416, 0x04B0), 0)
    vfi = vblock("VarFileInfo", b"", 1, [var])
    return vblock("VS_VERSION_INFO", fixed, 0, [sfi, vfi])


# ---------- Árvore de recursos + COFF ----------

def build_rsrc(resources):
    """resources: {type_id: {name_id: bytes}} -> (section_bytes, reloc_offsets)."""
    LANG = 0x0416
    types = sorted(resources)
    # Calcula tamanhos: diretórios primeiro, depois entradas de dados, depois dados.
    dirs = []  # (offset placeholder) montamos em duas passadas
    n_type = len(types)
    root_size = 16 + 8 * n_type
    layout = []
    off = root_size
    type_dir_off = {}
    for t in types:
        type_dir_off[t] = off
        off += 16 + 8 * len(resources[t])
    name_dir_off = {}
    for t in types:
        for n in sorted(resources[t]):
            name_dir_off[(t, n)] = off
            off += 16 + 8
    data_entry_off = {}
    for t in types:
        for n in sorted(resources[t]):
            data_entry_off[(t, n)] = off
            off += 16
    data_off = {}
    for t in types:
        for n in sorted(resources[t]):
            off = (off + 7) & ~7
            data_off[(t, n)] = off
            off += len(resources[t][n])
    sec = bytearray(off)
    relocs = []

    def dirhdr(pos, count):
        struct.pack_into("<IIHHHH", sec, pos, 0, 0, 0, 0, 0, count)

    dirhdr(0, n_type)
    for i, t in enumerate(types):
        struct.pack_into("<II", sec, 16 + 8 * i, t, 0x80000000 | type_dir_off[t])
        names = sorted(resources[t])
        dirhdr(type_dir_off[t], len(names))
        for j, n in enumerate(names):
            struct.pack_into("<II", sec, type_dir_off[t] + 16 + 8 * j, n, 0x80000000 | name_dir_off[(t, n)])
            dirhdr(name_dir_off[(t, n)], 1)
            struct.pack_into("<II", sec, name_dir_off[(t, n)] + 16, LANG, data_entry_off[(t, n)])
            de = data_entry_off[(t, n)]
            struct.pack_into("<IIII", sec, de, data_off[(t, n)], len(resources[t][n]), 0, 0)
            relocs.append(de)  # OffsetToData é um RVA: o linker soma o endereço da seção
            sec[data_off[(t, n)]:data_off[(t, n)] + len(resources[t][n])] = resources[t][n]
    return bytes(sec), relocs


def coff(section, relocs):
    hdr_size = 20 + 40
    reloc_off = hdr_size + len(section)
    sym_off = reloc_off + 10 * len(relocs)
    out = struct.pack("<HHIIIHH", 0x8664, 1, 0, sym_off, 1, 0, 0)
    out += struct.pack("<8sIIIIIIHHI", b".rsrc", 0, 0, len(section), hdr_size, reloc_off, 0, len(relocs), 0, 0x40000040)
    out += section
    for r in relocs:
        out += struct.pack("<IIH", r, 0, 3)  # IMAGE_REL_AMD64_ADDR32NB
    out += struct.pack("<8sIhHBB", b".rsrc", 0, 1, 0, 3, 0)
    out += struct.pack("<I", 4)  # tabela de nomes vazia
    return out


imgs = icon_images()
write_ico(f"{ROOT}/build/icone.ico", imgs)
draw_icon(256).save(f"{ROOT}/build/icone.png")
RT_ICON, RT_GROUP_ICON, RT_VERSION = 3, 14, 16
icons = {i + 1: png for i, (sz, png) in enumerate(imgs)}
grp = struct.pack("<HHH", 0, 1, len(imgs))
for i, (sz, png) in enumerate(imgs):
    b = 0 if sz >= 256 else sz
    grp += struct.pack("<BBBBHHIH", b, b, 0, 0, 1, 32, len(png), i + 1)
sec, rel = build_rsrc({RT_ICON: icons, RT_GROUP_ICON: {1: grp}, RT_VERSION: {1: version_info()}})
open(f"{ROOT}/rsrc_windows_amd64.syso", "wb").write(coff(sec, rel))
print("ok:", len(sec), "bytes de recursos,", len(rel), "relocações")
