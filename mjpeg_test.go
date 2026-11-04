package main

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

// testPicture desenha uma imagem com texto, cores e degradê (parecida com uma tela).
func testPicture(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{uint8(x * 255 / w), uint8(y * 255 / h), uint8((x + y) % 256), 255})
		}
	}
	fillRect(img, image.Rect(w/4, h/4, w/2, h/2), color.RGBA{240, 240, 245, 255})
	for i := 0; i < 12; i++ {
		drawText(img, 10, 20+i*fontH, "<20:15:03> \"Real Calvon\": !next C1 — Você entrou na posição 1", color.RGBA{10, 10, 20, 255}, 1)
	}
	return img
}

func toBGRA(img *image.RGBA) []byte {
	out := make([]byte, len(img.Pix))
	for i := 0; i < len(out); i += 4 {
		out[i], out[i+1], out[i+2], out[i+3] = img.Pix[i+2], img.Pix[i+1], img.Pix[i], 255
	}
	return out
}

// encodeBands comprime todas as faixas de uma imagem BGRA.
func encodeBands(t testing.TB, c *bandCodec, bgra []byte) [][]byte {
	t.Helper()
	var enc bandEncoder
	segs := make([][]byte, c.bands)
	for i := range segs {
		y0, y1 := c.bandRows(i)
		s, err := enc.encode(bgra, c.w, y0, y1)
		if err != nil {
			t.Fatal(err)
		}
		segs[i] = s
	}
	return segs
}

func TestBandJPEGIgualAoInteiro(t *testing.T) {
	for _, sz := range [][2]int{{640, 400}, {1001, 403}, {17, 9}, {2560, 37}} {
		w, h := sz[0], sz[1]
		img := testPicture(w, h)
		c, err := newBandCodec(w, h)
		if err != nil {
			t.Fatal(err)
		}
		segs := encodeBands(t, c, toBGRA(img))
		jp := c.assemble(nil, segs)
		if len(jp) != c.jpegSize(segs) {
			t.Fatalf("%dx%d: tamanho calculado %d, real %d", w, h, c.jpegSize(segs), len(jp))
		}
		got, err := jpeg.Decode(bytes.NewReader(jp))
		if err != nil {
			t.Fatalf("%dx%d: o JPEG montado não abre: %v", w, h, err)
		}
		var whole bytes.Buffer
		jpeg.Encode(&whole, img, &jpeg.Options{Quality: jpegQ})
		want, _ := jpeg.Decode(&whole)
		if got.Bounds() != want.Bounds() {
			t.Fatalf("%dx%d: tamanho %v, esperava %v", w, h, got.Bounds(), want.Bounds())
		}
		gy, wy := got.(*image.YCbCr), want.(*image.YCbCr)
		if !bytes.Equal(gy.Y, wy.Y) || !bytes.Equal(gy.Cb, wy.Cb) || !bytes.Equal(gy.Cr, wy.Cr) {
			t.Fatalf("%dx%d: a imagem montada por faixas não é igual à comprimida inteira", w, h)
		}
	}
}

// Trocar só uma faixa dá o mesmo resultado que comprimir a imagem nova inteira.
func TestBandJPEGTrocaUmaFaixa(t *testing.T) {
	w, h := 800, 300
	img := testPicture(w, h)
	c, _ := newBandCodec(w, h)
	bgra := toBGRA(img)
	segs := encodeBands(t, c, bgra)
	drawText(img, 300, 100, "MUDOU AQUI", color.RGBA{200, 0, 0, 255}, 1) // linhas 100..120: faixas 6 e 7
	bgra2 := toBGRA(img)
	var enc bandEncoder
	for i := 0; i < c.bands; i++ {
		y0, y1 := c.bandRows(i)
		if !bytes.Equal(bgra[y0*w*4:y1*w*4], bgra2[y0*w*4:y1*w*4]) {
			segs[i], _ = enc.encode(bgra2, w, y0, y1)
		}
	}
	got, err := jpeg.Decode(bytes.NewReader(c.assemble(nil, segs)))
	if err != nil {
		t.Fatal(err)
	}
	full := encodeBands(t, c, bgra2)
	want, _ := jpeg.Decode(bytes.NewReader(c.assemble(nil, full)))
	if !bytes.Equal(got.(*image.YCbCr).Y, want.(*image.YCbCr).Y) {
		t.Fatal("trocar só as faixas que mudaram deu imagem diferente")
	}
}

func BenchmarkBandaUnica2560(b *testing.B) {
	w, h := 2560, 1369
	bgra := toBGRA(testPicture(w, h))
	c, _ := newBandCodec(w, h)
	var enc bandEncoder
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		y0, y1 := c.bandRows(i % c.bands)
		enc.encode(bgra, w, y0, y1)
	}
}
