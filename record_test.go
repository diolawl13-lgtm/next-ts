package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
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

// ---------- Ajudantes ----------

// shortTimes diminui os tempos do recorte durante um teste (20 s/10 s seriam lentos demais).
func shortTimes(t *testing.T, before, after time.Duration) {
	b, a := RecBefore, RecAfter
	RecBefore, RecAfter = before, after
	t.Cleanup(func() { RecBefore, RecAfter = b, a })
}

// solidBGRA: imagem toda de uma cor (em BGRA, como vem da captura).
func solidBGRA(w, h int, c color.RGBA) []byte {
	b := make([]byte, w*h*4)
	for i := 0; i < len(b); i += 4 {
		b[i], b[i+1], b[i+2], b[i+3] = c.B, c.G, c.R, 255
	}
	return b
}

// idxColor: uma cor diferente para cada número (resiste à compressão do JPEG).
func idxColor(i int) color.RGBA {
	return color.RGBA{uint8(i%8*32 + 16), uint8(i/8%8*32 + 16), uint8(i/64%8*32 + 16), 255}
}

// colorIdx: o número de volta, pela cor lida no vídeo (-1 se não for uma dessas cores).
func colorIdx(c color.RGBA) int {
	q := func(v uint8) int {
		if int(v)%32 < 4 || int(v)%32 > 28 {
			return -1
		}
		return int(v) / 32
	}
	r, g, b := q(c.R), q(c.G), q(c.B)
	if r < 0 || g < 0 || b < 0 {
		return -1
	}
	return r + 8*g + 64*b
}

func decodeAt(t *testing.T, jp []byte, x, y int) color.RGBA {
	t.Helper()
	img, err := jpeg.Decode(bytes.NewReader(jp))
	if err != nil {
		t.Fatalf("quadro não abre: %v", err)
	}
	return color.RGBAModel.Convert(img.At(x, y)).(color.RGBA)
}

// shownAt: os dados da imagem que aparece em cada quadro (o "repete o anterior" vira a anterior).
func shownAt(in aviInfo) [][]byte {
	out := make([][]byte, len(in.chunks))
	var last []byte
	for i, c := range in.chunks {
		if len(c) > 0 {
			last = c
		}
		out[i] = last
	}
	return out
}

// testFrame: um quadro inteiro (todas as faixas) de uma cor, guardado no gravador.
func testFrame(t *testing.T, r *Recorder, c *bandCodec, col color.RGBA) *vframe {
	t.Helper()
	segs := encodeBands(t, c, solidBGRA(c.w, c.h, col))
	f := &vframe{segs: make([]*seg, len(segs)), codec: c}
	for i, b := range segs {
		f.segs[i] = r.store.put(b)
	}
	r.hold(f)
	return f
}

// fakeCam é uma janela do TS de mentira: cada captura tem uma cor nova (o número da
// captura), e anota o instante de cada uma.
type fakeCam struct {
	mu    sync.Mutex
	w, h  int
	n     int
	at    []time.Time
	st    atomic.Int32
	slow  atomic.Int64 // atraso de cada captura (ns)
	buf   []byte
	fixed bool // sempre a mesma imagem
}

