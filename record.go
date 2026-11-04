package main

// Gravação em vídeo da janela do TeamSpeak durante o monitoramento (1.6.0).
//
//   - só o conteúdo da janela do TeamSpeak, do tamanho dela (sem faixa nem bordas);
//   - 30 quadros por segundo de verdade: a câmera captura a cada 1/30 s, num horário
//     fixo pelo relógio monotônico. Cada imagem fica no instante em que foi
//     capturada: se uma captura atrasar, o vídeo continua na velocidade real;
//   - o vídeo final tem só 30 s: os 20 s antes do envio do "!next" (instante T em que
//     o comando sai pelo ClientQuery) e os 10 s depois. Enquanto espera, o app guarda
//     só os últimos 20 s (memória circular): pode esperar horas sem encher nada;
//   - as imagens ficam guardadas já comprimidas, em faixas de 16 linhas (mjpeg.go):
//     só as faixas que mudaram são comprimidas de novo, e as iguais são compartilhadas
//     entre os quadros. Nada de centenas de imagens inteiras na memória;
//   - limite de memória: passou dele, as faixas novas vão para um arquivo temporário
//     rotativo (com limite de tamanho), apagado quando a gravação termina;
//   - formato AVI com imagens JPEG (MJPEG): abre no VLC e na maioria dos players;
//   - nunca apaga vídeos já salvos;
//   - nada disso atrapalha o monitoramento nem o envio (roda em paralelo).

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	RecFPS       = 30
	RecTailOther = 2 * time.Second // depois de Parar ou de o plano falhar (como antes)
	recDefaultW  = 1280            // tamanho do vídeo se o TS não estiver aberto no começo
	recDefaultH  = 720
	recFileMax   = 1900 << 20 // limite de um vídeo AVI (o formato não vai muito além de 2 GB)
)

// Tempos e limites (variáveis só para os testes poderem diminuir).
var (
	RecBefore        = 20 * time.Second // guardado antes do envio
	RecAfter         = 10 * time.Second // gravado depois do envio
	recRAMMax  int64 = 256 << 20        // faixas comprimidas na memória; passou disso, arquivo temporário
	recDiskMax int64 = 4 << 30          // limite do arquivo temporário
	recSlack   int64 = 64 << 20         // lixo que o temporário pode ter antes de ser reescrito
)

// slotOffset: instante do quadro k do vídeo (k/30 s), sem acumular erro de arredondamento.
func slotOffset(k int) time.Duration { return time.Duration(int64(k) * int64(time.Second) / RecFPS) }

// ---------- Letras (avisos no lugar da janela) ----------

var (
	fontOnce  sync.Once
	fontAlpha []byte
	fontIndex map[rune]int
)

func loadFont() {
	fontOnce.Do(func() {
		fontAlpha, _ = base64.StdEncoding.DecodeString(fontAlphaB64)
		fontIndex = map[rune]int{}
		i := 0
		for _, r := range fontRunes {
			fontIndex[r] = i
			i++
		}
	})
}

// drawText escreve `text` em (x, y) com a cor `c`, aumentando `scale` vezes.
// Devolve a posição x depois do texto. Corta no fim da imagem.
func drawText(img *image.RGBA, x, y int, text string, c color.RGBA, scale int) int {
	loadFont()
	if scale < 1 {
		scale = 1
	}
	b := img.Bounds()
	for _, r := range text {
		gi, ok := fontIndex[r]
		if !ok {
			gi = fontIndex['?']
		}
		if x+fontW*scale > b.Max.X {
			break
		}
		g := fontAlpha[gi*fontW*fontH : (gi+1)*fontW*fontH]
		for gy := 0; gy < fontH*scale; gy++ {
			py := y + gy
			if py < b.Min.Y || py >= b.Max.Y {
				continue
			}
			for gx := 0; gx < fontW*scale; gx++ {
				a := uint32(g[(gy/scale)*fontW+gx/scale])
				if a == 0 || x+gx < b.Min.X {
					continue
				}
				o := img.PixOffset(x+gx, py)
				p := img.Pix[o : o+4 : o+4]
				p[0] = uint8((uint32(c.R)*a + uint32(p[0])*(255-a)) / 255)
				p[1] = uint8((uint32(c.G)*a + uint32(p[1])*(255-a)) / 255)
				p[2] = uint8((uint32(c.B)*a + uint32(p[2])*(255-a)) / 255)
				p[3] = 255
			}
		}
		x += fontW * scale
	}
	return x
}

func textWidth(text string, scale int) int { return utf8.RuneCountInString(text) * fontW * scale }

func fillRect(img *image.RGBA, r image.Rectangle, c color.RGBA) {
	r = r.Intersect(img.Bounds())
	for y := r.Min.Y; y < r.Max.Y; y++ {
		o := img.PixOffset(r.Min.X, y)
		row := img.Pix[o : o+4*r.Dx()]
		for i := 0; i < len(row); i += 4 {
			row[i], row[i+1], row[i+2], row[i+3] = c.R, c.G, c.B, 255
		}
	}
}

// fillWith desenha `src` ocupando `dst` inteira. Do mesmo tamanho: copia direto.
// Se o TS mudou de tamanho no meio da gravação, ajusta ao tamanho do começo
// (sem faixas nem bordas).
func fillWith(dst, src *image.RGBA) {
	sb, db := src.Bounds(), dst.Bounds()
	sw, sh, dw, dh := sb.Dx(), sb.Dy(), db.Dx(), db.Dy()
	if sw <= 0 || sh <= 0 {
		return
	}
	if sw == dw && sh == dh {
		for y := 0; y < sh; y++ {
			so, do := src.PixOffset(sb.Min.X, sb.Min.Y+y), dst.PixOffset(db.Min.X, db.Min.Y+y)
			copy(dst.Pix[do:do+4*sw], src.Pix[so:so+4*sw])
		}
		return
	}
	for y := 0; y < dh; y++ {
		sy := sb.Min.Y + y*sh/dh
		do := dst.PixOffset(db.Min.X, db.Min.Y+y)
		for x := 0; x < dw; x++ {
			so := src.PixOffset(sb.Min.X+x*sw/dw, sy)
			copy(dst.Pix[do+4*x:do+4*x+4], src.Pix[so:so+4])
		}
	}
}

// recMargin: a memória circular guarda um pouco mais que os 20 s (o envio é marcado
// alguns milésimos depois de sair; assim o começo do recorte nunca falta).
const recMargin = 2 * time.Second

// ---------- Faixas comprimidas (memória + arquivo temporário rotativo) ----------

// seg é uma faixa comprimida. É compartilhada por todos os quadros em que ela é igual.
type seg struct {
	data    []byte // em memória (nil se estiver no arquivo temporário)
	off     int64  // posição no arquivo temporário (protegida por segStore.mu)
	n       int
	spooled bool // definido ao criar; nunca muda
	refs    int  // quadros que usam esta faixa (protegido por Recorder.mu)
}

