package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNextCommand(t *testing.T) {
	cases := []struct {
		code string
		on   bool
		h, m int
		want string
	}{
		{"C1", false, 2, 10, "!next C1"}, // desligado: o tempo não entra
		{"C1", true, 2, 10, "!next C1 2:10"},
		{"C1", true, 2, 0, "!next C1 2:00"},
		{"C1", true, 2, 30, "!next C1 2:30"},
		{"AB1", true, 0, 5, "!next AB1 0:05"}, // minutos sempre com 2 algarismos
		{"D13", true, 10, 0, "!next D13 10:00"},
		{"Y11", true, 1, 9, "!next Y11 1:09"},
	}
	for _, c := range cases {
		if got := NextCommand(c.code, c.on, c.h, c.m); got != c.want {
			t.Errorf("NextCommand(%q, %v, %d, %d) = %q, quero %q", c.code, c.on, c.h, c.m, got, c.want)
		}
	}
}

func TestValidateDuration(t *testing.T) {
	ok := [][2]int{{2, 10}, {0, 1}, {0, 59}, {1, 0}, {10, 0}, {99, 59}}
	bad := [][2]int{{0, 0}, {-1, 10}, {2, -5}, {2, 60}, {0, 75}, {100, 0}}
	for _, d := range ok {
		if err := ValidateDuration(d[0], d[1]); err != nil {
			t.Errorf("%d:%02d devia ser aceito: %v", d[0], d[1], err)
		}
	}
	for _, d := range bad {
		if err := ValidateDuration(d[0], d[1]); err == nil {
			t.Errorf("%d:%d devia ser recusado", d[0], d[1])
		}
	}
}

func TestReservedTime(t *testing.T) {
	h, m, ok := reservedTime("Você entrou na posição 1 da fila de C1. Tempo reservado: 2:10.")
	if !ok || h != 2 || m != 10 {
		t.Fatalf("leu %d:%02d (%v)", h, m, ok)
	}
	if _, _, ok := reservedTime("Você entrou na posição 1 da fila de C1."); ok {
		t.Fatal("achou tempo onde não tem")
	}
}

// Preferências de antes da 1.7.0 (sem o tempo) e arquivo mexido à mão.
func TestSettingsTempoDoClaim(t *testing.T) {
	dir := t.TempDir()
	st := &Store{dir: dir}
	write := func(s string) {
		if err := os.WriteFile(filepath.Join(dir, "preferencias.json"), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"port":25639,"code":"C1"}`)
	if s := st.Load(); s.UseDuration || s.DurHours != 2 || s.DurMinutes != 0 {
		t.Fatalf("antigo: %v %d:%d", s.UseDuration, s.DurHours, s.DurMinutes)
	}
	write(`{"port":25639,"useDuration":true,"durHours":2,"durMinutes":10}`)
	if s := st.Load(); !s.UseDuration || s.DurHours != 2 || s.DurMinutes != 10 {
		t.Fatalf("salvo: %v %d:%d", s.UseDuration, s.DurHours, s.DurMinutes)
	}
	write(`{"port":25639,"useDuration":true,"durHours":0,"durMinutes":75}`)
	if s := st.Load(); s.UseDuration || s.DurHours != 2 || s.DurMinutes != 0 {
		t.Fatalf("mexido: %v %d:%d", s.UseDuration, s.DurHours, s.DurMinutes)
	}
}