func (c *fakeCam) capture(int) ([]byte, int, int, capState) {
	if d := c.slow.Load(); d > 0 {
		time.Sleep(time.Duration(d))
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if st := capState(c.st.Load()); st != capOK {
		return nil, 0, 0, st
	}
	c.at = append(c.at, time.Now())
	col := idxColor(c.n)
	if c.fixed {
		col = color.RGBA{0, 0, 255, 255}
	}
	c.n++
	c.buf = solidBGRA(c.w, c.h, col)
	return c.buf, c.w, c.h, capOK
}

func newTestRecorder(t *testing.T, cam *fakeCam) (*Recorder, *[]string, *sync.Mutex) {
	var mu sync.Mutex
	var logs []string
	r := NewRecorder(t.TempDir(), func(kind, text string) {
		mu.Lock()
		logs = append(logs, kind+": "+text)
		mu.Unlock()
	})
	r.capture, r.release = cam.capture, func() {}
	t.Cleanup(func() { // nada do gravador pode continuar rodando depois do teste
		r.Close()
		r.mu.Lock()
		p := r.lastPipe
		r.mu.Unlock()
		if p != nil {
			<-p.done
		}
	})
	return r, &logs, &mu
}

func joinedLogs(logs *[]string, mu *sync.Mutex) string {
	mu.Lock()
	defer mu.Unlock()
	return strings.Join(*logs, "\n")
}

// waitSaved espera o gravador terminar de salvar tudo.
func waitSaved(t *testing.T, r *Recorder, timeout time.Duration) {
	t.Helper()
	for end := time.Now().Add(timeout); ; time.Sleep(20 * time.Millisecond) {
		r.mu.Lock()
		busy := len(r.active) > 0 || len(r.saving) > 0 || r.pipe != nil
		r.mu.Unlock()
		if !busy {
			return
		}
		if time.Now().After(end) {
			t.Fatalf("o gravador não terminou: %+v", r.View())
		}
	}
}

// ---------- Recorte (sem esperar de verdade) ----------

// O recorte completo: 20 s antes e 10 s depois do envio, 900 quadros, e cada quadro
// mostra a imagem que estava na tela naquele instante (mesmo com capturas atrasadas).
func TestRecorteCompleto(t *testing.T) {
	r := NewRecorder(t.TempDir(), nil)
	c, _ := newBandCodec(64, 48)
	base := time.Now()
	s := &recSession{base: filepath.Join(r.dir, "corte"), done: make(chan struct{})}
	// 40 s de capturas a 30 por segundo, com um buraco (captura atrasada) e repetições.
	var frames []*vframe
	at := func(i int) time.Time { return base.Add(slotOffset(i)) }
	T := at(900).Add(5 * time.Millisecond)
	for i := 0; i < 1200; i++ {
		if i == 901 { // o envio: a partir daqui a memória guarda até T + 10 s
			s.kind, s.ref, s.endAt = "sent", T, T.Add(RecAfter)
			s.trim(T, r)
		}
		if i >= 700 && i < 712 {
			continue // 0,4 s sem captura nenhuma (o PC travou)
		}
		var f *vframe
		if i%3 == 0 || len(frames) == 0 {
			f = testFrame(t, r, c, idxColor(len(frames)))
			frames = append(frames, f)
		} else {
			f = frames[len(frames)-1] // imagem igual à anterior
		}
		s.push(at(i).Add(time.Millisecond), f)
		s.trim(at(i), r)
	}
	plan := planClip(s)
	if plan.slots != 900 || plan.partial || plan.before != RecBefore {
		t.Fatalf("plano: %+v", plan)
	}
	path, info, err := r.writeClip(s, append([]clipFrame(nil), s.frames...), plan)
	if err != nil {
		t.Fatal(err)
	}
	in := readAVI(t, path)
	if in.totalFrames != 900 || len(in.chunks) != 900 || in.rate != 30 || in.scale != 1 {
		t.Fatalf("vídeo: %d quadros (%d pedaços), %d/%d por segundo", in.totalFrames, len(in.chunks), in.rate, in.scale)
	}
	if len(in.chunks[0]) == 0 {
		t.Fatal("o primeiro quadro do recorte não é uma imagem completa")
	}
	shown := shownAt(in)
	// Quadro k mostra a última imagem capturada até start + k/30 s.
	for k := 0; k < 900; k++ {
		slot := plan.start.Add(slotOffset(k))
		want := -1
		for _, cf := range s.frames {
			if !cf.at.After(slot) {
				want = colorIdx(idxColorOf(frames, cf.f))
			}
		}
		got := colorIdx(decodeAt(t, shown[k], 10, 10))
		if got != want {
			t.Fatalf("quadro %d (%.3f s): mostra a imagem %d, devia ser %d", k, slot.Sub(plan.start).Seconds(), got, want)
		}
	}
	// Repetidos não ocupam espaço (só 1 de cada 3 capturas é nova).
	empty := 0
	for _, ch := range in.chunks {
		if len(ch) == 0 {
			empty++
		}
	}
	if empty < 550 || info.images+empty != 900 {
		t.Fatalf("pedaços vazios: %d, imagens: %d", empty, info.images)
	}
}

func idxColorOf(frames []*vframe, f *vframe) color.RGBA {
	for i, x := range frames {
		if x == f {
			return idxColor(i)
		}
	}
	return color.RGBA{}
}

// Imagem parada bem na borda do corte: o 1º quadro do vídeo é ela, completa (mesmo
// tendo sido capturada muito antes dos 20 s).
func TestRecorteImagemParadaNaBorda(t *testing.T) {
	r := NewRecorder(t.TempDir(), nil)
	c, _ := newBandCodec(40, 40)
	base := time.Now()
	s := &recSession{base: filepath.Join(r.dir, "borda"), done: make(chan struct{})}
	still := testFrame(t, r, c, idxColor(5))
	s.push(base, still)
	// 60 s com a mesma imagem (capturas iguais: nada novo).
	for i := 1; i < 1800; i++ {
		s.push(base.Add(slotOffset(i)), still)
		s.trim(base.Add(slotOffset(i)), r)
	}
	later := testFrame(t, r, c, idxColor(9))
	s.push(base.Add(slotOffset(1800)), later)
	T := base.Add(slotOffset(1800) + 5*time.Second)
	s.kind, s.ref, s.endAt = "sent", T, T.Add(RecAfter)
	s.trim(T, r)
	if len(s.frames) != 2 || s.frames[0].f != still {
		t.Fatalf("memória circular: %d quadros", len(s.frames))
	}
	plan := planClip(s)
	path, _, err := r.writeClip(s, s.frames, plan)
	if err != nil {
		t.Fatal(err)
	}
	in := readAVI(t, path)
	if len(in.chunks) != 900 || len(in.chunks[0]) == 0 {
		t.Fatalf("%d quadros, primeiro com %d bytes", len(in.chunks), len(in.chunks[0]))
	}
	if got := colorIdx(decodeAt(t, in.chunks[0], 5, 5)); got != 5 {
		t.Fatalf("o primeiro quadro mostra %d, devia ser a imagem parada (5)", got)
	}
	shown := shownAt(in)
	if got := colorIdx(decodeAt(t, shown[449], 5, 5)); got != 5 { // 15 s depois do começo: ainda a parada
		t.Fatalf("aos 15 s: %d", got)
	}
	if got := colorIdx(decodeAt(t, shown[450], 5, 5)); got != 9 { // aos 15 s (T-5 s) entrou a nova
		t.Fatalf("aos 15 s: %d, devia ser a nova (9)", got)
	}
}

// A memória circular nunca guarda mais que os 20 s (e solta a memória do resto).
func TestMemoriaCircularDescartaOVelho(t *testing.T) {
	r := NewRecorder(t.TempDir(), nil)
	c, _ := newBandCodec(64, 64)
	base := time.Now()
	s := &recSession{base: filepath.Join(r.dir, "x"), done: make(chan struct{})}
	r.active = []*recSession{s}
	var peak int64
	for i := 0; i < 30*120; i++ { // 2 minutos, imagem nova a cada captura
		at := base.Add(slotOffset(i))
		f := testFrame(t, r, c, idxColor(i))
		s.push(at, f)
		r.unref(f) // só a gravação fica com ele
		s.trim(at, r)
		ram, _ := r.store.usage()
		peak = max(peak, ram)
		if span := at.Sub(s.frames[0].at); span > RecBefore+recMargin+slotOffset(1) {
			t.Fatalf("aos %v a memória guarda %v", at.Sub(base), span)
		}
	}
	if n := len(s.frames); n > (30*int(RecBefore+recMargin)/int(time.Second))+2 {
		t.Fatalf("quadros guardados: %d", n)
	}
	ram, _ := r.store.usage()
	per := ram / int64(len(s.frames))
	if peak > per*int64(len(s.frames)+5) {
		t.Fatalf("a memória não foi solta: pico %d, agora %d", peak, ram)
	}
	t.Logf("2 min a 30 imagens novas por segundo: %d quadros guardados, %s na memória", len(s.frames), fmtMB(ram))
}

// Passou do limite de memória: as faixas vão para o temporário (com limite, reescrito
// quando tem lixo demais) e voltam iguais. Sem espaço, ficam na memória (e avisa).
func TestTemporarioRotativo(t *testing.T) {
	oldR, oldD, oldS := recRAMMax, recDiskMax, recSlack
	t.Cleanup(func() { recRAMMax, recDiskMax, recSlack = oldR, oldD, oldS })
	recRAMMax, recDiskMax, recSlack = 3000, 20000, 0
	st := &segStore{dir: filepath.Join(t.TempDir(), ".temp")}
	data := func(i int) []byte { return bytes.Repeat([]byte{byte(i)}, 1000) }
	var segs []*seg
	for i := 0; i < 15; i++ {
		segs = append(segs, st.put(data(i)))
	}
	spooled := 0
	for _, s := range segs {
		if s.spooled {
			spooled++
		}
	}
	if spooled != 12 {
		t.Fatalf("faixas no temporário: %d", spooled)
	}
	for i := 3; i < 13; i++ { // descarta a maior parte
		st.drop(segs[i])
	}
	for i := 15; i < 45; i++ { // o temporário é reescrito sem o lixo e nunca passa do limite
		segs = append(segs, st.put(data(i)))
		if _, disk := st.usage(); disk > recDiskMax {
			t.Fatalf("temporário passou do limite: %d", disk)
		}
	}
	for i, s := range segs {
		if i >= 3 && i < 13 {
			continue
		}
		b, err := st.bytesOf(s, nil)
		if err != nil || !bytes.Equal(b, data(i)) {
			t.Fatalf("faixa %d voltou diferente (%v)", i, err)
		}
	}
	if p := st.newProblem(); p == "" {
		t.Fatal("o temporário chegou ao limite e não avisou")
	} else if st.newProblem() != "" {
		t.Fatal("avisou duas vezes")
	}
	path := st.path
	st.close()
	if fileExists(path) {
		t.Fatal("o temporário não foi apagado")
	}
}

// ---------- Gravação de ponta a ponta (tempos curtos) ----------

func TestRecorderSoJanelaDoTS(t *testing.T) {
	shortTimes(t, 2*time.Second, time.Second)
	cam := &fakeCam{w: 401, h: 300, fixed: true} // largura ímpar de propósito
	r, logs, lmu := newTestRecorder(t, cam)
	file, err := r.Begin("C1", false)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(700 * time.Millisecond)
	cam.st.Store(int32(capMinimized))
	for end := time.Now().Add(3 * time.Second); !strings.Contains(r.View().Note, "minimizado"); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatalf("visão: %+v", r.View())
		}
	}
	v := r.View()
	if !v.On || v.Phase != "buffer" || v.Kept <= 0.5 || v.Kept > 2.01 || v.FPS < 20 {
		t.Fatalf("visão: %+v", v)
	}
	time.Sleep(200 * time.Millisecond) // a tela do aviso entra na gravação
	r.End(300 * time.Millisecond)
	r.Close()
	if v := r.View(); v.On || v.Last != filepath.Base(file) {
		t.Fatalf("depois de fechar: %+v", v)
	}
	in := readAVI(t, file)
	if in.w != 401 || in.h != 300 { // só a janela do TS: sem faixa e sem bordas
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
	last := shownAt(in)[len(in.chunks)-1]
	if c := decodeAt(t, last, 5, 5); c.B > 100 {
		t.Fatalf("depois de minimizar deveria mostrar a tela do aviso: %v", c)
	}
	j := joinedLogs(logs, lmu)
	if !strings.Contains(j, "minimizado") || !strings.Contains(j, "Vídeo salvo") {
		t.Fatalf("avisos: %s", j)
	}
}