// segStore guarda as faixas: na memória até recRAMMax; depois, num arquivo temporário
// (só dados comprimidos), que é reescrito sem o lixo quando cresce demais. Tudo some
// quando a gravação termina.
type segStore struct {
	mu       sync.Mutex
	dir      string
	ram      int64
	f        *os.File
	path     string
	end      int64 // tamanho do temporário
	live     int64 // bytes ainda usados no temporário
	spooled  map[*seg]struct{}
	peakRAM  int64
	peakDisk int64
	problem  string // não deu para usar o temporário (as faixas continuam na memória)
	told     string // último problema já avisado
}

func (st *segStore) put(b []byte) *seg {
	st.mu.Lock()
	defer st.mu.Unlock()
	s := &seg{n: len(b)}
	if st.ram+int64(len(b)) > recRAMMax && st.spool(s, b) {
		return s
	}
	// Nunca joga fora o que precisa: sem o temporário, passa do limite de memória (e avisa).
	s.data = b
	st.ram += int64(len(b))
	st.peakRAM = max(st.peakRAM, st.ram)
	return s
}

// spool grava a faixa no arquivo temporário. false = não deu (fica na memória).
func (st *segStore) spool(s *seg, b []byte) bool {
	if st.f == nil {
		if err := os.MkdirAll(st.dir, 0o755); err != nil {
			st.problem = "não consegui criar a pasta temporária (" + err.Error() + ")"
			return false
		}
		f, err := os.CreateTemp(st.dir, "gravacao-*.tmp")
		if err != nil {
			st.problem = "não consegui criar o arquivo temporário (" + err.Error() + ")"
			return false
		}
		st.f, st.path, st.spooled = f, f.Name(), map[*seg]struct{}{}
	}
	// Rotação: o arquivo só cresce para a frente; quando tem lixo demais, é reescrito.
	if st.end+int64(len(b)) > recDiskMax || st.end > 2*st.live+recSlack {
		st.compact()
	}
	if st.end+int64(len(b)) > recDiskMax {
		st.problem = "o arquivo temporário chegou ao limite de tamanho"
		return false
	}
	if _, err := st.f.WriteAt(b, st.end); err != nil {
		st.problem = "não consegui gravar no arquivo temporário (" + err.Error() + ")"
		return false
	}
	s.off, s.spooled = st.end, true
	st.end += int64(len(b))
	st.live += int64(len(b))
	st.spooled[s] = struct{}{}
	st.peakDisk = max(st.peakDisk, st.end)
	return true
}

// compact reescreve o temporário só com as faixas ainda usadas.
func (st *segStore) compact() {
	nf, err := os.CreateTemp(st.dir, "gravacao-*.tmp")
	if err != nil {
		return
	}
	var pos int64
	var buf []byte
	for s := range st.spooled {
		if cap(buf) < s.n {
			buf = make([]byte, s.n)
		}
		b := buf[:s.n]
		_, err := st.f.ReadAt(b, s.off)
		if err == nil {
			_, err = nf.WriteAt(b, pos)
		}
		if err != nil {
			nf.Close()
			os.Remove(nf.Name())
			return
		}
		s.off = pos
		pos += int64(s.n)
	}
	st.f.Close()
	os.Remove(st.path)
	st.f, st.path, st.end, st.live = nf, nf.Name(), pos, pos
}

func (st *segStore) drop(s *seg) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if s.spooled {
		st.live -= int64(s.n)
		delete(st.spooled, s)
	} else {
		st.ram -= int64(len(s.data))
		s.data = nil
	}
}

// bytesOf devolve os dados da faixa (lendo do temporário, se for o caso).
func (st *segStore) bytesOf(s *seg, scratch []byte) ([]byte, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if !s.spooled {
		return s.data, nil
	}
	if cap(scratch) < s.n {
		scratch = make([]byte, s.n)
	}
	b := scratch[:s.n]
	_, err := st.f.ReadAt(b, s.off)
	return b, err
}

func (st *segStore) usage() (ram, disk int64) {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.ram, st.end
}

func (st *segStore) peaks() (ram, disk int64) {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.peakRAM, st.peakDisk
}

// newProblem devolve o problema do temporário uma vez só ("" se não tiver novidade).
func (st *segStore) newProblem() string {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.problem == "" || st.problem == st.told {
		return ""
	}
	st.told = st.problem
	return st.problem
}

// close apaga o arquivo temporário (quando nenhuma gravação precisa mais dele).
func (st *segStore) close() {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.f != nil {
		st.f.Close()
		os.Remove(st.path)
		st.f, st.path, st.end, st.live, st.spooled = nil, "", 0, 0, nil
	}
	st.problem, st.told = "", ""
}

// ---------- Quadros ----------

// vframe é uma imagem inteira: uma faixa comprimida para cada 16 linhas.
type vframe struct {
	segs   []*seg
	codec  *bandCodec // tamanho e tabelas do JPEG (o mesmo em toda a gravação)
	notice capState   // capOK = imagem da janela; outro = tela de aviso (TS minimizado...)
	refs   int        // quem usa este quadro (câmera e gravações; protegido por Recorder.mu)
}

// clipFrame: um quadro na memória circular de uma gravação, com o instante em que
// ele foi capturado (relógio monotônico).
type clipFrame struct {
	at time.Time
	f  *vframe
}

// ---------- Situação da janela do TS na captura ----------

type capState int

const (
	capOK capState = iota
	capMinimized
	capNotFound
	capFailed
	capBlack    // achou a janela, mas a imagem veio toda preta
	capElevated // o TS está como administrador e o Next TS não: o Windows não deixa copiar
)

// allBlack confere (por amostragem) se a imagem veio toda preta.
func allBlack(img *image.RGBA) bool {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y += max(1, b.Dy()/40) {
		for x := b.Min.X; x < b.Max.X; x += max(1, b.Dx()/40) {
			if p := img.RGBAAt(x, y); p.R > 8 || p.G > 8 || p.B > 8 {
				return false
			}
		}
	}
	return true
}

// asImage: os bytes de uma imagem (BGRA ou RGBA, 4 bytes por ponto) vistos como *image.RGBA.
func asImage(pix []byte, w, h int) *image.RGBA {
	return &image.RGBA{Pix: pix[:w*h*4], Stride: w * 4, Rect: image.Rect(0, 0, w, h)}
}

var capNotes = map[capState]string{
	capOK:        "",
	capMinimized: "TeamSpeak minimizado: o vídeo mostra um aviso no lugar da janela.",
	capNotFound:  "Não achei a janela do TeamSpeak.",
	capFailed:    "Não consegui copiar a janela do TeamSpeak (motivo no registro.txt).",
	capBlack:     "A imagem da janela do TeamSpeak está vindo toda preta.",
	capElevated:  "O TeamSpeak está como administrador: abra o Next TS como administrador para gravar.",
}

