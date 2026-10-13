package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSameParty(t *testing.T) {
	cases := []struct {
		hunter, next string
		want         bool
	}{
		{"Lord Calvon", "Lord Calvon", true},
		{"lord calvon", "Lord  Calvon", true},
		{"Bruneo Berserker / Emma Reilly", "bruneo berserker/emma Reilly", true},
		{"Dudu", "Dudu / Pedrinho", true},
		{"Elite'Nitrox", "Elite Nitrox", true},
		{"Kira Zinha", "Lord Calvon", false},
		{"Pedro", "Pedrinho", false},
		{"", "Lord Calvon", false},
	}
	for _, c := range cases {
		if got := sameParty(c.hunter, c.next); got != c.want {
			t.Errorf("sameParty(%q, %q) = %v, quero %v", c.hunter, c.next, got, c.want)
		}
	}
}

// aviInfo lê o arquivo e confere a estrutura básica do AVI.
type aviInfo struct {
	riffSize, moviSize, totalFrames, length, w, h, rate, scale uint32
	chunks                                                     [][]byte // dados de cada quadro, na ordem
	idx                                                        [][4]uint32
}

func readAVI(t *testing.T, path string) aviInfo {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	u := func(o int) uint32 { return binary.LittleEndian.Uint32(b[o:]) }
	if string(b[0:4]) != "RIFF" || string(b[8:12]) != "AVI " || string(b[212:216]) != "LIST" || string(b[220:224]) != "movi" {
		t.Fatalf("cabeçalho AVI errado")
	}
	in := aviInfo{riffSize: u(4), moviSize: u(216), totalFrames: u(48), length: u(140), w: u(64), h: u(68), scale: u(128), rate: u(132)}
	if int(in.riffSize) != len(b)-8 {
		t.Fatalf("tamanho RIFF %d, arquivo %d", in.riffSize, len(b))
	}
	pos := 224
	end := aviMoviFourCC + int(in.moviSize)
	for pos < end {
		if string(b[pos:pos+4]) != "00dc" {
			t.Fatalf("pedaço inesperado %q em %d", b[pos:pos+4], pos)
		}
		n := int(u(pos + 4))
		in.chunks = append(in.chunks, b[pos+8:pos+8+n])
		pos += 8 + n + n%2
	}
	if pos != end || string(b[pos:pos+4]) != "idx1" {
		t.Fatalf("índice fora do lugar: %d %d", pos, end)
	}
	n := int(u(pos+4)) / 16
	for i := 0; i < n; i++ {
		o := pos + 8 + 16*i
		in.idx = append(in.idx, [4]uint32{u(o), u(o + 4), u(o + 8), u(o + 12)})
	}
	return in
}

func TestAVIWriter(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.avi")
	a, err := newAVI(p, 100, 60)
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 100, 60))
	var buf bytes.Buffer
	jpeg.Encode(&buf, img, nil)
	jp := append([]byte(nil), buf.Bytes()...)
	if len(jp)%2 == 0 {
		jp = append(jp, 0) // força um tamanho ímpar (testa o preenchimento)
	}
	for _, d := range [][]byte{jp, nil, nil, jp} {
		if err := a.frame(d); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.close(); err != nil {
		t.Fatal(err)
	}
	in := readAVI(t, p)
	if in.totalFrames != 4 || in.length != 4 || len(in.chunks) != 4 || len(in.idx) != 4 {
		t.Fatalf("quadros: %d %d %d %d", in.totalFrames, in.length, len(in.chunks), len(in.idx))
	}
	if in.w != 100 || in.h != 60 || in.rate/in.scale != RecFPS {
		t.Fatalf("tamanho/ritmo errado: %+v", in)
	}
	if len(in.chunks[1]) != 0 || len(in.chunks[0]) != len(jp) {
		t.Fatalf("conteúdo dos quadros errado")
	}
	if in.idx[0][1] != 0x10 || in.idx[1][1] != 0 || in.idx[0][2] != 4 {
		t.Fatalf("índice errado: %v", in.idx)
	}
}

func TestAllBlack(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 300, 200))
	fillRect(img, img.Bounds(), color.RGBA{0, 0, 0, 255})
	if !allBlack(img) {
		t.Fatal("imagem preta não foi reconhecida")
	}
	drawText(img, 100, 90, "TeamSpeak", color.RGBA{255, 255, 255, 255}, 2)
	if allBlack(img) {
		t.Fatal("imagem com texto foi tida como preta")
	}
}

func TestDrawTextNaoEstoura(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 50, 10))
	drawText(img, 40, 5, "Ação → teste ✓ 字", color.RGBA{255, 255, 255, 255}, 2) // passa da borda e tem letras sem desenho
	drawText(img, -5, -15, "abc", color.RGBA{255, 255, 255, 255}, 1)
}