// Envio depois de muito tempo: o vídeo tem exatamente os "20 s" antes e os "10 s" depois
// (aqui 1,5 s + 0,6 s), 30 quadros por segundo, e cada quadro é a captura daquele instante.
// Um monitoramento novo começando logo depois não corta o fim do anterior.
func TestRecorderEnvioEMonitoramentoNovo(t *testing.T) {
	shortTimes(t, 1500*time.Millisecond, 600*time.Millisecond)
	cam := &fakeCam{w: 96, h: 64}
	r, logs, lmu := newTestRecorder(t, cam)
	if err := os.WriteFile(filepath.Join(r.dir, "antigo.avi"), []byte("vídeo antigo"), 0o644); err != nil {
		t.Fatal(err)
	}
	fileA, _ := r.Begin("C1", false)
	time.Sleep(3 * time.Second) // "muito tempo": bem mais que os 1,5 s guardados
	T := time.Now()
	r.Mark(T, "sent")
	r.End(RecTailOther) // o app chama End depois: não pode mudar nada
	fileB, _ := r.Begin("C2", false)
	if fileB == fileA {
		t.Fatal("mesmo arquivo para dois monitoramentos")
	}
	v := r.View()
	if v.Others != 1 || v.Phase != "buffer" {
		t.Fatalf("visão do monitoramento novo: %+v", v)
	}
	time.Sleep(400 * time.Millisecond)
	r.End(100 * time.Millisecond)
	waitSaved(t, r, 10*time.Second)

	in := readAVI(t, fileA)
	want := 63 // (1,5 + 0,6) s x 30
	if d := int(in.totalFrames) - want; d < -1 || d > 1 {
		t.Fatalf("quadros: %d, esperava %d (±1)", in.totalFrames, want)
	}
	if len(in.chunks[0]) == 0 {
		t.Fatal("o primeiro quadro não é uma imagem completa")
	}
	// Cada quadro mostra a captura feita até aquele instante (com folga de 1 captura).
	cam.mu.Lock()
	ats := append([]time.Time(nil), cam.at...)
	cam.mu.Unlock()
	start := T.Add(-RecBefore)
	shown := shownAt(in)
	for k := range shown {
		slot := start.Add(slotOffset(k))
		want := -1
		for i, a := range ats {
			if !a.After(slot) {
				want = i
			}
		}
		got := colorIdx(decodeAt(t, shown[k], 48, 32))
		if got < want-1 || got > want+1 {
			t.Fatalf("quadro %d: mostra a captura %d, devia ser a %d", k, got, want)
		}
	}
	inB := readAVI(t, fileB)
	if inB.totalFrames < 12 || inB.totalFrames > 20 { // 0,4 s + 0,1 s
		t.Fatalf("vídeo do monitoramento novo: %d quadros", inB.totalFrames)
	}
	if b, _ := os.ReadFile(filepath.Join(r.dir, "antigo.avi")); string(b) != "vídeo antigo" {
		t.Fatal("o vídeo antigo foi mexido")
	}
	if fileExists(r.tmp) {
		ls, _ := os.ReadDir(r.tmp)
		t.Fatalf("sobrou a pasta temporária: %v", ls)
	}
	j := joinedLogs(logs, lmu)
	if !strings.Contains(j, "1,5 s antes e 0,6 s depois do envio") {
		t.Fatalf("histórico: %s", j)
	}
}