var capLogs = map[capState]string{
	capMinimized: "Gravação: o TeamSpeak está minimizado. Enquanto estiver assim, o vídeo mostra um aviso no lugar da janela (o monitoramento continua normal).",
	capNotFound:  "Gravação: não achei a janela do TeamSpeak. Enquanto não achar, o vídeo mostra um aviso no lugar dela.",
	capFailed:    "Gravação: não consegui copiar a imagem da janela do TeamSpeak agora. Vou continuar tentando. O motivo fica anotado no registro.txt.",
	capBlack:     "Gravação: achei a janela do TeamSpeak, mas a imagem dela está vindo toda preta. O monitoramento continua normal. Se continuar assim, me avise (mande o registro.txt).",
	capElevated:  "Gravação: o TeamSpeak está aberto como administrador e o Next TS não; assim o Windows não deixa copiar a janela. Para gravar, feche o Next TS e abra de novo com o botão direito → \"Executar como administrador\" (ou abra o TeamSpeak sem administrador). O monitoramento continua normal.",
}

// ---------- Câmera (uma só, compartilhada pelas gravações em andamento) ----------

// pipeline captura a janela do TS a 30 quadros por segundo, compara com a imagem
// anterior faixa por faixa e comprime só as faixas que mudaram.
type pipeline struct {
	r           *Recorder
	after       *pipeline // câmera anterior (espera ela soltar a captura antes de começar)
	done        chan struct{}
	w, h        int
	codec       *bandCodec
	provisional bool     // tamanho padrão: a janela do TS ainda não apareceu
	prev        []byte   // a última imagem (BGRA)
	have        bool     // prev e cur já têm uma imagem completa
	canvas      []byte   // para ajustar o tamanho ou desenhar um aviso (só quando precisa)
	canvasSt    capState // o aviso que está desenhado no canvas (-1 = nenhum)
	src         []byte   // de onde os trabalhadores comprimem
	encErr      bool
	// compressão em paralelo (quantidade fixa de trabalhadores, fila com tamanho fixo)
	encs    []*bandEncoder
	jobs    chan int
	results [][]byte
	errs    []error
	wg      sync.WaitGroup
	changed []int
	// protegidos por Recorder.mu
	cur      *vframe // quadro atual (escrito só pela câmera)
	lastSt   capState
	captures int
	images   int
	skipped  int // horários de captura perdidos (atraso)
	capTime  time.Duration
	procTime time.Duration
}

func newPipeline(r *Recorder, after *pipeline) *pipeline {
	n := min(max(runtime.NumCPU()/2, 1), 4)
	p := &pipeline{r: r, after: after, done: make(chan struct{}), lastSt: -1, canvasSt: -1,
		jobs: make(chan int, 1<<16/bandH)} // cabe uma imagem inteira: nunca trava
	p.encs = make([]*bandEncoder, n)
	for i := range p.encs {
		p.encs[i] = &bandEncoder{}
	}
	return p
}

// shot é uma captura: a imagem (BGRA) e o instante em que foi feita.
type shot struct {
	at      time.Time
	pix     []byte
	w, h    int
	st      capState
	took    time.Duration
	skipped int // horários perdidos antes desta captura (atraso)
}

func (p *pipeline) run() {
	defer close(p.done)
	if p.after != nil {
		<-p.after.done // a câmera anterior ainda está soltando a captura
	}
	for i := 1; i < len(p.encs); i++ {
		go p.worker(p.encs[i])
	}
	defer close(p.jobs)
	shots := make(chan shot) // sem fila: no máximo uma captura esperando
	stop := make(chan struct{})
	go p.camera(shots, stop)
	for sh := range shots {
		c := time.Now()
		p.process(sh.at, sh.pix, sh.w, sh.h, sh.st)
		if !p.r.afterFrame(p, sh.at, sh.took, time.Since(c), sh.skipped) {
			close(stop) // nenhuma gravação precisa mais da câmera
			for range shots {
			}
			break
		}
	}
	p.r.release() // a câmera já parou: pode soltar a captura
}

// camera captura no horário (a cada 1/30 s, pelo relógio monotônico), em paralelo com
// a comparação e a compressão da captura anterior. Usa duas memórias de imagem, uma de
// cada vez: enquanto uma é comparada, a outra recebe a captura seguinte (a mesma só é
// usada de novo depois que a anterior foi entregue, ou seja, já foi processada).
// Se uma captura atrasar, os horários que passaram são pulados: cada imagem fica no
// instante real em que foi capturada.
func (p *pipeline) camera(shots chan<- shot, stop <-chan struct{}) {
	defer close(shots)
	t0 := time.Now()
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	buf, last := 0, -1
	for k := 0; ; buf ^= 1 {
		select {
		case <-stop:
			return
		default:
		}
		if d := time.Until(t0.Add(slotOffset(k))); d > 0 {
			timer.Reset(d)
			select {
			case <-timer.C:
			case <-stop:
				return
			}
		}
		at := time.Now()
		pix, w, h, st := p.r.capture(buf)
		sh := shot{at: at, pix: pix, w: w, h: h, st: st, took: time.Since(at), skipped: k - last - 1}
		select {
		case shots <- sh:
		case <-stop:
			return
		}
		last = k
		k = max(int(int64(time.Since(t0))*RecFPS/int64(time.Second))+1, k+1)
	}
}

func (p *pipeline) worker(e *bandEncoder) {
	for i := range p.jobs {
		p.encodeBand(e, i)
		p.wg.Done()
	}
}

func (p *pipeline) encodeBand(e *bandEncoder, i int) {
	y0, y1 := p.codec.bandRows(i)
	p.results[i], p.errs[i] = e.encode(p.src, p.w, y0, y1)
}

// encodeList comprime as faixas da lista (de p.src); várias de uma vez, em paralelo.
func (p *pipeline) encodeList(list []int) {
	if len(list) <= 2 || len(p.encs) == 1 {
		for _, i := range list {
			p.encodeBand(p.encs[0], i)
		}
		return
	}
	p.wg.Add(len(list))
	for _, i := range list {
		p.jobs <- i
	}
	for idle := false; !idle; { // a própria câmera também trabalha
		select {
		case i := <-p.jobs:
			p.encodeBand(p.encs[0], i)
			p.wg.Done()
		default:
			idle = true
		}
	}
	p.wg.Wait()
}

// newFrame monta um quadro: as faixas da lista vêm de p.results; as outras, de base.
// nil = não deu (só quando não tem base).
func (p *pipeline) newFrame(list []int, base *vframe, notice capState) *vframe {
	f := &vframe{segs: make([]*seg, p.codec.bands), codec: p.codec, notice: notice}
	if base != nil {
		copy(f.segs, base.segs)
	}
	stride := p.w * 4
	for _, i := range list {
		if err := p.errs[i]; err != nil {
			if !p.encErr {
				p.encErr = true
				p.r.log("warn", "Gravação: erro ao comprimir a imagem (%s).", err.Error())
			}
			if base == nil {
				for _, s := range f.segs {
					if s != nil {
						p.r.store.drop(s)
					}
				}
				return nil
			}
			y0, _ := p.codec.bandRows(i)
			p.prev[y0*stride] ^= 0xff // fica a faixa anterior; na próxima captura tenta de novo
			continue
		}
		f.segs[i] = p.r.store.put(p.results[i])
		p.results[i] = nil
	}
	if msg := p.r.store.newProblem(); msg != "" {
		p.r.log("warn", "Gravação: %s. O vídeo continua (as imagens ficam na memória).", msg)
	}
	return f
}

