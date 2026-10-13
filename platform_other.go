//go:build !windows

package main

// Versão para testes em Linux. O app de verdade é o de Windows.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func protect(data []byte) ([]byte, error) { return append([]byte("TEST:"), data...), nil }
func unprotect(data []byte) ([]byte, error) {
	if len(data) < 5 || string(data[:5]) != "TEST:" {
		return nil, fmt.Errorf("formato inválido")
	}
	return data[5:], nil
}

func dataDir() string {
	if d := os.Getenv("NEXTTS_DATA"); d != "" {
		return d
	}
	base, _ := os.UserConfigDir()
	return filepath.Join(base, "NextTS")
}

func messageBox(title, text string) { fmt.Fprintln(os.Stderr, title+": "+text) }

func openWindow(url, profileDir string) error { return openDefaultBrowser(url) }

func openDefaultBrowser(url string) error { return exec.Command("xdg-open", url).Start() }

func openFolder(dir string) error {
	if os.Getenv("NEXTTS_NO_BROWSER") != "" {
		return nil // testes: não abre nada
	}
	return exec.Command("xdg-open", dir).Start()
}