func TestRecorderSoJanelaDoTS(t *testing.T) {
	dir := t.TempDir()
	var logs []string
	r := NewRecorder(dir, func(kind, text string) { logs = append(logs, kind+": "+text) })
	var state atomic.Int32
	r.capture = func() (*image.RGBA, capState) {
		if st := capState(state.Load()); st != capOK {
			return nil, st
		}
		img := image.NewRGBA(image.Rect(0, 0, 401, 300)) // largura ímpar de propósito
		fillRect(img, img.Bounds(), color.RGBA{0, 0, 255, 255})
		return img, capOK
	}
	file, err := r.Begin("C1", false)
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Now()
	time.Sleep(700 * time.Millisecond)
	state.Store(int32(capMinimized))
	// Espera o aviso aparecer (até 3 s: o PC pode estar ocupado).
	for end := time.Now().Add(3 * time.Second); !strings.Contains(r.View().Note, "minimizado"); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatalf("visão: %+v", r.View())
		}
	}
	if !r.View().On {
		t.Fatal("parou de gravar sozinho")
	}
	r.End(300 * time.Millisecond)
	r.Close()
	elapsed := time.Since(t0)
	if v := r.View(); v.On || v.Last != filepath.Base(file) {
		t.Fatalf("depois de fechar: %+v", v)
	}
	in := readAVI(t, file)
	// 5 quadros por segundo de gravação (o tempo conta, mesmo repetindo quadros).
	want := int(elapsed / recFrame)
	if d := int(in.totalFrames) - want; d < -2 || d > 2 {
		t.Fatalf("quadros: %d, esperava ~%d (%v)", in.totalFrames, want, elapsed)
	}
	// O vídeo tem exatamente o tamanho da janela do TS: sem faixa e sem bordas.
	if in.w != 401 || in.h != 300 {
		t.Fatalf("tamanho do vídeo %dx%d, esperava 401x300", in.w, in.h)
	}
	img, err := jpeg.Decode(bytes.NewReader(in.chunks[0]))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range [][2]int{{2, 2}, {200, 150}, {398, 297}, {2, 297}} { // cantos e meio: tudo azul
		if c := color.RGBAModel.Convert(img.At(p[0], p[1])).(color.RGBA); c.B < 200 || c.R > 60 || c.G > 60 {
			t.Fatalf("ponto %v deveria ser azul (só a janela do TS): %v", p, c)
		}
	}
	// Minimizado: a última imagem gravada é a tela do aviso (do mesmo tamanho).
	var last []byte
	for _, c := range in.chunks {
		if len(c) > 0 {
			last = c
		}
	}
	li, err := jpeg.Decode(bytes.NewReader(last))
	if err != nil {
		t.Fatal(err)
	}
	if c := color.RGBAModel.Convert(li.At(5, 5)).(color.RGBA); c.B > 100 {
		t.Fatalf("depois de minimizar deveria mostrar a tela do aviso: %v", c)
	}
	joined := strings.Join(logs, "\n")
	if !strings.Contains(joined, "minimizado") || !strings.Contains(joined, "Gravação salva") {
		t.Fatalf("avisos: %s", joined)
	}
}

// O aviso que passa rolando na barra do TS não pode fazer gravar imagem nova
// toda hora; uma mensagem nova no chat tem que entrar na hora.
func TestFrameDiffAvisoRolando(t *testing.T) {
	const w, h = 640, 400
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	d := newFrameDiff(w, h)
	at := time.Date(2026, 10, 13, 20, 0, 0, 0, time.Local)
	written := 0
	var when []int
	for i := 0; i < 60; i++ { // 12 s a 5 quadros por segundo
		fillRect(img, img.Bounds(), color.RGBA{230, 230, 235, 255})
		drawText(img, 600-i*7, 380, "Announcement - Start your own Community Server", color.RGBA{0, 0, 0, 255}, 1)
		if i >= 40 {
			drawText(img, 10, 200, `<20:15:03> "Real Calvon": !next C1`, color.RGBA{0, 0, 0, 255}, 1)
		}
		if d.need(img.Pix, at, i == 0) {
			written++
			when = append(when, i)
		}
		at = at.Add(recFrame)
	}
	t.Logf("imagens gravadas: %d de 60, nos quadros %v", written, when)
	// 1ª + umas 5 até perceber que o aviso "se mexe sozinho" + 1 a cada 2 s (10 quadros) + a do chat.
	if written > 16 {
		t.Fatalf("gravou imagem nova demais por causa do aviso rolando: %d", written)
	}
	found := false
	for _, i := range when {
		found = found || i == 40
	}
	if !found {
		t.Fatalf("a mensagem nova do chat (quadro 40) não entrou na hora: %v", when)
	}
	// Tudo parado: nenhuma imagem nova.
	still := image.NewRGBA(image.Rect(0, 0, w, h))
	d2 := newFrameDiff(w, h)
	n := 0
	for i := 0; i < 20; i++ {
		if d2.need(still.Pix, at.Add(time.Duration(i)*recFrame), i == 0) {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("imagem parada gravou %d vezes", n)
	}
}
