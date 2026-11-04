//go:build windows

package main

// Testes da captura de verdade (Windows/Wine). Só rodam com NEXTTS_WINE_TEST definido.
//
//   NEXTTS_WINE_TEST=1        o próprio teste abre a janela "TeamSpeak 3" (metade de cima
//                             vermelha, metade de baixo azul). O executável do teste precisa
//                             se chamar ts3client_... (ex.: ts3client_teste.exe).
//   NEXTTS_WINE_TEST=desktop  igual, e também testa a janela minimizada (precisa de um
//                             gerenciador de janelas, ex.: a área de trabalho virtual do Wine).
//   NEXTTS_WINE_TEST=outro    a janela do TS é de OUTRO programa já aberto (ts3client_win64.exe
//                             de teste), como no PC de verdade.

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"os"
	"runtime"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

var (
	tRegisterClassEx = user32.NewProc("RegisterClassExW")
	tCreateWindowEx  = user32.NewProc("CreateWindowExW")
	tDefWindowProc   = user32.NewProc("DefWindowProcW")
	tPeekMessage     = user32.NewProc("PeekMessageW")
	tTranslate       = user32.NewProc("TranslateMessage")
	tDispatch        = user32.NewProc("DispatchMessageW")
	tShowWindow      = user32.NewProc("ShowWindow")
	tSetWindowPos    = user32.NewProc("SetWindowPos")
	tFillRect        = user32.NewProc("FillRect")
	tGetClientRect   = user32.NewProc("GetClientRect")
	tBeginPaint      = user32.NewProc("BeginPaint")
	tEndPaint        = user32.NewProc("EndPaint")
	tCreateBrush     = gdi32.NewProc("CreateSolidBrush")
	tGetModule       = kernel32.NewProc("GetModuleHandleW")
)

type tWndClassEx struct {
	Size, Style                        uint32
	WndProc                            uintptr
	ClsExtra, WndExtra                 int32
	Instance, Icon, Cursor, Background uintptr
	MenuName, ClassName                *uint16
	IconSm                             uintptr
}

type tMsg struct {
	Hwnd    uintptr
	Message uint32
	W, L    uintptr
	Time    uint32
	X, Y    int32
}

var tRed, tBlue, tGreen uintptr

// A janela principal de teste é vermelha em cima e azul embaixo; as outras, verdes.
var tMain uintptr

func tPaint(h, hdc uintptr) {
	var r rect
	tGetClientRect.Call(h, uintptr(unsafe.Pointer(&r)))
	if h != tMain {
		tFillRect.Call(hdc, uintptr(unsafe.Pointer(&r)), tGreen)
		return
	}
	top, bot := r, r
	top.Bottom, bot.Top = r.Bottom/2, r.Bottom/2
	tFillRect.Call(hdc, uintptr(unsafe.Pointer(&top)), tRed)
	tFillRect.Call(hdc, uintptr(unsafe.Pointer(&bot)), tBlue)
}

var tWndProc = syscall.NewCallback(func(h uintptr, m uint32, w, l uintptr) uintptr {
	switch m {
	case 0x000F: // WM_PAINT
		var ps [72]byte
		hdc, _, _ := tBeginPaint.Call(h, uintptr(unsafe.Pointer(&ps[0])))
		tPaint(h, hdc)
		tEndPaint.Call(h, uintptr(unsafe.Pointer(&ps[0])))
		return 0
	case 0x0318: // WM_PRINTCLIENT
		tPaint(h, w)
		return 0
	}
	r, _, _ := tDefWindowProc.Call(h, uintptr(m), w, l)
	return r
})

type winSpec struct {
	title      string
	exStyle    uintptr
	x, y, w, h int
	style      uintptr // 0 = janela normal com barra de título
}

