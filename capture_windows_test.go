//go:build windows

package main

// Teste da captura de verdade (Windows/Wine). Só roda com NEXTTS_WINE_TEST=1.
// O próprio teste abre uma janela "TeamSpeak 3" (metade de cima vermelha, metade de
// baixo azul). Para o app reconhecer a janela como do TeamSpeak, o executável do
// teste precisa se chamar ts3client_... (ex.: ts3client_teste.exe).

import (
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

var tRed, tBlue uintptr

func tPaint(h, hdc uintptr) {
	var r rect
	tGetClientRect.Call(h, uintptr(unsafe.Pointer(&r)))
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

// abre a janela de teste numa thread com fila de mensagens; devolve a janela e um canal de comandos.
func testWindow(t *testing.T) (uintptr, chan func(uintptr)) {
	hwnd := make(chan uintptr)
	cmds := make(chan func(uintptr), 4)
	go func() {
		runtime.LockOSThread()
		tRed, _, _ = tCreateBrush.Call(0x0000FF)
		tBlue, _, _ = tCreateBrush.Call(0xFF0000)
		inst, _, _ := tGetModule.Call(0)
		cls, _ := syscall.UTF16PtrFromString("NextTSTeste")
		wc := tWndClassEx{Size: uint32(unsafe.Sizeof(tWndClassEx{})), WndProc: tWndProc, Instance: inst, ClassName: cls}
		tRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc)))
		title, _ := syscall.UTF16PtrFromString("TeamSpeak 3")
		h, _, _ := tCreateWindowEx.Call(0, uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(title)),
			0x10CF0000, 50, 50, 640, 400, 0, 0, inst, 0) // WS_OVERLAPPEDWINDOW | WS_VISIBLE
		hwnd <- h
		var m tMsg
		for {
			select {
			case f := <-cmds:
				f(h)
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
	h := <-hwnd
	if h == 0 {
		t.Fatal("não abriu a janela de teste")
	}
	time.Sleep(500 * time.Millisecond)
	return h, cmds
}

func TestCaptureJanelaDoTS(t *testing.T) {
	if os.Getenv("NEXTTS_WINE_TEST") == "" {
		t.Skip("defina NEXTTS_WINE_TEST=1 (e chame o executável de ts3client_...)")
	}
	if _, st := captureTS(); st != capNotFound {
		t.Fatalf("sem janela aberta: esperava 'não encontrada', veio %v", st)
	}
	_, cmds := testWindow(t)
	tsWinMu.Lock()
	tsWin = 0 // procura de novo
	tsWinMu.Unlock()
	img, st := captureTS()
	if st != capOK || img == nil {
		t.Fatalf("captura falhou: %v", st)
	}
	b := img.Bounds()
	t.Logf("janela capturada: %dx%d", b.Dx(), b.Dy())
	if b.Dx() != 640 || b.Dy() != 400 {
		t.Fatalf("tamanho inesperado: %v", b)
	}
	at := func(fx, fy float64) color.RGBA { return img.RGBAAt(int(fx*float64(b.Dx())), int(fy*float64(b.Dy()))) }
	top, bottom := at(0.5, 0.40), at(0.5, 0.85)
	t.Logf("cores: em cima %v, embaixo %v", top, bottom)
	if top.R < 200 || top.G > 60 || top.B > 60 {
		t.Fatalf("em cima deveria ser vermelho: %v", top)
	}
	if bottom.B < 200 || bottom.R > 60 || bottom.G > 60 {
		t.Fatalf("embaixo deveria ser azul: %v", bottom)
	}

	// Gravação de ponta a ponta com a captura de verdade.
	dir := t.TempDir()
	r := NewRecorder(dir, func(kind, text string) { t.Log(kind, text) })
	file, err := r.Begin("C1", "teste no Windows", false)
	if err != nil {
		t.Fatal(err)
	}
	r.Event("ok", "20:15:03", `ENVIADO "!next C1" para x3tBot`)
	time.Sleep(1200 * time.Millisecond)
	r.End(0)
	r.Close()
	in := readAVI(t, file)
	if in.totalFrames < 4 || in.w != recMinW || in.h != 400+recBarLines*fontH+12 {
		t.Fatalf("vídeo: %d quadros, %dx%d", in.totalFrames, in.w, in.h)
	}

	// Minimizada (precisa de um gerenciador de janelas, ex.: a área de trabalho virtual do Wine).
	if os.Getenv("NEXTTS_WINE_TEST") == "desktop" {
		done := make(chan bool)
		cmds <- func(h uintptr) { tShowWindow.Call(h, 6); done <- true } // SW_MINIMIZE
		<-done
		time.Sleep(500 * time.Millisecond)
		if _, st := captureTS(); st != capMinimized {
			t.Fatalf("minimizada: esperava 'minimizado', veio %v", st)
		}
	}
}
