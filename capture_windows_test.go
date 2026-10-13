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
			h, _, _ := tCreateWindowEx.Call(s.exStyle, uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(title)),
				0x10CF0000, uintptr(s.x), uintptr(s.y), uintptr(s.w), uintptr(s.h), 0, 0, inst, 0) // WS_OVERLAPPEDWINDOW | WS_VISIBLE
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

// redBlue confere a imagem da janela principal: vermelho em cima e azul embaixo.
func redBlue(t *testing.T, what string, img *image.RGBA) {
	t.Helper()
	b := img.Bounds()
	at := func(fx, fy float64) color.RGBA { return img.RGBAAt(int(fx*float64(b.Dx())), int(fy*float64(b.Dy()))) }
	top, bottom := at(0.5, 0.40), at(0.5, 0.85)
	t.Logf("%s: %dx%d, em cima %v, embaixo %v", what, b.Dx(), b.Dy(), top, bottom)
	if top.R < 200 || top.G > 60 || top.B > 60 || bottom.B < 200 || bottom.R > 60 || bottom.G > 60 {
		t.Fatalf("%s: cores erradas (em cima %v, embaixo %v)", what, top, bottom)
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
		winSpec{"TeamSpeak 3", 0, 50, 50, 640, 400},
		winSpec{"TeamSpeak 3 - aviso", wsExToolWindow, 700, 50, 900, 700},
		winSpec{"TeamSpeak 3", 0, 700, 600, 150, 100},
	)
	resetCapture()
	if h, _ := findTSWindow(); h != hs[0] {
		t.Fatalf("escolheu a janela errada: %s", describeWindow(h))
	}
	img, st := captureTS()
	if st != capOK || img == nil || img.Bounds().Dx() != 640 || img.Bounds().Dy() != 400 {
		t.Fatalf("captura falhou: %v %v", st, img)
	}
	redBlue(t, "automático", img)

	// Cada jeito de copiar, sozinho.
	r, _ := windowRect(hs[0])
	for m := 0; m < capMethods; m++ {
		img, why := captureWith(m, hs[0], r, 640, 400)
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
	file, err := rec.Begin("C1", "teste no Windows", false)
	if err != nil {
		t.Fatal(err)
	}
	rec.Event("ok", "20:15:03", `ENVIADO "!next C1" para x3tBot`)
	time.Sleep(1200 * time.Millisecond)
	rec.End(0)
	rec.Close()
	in := readAVI(t, file)
	if in.totalFrames < 4 || in.w != recMinW || in.h != 400+recBarLines*fontH+12 {
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