// setSize prepara a câmera para o tamanho do vídeo: o do conteúdo da janela do TS ou,
// enquanto ela não aparece, 1280x720.
func (p *pipeline) setSize(st capState, w, h int) {
	cw, ch := recDefaultW, recDefaultH
	if st == capOK {
		cw, ch = w, h
	}
	c, err := newBandCodec(cw, ch)
	if err != nil {
		p.r.log("warn", "Gravação: a janela do TeamSpeak tem um tamanho que não dá para gravar (%dx%d); o vídeo fica com %dx%d.", cw, ch, recDefaultW, recDefaultH)
		cw, ch = recDefaultW, recDefaultH
		c, _ = newBandCodec(cw, ch)
	}
	old := p.codec
	p.codec, p.w, p.h = c, cw, ch
	p.provisional = st != capOK
	p.prev = make([]byte, cw*ch*4)
	p.canvas, p.canvasSt, p.have = nil, -1, false
	p.results, p.errs = make([][]byte, c.bands), make([]error, c.bands)
	if old != nil {
		p.redoNotices(old)
	}
}

// redoNotices: a janela do TS apareceu depois do Iniciar. As telas de aviso guardadas
// até agora (no tamanho padrão) são refeitas no tamanho da janela, para o vídeo ter
// um tamanho só e nada da memória circular se perder.
func (p *pipeline) redoNotices(old *bandCodec) {
	all := make([]int, p.codec.bands)
	for i := range all {
		all[i] = i
	}
	repl := map[capState]*vframe{}
	var cv []byte
	for _, st := range p.r.noticesOf(old) {
		if cv == nil {
			cv = make([]byte, p.w*p.h*4)
		}
		drawNotice(asImage(cv, p.w, p.h), st)
		p.src = cv
		p.encodeList(all)
		if f := p.newFrame(all, nil, st); f != nil {
			repl[st] = f
		}
	}
	p.r.replaceFrames(p, old, repl)
}

func (p *pipeline) process(at time.Time, pix []byte, w, h int, st capState) {
	if st == capOK && (w <= 0 || h <= 0 || len(pix) < w*h*4) {
		st = capFailed
	}
	if st == capOK && allBlack(asImage(pix, w, h)) {
		st = capBlack
	}
	p.r.noteCapture(p, st)
	if p.codec == nil || (p.provisional && st == capOK) {
		p.setSize(st, w, h)
	}
	src := pix
	if st != capOK || w != p.w || h != p.h {
		if p.canvas == nil {
			p.canvas = make([]byte, p.w*p.h*4)
		}
		cv := asImage(p.canvas, p.w, p.h)
		if st == capOK {
			fillWith(cv, asImage(pix, w, h)) // o TS mudou de tamanho: ajusta ao tamanho do começo
			p.canvasSt = -1
		} else if p.canvasSt != st {
			drawNotice(cv, st)
			p.canvasSt = st
		}
		src = p.canvas
	}
	// Faixas que mudaram desde a captura anterior.
	p.changed = p.changed[:0]
	stride := p.w * 4
	for i := 0; i < p.codec.bands; i++ {
		y0, y1 := p.codec.bandRows(i)
		a, b := src[y0*stride:y1*stride], p.prev[y0*stride:y1*stride]
		if !p.have || !bytes.Equal(a, b) {
			copy(b, a)
			p.changed = append(p.changed, i)
		}
	}
	if len(p.changed) == 0 {
		p.r.publish(p, at, nil) // imagem igual: o vídeo repete a anterior (sem ocupar espaço)
		return
	}
	p.src = p.prev
	p.encodeList(p.changed)
	var base *vframe
	if p.have {
		base = p.cur
	}
	f := p.newFrame(p.changed, base, st)
	if f == nil {
		p.r.publish(p, at, nil)
		return
	}
	p.have = true
	p.r.publish(p, at, f)
}

// ---------- Gravações ----------

// RecView é o que a janelinha mostra sobre a gravação.
type RecView struct {
	On        bool    `json:"on"`        // gravando ou salvando
	Phase     string  `json:"phase"`     // buffer (guardando os últimos 20 s), after (10 s depois do envio), saving
	Kind      string  `json:"kind"`      // depois de quê: sent (envio), tested (teria enviado), stopped (parou)
	File      string  `json:"file"`      // nome do vídeo
	Note      string  `json:"note"`      // aviso (ex.: TS minimizado)
	Kept      float64 `json:"kept"`      // segundos guardados agora (o que entraria no vídeo)
	AfterLeft float64 `json:"afterLeft"` // segundos que faltam depois do envio
	FPS       float64 `json:"fps"`       // capturas por segundo de verdade (últimos 2 s)
	MB        int     `json:"mb"`        // memória usada pelas imagens comprimidas
	TempMB    int     `json:"tempMB"`    // arquivo temporário
	Others    int     `json:"others"`    // vídeos de monitoramentos anteriores ainda terminando
	Dir       string  `json:"dir"`       // pasta das gravações
	Last      string  `json:"last"`      // último vídeo salvo (nome)
	LastInfo  string  `json:"lastInfo"`  // ex.: "30,0 s — 20,0 s antes e 10,0 s depois do envio"
	HasLast   bool    `json:"hasLast"`   // tem algum vídeo para abrir
}

type recSession struct {
	base   string // caminho do vídeo sem ".avi"
	file   string
	pipe   *pipeline
	frames []clipFrame // memória circular (o primeiro pode ser anterior à janela: é a imagem na borda)
	caps   []time.Time // instantes das capturas (para medir o fps de verdade)
	// Fim: ref = instante de referência (T do envio, ou de quando parou);
	// o vídeo vai de ref-20 s até endAt.
	kind   string // "sent", "tested", "stopped", "closed" ("" = ainda monitorando)
	ref    time.Time
	endAt  time.Time
	cut    bool // o app foi fechado antes da hora
	saving bool
	done   chan struct{}
}

// lower: até onde a memória circular precisa voltar.
func (s *recSession) lower(now time.Time) time.Time {
	if !s.ref.IsZero() {
		return s.ref.Add(-RecBefore) // depois do envio, os 20 s antes ficam congelados
	}
	return now.Add(-RecBefore)
}

type Recorder struct {
	mu       sync.Mutex
	dir      string
	tmp      string
	store    *segStore
	pipe     *pipeline // câmera ligada
	lastPipe *pipeline
	cur      *recSession   // gravação do monitoramento atual (ou do último)
	active   []*recSession // recebendo quadros
	saving   []*recSession
	names    map[string]bool // nomes de vídeo em uso
	note     string
	last     string
	lastPath string
	lastInfo string
	logf     func(kind, text string) // avisa o histórico do app (nunca chamado com mu travado)
	// capture copia a janela do TS para a memória de imagem buf (0 ou 1) e devolve os
	// pontos em BGRA (valem até a próxima captura nessa mesma memória); release solta o
	// que a captura guardou. Trocáveis nos testes.
	capture func(buf int) ([]byte, int, int, capState)
	release func()
}