// testWindows abre as janelas numa thread com fila de mensagens. A primeira é a "principal".
func testWindows(t *testing.T, specs ...winSpec) ([]uintptr, chan func()) {
	out := make(chan []uintptr)
	cmds := make(chan func(), 4)
	go func() {
		runtime.LockOSThread()
		tRed, _, _ = tCreateBrush.Call(0x0000FF)
		tBlue, _, _ = tCreateBrush.Call(0xFF0000)
		tGreen, _, _ = tCreateBrush.Call(0x00FF00)
		inst, _, _ := tGetModule.Call(0)
		cls, _ := syscall.UTF16PtrFromString("NextTSTeste")
		wc := tWndClassEx{Size: uint32(unsafe.Sizeof(tWndClassEx{})), WndProc: tWndProc, Instance: inst, ClassName: cls}
		tRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc)))
		var hs []uintptr
		for i, s := range specs {
			title, _ := syscall.UTF16PtrFromString(s.title)
			style := s.style
			if style == 0 {
				style = 0x10CF0000 // WS_OVERLAPPEDWINDOW | WS_VISIBLE
			}
			h, _, _ := tCreateWindowEx.Call(s.exStyle, uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(title)),
				style, uintptr(s.x), uintptr(s.y), uintptr(s.w), uintptr(s.h), 0, 0, inst, 0)
			if i == 0 {
				tMain = h
			}
			hs = append(hs, h)
		}
		out <- hs
		var m tMsg
		for {
			select {
			case f := <-cmds:
				f()
			default:
			}
			for {
				r, _, _ := tPeekMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0, 1) // PM_REMOVE
				if r == 0 {
					break
				}
				tTranslate.Call(uintptr(unsafe.Pointer(&m)))
				tDispatch.Call(uintptr(unsafe.Pointer(&m)))
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	hs := <-out
	for _, h := range hs {
		if h == 0 {
			t.Fatal("não abriu a janela de teste")
		}
	}
	time.Sleep(700 * time.Millisecond)
	return hs, cmds
}

// toRGBA copia uma imagem BGRA (como vem da captura) para RGBA.
func toRGBA(pix []byte, w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < w*h*4; i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = pix[i+2], pix[i+1], pix[i], 255
	}
	return img
}

// captureWith copia o conteúdo da janela por um jeito só (para testar cada um).
func captureWith(m int, h uintptr, c rect) (*image.RGBA, string) {
	tsWinMu.Lock()
	defer tsWinMu.Unlock()
	if why := grab(m, h, c, 0); why != "" {
		return nil, why
	}
	return toRGBA(capDIB[0].pix(), capDIB[0].w, capDIB[0].h), ""
}

// captureImg: captureTS com a imagem em RGBA.
func captureImg() (*image.RGBA, capState) {
	pix, w, h, st := captureTS(0)
	if st != capOK {
		return nil, st
	}
	return toRGBA(pix, w, h), st
}

func resetCapture() {
	tsWinMu.Lock()
	tsWin, capBest, capNote = 0, -1, ""
	tsWinMu.Unlock()
	capSkip = [capMethods]bool{}
}

// redBlue confere a imagem da janela principal: só o conteúdo dela, vermelho na metade
// de cima e azul na de baixo, ATÉ AS BORDAS (sem barra de título, sem borda e sem a
// tarja verde que fica em cima da barra de título).
func redBlue(t *testing.T, what string, img *image.RGBA) {
	t.Helper()
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	red := func(c color.RGBA) bool { return c.R > 200 && c.G < 60 && c.B < 60 }
	blue := func(c color.RGBA) bool { return c.B > 200 && c.R < 60 && c.G < 60 }
	t.Logf("%s: %dx%d, canto de cima %v, canto de baixo %v", what, w, h, img.RGBAAt(0, 0), img.RGBAAt(w-1, h-1))
	for x := 0; x < w; x++ {
		if !red(img.RGBAAt(x, 0)) || !red(img.RGBAAt(x, h/2-2)) || !blue(img.RGBAAt(x, h-1)) || !blue(img.RGBAAt(x, h/2+2)) {
			t.Fatalf("%s: coluna %d com cor errada (topo %v, fundo %v): não é só o conteúdo da janela", what, x, img.RGBAAt(x, 0), img.RGBAAt(x, h-1))
		}
	}
	for y := 0; y < h; y++ {
		for _, x := range []int{0, w - 1} {
			if c := img.RGBAAt(x, y); !red(c) && !blue(c) {
				t.Fatalf("%s: borda (%d,%d) com cor %v", what, x, y, c)
			}
		}
	}
}

