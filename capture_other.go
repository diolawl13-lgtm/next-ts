//go:build !windows

package main

// Versão para testes em Linux: uma "janela do TeamSpeak" de mentira.
//
// NEXTTS_FAKE_TS aponta para um arquivo de texto:
//   - "min"  = TS minimizado;
//   - "none" = TS fechado;
//   - "falha" = a cópia da janela falha;
//   - outro texto = desenha uma janela com essas linhas (ex.: o chat com o bot);
//     uma linha "ROLANDO: texto" vira um aviso que passa rolando na barra de baixo
//     (como o "Announcement" do TS de verdade: anda 1 ponto a cada 20 ms);
//     uma linha "RELOGIO" mostra a hora com milésimos (muda a cada captura; serve
//     para conferir no vídeo o ritmo e o instante de cada quadro).
//
// NEXTTS_FAKE_SIZE=LxA muda o tamanho da janela (padrão 1000x640) e NEXTTS_FAKE_BG
// põe uma imagem PNG de fundo (ex.: um print do TS de verdade), para medir a
// gravação com uma janela grande e parecida com a real.
// Sem NEXTTS_FAKE_TS, age como se o TS não estivesse aberto.

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"strings"
	"sync"
	"time"
)

var fakeTS struct {
	sync.Mutex
	buf [2][]byte // as imagens devolvidas (BGRA), reaproveitadas
	bg  []byte    // fundo pronto (BGRA)
	key string    // tamanho e fundo do bg
}

func fakeSize() (int, int) {
	var w, h int
	if _, err := fmt.Sscanf(os.Getenv("NEXTTS_FAKE_SIZE"), "%dx%d", &w, &h); err == nil && w >= 200 && h >= 150 {
		return w, h
	}
	return 1000, 640
}

// fakeBackground prepara o fundo da janela de mentira (BGRA).
func fakeBackground(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	fillRect(img, img.Bounds(), bgr(color.RGBA{236, 238, 242, 255}))
	if p := os.Getenv("NEXTTS_FAKE_BG"); p != "" {
		if f, err := os.Open(p); err == nil {
			if src, err := png.Decode(f); err == nil {
				b := src.Bounds()
				for y := 0; y < h; y++ {
					for x := 0; x < w; x++ {
						r, g, bl, _ := src.At(b.Min.X+x*b.Dx()/w, b.Min.Y+y*b.Dy()/h).RGBA()
						o := img.PixOffset(x, y)
						img.Pix[o], img.Pix[o+1], img.Pix[o+2], img.Pix[o+3] = uint8(bl>>8), uint8(g>>8), uint8(r>>8), 255
					}
				}
			}
			f.Close()
		}
	}
	fillRect(img, image.Rect(0, 0, w, 30), bgr(color.RGBA{40, 60, 90, 255}))
	drawText(img, 10, 5, "TeamSpeak 3 (simulado para teste)", bgr(color.RGBA{255, 255, 255, 255}), 1)
	fillRect(img, image.Rect(0, h*400/640, w, h*400/640+2), bgr(color.RGBA{150, 150, 160, 255}))
	return img.Pix
}

func captureTS(buf int) ([]byte, int, int, capState) {
	p := os.Getenv("NEXTTS_FAKE_TS")
	if p == "" {
		return nil, 0, 0, capNotFound
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, 0, 0, capFailed
	}
	text := strings.TrimSpace(string(b))
	switch text {
	case "min":
		return nil, 0, 0, capMinimized
	case "none":
		return nil, 0, 0, capNotFound
	case "falha":
		return nil, 0, 0, capFailed
	}
	fakeTS.Lock()
	defer fakeTS.Unlock()
	w, h := fakeSize()
	if key := fmt.Sprintf("%dx%d %s", w, h, os.Getenv("NEXTTS_FAKE_BG")); key != fakeTS.key || fakeTS.bg == nil {
		fakeTS.bg, fakeTS.key = fakeBackground(w, h), key
	}
	if len(fakeTS.buf[buf]) != w*h*4 {
		fakeTS.buf[buf] = make([]byte, w*h*4)
	}
	out := fakeTS.buf[buf]
	copy(out, fakeTS.bg)
	img := asImage(out, w, h)
	ink := bgr(color.RGBA{20, 20, 30, 255})
	y := h*400/640 + 10
	for _, ln := range strings.Split(text, "\n") {
		if rest, ok := strings.CutPrefix(ln, "ROLANDO: "); ok {
			x := w - int(time.Now().UnixMilli()/20%int64(w+600)) // anda 1 ponto a cada 20 ms
			drawText(img, x, h-25, rest, ink, 1)
			continue
		}
		if strings.TrimSpace(ln) == "RELOGIO" {
			ln = "relógio " + time.Now().Format("15:04:05.000")
		}
		drawText(img, 12, y, ln, ink, 1)
		y += fontH + 2
	}
	return out, w, h, capOK
}

func captureRelease() {
	fakeTS.Lock()
	fakeTS.buf, fakeTS.bg, fakeTS.key = [2][]byte{}, nil, ""
	fakeTS.Unlock()
}
