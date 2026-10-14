package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const Version = "1.5.0"

// Preset é um "atalho de claim": de onde ler (canal) e para quem mandar (bot).
// Guarda o UID do bot (estável) e os nomes só para mostrar.
type Preset struct {
	Name        string `json:"name"`        // rótulo do atalho (ex.: "x3tBot", "MissClick Test")
	ChannelName string `json:"channelName"` // canal da lista
	BotUID      string `json:"botUID"`      // identidade de quem recebe o comando
	BotName     string `json:"botName"`     // nome do bot (só para mostrar)
}

// Favorite é um "plano salvo": respawns (A, B, C), janela e opções.
// O canal, o bot e o Modo teste não entram.
type Favorite struct {
	Name        string   `json:"name"`
	Codes       []string `json:"codes"`
	WindowOn    bool     `json:"windowOn"`
	WindowStart string   `json:"windowStart"`
	WindowEnd   string   `json:"windowEnd"`
	WaitGiveUp  bool     `json:"waitGiveUp"`
	Record      bool     `json:"record"`
}

// Settings são as escolhas lembradas entre uma abertura e outra.
// O modo teste nunca é lembrado (para não esquecer ligado sem querer).
type Settings struct {
	Port          int        `json:"port"`
	Remember      bool       `json:"remember"`
	KeyProtected  string     `json:"keyProtected,omitempty"` // API Key protegida pelo Windows (DPAPI)
	ServerName    string     `json:"serverName"`
	ChannelName   string     `json:"channelName"`
	ManualChannel bool       `json:"manualChannel"` // o usuário escolheu o canal de propósito
	BotUID        string     `json:"botUID"`
	BotName       string     `json:"botName"`
	Code          string     `json:"code"`  // Plano A
	CodeB         string     `json:"codeB"` // Plano B (vazio = sem)
	CodeC         string     `json:"codeC"` // Plano C (vazio = sem)
	WindowOn      bool       `json:"windowOn"`
	WindowStart   string     `json:"windowStart"`
	WindowEnd     string     `json:"windowEnd"`
	WaitGiveUp    bool       `json:"waitGiveUp"` // esperar o next desistir
	Record        bool       `json:"record"`     // gravar a janela do TS em vídeo
	Presets       []Preset   `json:"presets"`
	Favorites     []Favorite `json:"favorites"` // planos salvos
	// Atalhos sugeridos automaticamente que o usuário apagou (não recriar).
	PresetsRemoved []string `json:"presetsRemoved"`
}

func presetKey(channelName, botUID string) string { return channelName + "\x00" + botUID }

func defaultSettings() Settings {
	return Settings{Port: 25639, Remember: true, Code: "C1", WindowStart: "20:00", WindowEnd: "22:30"}
}

func (s *Settings) savedKey() string {
	if s.KeyProtected == "" {
		return ""
	}
	raw, err := base64.StdEncoding.DecodeString(s.KeyProtected)
	if err != nil {
		return ""
	}
	plain, err := unprotect(raw)
	if err != nil {
		return ""
	}
	return string(plain)
}

func (s *Settings) setSavedKey(key string) error {
	enc, err := protect([]byte(key))
	if err != nil {
		s.KeyProtected = ""
		return err
	}
	s.KeyProtected = base64.StdEncoding.EncodeToString(enc)
	return nil
}

type Store struct {
	dir string
	mu  sync.Mutex
}

func (st *Store) path() string { return filepath.Join(st.dir, "preferencias.json") }

func (st *Store) Load() Settings {
	st.mu.Lock()
	defer st.mu.Unlock()
	s := defaultSettings()
	b, err := os.ReadFile(st.path())
	if err != nil {
		return s
	}
	if json.Unmarshal(b, &s) != nil {
		return defaultSettings() // arquivo estragado: começa do zero
	}
	if s.Port <= 0 || s.Port > 65535 {
		s.Port = 25639
	}
	if _, ok := ParseHM(s.WindowStart); !ok {
		s.WindowStart = "20:00"
	}
	if _, ok := ParseHM(s.WindowEnd); !ok {
		s.WindowEnd = "22:30"
	}
	if _, ok := NormalizeCode(s.Code); !ok {
		s.Code = "C1"
	}
	if c, ok := NormalizeCode(s.CodeB); !ok || c == s.Code {
		s.CodeB = ""
	}
	if c, ok := NormalizeCode(s.CodeC); !ok || s.CodeB == "" || c == s.Code || c == s.CodeB {
		s.CodeC = ""
	}
	return s
}

func (st *Store) Save(s Settings) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	b, _ := json.MarshalIndent(s, "", "  ")
	tmp := st.path() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, st.path())
}

// ---- Registro em arquivo (sem chave) ----

var (
	logMu   sync.Mutex
	logFile *os.File
)

func openLog(dir string) {
	p := filepath.Join(dir, "registro.txt")
	if fi, err := os.Stat(p); err == nil && fi.Size() > 1<<20 {
		_ = os.Rename(p, filepath.Join(dir, "registro-anterior.txt"))
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err == nil {
		logFile = f
	}
}

func fileLog(format string, args ...any) {
	logMu.Lock()
	defer logMu.Unlock()
	if logFile == nil {
		return
	}
	fmt.Fprintf(logFile, "%s %s\n", time.Now().Format("2006-01-02 15:04:05.000"), fmt.Sprintf(format, args...))
}