// Envio antes de completar os "20 s": salva o que tem e avisa que é parcial.
func TestRecorderEnvioCedoParcial(t *testing.T) {
	shortTimes(t, 3*time.Second, 500*time.Millisecond)
	cam := &fakeCam{w: 64, h: 48}
	r, logs, lmu := newTestRecorder(t, cam)
	file, _ := r.Begin("C1", true)
	time.Sleep(800 * time.Millisecond)
	r.Mark(time.Now(), "tested")
	waitSaved(t, r, 5*time.Second)
	in := readAVI(t, file)
	if in.totalFrames < 36 || in.totalFrames > 42 { // ~0,8 s + 0,5 s
		t.Fatalf("quadros: %d", in.totalFrames)
	}
	j := joinedLogs(logs, lmu)
	if !strings.Contains(j, "parcial") || !strings.Contains(j, "teria enviado") || !strings.Contains(filepath.Base(file), "_teste") {
		t.Fatalf("histórico: %s (%s)", j, file)
	}
	if v := r.View(); !strings.Contains(v.LastInfo, "parcial") {
		t.Fatalf("visão: %+v", v)
	}
}

// Captura lenta (o PC está ocupado): o vídeo continua com a duração certa (velocidade
// real), repetindo a imagem enquanto não chega outra.
func TestRecorderCapturaLenta(t *testing.T) {
	shortTimes(t, time.Second, 500*time.Millisecond)
	cam := &fakeCam{w: 64, h: 48}
	cam.slow.Store(int64(90 * time.Millisecond))
	r, _, _ := newTestRecorder(t, cam)
	file, _ := r.Begin("C1", false)
	time.Sleep(1500 * time.Millisecond)
	T := time.Now()
	r.Mark(T, "sent")
	waitSaved(t, r, 5*time.Second)
	in := readAVI(t, file)
	if d := int(in.totalFrames) - 45; d < -1 || d > 1 {
		t.Fatalf("quadros: %d, esperava 45", in.totalFrames)
	}
	images := 0
	for _, c := range in.chunks {
		if len(c) > 0 {
			images++
		}
	}
	if images < 10 || images > 20 { // ~11 capturas por segundo
		t.Fatalf("imagens diferentes: %d", images)
	}
}

