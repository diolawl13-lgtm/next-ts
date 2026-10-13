//go:build windows

package main

// Copia a imagem da janela do TeamSpeak 3 para a gravação.
//
// Usa PrintWindow com PW_RENDERFULLCONTENT: pega a janela mesmo que ela esteja
// atrás de outra (ex.: atrás do Tibia). Só não dá quando ela está minimizada.
// Não mexe em teclado, mouse nem traz o TS para a frente.

import (
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
	procEnumWindows             = user32.NewProc("EnumWindows")
	procIsWindow                = user32.NewProc("IsWindow")
	procIsWindowVisible         = user32.NewProc("IsWindowVisible")
	procIsIconic                = user32.NewProc("IsIconic")
	procGetWindow               = user32.NewProc("GetWindow")
	procGetWindowRect           = user32.NewProc("GetWindowRect")
	procGetWindowTextW          = user32.NewProc("GetWindowTextW")
	procGetWindowThreadProcID   = user32.NewProc("GetWindowThreadProcessId")
	procGetDC                   = user32.NewProc("GetDC")
	procReleaseDC               = user32.NewProc("ReleaseDC")
	procPrintWindow             = user32.NewProc("PrintWindow")
	procSetProcessDpiAwareCtx   = user32.NewProc("SetProcessDpiAwarenessContext")
	procSetProcessDPIAware      = user32.NewProc("SetProcessDPIAware")
	procCreateCompatibleDC      = gdi32.NewProc("CreateCompatibleDC")
	procCreateCompatibleBitmap  = gdi32.NewProc("CreateCompatibleBitmap")
	procSelectObject            = gdi32.NewProc("SelectObject")
	procDeleteObject            = gdi32.NewProc("DeleteObject")
	procDeleteDC                = gdi32.NewProc("DeleteDC")
	procGetDIBits               = gdi32.NewProc("GetDIBits")
	procOpenProcess             = kernel32.NewProc("OpenProcess")
	procCloseHandle             = kernel32.NewProc("CloseHandle")
	procQueryFullProcessImageNm = kernel32.NewProc("QueryFullProcessImageNameW")
)

const (
	gwOwner                  = 4
	pwRenderFullContent      = 2
	processQueryLimitedInfo  = 0x1000
	dpiAwarenessPerMonitorV2 = ^uintptr(3) // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 = -4
	maxCaptureSide           = 8000
)

type rect struct{ Left, Top, Right, Bottom int32 }

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

// Tamanho real da janela (em pontos da tela), mesmo com o "zoom" do Windows (DPI).
var dpiOnce sync.Once

func dpiAware() {
	dpiOnce.Do(func() {
		if procSetProcessDpiAwareCtx.Find() == nil {
			if r, _, _ := procSetProcessDpiAwareCtx.Call(dpiAwarenessPerMonitorV2); r != 0 {
				return
			}
		}
		if procSetProcessDPIAware.Find() == nil {
			procSetProcessDPIAware.Call()
		}
	})
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

// findTSWindow acha a janela principal do TeamSpeak 3 (ts3client_win64.exe / ts3client_win32.exe).
func findTSWindow() uintptr {
	var best uintptr
	bestScore := int64(-1)
	for _, h := range topWindows() {
		if v, _, _ := procIsWindowVisible.Call(h); v == 0 {
			continue
		}
		if o, _, _ := procGetWindow.Call(h, gwOwner); o != 0 {
			continue // janelinhas que pertencem a outra (diálogos)
		}
		var pid uint32
		procGetWindowThreadProcID.Call(h, uintptr(unsafe.Pointer(&pid)))
		if !strings.HasPrefix(processName(pid), "ts3client") {
			continue
		}
		var score int64
		if strings.Contains(strings.ToLower(windowText(h)), "teamspeak") {
			score += 1 << 40
		}
		if ic, _, _ := procIsIconic.Call(h); ic == 0 {
			var r rect
			procGetWindowRect.Call(h, uintptr(unsafe.Pointer(&r)))
			score += int64(r.Right-r.Left) * int64(r.Bottom-r.Top)
		}
		if score > bestScore {
			best, bestScore = h, score
		}
	}
	return best
}

var (
	tsWinMu   sync.Mutex
	tsWin     uintptr
	tsWinTime time.Time
)

func captureTS() (*image.RGBA, capState) {
	dpiAware()
	tsWinMu.Lock()
	h := tsWin
	if ok, _, _ := procIsWindow.Call(h); h == 0 || ok == 0 || time.Since(tsWinTime) > 2*time.Second {
		h = findTSWindow()
		tsWin, tsWinTime = h, time.Now()
	}
	tsWinMu.Unlock()
	if h == 0 {
		return nil, capNotFound
	}
	if ic, _, _ := procIsIconic.Call(h); ic != 0 {
		return nil, capMinimized
	}
	var r rect
	if ok, _, _ := procGetWindowRect.Call(h, uintptr(unsafe.Pointer(&r))); ok == 0 {
		return nil, capFailed
	}
	w, ht := int(r.Right-r.Left), int(r.Bottom-r.Top)
	if w <= 0 || ht <= 0 || w > maxCaptureSide || ht > maxCaptureSide {
		return nil, capFailed
	}
	screen, _, _ := procGetDC.Call(0)
	if screen == 0 {
		return nil, capFailed
	}
	defer procReleaseDC.Call(0, screen)
	mem, _, _ := procCreateCompatibleDC.Call(screen)
	if mem == 0 {
		return nil, capFailed
	}
	defer procDeleteDC.Call(mem)
	bmp, _, _ := procCreateCompatibleBitmap.Call(screen, uintptr(w), uintptr(ht))
	if bmp == 0 {
		return nil, capFailed
	}
	defer procDeleteObject.Call(bmp)
	old, _, _ := procSelectObject.Call(mem, bmp)
	ok, _, _ := procPrintWindow.Call(h, mem, pwRenderFullContent)
	if ok == 0 {
		ok, _, _ = procPrintWindow.Call(h, mem, 0)
	}
	procSelectObject.Call(mem, old) // GetDIBits pede o desenho fora do DC
	if ok == 0 {
		return nil, capFailed
	}
	bi := bitmapInfo{Size: 40, Width: int32(w), Height: -int32(ht), Planes: 1, BitCount: 32}
	img := image.NewRGBA(image.Rect(0, 0, w, ht))
	if n, _, _ := procGetDIBits.Call(mem, bmp, 0, uintptr(ht), uintptr(unsafe.Pointer(&img.Pix[0])), uintptr(unsafe.Pointer(&bi)), 0); n == 0 {
		return nil, capFailed
	}
	p := img.Pix
	for i := 0; i < len(p); i += 4 { // BGRA → RGBA
		p[i], p[i+2], p[i+3] = p[i+2], p[i], 255
	}
	return img, capOK
}