func TestCaptureJanelaDoTS(t *testing.T) {
	mode := os.Getenv("NEXTTS_WINE_TEST")
	if mode == "" || mode == "outro" {
		t.Skip("defina NEXTTS_WINE_TEST=1 (e chame o executável de ts3client_...)")
	}
	resetCapture()
	if _, st := captureImg(); st != capNotFound {
		t.Fatalf("sem janela aberta: esperava 'não encontrada', veio %v", st)
	}
	// A principal (640x400) e, mais longe, duas que NÃO são a principal:
	// uma "ferramenta" maior (avisos/overlay) e uma pequena demais.
	hs, cmds := testWindows(t,
		winSpec{"TeamSpeak 3", 0, 50, 50, 640, 400, 0},
		winSpec{"TeamSpeak 3 - aviso", wsExToolWindow, 700, 50, 900, 700, 0},
		winSpec{"TeamSpeak 3", 0, 700, 600, 150, 100, 0},
		winSpec{"tarja", 0x08 | wsExToolWindow, 900, 800, 50, 10, 0x90000000}, // WS_EX_TOPMOST, WS_POPUP | WS_VISIBLE
	)
	resetCapture()
	if h, _ := findTSWindow(); h != hs[0] {
		t.Fatalf("escolheu a janela errada: %s", describeWindow(h))
	}
	r, _ := windowRect(hs[0])
	c, _ := clientRect(hs[0])
	cw, ch := int(c.Right-c.Left), int(c.Bottom-c.Top)
	t.Logf("janela inteira %dx%d, conteúdo %dx%d (barra de título de %d pontos)", r.Right-r.Left, r.Bottom-r.Top, cw, ch, c.Top-r.Top)
	// Uma tarja (verde) por cima da barra de título, como a tarja preta do vídeo de 13/10.
	if bar := int(c.Top - r.Top); bar > 6 {
		tSetWindowPos.Call(hs[3], ^uintptr(0), uintptr(r.Left+30), uintptr(r.Top+2), 300, uintptr(bar-4), 0x0040) // HWND_TOPMOST, SWP_SHOWWINDOW
		time.Sleep(500 * time.Millisecond)
		// Confere que a tarja aparece mesmo na tela, em cima da barra de título (senão o teste não prova nada).
		screen, _, _ := procGetDC.Call(0)
		d, _ := newDIB(screen, int(r.Right-r.Left), int(r.Bottom-r.Top))
		procBitBlt.Call(d.mem, 0, 0, uintptr(r.Right-r.Left), uintptr(r.Bottom-r.Top), screen, uintptr(r.Left), uintptr(r.Top), srcCopy)
		whole := toRGBA(d.pix(), d.w, d.h)
		d.free()
		procReleaseDC.Call(0, screen)
		green := 0
		for i := 0; i < len(whole.Pix); i += 4 {
			if whole.Pix[i] < 60 && whole.Pix[i+1] > 200 && whole.Pix[i+2] < 60 {
				green++
			}
		}
		t.Logf("tarja na tela, em cima da barra de título: %d pontos verdes na janela inteira", green)
		if green == 0 {
			t.Fatal("a tarja de teste não apareceu na tela")
		}
	} else {
		t.Logf("sem barra de título aqui (%d pontos): o teste da tarja fica para a área de trabalho virtual", bar)
	}
	img, st := captureImg()
	if st != capOK || img == nil || img.Bounds().Dx() != cw || img.Bounds().Dy() != ch {
		t.Fatalf("captura falhou: %v %v (esperava %dx%d)", st, img, cw, ch)
	}
	redBlue(t, "automático", img)

	// Cada jeito de copiar, sozinho.
	for m := 0; m < capMethods; m++ {
		img, why := captureWith(m, hs[0], c)
		if img == nil {
			t.Fatalf("%s falhou: %s", capMethodName[m], why)
		}
		redBlue(t, capMethodName[m], img)
	}
	// Sem os dois primeiros jeitos, os outros dão conta.
	resetCapture()
	capSkip[capPrintFull], capSkip[capPrint] = true, true
	if img, st := captureImg(); st != capOK {
		t.Fatalf("sem PrintWindow: %v", st)
	} else {
		redBlue(t, "sem PrintWindow", img)
	}
	resetCapture()

	// Velocidade da captura de verdade (a 30 quadros por segundo, cada uma tem ~33 ms).
	t0 := time.Now()
	for i := 0; i < 30; i++ {
		if _, _, _, st := captureTS(i % 2); st != capOK {
			t.Fatalf("captura %d: %v", i, st)
		}
	}
	t.Logf("captura de %dx%d: %.1f ms cada (jeito: %s)", cw, ch, float64(time.Since(t0).Microseconds())/30000, capMethodName[capBest])
	captureRelease()
	if capDIB[0] != nil || capDIB[1] != nil {
		t.Fatal("captureRelease não soltou as imagens do Windows")
	}

	// Gravação de ponta a ponta com a captura de verdade ("20 s" = 1 s, "10 s" = 0,5 s).
	b, a := RecBefore, RecAfter
	RecBefore, RecAfter = time.Second, 500*time.Millisecond
	defer func() { RecBefore, RecAfter = b, a }()
	dir := t.TempDir()
	rec := NewRecorder(dir, func(kind, text string) { t.Log(kind, text) })
	file, err := rec.Begin("C1", false)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)
	t.Logf("gravando: %+v", rec.View())
	rec.Mark(time.Now(), "sent")
	time.Sleep(800 * time.Millisecond)
	rec.Close()
	in := readAVI(t, file)
	if d := int(in.totalFrames) - 45; d < -1 || d > 1 || int(in.w) != cw || int(in.h) != ch { // só o conteúdo da janela do TS
		t.Fatalf("vídeo: %d quadros, %dx%d", in.totalFrames, in.w, in.h)
	}
	first, err := jpeg.Decode(bytes.NewReader(in.chunks[0]))
	if err != nil {
		t.Fatalf("o primeiro quadro não abre: %v", err)
	}
	// No vídeo (JPEG) a divisa vermelho/azul fica borrada: confere longe dela, até as bordas.
	rgba := image.NewRGBA(first.Bounds())
	draw.Draw(rgba, rgba.Bounds(), first, image.Point{}, draw.Src)
	for _, x := range []int{0, cw / 2, cw - 1} {
		for _, y := range []int{0, ch/2 - 10} {
			if c := rgba.RGBAAt(x, y); c.R < 200 || c.B > 60 {
				t.Fatalf("vídeo: (%d,%d) devia ser vermelho: %v", x, y, c)
			}
		}
		for _, y := range []int{ch/2 + 10, ch - 1} {
			if c := rgba.RGBAAt(x, y); c.B < 200 || c.R > 60 {
				t.Fatalf("vídeo: (%d,%d) devia ser azul: %v", x, y, c)
			}
		}
	}

	// Minimizada (precisa de um gerenciador de janelas).
	if mode == "desktop" {
		done := make(chan bool)
		cmds <- func() { tShowWindow.Call(hs[0], 6); done <- true } // SW_MINIMIZE
		<-done
		time.Sleep(500 * time.Millisecond)
		resetCapture()
		if _, st := captureImg(); st != capMinimized {
			t.Fatalf("minimizada: esperava 'minimizado', veio %v", st)
		}
	}
}