// TS fechado no começo e aberto depois: o vídeo fica do tamanho da janela, e as telas
// de aviso de antes (já na memória) são refeitas nesse tamanho, sem perder nada.
func TestRecorderTSApareceDepois(t *testing.T) {
	shortTimes(t, 2*time.Second, 300*time.Millisecond)
	cam := &fakeCam{w: 401, h: 300, fixed: true}
	cam.st.Store(int32(capNotFound))
	r, _, _ := newTestRecorder(t, cam)
	file, _ := r.Begin("C1", false)
	time.Sleep(500 * time.Millisecond)
	cam.st.Store(int32(capOK))
	time.Sleep(700 * time.Millisecond)
	r.Mark(time.Now(), "sent")
	waitSaved(t, r, 5*time.Second)
	in := readAVI(t, file)
	if in.w != 401 || in.h != 300 {
		t.Fatalf("tamanho %dx%d", in.w, in.h)
	}
	if d := int(in.totalFrames) - 45; d < -2 || d > 1 { // 1,2 s + 0,3 s
		t.Fatalf("quadros: %d", in.totalFrames)
	}
	if c := decodeAt(t, in.chunks[0], 5, 5); c.B > 100 { // começa com o aviso (escuro)
		t.Fatalf("primeiro quadro: %v", c)
	}
	if c := decodeAt(t, shownAt(in)[len(in.chunks)-1], 200, 150); c.B < 200 {
		t.Fatalf("último quadro: %v", c)
	}
}

