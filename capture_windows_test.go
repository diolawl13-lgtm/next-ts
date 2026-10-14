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
	"image"
	"image/color"
	"os"
	"runtime"
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
	if _, st := captureTS(); st != capNotFound {
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
		whole := d.crop(0, 0, int(r.Right-r.Left), int(r.Bottom-r.Top))
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
	img, st := captureTS()
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
	if img, st := captureTS(); st != capOK {
		t.Fatalf("sem PrintWindow: %v", st)
	} else {
		redBlue(t, "sem PrintWindow", img)
	}
	resetCapture()

	// Gravação de ponta a ponta com a captura de verdade.
	dir := t.TempDir()
	rec := NewRecorder(dir, func(kind, text string) { t.Log(kind, text) })
	file, err := rec.Begin("C1", false)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(1200 * time.Millisecond)
	rec.End(0)
	rec.Close()
	in := readAVI(t, file)
	if in.totalFrames < 4 || int(in.w) != cw || int(in.h) != ch { // só o conteúdo da janela do TS
		t.Fatalf("vídeo: %d quadros, %dx%d", in.totalFrames, in.w, in.h)
	}

	// Minimizada (precisa de um gerenciador de janelas).
	if mode == "desktop" {
		done := make(chan bool)
		cmds <- func() { tShowWindow.Call(hs[0], 6); done <- true } // SW_MINIMIZE
		<-done
		time.Sleep(500 * time.Millisecond)
		resetCapture()
		if _, st := captureTS(); st != capMinimized {
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
	img, st := captureTS()
	if st != capOK || img == nil {
		t.Fatalf("captura falhou: %v (%s)", st, capNote)
	}
	t.Logf("jeito usado: %s", capMethodName[capBest])
	redBlue(t, "outro programa", img)
}
