//go:build windows

package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"unsafe"
)

var (
	crypt32           = syscall.NewLazyDLL("crypt32.dll")
	kernel32          = syscall.NewLazyDLL("kernel32.dll")
	user32            = syscall.NewLazyDLL("user32.dll")
	procProtectData   = crypt32.NewProc("CryptProtectData")
	procUnprotectData = crypt32.NewProc("CryptUnprotectData")
	procLocalFree     = kernel32.NewProc("LocalFree")
	procMessageBoxW   = user32.NewProc("MessageBoxW")
)

type dataBlob struct {
	cbData uint32
	pbData *byte
}

const cryptprotectUIForbidden = 0x1

func blobCopy(b dataBlob) []byte {
	if b.pbData == nil || b.cbData == 0 {
		return nil
	}
	out := make([]byte, b.cbData)
	copy(out, unsafe.Slice(b.pbData, b.cbData))
	return out
}

// protect guarda a chave protegida pelo usuário do Windows (DPAPI).
// Só este usuário, neste PC, consegue abrir.
func protect(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("chave vazia")
	}
	in := dataBlob{cbData: uint32(len(data)), pbData: &data[0]}
	var out dataBlob
	r, _, err := procProtectData.Call(uintptr(unsafe.Pointer(&in)), 0, 0, 0, 0, cryptprotectUIForbidden, uintptr(unsafe.Pointer(&out)))
	if r == 0 {
		return nil, err
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.pbData)))
	return blobCopy(out), nil
}

func unprotect(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("vazio")
	}
	in := dataBlob{cbData: uint32(len(data)), pbData: &data[0]}
	var out dataBlob
	r, _, err := procUnprotectData.Call(uintptr(unsafe.Pointer(&in)), 0, 0, 0, 0, cryptprotectUIForbidden, uintptr(unsafe.Pointer(&out)))
	if r == 0 {
		return nil, err
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.pbData)))
	return blobCopy(out), nil
}

func dataDir() string {
	if d := os.Getenv("NEXTTS_DATA"); d != "" {
		return d
	}
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		base, _ = os.UserConfigDir()
	}
	return filepath.Join(base, "NextTS")
}

func messageBox(title, text string) {
	t, _ := syscall.UTF16PtrFromString(title)
	m, _ := syscall.UTF16PtrFromString(text)
	procMessageBoxW.Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), 0x10|0x40000) // MB_ICONERROR | MB_TOPMOST
}

func browserCandidates() []string {
	var out []string
	for _, env := range []string{"ProgramFiles(x86)", "ProgramFiles", "LOCALAPPDATA"} {
		if base := os.Getenv(env); base != "" {
			out = append(out, filepath.Join(base, "Microsoft", "Edge", "Application", "msedge.exe"))
		}
	}
	for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "LOCALAPPDATA"} {
		if base := os.Getenv(env); base != "" {
			out = append(out, filepath.Join(base, "Google", "Chrome", "Application", "chrome.exe"))
		}
	}
	return out
}

// openWindow abre a janelinha do app (Edge/Chrome em modo aplicativo, sem barra de endereço).
func openWindow(url, profileDir string) error {
	for _, exe := range browserCandidates() {
		if _, err := os.Stat(exe); err != nil {
			continue
		}
		cmd := exec.Command(exe,
			"--app="+url,
			"--user-data-dir="+profileDir,
			"--window-size=520,900",
			"--no-first-run",
			"--no-default-browser-check",
			"--disable-sync",
			"--disable-extensions",
		)
		if err := cmd.Start(); err == nil {
			go cmd.Wait()
			return nil
		}
	}
	return openDefaultBrowser(url)
}

// openFolder abre uma pasta no Explorador de Arquivos.
func openFolder(dir string) error {
	cmd := exec.Command("explorer.exe", dir)
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}

func openDefaultBrowser(url string) error {
	cmd := exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}
