//go:build !windows

package main

// Versão para testes em Linux: uma "janela do TeamSpeak" de mentira.
//
// NEXTTS_FAKE_TS aponta para um arquivo de texto:
//   - "min"  = TS minimizado;
//   - "none" = TS fechado;
//   - outro texto = desenha uma janela com essas linhas (ex.: o chat com o bot);
//     uma linha "ROLANDO: texto" vira um aviso que passa rolando na barra de baixo
//     (como o "Announcement" do TS de verdade).
// Sem a variável, age como se o TS não estivesse aberto.

import (
	"image"
	"image/color"
	"os"
	"strings"
	"time"
)

func captureTS() (*image.RGBA, capState) {
	p := os.Getenv("NEXTTS_FAKE_TS")
	if p == "" {
		return nil, capNotFound
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, capFailed
	}
	text := strings.TrimSpace(string(b))
	switch text {
	case "min":
		return nil, capMinimized
	case "none":
		return nil, capNotFound
	}
	img := image.NewRGBA(image.Rect(0, 0, 1000, 640))
	fillRect(img, img.Bounds(), color.RGBA{236, 238, 242, 255})
	fillRect(img, image.Rect(0, 0, 1000, 30), color.RGBA{40, 60, 90, 255})
	drawText(img, 10, 5, "TeamSpeak 3 (simulado para teste)", color.RGBA{255, 255, 255, 255}, 1)
	fillRect(img, image.Rect(0, 400, 1000, 402), color.RGBA{150, 150, 160, 255})
	y := 410
	for _, ln := range strings.Split(text, "\n") {
		if rest, ok := strings.CutPrefix(ln, "ROLANDO: "); ok {
			x := 1000 - int(time.Now().UnixMilli()/20%1600) // anda 10 pontos a cada 0,2 s
			drawText(img, x, 615, rest, color.RGBA{20, 20, 30, 255}, 1)
			continue
		}
		drawText(img, 12, y, ln, color.RGBA{20, 20, 30, 255}, 1)
		y += fontH + 2
	}
	return img, capOK
}