// Sem espaço na memória: tudo vai para o temporário, o vídeo sai igual e o temporário
// é apagado no fim.
func TestRecorderComTemporario(t *testing.T) {
	shortTimes(t, time.Second, 300*time.Millisecond)
	old := recRAMMax
	recRAMMax = 1
	t.Cleanup(func() { recRAMMax = old })
	cam := &fakeCam{w: 64, h: 48}
	r, _, _ := newTestRecorder(t, cam)
	file, _ := r.Begin("C1", false)
	time.Sleep(1300 * time.Millisecond)
	if v := r.View(); v.TempMB < 0 || !fileExists(r.tmp) {
		t.Fatalf("não usou o temporário: %+v", v)
	}
	r.Mark(time.Now(), "sent")
	waitSaved(t, r, 5*time.Second)
	in := readAVI(t, file)
	if d := int(in.totalFrames) - 39; d < -1 || d > 1 {
		t.Fatalf("quadros: %d", in.totalFrames)
	}
	for _, c := range in.chunks {
		if len(c) > 0 {
			if colorIdx(decodeAt(t, c, 32, 24)) < 0 {
				t.Fatal("imagem estragada vinda do temporário")
			}
		}
	}
	if fileExists(r.tmp) {
		t.Fatal("o temporário não foi apagado")
	}
}

// Fechar o app nos 10 s depois do envio: salva o que tem (sem passar de 30 s).
func TestRecorderFecharDepoisDoEnvio(t *testing.T) {
	shortTimes(t, time.Second, 5*time.Second)
	cam := &fakeCam{w: 64, h: 48}
	r, logs, lmu := newTestRecorder(t, cam)
	file, _ := r.Begin("C1", false)
	time.Sleep(1200 * time.Millisecond)
	r.Mark(time.Now(), "sent")
	time.Sleep(500 * time.Millisecond)
	r.Close()
	in := readAVI(t, file)
	if d := int(in.totalFrames) - 45; d < -2 || d > 2 { // 1 s + 0,5 s
		t.Fatalf("quadros: %d", in.totalFrames)
	}
	if j := joinedLogs(logs, lmu); !strings.Contains(j, "fechado antes") {
		t.Fatalf("histórico: %s", j)
	}
}

