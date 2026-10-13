package main

// Gravação em vídeo da janela do TeamSpeak durante o monitoramento.
//
//   - 5 quadros por segundo, do Iniciar até 10 s depois de enviar (ou do teste);
//   - embaixo da imagem do TS vai uma faixa com o relógio do PC e os últimos
//     acontecimentos (ex.: "20:15:03 ENVIADO "!next C1" para x3tBot");
//   - quadro igual ao anterior não é gravado de novo (o vídeo fica bem menor);
//   - formato AVI com imagens JPEG (MJPEG): abre no VLC e na maioria dos players;
//   - nunca apaga gravações antigas;
//   - um problema na gravação nunca atrapalha o monitoramento nem o envio.

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	RecFPS       = 5
	recFrame     = time.Second / RecFPS
	RecTailSent  = 10 * time.Second // continua gravando depois de enviar (pega a resposta do bot)
	RecTailOther = 2 * time.Second
	recMinW      = 960
	recDefaultW  = 1280
	recDefaultH  = 720
	recJPEGQ     = 80
	recPartLimit = 1 << 30 // 1 GB por arquivo; passou disso, continua num "_parte2"
	recBarLines  = 3
)

// ---------- Letras da faixa ----------

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
				if a == 0 {
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

// fitText corta o texto em `n` letras, terminando com "…" se precisar.
func fitText(text string, n int) string {
	r := []rune(text)
	if n < 1 || len(r) <= n {
		return text
	}
	return string(r[:n-1]) + "…"
}

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

// fitInto desenha `src` dentro de `dst` (área r), mantendo a proporção.
// Do mesmo tamanho: copia direto. Maior: reduz. Menor: centraliza.
func fitInto(dst *image.RGBA, r image.Rectangle, src *image.RGBA) {
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	if sw <= 0 || sh <= 0 {
		return
	}
	if sw == r.Dx() && sh == r.Dy() {
		for y := 0; y < sh; y++ {
			so := src.PixOffset(src.Bounds().Min.X, src.Bounds().Min.Y+y)
			do := dst.PixOffset(r.Min.X, r.Min.Y+y)
			copy(dst.Pix[do:do+4*sw], src.Pix[so:so+4*sw])
		}
		return
	}
	w, h := sw, sh
	if w > r.Dx() || h > r.Dy() {
		if w*r.Dy() > h*r.Dx() {
			w, h = r.Dx(), sh*r.Dx()/sw
		} else {
			w, h = sw*r.Dy()/sh, r.Dy()
		}
	}
	if w <= 0 || h <= 0 {
		return
	}
	ox, oy := r.Min.X+(r.Dx()-w)/2, r.Min.Y+(r.Dy()-h)/2
	for y := 0; y < h; y++ {
		sy := src.Bounds().Min.Y + y*sh/h
		do := dst.PixOffset(ox, oy+y)
		for x := 0; x < w; x++ {
			so := src.PixOffset(src.Bounds().Min.X+x*sw/w, sy)
			copy(dst.Pix[do+4*x:do+4*x+4], src.Pix[so:so+4])
		}
	}
}

// ---------- Arquivo AVI (MJPEG) ----------

// aviWriter grava um AVI simples com um único fluxo de vídeo MJPEG.
// O cabeçalho é atualizado de tempos em tempos: se o PC desligar no meio,
// o que já foi gravado continua abrindo.
type aviWriter struct {
	f        *os.File
	w, h     int
	frames   int
	idx      bytes.Buffer
	end      int64 // tamanho atual do arquivo
	maxChunk uint32
}

const aviMoviFourCC = 220 // posição do "movi" (os índices contam a partir dele)

func le32(b []byte, off int, v uint32) { binary.LittleEndian.PutUint32(b[off:], v) }

func newAVI(path string, w, h int) (*aviWriter, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
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
	le32(hd, 32, uint32(recFrame/time.Microsecond)) // microssegundos por quadro
	le32(hd, 44, 0x10)                              // AVIF_HASINDEX
	le32(hd, 56, 1)                                 // 1 fluxo
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
	le32(hd, 132, RecFPS)     // dwRate: 5 quadros por segundo
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
	if _, err := f.Write(hd); err != nil {
		f.Close()
		return nil, err
	}
	a := &aviWriter{f: f, w: w, h: h, end: int64(len(hd))}
	if err := a.patch(); err != nil {
		f.Close()
		return nil, err
	}
	return a, nil
}

// frame grava um quadro. data vazio = "repete o quadro anterior" (não ocupa espaço).
func (a *aviWriter) frame(data []byte) error {
	n := len(data)
	ck := make([]byte, 8, 8+n+1)
	copy(ck, "00dc")
	le32(ck, 4, uint32(n))
	ck = append(ck, data...)
	if n%2 == 1 {
		ck = append(ck, 0)
	}
	if _, err := a.f.WriteAt(ck, a.end); err != nil {
		return err
	}
	var e [16]byte
	copy(e[:], "00dc")
	if n > 0 {
		le32(e[:], 4, 0x10) // quadro completo (keyframe)
	}
	le32(e[:], 8, uint32(a.end-aviMoviFourCC))
	le32(e[:], 12, uint32(n))
	a.idx.Write(e[:])
	a.end += int64(len(ck))
	a.frames++
	if uint32(n) > a.maxChunk {
		a.maxChunk = uint32(n)
	}
	return nil
}

// patch atualiza os tamanhos e a contagem de quadros no cabeçalho.
func (a *aviWriter) patch() error {
	var b [4]byte
	put := func(off int64, v uint32) error {
		le32(b[:], 0, v)
		_, err := a.f.WriteAt(b[:], off)
		return err
	}
	for _, p := range []struct {
		off int64
		v   uint32
	}{
		{4, uint32(a.end - 8)},
		{216, uint32(a.end - aviMoviFourCC)},
		{48, uint32(a.frames)},
		{140, uint32(a.frames)},
		{60, a.maxChunk + 8},
		{144, a.maxChunk + 8},
		{36, a.maxChunk * RecFPS},
	} {
		if err := put(p.off, p.v); err != nil {
			return err
		}
	}
	return nil
}

func (a *aviWriter) close() error {
	idx := make([]byte, 8, 8+a.idx.Len())
	copy(idx, "idx1")
	le32(idx, 4, uint32(a.idx.Len()))
	idx = append(idx, a.idx.Bytes()...)
	movEnd := a.end
	_, err := a.f.WriteAt(idx, a.end)
	if err == nil {
		a.end += int64(len(idx))
		err = a.patch()
		if err == nil {
			// O "movi" termina antes do índice.
			var b [4]byte
			le32(b[:], 0, uint32(movEnd-aviMoviFourCC))
			_, err = a.f.WriteAt(b[:], 216)
		}
	}
	if e := a.f.Close(); err == nil {
		err = e
	}
	return err
}

// ---------- Gravador ----------

// Situação da janela do TS na captura.
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

type recEvent struct {
	time, kind, text string
}

// RecView é o que a janelinha mostra sobre a gravação.
type RecView struct {
	On      bool   `json:"on"`
	File    string `json:"file"`    // arquivo atual (só o nome)
	Note    string `json:"note"`    // aviso (ex.: TS minimizado)
	Seconds int    `json:"seconds"` // duração gravada
	MB      int    `json:"mb"`      // tamanho em MB
	Dir     string `json:"dir"`     // pasta das gravações
	Last    string `json:"last"`    // última gravação terminada (nome)
}

type recSession struct {
	id      int
	base    string // caminho sem ".avi"
	header  string
	events  []recEvent
	stopAt  time.Time
	stopNow chan struct{}
	done    chan struct{}
	once    sync.Once
	file    string
	note    string
	seconds int
	bytes   int64
}

type Recorder struct {
	mu   sync.Mutex
	dir  string
	cur  *recSession
	n    int
	last string
	logf func(kind, text string) // avisa o histórico do app (nunca chamado com mu travado)
	// capture pega a imagem da janela do TS (trocável nos testes).
	capture func() (*image.RGBA, capState)
}

func NewRecorder(dir string, logf func(kind, text string)) *Recorder {
	return &Recorder{dir: dir, logf: logf, capture: captureTS}
}

func (r *Recorder) Dir() string { return r.dir }

// Begin começa uma gravação nova (encerra na hora a anterior, se ainda estiver gravando).
func (r *Recorder) Begin(code, header string, test bool) (string, error) {
	if err := os.MkdirAll(r.dir, 0o755); err != nil {
		return "", err
	}
	name := now().Format("2006-01-02_15-04-05") + "_" + code
	if test {
		name += "_teste"
	}
	base := filepath.Join(r.dir, name)
	for i := 2; fileExists(base + ".avi"); i++ {
		base = filepath.Join(r.dir, fmt.Sprintf("%s_%d", name, i))
	}
	r.mu.Lock()
	if r.cur != nil {
		r.cur.halt()
	}
	r.n++
	s := &recSession{id: r.n, base: base, header: header, stopNow: make(chan struct{}), done: make(chan struct{}), file: filepath.Base(base + ".avi")}
	r.cur = s
	r.mu.Unlock()
	go r.loop(s)
	return base + ".avi", nil
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

func (s *recSession) halt() { s.once.Do(func() { close(s.stopNow) }) }

// Event põe um acontecimento na faixa do vídeo (só os mais recentes aparecem).
func (r *Recorder) Event(kind, at, text string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cur == nil {
		return
	}
	s := r.cur
	s.events = append(s.events, recEvent{at, kind, text})
	if len(s.events) > recBarLines-1 {
		s.events = s.events[len(s.events)-(recBarLines-1):]
	}
}

// End encerra a gravação atual depois de `tail` (continua gravando até lá).
func (r *Recorder) End(tail time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cur == nil {
		return
	}
	at := time.Now().Add(tail)
	if r.cur.stopAt.IsZero() || at.Before(r.cur.stopAt) {
		r.cur.stopAt = at
	}
}

// Close encerra já e espera o arquivo ficar pronto (usado ao fechar o app).
// Não pode ser chamado com o a.mu do app travado.
func (r *Recorder) Close() {
	r.mu.Lock()
	s := r.cur
	r.mu.Unlock()
	if s == nil {
		return
	}
	s.halt()
	select {
	case <-s.done:
	case <-time.After(10 * time.Second):
	}
}

func (r *Recorder) View() RecView {
	r.mu.Lock()
	defer r.mu.Unlock()
	v := RecView{Dir: r.dir, Last: r.last}
	if s := r.cur; s != nil {
		v.On, v.File, v.Note, v.Seconds, v.MB = true, s.file, s.note, s.seconds, int(s.bytes>>20)
	}
	return v
}

func (r *Recorder) log(kind, format string, args ...any) {
	if r.logf != nil {
		r.logf(kind, fmt.Sprintf(format, args...))
	}
}

// loop captura, monta a imagem com a faixa e grava, 5 vezes por segundo.
func (r *Recorder) loop(s *recSession) {
	defer close(s.done)
	img, st := r.capture()
	w, h := recDefaultW, recDefaultH
	if st == capOK && img != nil {
		w, h = img.Bounds().Dx(), img.Bounds().Dy()
	}
	w = max(w, recMinW)
	w, h = (w+1)&^1, (h+1)&^1
	barH := recBarLines*fontH + 12
	canvas := image.NewRGBA(image.Rect(0, 0, w, h+barH))

	part, total := 1, int64(0)
	var avi *aviWriter
	open := func() error {
		p := s.base + ".avi"
		if part > 1 {
			p = fmt.Sprintf("%s_parte%d.avi", s.base, part)
		}
		a, err := newAVI(p, canvas.Bounds().Dx(), canvas.Bounds().Dy())
		if err != nil {
			return err
		}
		avi = a
		r.mu.Lock()
		s.file = filepath.Base(p)
		r.mu.Unlock()
		return nil
	}
	if err := open(); err != nil {
		r.log("warn", "Não consegui criar o arquivo da gravação (%s). O monitoramento continua, sem gravar.", err.Error())
		r.finishSession(s, "")
		return
	}

	t0 := time.Now()
	written := 0 // quadros já gravados neste arquivo
	partStart := 0
	var lastHash uint64
	var buf bytes.Buffer
	lastState := capOK
	if st != capOK {
		lastState = -1 // avisa já no primeiro quadro
	}
	failed := ""
	lastPatch := time.Now()
	ticker := time.NewTicker(recFrame)
	defer ticker.Stop()

	for first := true; ; first = false {
		if !first {
			img, st = r.capture()
		}
		if st == capOK && img != nil && allBlack(img) {
			st = capBlack
		}
		if st != lastState {
			switch st {
			case capOK:
				if lastState != -1 {
					r.log("info", "Gravação: voltei a ver a janela do TeamSpeak.")
				}
			case capMinimized:
				r.log("warn", "Gravação: o TeamSpeak está minimizado. Enquanto estiver assim, o vídeo mostra um aviso no lugar da janela (o monitoramento continua normal).")
			case capNotFound:
				r.log("warn", "Gravação: não achei a janela do TeamSpeak. Enquanto não achar, o vídeo mostra um aviso no lugar dela.")
			case capFailed:
				r.log("warn", "Gravação: não consegui copiar a imagem da janela do TeamSpeak agora. Vou continuar tentando. O motivo fica anotado no registro.txt.")
			case capBlack:
				r.log("warn", "Gravação: achei a janela do TeamSpeak, mas a imagem dela está vindo toda preta. O monitoramento continua normal. Se continuar assim, me avise (mande o registro.txt).")
			case capElevated:
				r.log("warn", "Gravação: o TeamSpeak está aberto como administrador e o Next TS não; assim o Windows não deixa copiar a janela. Para gravar, feche o Next TS e abra de novo com o botão direito → \"Executar como administrador\" (ou abra o TeamSpeak sem administrador). O monitoramento continua normal.")
			}
			lastState = st
			r.mu.Lock()
			s.note = map[capState]string{capOK: "", capMinimized: "TeamSpeak minimizado: o vídeo mostra um aviso no lugar da janela.",
				capNotFound: "Não achei a janela do TeamSpeak.", capFailed: "Não consegui copiar a janela do TeamSpeak (motivo no registro.txt).",
				capBlack:    "A imagem da janela do TeamSpeak está vindo toda preta.",
				capElevated: "O TeamSpeak está como administrador: abra o Next TS como administrador para gravar."}[st]
			r.mu.Unlock()
		}
		r.compose(canvas, h, img, st, s)

		hs := fnv.New64a()
		hs.Write(canvas.Pix)
		sum := hs.Sum64()
		target := int(time.Since(t0)/recFrame) - partStart // quadros que já deviam existir
		var err error
		for written < target && err == nil && written > 0 {
			err = avi.frame(nil) // tempo sem mudança (ou atraso da captura): repete
			written++
		}
		if err == nil {
			if written == 0 || sum != lastHash {
				buf.Reset()
				if err = jpeg.Encode(&buf, canvas, &jpeg.Options{Quality: recJPEGQ}); err == nil {
					err = avi.frame(buf.Bytes())
				}
			} else {
				err = avi.frame(nil)
			}
			written++
			lastHash = sum
		}
		if err == nil && time.Since(lastPatch) > 2*time.Second {
			err = avi.patch()
			lastPatch = time.Now()
		}
		r.mu.Lock()
		s.seconds = int(time.Since(t0).Seconds())
		s.bytes = total + avi.end
		r.mu.Unlock()
		if err != nil {
			failed = err.Error()
			break
		}
		if avi.end > recPartLimit {
			total += avi.end
			if err = avi.close(); err == nil {
				partStart += written
				written, part = 0, part+1
				err = open()
			}
			if err != nil {
				failed = err.Error()
				avi = nil
				break
			}
		}

		r.mu.Lock()
		stopAt := s.stopAt
		r.mu.Unlock()
		if !stopAt.IsZero() && !time.Now().Before(stopAt) {
			break
		}
		select {
		case <-s.stopNow:
		case <-ticker.C:
			continue
		}
		break
	}

	var cerr error
	if avi != nil {
		cerr = avi.close()
		total += avi.end
	}
	switch {
	case failed != "":
		r.log("warn", "A gravação parou por um erro (%s). O que foi gravado até aqui ficou salvo em %s. O monitoramento continua normal.", failed, s.base+".avi")
	case cerr != nil:
		r.log("warn", "Não consegui terminar de salvar a gravação (%s). Arquivo: %s", cerr.Error(), s.base+".avi")
	default:
		extra := ""
		if part > 1 {
			extra = fmt.Sprintf(" em %d partes", part)
		}
		r.log("ok", "Gravação salva%s: %s (%s, %s).", extra, s.base+".avi", fmtDur(time.Since(t0)), fmtMB(total))
	}
	r.finishSession(s, filepath.Base(s.base+".avi"))
}

func (r *Recorder) finishSession(s *recSession, name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if name != "" {
		r.last = name
	}
	if r.cur == s {
		r.cur = nil
	}
}

func fmtDur(d time.Duration) string {
	sec := int(d.Seconds())
	if sec < 60 {
		return fmt.Sprintf("%d s", sec)
	}
	if sec < 3600 {
		return fmt.Sprintf("%d min %d s", sec/60, sec%60)
	}
	return fmt.Sprintf("%d h %d min", sec/3600, sec%3600/60)
}

func fmtMB(b int64) string {
	if b < 1<<20 {
		return fmt.Sprintf("%d KB", (b+1023)>>10)
	}
	return strings.Replace(fmt.Sprintf("%.1f MB", float64(b)/(1<<20)), ".", ",", 1)
}

var (
	colBar    = color.RGBA{18, 16, 13, 255}
	colLine   = color.RGBA{138, 106, 42, 255}
	colGold   = color.RGBA{243, 213, 138, 255}
	colText   = color.RGBA{232, 227, 216, 255}
	colMuted  = color.RGBA{163, 154, 137, 255}
	colOK     = color.RGBA{111, 227, 106, 255}
	colWarn   = color.RGBA{255, 207, 90, 255}
	colErr    = color.RGBA{255, 107, 94, 255}
	colBot    = color.RGBA{255, 224, 102, 255}
	colInfo   = color.RGBA{201, 226, 255, 255}
	colRec    = color.RGBA{230, 50, 40, 255}
	colScreen = color.RGBA{30, 30, 34, 255}
)

// compose monta o quadro: imagem do TS em cima (altura h) e a faixa embaixo.
func (r *Recorder) compose(canvas *image.RGBA, h int, img *image.RGBA, st capState, s *recSession) {
	w := canvas.Bounds().Dx()
	top := image.Rect(0, 0, w, h)
	fillRect(canvas, top, colScreen)
	if st == capOK && img != nil {
		fitInto(canvas, top, img)
	} else {
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
			drawText(canvas, (w-textWidth(ln, sc))/2, y, ln, c, sc)
			y += fontH*sc + 6
		}
	}
	// Faixa: relógio do PC + plano, e os últimos acontecimentos.
	bar := image.Rect(0, h, w, canvas.Bounds().Dy())
	fillRect(canvas, bar, colBar)
	fillRect(canvas, image.Rect(0, h, w, h+2), colLine)
	r.mu.Lock()
	header := s.header
	events := append([]recEvent(nil), s.events...)
	r.mu.Unlock()
	t := now()
	y := h + 6
	x := 8
	fillRect(canvas, image.Rect(x, y+5, x+10, y+15), colRec) // fixa: piscar dobraria o tamanho do vídeo
	x = drawText(canvas, x+16, y, "REC", colRec, 1)
	x = drawText(canvas, x+12, y, t.Format("02/01/2006 15:04:05"), colGold, 1)
	drawText(canvas, x+12, y, "· "+header, colText, 1)
	for i := 0; i < recBarLines-1; i++ {
		y += fontH
		if i >= len(events) {
			continue
		}
		e := events[i]
		c := map[string]color.RGBA{"ok": colOK, "warn": colWarn, "err": colErr, "bot": colBot, "info": colInfo}[e.kind]
		if c == (color.RGBA{}) {
			c = colText
		}
		x := drawText(canvas, 8, y, e.time, colMuted, 1)
		drawText(canvas, x+10, y, fitText(e.text, (w-x-18)/fontW), c, 1)
	}
}
