package main

// JPEG por faixas, para gravar a 30 quadros por segundo sem recomprimir a tela toda.
//
// A imagem é dividida em faixas horizontais de 16 linhas (uma "linha de blocos" do
// JPEG 4:2:0). Cada faixa é comprimida sozinha pelo codificador JPEG do próprio Go,
// e os dados dela viram um trecho independente do JPEG final, separado dos outros
// por "marcadores de reinício" (RST0..RST7, parte do JPEG padrão desde sempre).
// Quando só uma parte da tela muda (ex.: o aviso que rola na barra do TS), só as
// faixas dessa parte são comprimidas de novo; as outras são reaproveitadas.
//
// O JPEG montado é baseline, 4:2:0, com DRI = blocos por linha, e decodifica igual
// à imagem inteira comprimida de uma vez (cada faixa começa com os valores DC
// zerados, exatamente como depois de um marcador de reinício).

import (
	"bytes"
	"errors"
	"image"
	"image/jpeg"
)

const (
	bandH    = 16 // altura de uma faixa (uma linha de blocos 16x16 do JPEG 4:2:0)
	jpegQ    = 80 // qualidade (a mesma de antes)
	soiEOILn = 4
)

// bandCodec comprime faixas de uma imagem w x h e monta o JPEG completo.
type bandCodec struct {
	w, h   int
	bands  int
	header []byte // SOI + DQT + SOF0 (w x h) + DHT + DRI + SOS
}

func newBandCodec(w, h int) (*bandCodec, error) {
	if w <= 0 || h <= 0 || w >= 1<<16 || h >= 1<<16 {
		return nil, errors.New("tamanho de imagem inválido para JPEG")
	}
	c := &bandCodec{w: w, h: h, bands: (h + bandH - 1) / bandH}
	// Uma faixa qualquer comprimida pelo Go dá as tabelas (iguais em todas as faixas).
	var enc bandEncoder
	probe, err := enc.encodeRaw(image.NewRGBA(image.Rect(0, 0, w, min(h, bandH))))
	if err != nil {
		return nil, err
	}
	dqt, dht, err := jpegTables(probe)
	if err != nil {
		return nil, err
	}
	mcus := (w + 15) / 16
	if mcus >= 1<<16 {
		return nil, errors.New("imagem larga demais")
	}
	hd := []byte{0xff, 0xd8}
	hd = append(hd, dqt...)
	hd = append(hd, 0xff, 0xc0, 0x00, 0x11, 8, byte(h>>8), byte(h), byte(w>>8), byte(w), 3,
		1, 0x22, 0, 2, 0x11, 1, 3, 0x11, 1) // SOF0: 3 componentes, 4:2:0
	hd = append(hd, dht...)
	hd = append(hd, 0xff, 0xdd, 0x00, 0x04, byte(mcus>>8), byte(mcus)) // DRI: um trecho por faixa
	hd = append(hd, 0xff, 0xda, 0x00, 0x0c, 0x03, 0x01, 0x00, 0x02, 0x11, 0x03, 0x11, 0x00, 0x3f, 0x00)
	c.header = hd
	return c, nil
}

// bandRows: linhas da faixa i (a última pode ter menos de 16).
func (c *bandCodec) bandRows(i int) (y0, y1 int) {
	y0 = i * bandH
	return y0, min(y0+bandH, c.h)
}

// jpegSize: tamanho do JPEG montado com estas faixas.
func (c *bandCodec) jpegSize(segs [][]byte) int {
	n := len(c.header) + 2 + 2*(len(segs)-1)
	for _, s := range segs {
		n += len(s)
	}
	return n
}

// assemble monta o JPEG completo (cabeçalho + faixas com RST entre elas + EOI) em dst.
func (c *bandCodec) assemble(dst []byte, segs [][]byte) []byte {
	dst = append(dst[:0], c.header...)
	for i, s := range segs {
		if i > 0 {
			dst = append(dst, 0xff, 0xd0+byte((i-1)%8))
		}
		dst = append(dst, s...)
	}
	return append(dst, 0xff, 0xd9)
}

// bandEncoder comprime uma faixa. Cada um tem os próprios buffers (um por trabalhador).
type bandEncoder struct {
	out  flushBuffer
	rgba *image.RGBA
}

// flushBuffer evita que o codificador do Go crie um buffer novo a cada faixa.
type flushBuffer struct{ bytes.Buffer }

func (*flushBuffer) Flush() error { return nil }

// encode comprime as linhas y0..y1 de uma imagem BGRA (w x h, 4 bytes por ponto)
// e devolve só os dados da faixa (sem cabeçalho), numa cópia nova.
func (e *bandEncoder) encode(bgra []byte, w, y0, y1 int) ([]byte, error) {
	rows := y1 - y0
	if e.rgba == nil || e.rgba.Rect.Dx() != w || e.rgba.Rect.Dy() != rows {
		e.rgba = image.NewRGBA(image.Rect(0, 0, w, rows))
	}
	src := bgra[y0*w*4 : y1*w*4]
	dst := e.rgba.Pix
	for i := 0; i+3 < len(src); i += 4 { // BGRA → RGBA (só a faixa)
		dst[i], dst[i+1], dst[i+2], dst[i+3] = src[i+2], src[i+1], src[i], 255
	}
	raw, err := e.encodeRaw(e.rgba)
	if err != nil {
		return nil, err
	}
	scan, err := jpegScan(raw)
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), scan...), nil
}

func (e *bandEncoder) encodeRaw(img *image.RGBA) ([]byte, error) {
	e.out.Reset()
	if err := jpeg.Encode(&e.out, img, &jpeg.Options{Quality: jpegQ}); err != nil {
		return nil, err
	}
	return e.out.Bytes(), nil
}

// jpegSegments percorre os marcadores do JPEG do Go até o início da varredura (SOS).
func jpegSegments(b []byte, fn func(marker byte, seg []byte)) (scanStart int, err error) {
	if len(b) < soiEOILn || b[0] != 0xff || b[1] != 0xd8 {
		return 0, errors.New("JPEG inválido")
	}
	i := 2
	for i+4 <= len(b) {
		if b[i] != 0xff {
			return 0, errors.New("JPEG inválido")
		}
		m := b[i+1]
		n := int(b[i+2])<<8 | int(b[i+3])
		if i+2+n > len(b) {
			return 0, errors.New("JPEG cortado")
		}
		fn(m, b[i:i+2+n])
		if m == 0xda {
			return i + 2 + n, nil
		}
		i += 2 + n
	}
	return 0, errors.New("JPEG sem varredura")
}

// jpegScan devolve os dados comprimidos (entre o SOS e o EOI).
func jpegScan(b []byte) ([]byte, error) {
	start, err := jpegSegments(b, func(byte, []byte) {})
	if err != nil {
		return nil, err
	}
	if len(b) < start+2 || b[len(b)-2] != 0xff || b[len(b)-1] != 0xd9 {
		return nil, errors.New("JPEG sem fim")
	}
	return b[start : len(b)-2], nil
}

// jpegTables devolve os blocos DQT e DHT (tabelas) de um JPEG do Go.
func jpegTables(b []byte) (dqt, dht []byte, err error) {
	_, err = jpegSegments(b, func(m byte, seg []byte) {
		switch m {
		case 0xdb:
			dqt = append(dqt, seg...)
		case 0xc4:
			dht = append(dht, seg...)
		}
	})
	if err == nil && (dqt == nil || dht == nil) {
		err = errors.New("JPEG sem tabelas")
	}
	return dqt, dht, err
}