func NewRecorder(dir string, logf func(kind, text string)) *Recorder {
	r := &Recorder{dir: dir, tmp: filepath.Join(dir, ".temp"), logf: logf, capture: captureTS, release: captureRelease, names: map[string]bool{}}
	r.store = &segStore{dir: r.tmp}
	r.lastPath = newestVideo(dir) // o último vídeo de antes de abrir o app
	// Temporários que sobraram de uma vez em que o app foi fechado à força
	// (nunca são vídeos prontos: esses ficam fora da pasta .temp).
	if old, _ := filepath.Glob(filepath.Join(r.tmp, "gravacao-*")); len(old) > 0 {
		for _, f := range old {
			os.Remove(f)
		}
		os.Remove(r.tmp)
		fileLog("[gravação] %d arquivo(s) temporário(s) que sobraram apagado(s) da pasta .temp", len(old))
	}
	return r
}

// newestVideo acha o vídeo mais novo da pasta ("" se não tiver nenhum).
func newestVideo(dir string) string {
	fs, _ := filepath.Glob(filepath.Join(dir, "*.avi"))
	best, bestT := "", time.Time{}
	for _, f := range fs {
		if fi, err := os.Stat(f); err == nil && fi.ModTime().After(bestT) {
			best, bestT = f, fi.ModTime()
		}
	}
	return best
}

// LastVideo devolve o caminho do último vídeo salvo ("" se não tiver).
func (r *Recorder) LastVideo() string {
	r.mu.Lock()
	p := r.lastPath
	r.mu.Unlock()
	if p != "" && fileExists(p) {
		return p
	}
	return newestVideo(r.dir)
}

func (r *Recorder) Dir() string { return r.dir }

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

func (r *Recorder) log(kind, format string, args ...any) {
	if r.logf != nil {
		r.logf(kind, fmt.Sprintf(format, args...))
	}
}

// Begin começa a gravação de um monitoramento. Não mexe nas anteriores: uma que ainda
// está gravando os 10 s depois de um envio continua até o fim, no arquivo dela.
func (r *Recorder) Begin(code string, test bool) (string, error) {
	if err := os.MkdirAll(r.dir, 0o755); err != nil {
		return "", err
	}
	name := now().Format("2006-01-02_15-04-05") + "_" + code
	if test {
		name += "_teste"
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	base := filepath.Join(r.dir, name)
	for i := 2; fileExists(base+".avi") || r.names[base]; i++ {
		base = filepath.Join(r.dir, fmt.Sprintf("%s_%d", name, i))
	}
	if c := r.cur; c != nil && c.kind == "" && contains(r.active, c) {
		r.endLocked(c, "stopped", time.Now(), RecTailOther) // não deveria acontecer: o monitoramento anterior já acabou
	}
	s := &recSession{base: base, file: filepath.Base(base + ".avi"), done: make(chan struct{})}
	r.names[base] = true
	r.cur = s
	r.active = append(r.active, s)
	if r.pipe == nil {
		r.pipe = newPipeline(r, r.lastPipe)
		r.lastPipe, r.note = r.pipe, ""
		go r.pipe.run()
	} else {
		s.push(time.Now(), r.pipe.cur) // começa com a imagem atual
	}
	s.pipe = r.pipe
	return base + ".avi", nil
}

// Mark marca o instante T em que o "!next" saiu (ou o "teria enviado", no Modo teste):
// o vídeo fica com os 20 s antes de T e continua gravando até T + 10 s.
func (r *Recorder) Mark(t time.Time, kind string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s := r.cur; s != nil && s.kind == "" && contains(r.active, s) {
		r.endLocked(s, kind, t, RecAfter)
	}
}

// End: o monitoramento acabou sem envio (Parar, falha, janela encerrada). O vídeo
// fica com os últimos 20 s e mais `tail` depois. Não faz nada se o envio já foi marcado.
func (r *Recorder) End(tail time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s := r.cur; s != nil && s.kind == "" && contains(r.active, s) {
		r.endLocked(s, "stopped", time.Now(), tail)
	}
}

func (r *Recorder) endLocked(s *recSession, kind string, ref time.Time, tail time.Duration) {
	s.kind, s.ref, s.endAt = kind, ref, ref.Add(tail)
	s.trim(ref, r)
	// Termina na hora certa mesmo se a captura travar.
	time.AfterFunc(time.Until(s.endAt)+50*time.Millisecond, func() {
		r.mu.Lock()
		r.finishDue(time.Now())
		r.mu.Unlock()
	})
}

// Close: o app vai fechar. Salva o que tiver (no máximo os últimos 30 s) e espera os
// vídeos ficarem prontos. Não pode ser chamado com o a.mu do app travado.
func (r *Recorder) Close() {
	r.mu.Lock()
	t := time.Now()
	for _, s := range r.active {
		if s.kind == "" {
			s.kind, s.ref = "closed", t
		}
		if s.endAt.After(t) {
			s.cut = s.kind == "sent" || s.kind == "tested"
		}
		s.endAt = t
	}
	r.finishDue(t)
	waits := append([]*recSession(nil), r.saving...)
	p := r.pipe
	r.mu.Unlock()
	deadline := time.After(30 * time.Second)
	for _, s := range waits {
		select {
		case <-s.done:
		case <-deadline:
			return
		}
	}
	if p != nil {
		select {
		case <-p.done:
		case <-deadline:
		}
	}
}

// push põe um quadro na memória circular da gravação. Precisa do Recorder.mu.
func (s *recSession) push(at time.Time, f *vframe) {
	if f == nil || (!s.endAt.IsZero() && at.After(s.endAt)) {
		return
	}
	n := len(s.frames)
	if n > 0 && s.frames[n-1].f.codec != f.codec {
		return // (não acontece: o tamanho do vídeo não muda no meio)
	}
	if n == 0 || s.frames[n-1].f != f {
		f.refs++
		s.frames = append(s.frames, clipFrame{at, f})
	}
	s.caps = append(s.caps, at)
}

// trim descarta o que ficou velho: fica só a janela de 20 s (com uma pequena folga) e
// a imagem que estava na tela no começo dela. Precisa do Recorder.mu.
func (s *recSession) trim(now time.Time, r *Recorder) {
	lo := s.lower(now)
	if s.ref.IsZero() {
		lo = lo.Add(-recMargin)
	}
	i := 0
	for i+1 < len(s.frames) && !s.frames[i+1].at.After(lo) {
		r.unref(s.frames[i].f)
		s.frames[i].f = nil
		i++
	}
	if i > 0 {
		n := copy(s.frames, s.frames[i:])
		clear(s.frames[n:])
		s.frames = s.frames[:n]
	}
	j := 0
	for j < len(s.caps) && s.caps[j].Before(lo) {
		j++
	}
	if j > 0 {
		s.caps = s.caps[:copy(s.caps, s.caps[j:])]
	}
}

// hold: mais um dono para o quadro (e as faixas dele, se ele é novo). Precisa do Recorder.mu.
func (r *Recorder) hold(f *vframe) {
	if f.refs == 0 {
		for _, s := range f.segs {
			s.refs++
		}
	}
	f.refs++
}

// unref: um dono a menos; sem nenhum, as faixas que só ele usava são descartadas.
func (r *Recorder) unref(f *vframe) {
	if f == nil {
		return
	}
	if f.refs--; f.refs > 0 {
		return
	}
	for _, s := range f.segs {
		if s.refs--; s.refs == 0 {
			r.store.drop(s)
		}
	}
	f.segs = nil
}

// publish entrega o quadro capturado (nil = igual ao anterior) às gravações.
func (r *Recorder) publish(p *pipeline, at time.Time, f *vframe) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if f != nil {
		r.hold(f) // a câmera guarda o quadro atual
		r.unref(p.cur)
		p.cur = f
		p.images++
	}
	for _, s := range r.active {
		if s.pipe == p {
			s.push(at, p.cur)
			s.trim(at, r)
		}
	}
}