// A janela do TS é de outro programa (como no PC de verdade).
func TestCaptureOutroPrograma(t *testing.T) {
	if os.Getenv("NEXTTS_WINE_TEST") != "outro" {
		t.Skip("defina NEXTTS_WINE_TEST=outro com o ts3client_win64.exe de teste aberto")
	}
	resetCapture()
	img, st := captureImg()
	if st != capOK || img == nil {
		t.Fatalf("captura falhou: %v (%s)", st, capNote)
	}
	t.Logf("jeito usado: %s", capMethodName[capBest])
	if out := os.Getenv("NEXTTS_PNG"); out != "" { // modo grande: guarda duas capturas para olhar
		save := func(name string, im *image.RGBA) {
			f, _ := os.Create(out + name)
			png.Encode(f, im)
			f.Close()
		}
		save("_1.png", img)
		time.Sleep(700 * time.Millisecond)
		img2, _ := captureImg()
		save("_2.png", img2)
		diff := 0
		for i := range img.Pix {
			if img.Pix[i] != img2.Pix[i] {
				diff++
			}
		}
		t.Logf("pontos diferentes entre duas capturas com 0,7 s: %d", diff/4)
		return
	}
	redBlue(t, "outro programa", img)
	t0 := time.Now()
	for i := 0; i < 60; i++ {
		if _, _, _, st := captureTS(i % 2); st != capOK {
			t.Fatalf("captura %d: %v", i, st)
		}
	}
	t.Logf("captura de %dx%d de outro programa: %.1f ms cada", img.Bounds().Dx(), img.Bounds().Dy(), float64(time.Since(t0).Microseconds())/60000)
	captureRelease()
}

