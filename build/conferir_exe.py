"""Confere o .exe gerado: lê a seção de recursos e valida ícones e versão."""
import struct, sys
d = open(sys.argv[1], 'rb').read()
pe = struct.unpack_from('<I', d, 0x3c)[0]
assert d[pe:pe+4] == b'PE\0\0'
nsec = struct.unpack_from('<H', d, pe+6)[0]
optsz = struct.unpack_from('<H', d, pe+20)[0]
opt = pe + 24
assert struct.unpack_from('<H', d, opt)[0] == 0x20b  # PE32+
subsys = struct.unpack_from('<H', d, opt+68)[0]
rsrc_rva, rsrc_size = struct.unpack_from('<II', d, opt+112+8*2)
secs = []
for i in range(nsec):
    o = opt + optsz + 40*i
    name = d[o:o+8].rstrip(b'\0').decode()
    vsize, va, rawsz, rawptr = struct.unpack_from('<IIII', d, o+8)
    secs.append((name, va, vsize, rawptr, rawsz))
def rva2off(rva):
    for n, va, vs, rp, rs in secs:
        if va <= rva < va + max(vs, rs):
            return rp + rva - va
    raise ValueError(hex(rva))
base = rva2off(rsrc_rva)
def walk(off, depth, path, out):
    named, ids = struct.unpack_from('<HH', d, base+off+12)
    for i in range(named+ids):
        nid, val = struct.unpack_from('<II', d, base+off+16+8*i)
        if val & 0x80000000:
            walk(val & 0x7fffffff, depth+1, path+[nid], out)
        else:
            rva, size = struct.unpack_from('<II', d, base+val)
            o = rva2off(rva)
            assert rsrc_rva <= rva and rva+size <= rsrc_rva+rsrc_size, 'dado fora da seção'
            out[tuple(path+[nid])] = d[o:o+size]
res = {}
walk(0, 0, [], res)
icons = {k[1]: v for k, v in res.items() if k[0] == 3}
assert all(v[:8] == b'\x89PNG\r\n\x1a\n' for v in icons.values()), 'ícone inválido'
grp = [v for k, v in res.items() if k[0] == 14][0]
r, t, n = struct.unpack_from('<HHH', grp, 0)
assert (r, t, n) == (0, 1, len(icons))
for i in range(n):
    w, h, cc, rs, pl, bc, sz, iid = struct.unpack_from('<BBBBHHIH', grp, 6+14*i)
    assert iid in icons and len(icons[iid]) == sz
ver = [v for k, v in res.items() if k[0] == 16][0]
wl, vl, ty = struct.unpack_from('<HHH', ver, 0)
assert wl == len(ver) and vl == 52 and ver[6:6+30].decode('utf-16-le') == 'VS_VERSION_INFO'
sig = struct.unpack_from('<I', ver, 40)[0]
assert sig == 0xFEEF04BD, hex(sig)
txt = ver.decode('utf-16-le', errors='ignore')
print('subsistema', subsys, '(2 = janela, sem console)')
print('ícones:', len(icons), 'tamanhos ok; versão:', 'FileVersion' in txt and 'Next TS' in txt)
print('EXE OK')
