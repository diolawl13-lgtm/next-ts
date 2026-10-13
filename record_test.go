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

func TestRecorderGravaComFaixa(t *testing.T) {
	dir := t.TempDir()
	var logs []string
	r := NewRecorder(dir, func(kind, text string) { logs = append(logs, kind+": "+text) })
	var state atomic.Int32
	r.capture = func() (*image.RGBA, capState) {
		if st := capState(state.Load()); st != capOK {
			return nil, st
		}
		img := image.NewRGBA(image.Rect(0, 0, 400, 300))
		fillRect(img, img.Bounds(), color.RGBA{0, 0, 255, 255})
		return img, capOK
	}
	file, err := r.Begin("C1", "Plano A C1 · comando para x3tBot", false)
	if err != nil {
		t.Fatal(err)
	}
	r.Event("ok", "20:15:03", `ENVIADO "!next C1" para x3tBot`)
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
	if in.w != recMinW { // 400 de largura vira o mínimo de 960
		t.Fatalf("largura %d", in.w)
	}
	img, err := jpeg.Decode(bytes.NewReader(in.chunks[0]))
	if err != nil {
		t.Fatal(err)
	}
	// Em cima a imagem do TS (azul, centralizada); embaixo a faixa escura.
	if c := color.RGBAModel.Convert(img.At(480, 150)).(color.RGBA); c.B < 200 || c.R > 60 {
		t.Fatalf("meio do quadro deveria ser azul: %v", c)
	}
	if c := color.RGBAModel.Convert(img.At(950, int(in.h)-3)).(color.RGBA); c.R > 60 {
		t.Fatalf("faixa deveria ser escura: %v", c)
	}
	joined := strings.Join(logs, "\n")
	if !strings.Contains(joined, "minimizado") || !strings.Contains(joined, "Gravação salva") {
		t.Fatalf("avisos: %s", joined)
	}
}