// noticesOf: as telas de aviso guardadas com o tamanho antigo.
func (r *Recorder) noticesOf(old *bandCodec) []capState {
	r.mu.Lock()
	defer r.mu.Unlock()
	seen := map[capState]bool{}
	var out []capState
	for _, s := range r.active {
		for _, cf := range s.frames {
			if cf.f.codec == old && !seen[cf.f.notice] {
				seen[cf.f.notice] = true
				out = append(out, cf.f.notice)
			}
		}
	}
	return out
}

// replaceFrames troca as telas de aviso do tamanho antigo pelas refeitas.
func (r *Recorder) replaceFrames(p *pipeline, old *bandCodec, repl map[capState]*vframe) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, f := range repl {
		r.hold(f)
	}
	for _, s := range r.active {
		out := s.frames[:0]
		for _, cf := range s.frames {
			if cf.f.codec != old {
				out = append(out, cf)
				continue
			}
			nf := repl[cf.f.notice]
			r.unref(cf.f)
			if nf == nil || (len(out) > 0 && out[len(out)-1].f == nf) {
				continue
			}
			nf.refs++
			out = append(out, clipFrame{cf.at, nf})
		}
		clear(s.frames[len(out):])
		s.frames = out
	}
	r.unref(p.cur)
	p.cur = nil
	for _, f := range repl {
		r.unref(f)
	}
}

// afterFrame: depois de cada captura. Termina as gravações que chegaram ao fim.
// false = a câmera pode parar (nenhuma gravação precisa dela).
func (r *Recorder) afterFrame(p *pipeline, at time.Time, capDur, procDur time.Duration, skipped int) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	p.captures++
	p.capTime += capDur
	p.procTime += procDur
	p.skipped += skipped
	r.finishDue(at)
	if len(r.active) > 0 && r.pipe == p {
		return true
	}
	if r.pipe == p {
		r.pipe = nil
	}
	r.unref(p.cur)
	p.cur = nil
	r.cleanupLocked()
	return false
}

// cleanupLocked apaga o temporário quando nada mais precisa dele. Precisa do Recorder.mu.
func (r *Recorder) cleanupLocked() {
	if r.pipe == nil && len(r.saving) == 0 && len(r.active) == 0 {
		r.store.close()
		os.Remove(r.tmp) // só se estiver vazia
	}
}

// finishDue tira da câmera as gravações que chegaram ao fim e manda salvar o vídeo.
// Precisa do Recorder.mu.
func (r *Recorder) finishDue(now time.Time) {
	keep := r.active[:0]
	for _, s := range r.active {
		if s.endAt.IsZero() || now.Before(s.endAt) {
			keep = append(keep, s)
			continue
		}
		s.saving = true
		r.saving = append(r.saving, s)
		go r.save(s)
	}
	clear(r.active[len(keep):])
	r.active = keep
}

func (r *Recorder) noteCapture(p *pipeline, st capState) {
	r.mu.Lock()
	changed := st != p.lastSt
	first := p.lastSt == -1
	if changed {
		p.lastSt = st
		if r.pipe == p {
			r.note = capNotes[st]
		}
	}
	r.mu.Unlock()
	if !changed {
		return
	}
	switch {
	case st == capOK && !first:
		r.log("info", "Gravação: voltei a ver a janela do TeamSpeak.")
	case st != capOK:
		r.log("warn", "%s", capLogs[st])
	}
}

func (r *Recorder) View() RecView {
	ram, disk := r.store.usage()
	r.mu.Lock()
	defer r.mu.Unlock()
	v := RecView{Dir: r.dir, Last: r.last, LastInfo: r.lastInfo, HasLast: r.lastPath != "", MB: int((ram + 1<<19) >> 20), TempMB: int((disk + 1<<19) >> 20)}
	s := r.cur
	for _, x := range append(append([]*recSession(nil), r.active...), r.saving...) {
		if x != s {
			v.Others++
		}
	}
	if s == nil {
		return v
	}
	if s.saving {
		v.On, v.Phase, v.File = true, "saving", s.file
		return v
	}
	if !contains(r.active, s) {
		return v
	}
	t := time.Now()
	v.On, v.File, v.Note = true, s.file, r.note
	v.Phase, v.Kind = "buffer", s.kind
	end := t
	if s.kind != "" {
		v.Phase = "after"
		v.AfterLeft = max(0, s.endAt.Sub(t).Seconds())
		if s.endAt.Before(end) {
			end = s.endAt
		}
	}
	if len(s.frames) > 0 {
		from := s.lower(t)
		if s.frames[0].at.After(from) {
			from = s.frames[0].at
		}
		v.Kept = max(0, end.Sub(from).Seconds())
	}
	// fps de verdade: capturas do último segundo e meio (ou desde o começo).
	if len(s.caps) > 1 && s.kind == "" {
		from := t.Add(-1500 * time.Millisecond)
		if s.caps[0].After(from) {
			from = s.caps[0]
		}
		n := 0
		for i := len(s.caps) - 1; i >= 0 && !s.caps[i].Before(from); i-- {
			n++
		}
		if span := t.Sub(from).Seconds(); span >= 0.3 {
			v.FPS = min(float64(RecFPS), float64(n-1)/span)
		}
	}
	return v
}

func contains(list []*recSession, s *recSession) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// ---------- Salvar o recorte ----------

// clipPlan diz de onde até onde vai o vídeo e quantos quadros ele tem.
type clipPlan struct {
	start, end time.Time
	slots      int
	partial    bool          // não tinha os 20 s antes inteiros
	before     time.Duration // quanto tem antes da referência
}

func planClip(s *recSession) clipPlan {
	var p clipPlan
	if len(s.frames) == 0 {
		return p
	}
	p.end = s.endAt
	ref := s.ref
	if ref.IsZero() || ref.After(p.end) {
		ref = p.end
	}
	p.start = ref.Add(-RecBefore)
	if s.frames[0].at.After(p.start) {
		p.start = s.frames[0].at // não tinha 20 s guardados ainda
		p.partial = true
	}
	p.before = ref.Sub(p.start)
	if !p.end.After(p.start) {
		p.end = p.start.Add(slotOffset(1))
	}
	p.slots = max(1, int((p.end.Sub(p.start)*RecFPS+time.Second/2)/time.Second))
	return p
}