// Medição com uma janela grande (2576x1408, como a do TS do usuário) de outro programa,
// com um print do TS de verdade de fundo e um aviso rolando: grava com os tempos de
// verdade (20 s antes + 10 s depois) e mede capturas por segundo, memória e tamanho.
func TestGravacaoJanelaGrande(t *testing.T) {
	if os.Getenv("NEXTTS_WINE_TEST") != "grande" {
		t.Skip("defina NEXTTS_WINE_TEST=grande com o ts3client_win64.exe de teste aberto em modo grande")
	}
	resetCapture()
	dir := t.TempDir()
	var logs []string
	var lmu sync.Mutex
	rec := NewRecorder(dir, func(kind, text string) { lmu.Lock(); logs = append(logs, text); lmu.Unlock() })
	var peakHeap, peakSys uint64
	stop := make(chan bool)
	go func() {
		var ms runtime.MemStats
		for {
			select {
			case <-stop:
				return
			case <-time.After(250 * time.Millisecond):
			}
			runtime.ReadMemStats(&ms)
			peakHeap, peakSys = max(peakHeap, ms.HeapInuse), max(peakSys, ms.Sys)
		}
	}()
	file, err := rec.Begin("C1", false)
	if err != nil {
		t.Fatal(err)
	}
	wait := 25 * time.Second
	if v := os.Getenv("NEXTTS_ESPERA"); v != "" {
		wait, _ = time.ParseDuration(v)
	}
	time.Sleep(wait)
	v := rec.View()
	t.Logf("antes do envio: %+v", v)
	T := time.Now()
	rec.Mark(T, "sent")
	t.Logf("T (envio) = %s", T.Format("15:04:05.000"))
	time.Sleep(5 * time.Second)
	t.Logf("5 s depois do envio: %+v", rec.View())
	time.Sleep(5500 * time.Millisecond)
	rec.Close()
	close(stop)
	in := readAVI(t, file)
	images := 0
	for _, c := range in.chunks {
		if len(c) > 0 {
			images++
		}
	}
	fi, _ := os.Stat(file)
	p := rec.lastPipe
	t.Logf("vídeo %dx%d: %d quadros (%.2f s), %d imagens diferentes, %.1f MB", in.w, in.h, in.totalFrames, float64(in.totalFrames)/30, images, float64(fi.Size())/(1<<20))
	t.Logf("câmera: %d capturas em %v (%.1f/s), captura média %.1f ms, comparação+compressão média %.1f ms, %d horários perdidos",
		p.captures, time.Since(T)+wait, float64(p.captures)/(time.Since(T)+wait).Seconds(),
		float64(p.capTime.Microseconds())/float64(p.captures)/1000, float64(p.procTime.Microseconds())/float64(p.captures)/1000, p.skipped)
	ram, disk := rec.store.peaks()
	t.Logf("memória: faixas comprimidas no máx. %s, temporário %s; heap do Go no máx. %.0f MB, memória do processo (Go) no máx. %.0f MB",
		fmtMB(ram), fmtMB(disk), float64(peakHeap)/(1<<20), float64(peakSys)/(1<<20))
	lmu.Lock()
	for _, l := range logs {
		t.Log(l)
	}
	lmu.Unlock()
	if d := int(in.totalFrames) - 900; d < -1 || d > 1 {
		t.Fatalf("quadros: %d, esperava 900 (±1)", in.totalFrames)
	}
	if len(in.chunks[0]) == 0 {
		t.Fatal("primeiro quadro vazio")
	}
	if os.Getenv("NEXTTS_GUARDAR") != "" {
		data, _ := os.ReadFile(file)
		os.WriteFile(os.Getenv("NEXTTS_GUARDAR"), data, 0o644)
	}
}
