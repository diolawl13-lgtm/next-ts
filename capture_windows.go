//go:build windows

package main

// Copia a imagem da janela do TeamSpeak 3 para a gravação.
//
// Tenta 3 jeitos, um depois do outro (e lembra qual funcionou):
//  1. PrintWindow com PW_RENDERFULLCONTENT: pega a janela mesmo atrás de outra;
//  2. a imagem que o Windows guarda da janela (GetWindowDC + BitBlt);
//  3. direto da tela, SÓ quando o TS está visível na frente (nunca grava o Tibia
//     no lugar do TS).
// Minimizada não dá. Não mexe em teclado, mouse nem traz o TS para a frente.
// O que acontece fica anotado no registro.txt (qual jeito funcionou ou por que falhou).

import (
	"fmt"
	"image"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var (
	gdi32                       = syscall.NewLazyDLL("gdi32.dll")
	advapi32                    = syscall.NewLazyDLL("advapi32.dll")
	dwmapi                      = syscall.NewLazyDLL("dwmapi.dll")
	procEnumWindows             = user32.NewProc("EnumWindows")
	procIsWindow                = user32.NewProc("IsWindow")
	procIsWindowVisible         = user32.NewProc("IsWindowVisible")
	procIsIconic                = user32.NewProc("IsIconic")
	procGetWindow               = user32.NewProc("GetWindow")
	procGetWindowRect           = user32.NewProc("GetWindowRect")
	procGetWindowTextW          = user32.NewProc("GetWindowTextW")
	procGetClassNameW           = user32.NewProc("GetClassNameW")
	procGetWindowLongW          = user32.NewProc("GetWindowLongW")
	procGetWindowThreadProcID   = user32.NewProc("GetWindowThreadProcessId")
	procWindowFromPoint         = user32.NewProc("WindowFromPoint")
	procGetAncestor             = user32.NewProc("GetAncestor")
	procGetDC                   = user32.NewProc("GetDC")
	procGetWindowDC             = user32.NewProc("GetWindowDC")
	procReleaseDC               = user32.NewProc("ReleaseDC")
	procPrintWindow             = user32.NewProc("PrintWindow")
	procSetProcessDpiAwareCtx   = user32.NewProc("SetProcessDpiAwarenessContext")
	procSetProcessDPIAware      = user32.NewProc("SetProcessDPIAware")
	procCreateCompatibleDC      = gdi32.NewProc("CreateCompatibleDC")
	procCreateDIBSection        = gdi32.NewProc("CreateDIBSection")
	procSelectObject            = gdi32.NewProc("SelectObject")
	procDeleteObject            = gdi32.NewProc("DeleteObject")
	procDeleteDC                = gdi32.NewProc("DeleteDC")
	procBitBlt                  = gdi32.NewProc("BitBlt")
	procGdiFlush                = gdi32.NewProc("GdiFlush")
	procOpenProcess             = kernel32.NewProc("OpenProcess")
	procCloseHandle             = kernel32.NewProc("CloseHandle")
	procGetCurrentProcess       = kernel32.NewProc("GetCurrentProcess")
	procQueryFullProcessImageNm = kernel32.NewProc("QueryFullProcessImageNameW")
	procOpenProcessToken        = advapi32.NewProc("OpenProcessToken")
	procGetTokenInformation     = advapi32.NewProc("GetTokenInformation")
	procDwmGetWindowAttribute   = dwmapi.NewProc("DwmGetWindowAttribute")
)

const (
	gwOwner                  = 4
	gwlExStyle               = ^uintptr(19) // GWL_EXSTYLE = -20
	wsExToolWindow           = 0x00000080
	gaRoot                   = 2
	pwRenderFullContent      = 2
	srcCopy                  = 0x00CC0020
	captureBlt               = 0x40000000
	processQueryLimitedInfo  = 0x1000
	tokenQuery               = 0x0008
	tokenElevationClass      = 20
	dwmwaCloaked             = 14
	dpiAwarenessPerMonitorV2 = ^uintptr(3) // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 = -4
	maxCaptureSide           = 8000
	minMainW, minMainH       = 200, 150 // menor que isso não é a janela principal do TS
)

type rect struct{ Left, Top, Right, Bottom int32 }

type point struct{ X, Y int32 }

type bitmapInfo struct {
	Size          uint32
	Width, Height int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPels, YPels  int32
	ClrUsed       uint32
	ClrImportant  uint32
	Colors        [4]uint32
}

// O app trabalha com o tamanho real das janelas (em pontos da tela), mesmo com o
// "zoom" do Windows (DPI). Tem que ser feito antes de tudo.
func init() {
	if procSetProcessDpiAwareCtx.Find() == nil {
		if r, _, _ := procSetProcessDpiAwareCtx.Call(dpiAwarenessPerMonitorV2); r != 0 {
			return
		}
	}
	if procSetProcessDPIAware.Find() == nil {
		procSetProcessDPIAware.Call()
	}
}

var (
	enumMu    sync.Mutex
	enumFound []uintptr
	enumCB    = syscall.NewCallback(func(h, _ uintptr) uintptr {
		enumFound = append(enumFound, h)
		return 1
	})
)

func topWindows() []uintptr {
	enumMu.Lock()
	defer enumMu.Unlock()
	enumFound = enumFound[:0]
	procEnumWindows.Call(enumCB, 0)
	return append([]uintptr(nil), enumFound...)
}

func windowText(h uintptr) string {
	buf := make([]uint16, 256)
	n, _, _ := procGetWindowTextW.Call(h, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.UTF16ToString(buf[:n])
}

func windowClass(h uintptr) string {
	buf := make([]uint16, 256)
	n, _, _ := procGetClassNameW.Call(h, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.UTF16ToString(buf[:n])
}

func windowRect(h uintptr) (rect, bool) {
	var r rect
	ok, _, _ := procGetWindowRect.Call(h, uintptr(unsafe.Pointer(&r)))
	return r, ok != 0
}

func isIconic(h uintptr) bool { r, _, _ := procIsIconic.Call(h); return r != 0 }

// cloaked: janela "escondida" pelo Windows (ex.: em outra área de trabalho virtual).
func cloaked(h uintptr) bool {
	if procDwmGetWindowAttribute.Find() != nil {
		return false
	}
	var v uint32
	r, _, _ := procDwmGetWindowAttribute.Call(h, dwmwaCloaked, uintptr(unsafe.Pointer(&v)), 4)
	return r == 0 && v != 0
}

func describeWindow(h uintptr) string {
	r, _ := windowRect(h)
	s := fmt.Sprintf("%q classe=%q %dx%d em (%d,%d)", windowText(h), windowClass(h), r.Right-r.Left, r.Bottom-r.Top, r.Left, r.Top)
	if isIconic(h) {
		s += " minimizada"
	}
	return s
}

// O nome do programa de cada processo, guardado por um tempo (é consultado várias vezes por segundo).
var (
	procNameMu    sync.Mutex
	procNames     = map[uint32]string{}
	procNamesTime time.Time
)

func processName(pid uint32) string {
	procNameMu.Lock()
	defer procNameMu.Unlock()
	if time.Since(procNamesTime) > time.Minute {
		procNames, procNamesTime = map[uint32]string{}, time.Now()
	}
	if n, ok := procNames[pid]; ok {
		return n
	}
	name := ""
	if h, _, _ := procOpenProcess.Call(processQueryLimitedInfo, 0, uintptr(pid)); h != 0 {
		buf := make([]uint16, 1024)
		size := uint32(len(buf))
		if r, _, _ := procQueryFullProcessImageNm.Call(h, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size))); r != 0 {
			name = strings.ToLower(filepath.Base(syscall.UTF16ToString(buf[:size])))
		}
		procCloseHandle.Call(h)
	}
	procNames[pid] = name
	return name
}

func windowPID(h uintptr) uint32 {
	var pid uint32
	procGetWindowThreadProcID.Call(h, uintptr(unsafe.Pointer(&pid)))
	return pid
}

// elevated: o processo está rodando como administrador.
func elevated(proc uintptr) (bool, bool) {
	var tok uintptr
	if r, _, _ := procOpenProcessToken.Call(proc, tokenQuery, uintptr(unsafe.Pointer(&tok))); r == 0 {
		return false, false
	}
	defer procCloseHandle.Call(tok)
	var v, n uint32
	if r, _, _ := procGetTokenInformation.Call(tok, tokenElevationClass, uintptr(unsafe.Pointer(&v)), 4, uintptr(unsafe.Pointer(&n))); r == 0 {
		return false, false
	}
	return v != 0, true
}

// tsAdminAndMeNot: o TS está como administrador e o Next TS não (o Windows não deixa copiar a janela).
func tsAdminAndMeNot(h uintptr) bool {
	ph, _, _ := procOpenProcess.Call(processQueryLimitedInfo, 0, uintptr(windowPID(h)))
	if ph == 0 {
		return false
	}
	defer procCloseHandle.Call(ph)
	ts, ok1 := elevated(ph)
	me, _, _ := procGetCurrentProcess.Call()
	mine, ok2 := elevated(me)
	return ok1 && ok2 && ts && !mine
}

// findTSWindow acha a janela principal do TeamSpeak 3 (ts3client_win64.exe / ts3client_win32.exe):
// visível, sem "dona" (não é um diálogo), não é janelinha de ferramenta, do tamanho de uma
// janela de verdade; das que sobrarem, a maior. Minimizada só é escolhida se não houver outra.
func findTSWindow() (uintptr, []string) {
	var best uintptr
	bestScore := int64(0)
	var seen []string
	for _, h := range topWindows() {
		if v, _, _ := procIsWindowVisible.Call(h); v == 0 {
			continue
		}
		if !strings.HasPrefix(processName(windowPID(h)), "ts3client") {
			continue
		}
		seen = append(seen, describeWindow(h))
		if o, _, _ := procGetWindow.Call(h, gwOwner); o != 0 {
			continue // diálogo que pertence a outra janela
		}
		if ex, _, _ := procGetWindowLongW.Call(h, gwlExStyle); ex&wsExToolWindow != 0 {
			continue // janelinha de ferramenta (avisos, menus)
		}
		if cloaked(h) {
			continue
		}
		var score int64
		if isIconic(h) {
			score = 1 // minimizada: só se não tiver outra
		} else if r, ok := windowRect(h); ok {
			w, ht := int64(r.Right-r.Left), int64(r.Bottom-r.Top)
			if w >= minMainW && ht >= minMainH {
				score = w * ht
			}
		}
		if score > bestScore {
			best, bestScore = h, score
		}
	}
	return best, seen
}

// Jeitos de copiar a janela (na ordem em que são tentados).
const (
	capPrintFull = iota // PrintWindow com PW_RENDERFULLCONTENT
	capPrint            // PrintWindow simples
	capWindowDC         // imagem guardada da janela (GetWindowDC + BitBlt)
	capScreen           // direto da tela (só com o TS visível na frente)
	capMethods
)

var capMethodName = [capMethods]string{"PrintWindow (completo)", "PrintWindow", "imagem da janela", "tela (TS na frente)"}

// capSkip desliga jeitos de copiar (só nos testes).
var capSkip [capMethods]bool

var (
	tsWinMu   sync.Mutex
	tsWin     uintptr
	tsWinTime time.Time
	capBest   = -1    // último jeito que funcionou
	capNote   string  // última anotação no registro (não repete)
	capLogged = false // já anotou as janelas do TS encontradas
)

func capLog(format string, args ...any) {
	s := fmt.Sprintf(format, args...)
	if s != capNote {
		capNote = s
		fileLog("[gravação] %s", s)
	}
}

// visibleOnTop: a janela aparece de verdade na tela (pelo menos 3 de 5 pontos são dela).
func visibleOnTop(h uintptr, r rect) bool {
	w, ht := r.Right-r.Left, r.Bottom-r.Top
	pts := []point{{r.Left + w/2, r.Top + ht/2}, {r.Left + w/10, r.Top + ht/10}, {r.Right - w/10, r.Top + ht/10},
		{r.Left + w/10, r.Bottom - ht/10}, {r.Right - w/10, r.Bottom - ht/10}}
	hits := 0
	for _, p := range pts {
		// WindowFromPoint recebe o POINT inteiro num único valor de 64 bits.
		at, _, _ := procWindowFromPoint.Call(uintptr(uint32(p.X)) | uintptr(uint32(p.Y))<<32)
		if at == 0 {
			continue
		}
		if root, _, _ := procGetAncestor.Call(at, gaRoot); root == h {
			hits++
		}
	}
	return hits >= 3
}

func captureTS() (*image.RGBA, capState) {
	tsWinMu.Lock()
	defer tsWinMu.Unlock()
	h := tsWin
	if ok, _, _ := procIsWindow.Call(h); h == 0 || ok == 0 || time.Since(tsWinTime) > 2*time.Second {
		nh, seen := findTSWindow()
		if nh != h {
			capBest = -1
			if nh != 0 {
				fileLog("[gravação] janela do TeamSpeak escolhida: %s", describeWindow(nh))
			}
		}
		if !capLogged && len(seen) > 0 {
			capLogged = true
			fileLog("[gravação] janelas visíveis do TeamSpeak: %s", strings.Join(seen, " | "))
		}
		h, tsWin, tsWinTime = nh, nh, time.Now()
	}
	if h == 0 {
		capLog("nenhuma janela do TeamSpeak encontrada")
		return nil, capNotFound
	}
	if isIconic(h) {
		capLog("janela do TeamSpeak minimizada")
		return nil, capMinimized
	}
	r, ok := windowRect(h)
	w, ht := int(r.Right-r.Left), int(r.Bottom-r.Top)
	if !ok || w <= 0 || ht <= 0 || w > maxCaptureSide || ht > maxCaptureSide {
		capLog("tamanho da janela estranho: %s", describeWindow(h))
		return nil, capFailed
	}

	// Ordem: primeiro o jeito que funcionou da última vez, depois os outros.
	order := []int{capPrintFull, capPrint, capWindowDC, capScreen}
	if capBest > 0 {
		order = append([]int{capBest}, order...)
	}
	var fails []string
	sawBlack := false
	tried := map[int]bool{}
	for _, m := range order {
		if tried[m] || capSkip[m] {
			continue
		}
		tried[m] = true
		img, why := captureWith(m, h, r, w, ht)
		if img == nil {
			fails = append(fails, capMethodName[m]+": "+why)
			continue
		}
		if allBlack(img) {
			sawBlack = true
			fails = append(fails, capMethodName[m]+": imagem toda preta")
			continue
		}
		if m != capBest {
			fileLog("[gravação] copiando a janela do TeamSpeak por: %s (%dx%d)", capMethodName[m], w, ht)
		}
		capBest, capNote = m, ""
		return img, capOK
	}
	capBest = -1
	if tsAdminAndMeNot(h) {
		capLog("o TeamSpeak está como administrador e o Next TS não. Falhas: %s", strings.Join(fails, "; "))
		return nil, capElevated
	}
	capLog("não consegui copiar %s. Falhas: %s", describeWindow(h), strings.Join(fails, "; "))
	if sawBlack {
		return image.NewRGBA(image.Rect(0, 0, w, ht)), capOK // o gravador mostra o aviso de "imagem preta"
	}
	return nil, capFailed
}

// captureWith copia a janela por um dos jeitos. Devolve a imagem ou o motivo da falha.
func captureWith(m int, h uintptr, r rect, w, ht int) (*image.RGBA, string) {
	if m == capScreen && !visibleOnTop(h, r) {
		return nil, "TS não está visível na frente"
	}
	screen, _, _ := procGetDC.Call(0)
	if screen == 0 {
		return nil, "GetDC falhou"
	}
	defer procReleaseDC.Call(0, screen)
	mem, _, _ := procCreateCompatibleDC.Call(screen)
	if mem == 0 {
		return nil, "CreateCompatibleDC falhou"
	}
	defer procDeleteDC.Call(mem)
	bi := bitmapInfo{Size: 40, Width: int32(w), Height: -int32(ht), Planes: 1, BitCount: 32}
	var bits unsafe.Pointer // memória da imagem, criada pelo Windows
	bmp, _, e := procCreateDIBSection.Call(mem, uintptr(unsafe.Pointer(&bi)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if bmp == 0 || bits == nil {
		return nil, fmt.Sprintf("CreateDIBSection falhou (%v)", e)
	}
	defer procDeleteObject.Call(bmp)
	old, _, _ := procSelectObject.Call(mem, bmp)
	defer procSelectObject.Call(mem, old)

	var ok uintptr
	switch m {
	case capPrintFull:
		ok, _, e = procPrintWindow.Call(h, mem, pwRenderFullContent)
	case capPrint:
		ok, _, e = procPrintWindow.Call(h, mem, 0)
	case capWindowDC:
		wdc, _, e2 := procGetWindowDC.Call(h)
		if wdc == 0 {
			return nil, fmt.Sprintf("GetWindowDC falhou (%v)", e2)
		}
		ok, _, e = procBitBlt.Call(mem, 0, 0, uintptr(w), uintptr(ht), wdc, 0, 0, srcCopy)
		procReleaseDC.Call(h, wdc)
	case capScreen:
		ok, _, e = procBitBlt.Call(mem, 0, 0, uintptr(w), uintptr(ht), screen, uintptr(r.Left), uintptr(r.Top), srcCopy|captureBlt)
	}
	if ok == 0 {
		return nil, fmt.Sprintf("o Windows recusou (%v)", e)
	}
	procGdiFlush.Call()
	img := image.NewRGBA(image.Rect(0, 0, w, ht))
	src := unsafe.Slice((*byte)(bits), w*ht*4)
	p := img.Pix
	for i := 0; i < len(p); i += 4 { // BGRA → RGBA
		p[i], p[i+1], p[i+2], p[i+3] = src[i+2], src[i+1], src[i], 255
	}
	return img, ""
}