func (r *Recorder) save(s *recSession) {
	defer close(s.done)
	r.mu.Lock()
	frames := append([]clipFrame(nil), s.frames...)
	caps := append([]time.Time(nil), s.caps...)
	plan := planClip(s)
	kind, cut, p := s.kind, s.cut, s.pipe
	r.mu.Unlock()

	path, info, err := r.writeClip(s, frames, plan)

	r.mu.Lock()
	for _, cf := range s.frames {
		r.unref(cf.f)
	}
	s.frames, s.caps = nil, nil
	s.saving = false
	r.saving = removeSession(r.saving, s)
	delete(r.names, s.base)
	var camera string
	if p != nil && p.captures > 0 {
		camera = fmt.Sprintf("; câmera: %d capturas, captura média %.1f ms, comparação e compressão média %.1f ms, %d horários perdidos por atraso",
			p.captures, float64(p.capTime.Microseconds())/float64(p.captures)/1000, float64(p.procTime.Microseconds())/float64(p.captures)/1000, p.skipped)
	}
	dur := slotOffset(plan.slots)
	what := clipWhat(kind, cut, plan, dur)
	if err == nil && info.images > 0 {
		r.last, r.lastPath = filepath.Base(path), path
		r.lastInfo = secsBR(dur) + " — " + what
	}
	r.cleanupLocked()
	r.mu.Unlock()
	ram, disk := r.store.peaks()

	if err != nil {
		r.log("warn", "Não consegui salvar a gravação (%s). O monitoramento continua normal.", err.Error())
		return
	}
	if info.images == 0 {
		r.log("info", "Gravação: não deu tempo de capturar nenhuma imagem; nenhum vídeo salvo.")
		return
	}
	// fps de verdade: capturas feitas dentro do trecho do vídeo, por segundo.
	n := 0
	for _, c := range caps {
		if !c.Before(plan.start) && c.Before(plan.end) {
			n++
		}
	}
	fps := float64(n) / plan.end.Sub(plan.start).Seconds()
	r.log("ok", "Vídeo salvo: %s (%s; %s; 30 fps, %s capturas por segundo de verdade; %s).",
		path, secsBR(dur), what, strings.Replace(fmt.Sprintf("%.1f", fps), ".", ",", 1), fmtMB(info.size))
	fileLog("[gravação] vídeo %s: %d quadros (%s), %d imagens diferentes, %.2f capturas/s, memória máx. %s, temporário máx. %s%s",
		filepath.Base(path), plan.slots, secsBR(dur), info.images, fps, fmtMB(ram), fmtMB(disk), camera)
}

// clipWhat descreve o trecho do vídeo, para o Histórico.
func clipWhat(kind string, cut bool, plan clipPlan, dur time.Duration) string {
	before, after := secsBR(plan.before), secsBR(max(0, dur-plan.before))
	switch kind {
	case "sent", "tested":
		ev := "do envio"
		if kind == "tested" {
			ev = "do \"teria enviado\""
		}
		s := before + " antes e " + after + " depois " + ev
		if plan.partial {
			s = "parcial: só " + before + " antes (o monitoramento tinha começado há pouco) e " + after + " depois " + ev
		}
		if cut {
			s += "; o app foi fechado antes dos 10 s depois"
		}
		return s
	case "closed":
		return "o app foi fechado; ficaram os últimos " + before + " disponíveis"
	}
	return "os últimos " + before + " antes de parar e " + after + " depois"
}

func removeSession(list []*recSession, s *recSession) []*recSession {
	out := list[:0]
	for _, x := range list {
		if x != s {
			out = append(out, x)
		}
	}
	clear(list[len(out):])
	return out
}

func secsBR(d time.Duration) string {
	return strings.Replace(fmt.Sprintf("%.1f s", d.Seconds()), ".", ",", 1)
}

type clipInfo struct {
	images int
	size   int64
}

// writeClip escreve o vídeo do recorte: um quadro a cada 1/30 s, de plan.start a
// plan.end. Cada quadro mostra a imagem que estava na tela naquele instante. O
// primeiro é sempre uma imagem completa; um quadro igual ao anterior é gravado como
// "repete o anterior" (não ocupa espaço). Escreve num temporário e só no fim dá o
// nome definitivo (sem nunca substituir um vídeo que já existe).
func (r *Recorder) writeClip(s *recSession, frames []clipFrame, plan clipPlan) (string, clipInfo, error) {
	var info clipInfo
	if len(frames) == 0 || plan.slots == 0 {
		return "", info, nil
	}
	codec := frames[0].f.codec
	if err := os.MkdirAll(r.tmp, 0o755); err != nil {
		return "", info, err
	}
	tmpPath := filepath.Join(r.tmp, "gravacao-"+filepath.Base(s.base)+".avi.part")
	a, err := newAVI(tmpPath, codec.w, codec.h)
	if err != nil {
		return "", info, err
	}
	ok := false
	defer func() {
		if !ok {
			a.abort()
			os.Remove(tmpPath)
		}
	}()
	var (
		jp      []byte
		scratch [][]byte
		parts   [][]byte
		prev    *vframe
		j       int
	)
	for k := 0; k < plan.slots; k++ {
		t := plan.start.Add(slotOffset(k))
		for j+1 < len(frames) && !frames[j+1].at.After(t) {
			j++
		}
		f := frames[j].f
		if f == prev || (prev != nil && a.end > recFileMax) {
			if err := a.frame(nil); err != nil {
				return "", info, err
			}
			continue
		}
		if len(scratch) < len(f.segs) {
			scratch = make([][]byte, len(f.segs))
			parts = make([][]byte, len(f.segs))
		}
		for i, sg := range f.segs {
			b, err := r.store.bytesOf(sg, scratch[i])
			if err != nil {
				return "", info, err
			}
			if sg.spooled {
				scratch[i] = b
			}
			parts[i] = b
		}
		jp = codec.assemble(jp, parts[:len(f.segs)])
		if err := a.frame(jp); err != nil {
			return "", info, err
		}
		prev = f
		info.images++
	}
	if err := a.close(); err != nil {
		return "", info, err
	}
	final := s.base + ".avi"
	for i := 2; fileExists(final); i++ { // nunca substitui um vídeo que já existe
		final = fmt.Sprintf("%s_%d.avi", s.base, i)
	}
	if err := os.Rename(tmpPath, final); err != nil {
		return "", info, err
	}
	ok = true
	if fi, err := os.Stat(final); err == nil {
		info.size = fi.Size()
	}
	return final, info, nil
}

// ---------- Arquivo AVI (MJPEG, 30 quadros por segundo) ----------