// Parar sem envio: os últimos "20 s" e mais 2 s (como antes).
func TestRecorderParar(t *testing.T) {
	shortTimes(t, time.Second, 10*time.Second)
	cam := &fakeCam{w: 64, h: 48}
	r, _, _ := newTestRecorder(t, cam)
	file, _ := r.Begin("C1", false)
	time.Sleep(2 * time.Second)
	r.End(200 * time.Millisecond)
	waitSaved(t, r, 5*time.Second)
	in := readAVI(t, file)
	if d := int(in.totalFrames) - 36; d < -1 || d > 1 {
		t.Fatalf("quadros: %d", in.totalFrames)
	}
	_ = fmt.Sprint()
}

// Medição (só com NEXTTS_MEDIR=1): janela de mentira grande (NEXTTS_FAKE_SIZE, com
// NEXTTS_FAKE_BG de fundo), relógio e aviso rolando mudando a cada captura, tempos de
// verdade (20 s antes + 10 s depois). Mede capturas por segundo, memória e tamanho.
func TestMedirJanelaGrande(t *testing.T) {
	if os.Getenv("NEXTTS_MEDIR") == "" {
		t.Skip("defina NEXTTS_MEDIR=1 (e NEXTTS_FAKE_TS, NEXTTS_FAKE_SIZE, NEXTTS_FAKE_BG)")
	}
	r := NewRecorder(t.TempDir(), func(kind, text string) { t.Log(text) })
	rss := func() int64 { // memória do processo agora (KB)
		b, _ := os.ReadFile("/proc/self/status")
		for _, l := range strings.Split(string(b), "\n") {
			if f := strings.Fields(l); len(f) > 1 && f[0] == "VmRSS:" {
				var v int64
				fmt.Sscan(f[1], &v)
				return v
			}
		}
		return 0
	}
	// A janela de mentira (com o print de fundo) é montada antes, para não entrar na conta.
	captureTS(0)
	captureTS(1)
	runtime.GC()
	debug.FreeOSMemory()
	base := rss()
	var peakHeap uint64
	var peakRSS, peakRSSSave int64
	var saving atomic.Bool
	stop := make(chan bool)
	go func() {
		var ms runtime.MemStats
		for {
			select {
			case <-stop:
				return
			case <-time.After(100 * time.Millisecond):
			}
			runtime.ReadMemStats(&ms)
			peakHeap = max(peakHeap, ms.HeapInuse)
			if saving.Load() {
				peakRSSSave = max(peakRSSSave, rss())
			} else {
				peakRSS = max(peakRSS, rss())
			}
		}
	}()
	file, _ := r.Begin("C1", false)
	time.Sleep(25 * time.Second)
	t.Logf("antes do envio: %+v", r.View())
	T := time.Now()
	r.Mark(T, "sent")
	time.Sleep(10 * time.Second)
	saving.Store(true)
	time.Sleep(500 * time.Millisecond)
	r.Close()
	close(stop)
	t.Logf("memória do processo: %d MB antes de gravar; gravando, no máx. %d MB (+%d MB); salvando, no máx. %d MB",
		base>>10, peakRSS>>10, (peakRSS-base)>>10, peakRSSSave>>10)
	in := readAVI(t, file)
	images := 0
	for _, c := range in.chunks {
		if len(c) > 0 {
			images++
		}
	}
	fi, _ := os.Stat(file)
	p := r.lastPipe
	ram, _ := r.store.peaks()
	status, _ := os.ReadFile("/proc/self/status")
	hwm := ""
	for _, l := range strings.Split(string(status), "\n") {
		if strings.HasPrefix(l, "VmHWM") {
			hwm = strings.Join(strings.Fields(l)[1:], " ")
		}
	}
	t.Logf("vídeo %dx%d: %d quadros (%.3f s), %d imagens diferentes, %.1f MB", in.w, in.h, in.totalFrames, float64(in.totalFrames)/30, images, float64(fi.Size())/(1<<20))
	t.Logf("câmera: %d capturas, captura média %.1f ms, comparação+compressão média %.1f ms, %d horários perdidos",
		p.captures, float64(p.capTime.Microseconds())/float64(p.captures)/1000, float64(p.procTime.Microseconds())/float64(p.captures)/1000, p.skipped)
	t.Logf("memória: faixas comprimidas no máx. %s; heap do Go no máx. %.0f MB; pico de memória do processo (VmHWM) %s", fmtMB(ram), float64(peakHeap)/(1<<20), hwm)
	if d := int(in.totalFrames) - 900; d < -1 || d > 1 {
		t.Fatalf("quadros: %d", in.totalFrames)
	}
}