// aviWriter grava um AVI simples com um único fluxo de vídeo MJPEG, em sequência.
type aviWriter struct {
	f        *os.File
	bw       *bufio.Writer
	w, h     int
	frames   int
	idx      bytes.Buffer
	end      int64 // tamanho atual do arquivo
	maxChunk uint32
}

const aviMoviFourCC = 220 // posição do "movi" (os índices contam a partir dele)

func le32(b []byte, off int, v uint32) { binary.LittleEndian.PutUint32(b[off:], v) }

func newAVI(path string, w, h int) (*aviWriter, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	hd := make([]byte, 224)
	copy(hd[0:], "RIFF")
	copy(hd[8:], "AVI ")
	copy(hd[12:], "LIST")
	le32(hd, 16, 192)
	copy(hd[20:], "hdrl")
	copy(hd[24:], "avih")
	le32(hd, 28, 56)
	le32(hd, 32, uint32(slotOffset(1)/time.Microsecond)) // microssegundos por quadro (33333)
	le32(hd, 44, 0x10)                                   // AVIF_HASINDEX
	le32(hd, 56, 1)                                      // 1 fluxo
	le32(hd, 64, uint32(w))
	le32(hd, 68, uint32(h))
	copy(hd[88:], "LIST")
	le32(hd, 92, 116)
	copy(hd[96:], "strl")
	copy(hd[100:], "strh")
	le32(hd, 104, 56)
	copy(hd[108:], "vids")
	copy(hd[112:], "MJPG")
	le32(hd, 128, 1)          // dwScale
	le32(hd, 132, RecFPS)     // dwRate: 30 quadros por segundo
	le32(hd, 148, 0xFFFFFFFF) // dwQuality: padrão
	binary.LittleEndian.PutUint16(hd[160:], uint16(w))
	binary.LittleEndian.PutUint16(hd[162:], uint16(h))
	copy(hd[164:], "strf")
	le32(hd, 168, 40)
	le32(hd, 172, 40)
	le32(hd, 176, uint32(w))
	le32(hd, 180, uint32(h))
	binary.LittleEndian.PutUint16(hd[184:], 1)
	binary.LittleEndian.PutUint16(hd[186:], 24)
	copy(hd[188:], "MJPG")
	le32(hd, 192, uint32(w*h*3))
	copy(hd[212:], "LIST")
	copy(hd[220:], "movi")
	a := &aviWriter{f: f, bw: bufio.NewWriterSize(f, 1<<20), w: w, h: h}
	if _, err := a.bw.Write(hd); err != nil {
		f.Close()
		return nil, err
	}
	a.end = int64(len(hd))
	return a, nil
}

// frame grava um quadro. data vazio = "repete o quadro anterior" (não ocupa espaço).
func (a *aviWriter) frame(data []byte) error {
	n := len(data)
	var ck [8]byte
	copy(ck[:], "00dc")
	le32(ck[:], 4, uint32(n))
	if _, err := a.bw.Write(ck[:]); err != nil {
		return err
	}
	if _, err := a.bw.Write(data); err != nil {
		return err
	}
	pad := n % 2
	if pad == 1 {
		if err := a.bw.WriteByte(0); err != nil {
			return err
		}
	}
	var e [16]byte
	copy(e[:], "00dc")
	if n > 0 {
		le32(e[:], 4, 0x10) // quadro completo (keyframe)
	}
	le32(e[:], 8, uint32(a.end-aviMoviFourCC))
	le32(e[:], 12, uint32(n))
	a.idx.Write(e[:])
	a.end += int64(8 + n + pad)
	a.frames++
	if uint32(n) > a.maxChunk {
		a.maxChunk = uint32(n)
	}
	return nil
}

// close escreve o índice, acerta o cabeçalho (tamanhos e quantidade de quadros) e fecha.
func (a *aviWriter) close() error {
	movEnd := a.end
	var hd [8]byte
	copy(hd[:], "idx1")
	le32(hd[:], 4, uint32(a.idx.Len()))
	a.bw.Write(hd[:])
	a.bw.Write(a.idx.Bytes())
	err := a.bw.Flush()
	total := movEnd + 8 + int64(a.idx.Len())
	put := func(off int64, v uint32) {
		if err != nil {
			return
		}
		var b [4]byte
		le32(b[:], 0, v)
		_, err = a.f.WriteAt(b[:], off)
	}
	put(4, uint32(total-8))
	put(216, uint32(movEnd-aviMoviFourCC))
	put(48, uint32(a.frames))
	put(140, uint32(a.frames))
	put(60, a.maxChunk+8)
	put(144, a.maxChunk+8)
	put(36, a.maxChunk*RecFPS)
	if err == nil {
		err = a.f.Sync()
	}
	if e := a.f.Close(); err == nil {
		err = e
	}
	return err
}

func (a *aviWriter) abort() { a.f.Close() }

func fmtMB(b int64) string {
	if b < 1<<20 {
		return fmt.Sprintf("%d KB", (b+1023)>>10)
	}
	return strings.Replace(fmt.Sprintf("%.1f MB", float64(b)/(1<<20)), ".", ",", 1)
}

// ---------- Aviso no lugar da janela (TS minimizado, fechado...) ----------

// As imagens da câmera são BGRA: as cores do aviso vão com vermelho e azul trocados.
func bgr(c color.RGBA) color.RGBA { return color.RGBA{c.B, c.G, c.R, c.A} }

var (
	colGold   = color.RGBA{243, 213, 138, 255}
	colMuted  = color.RGBA{163, 154, 137, 255}
	colScreen = color.RGBA{30, 30, 34, 255}
)

// drawNotice desenha o aviso (numa imagem BGRA) quando não dá para copiar a janela.
func drawNotice(canvas *image.RGBA, st capState) {
	w, h := canvas.Bounds().Dx(), canvas.Bounds().Dy()
	fillRect(canvas, canvas.Bounds(), bgr(colScreen))
	msg := map[capState][]string{
		capMinimized: {"TeamSpeak minimizado", "Não dá para gravar a janela enquanto ela estiver minimizada.", "O monitoramento continua normal."},
		capNotFound:  {"Janela do TeamSpeak não encontrada", "Abra o TeamSpeak (sem minimizar) para aparecer aqui.", "O monitoramento continua normal."},
		capFailed:    {"Não consegui copiar a janela do TeamSpeak", "Vou continuar tentando.", ""},
		capBlack:     {"A imagem do TeamSpeak veio toda preta", "Achei a janela, mas o Windows entregou a imagem preta.", "O monitoramento continua normal."},
		capElevated:  {"TeamSpeak aberto como administrador", "O Windows não deixa copiar a janela. Abra o Next TS como administrador.", "O monitoramento continua normal."},
	}[st]
	y := h/2 - 2*fontH
	for i, ln := range msg {
		sc, c := 1, colMuted
		if i == 0 {
			sc, c = 2, colGold
		}
		if textWidth(ln, sc) > w { // janela pequena: letra menor
			sc = 1
		}
		drawText(canvas, max(0, (w-textWidth(ln, sc))/2), y, ln, bgr(c), sc)
		y += fontH*sc + 6
	}
}
