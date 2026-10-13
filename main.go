package main

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

//go:embed web/index.html web/icone.png
var webFS embed.FS

type instanceInfo struct {
	Port  int    `json:"port"`
	Token string `json:"token"`
}

func main() {
	initClock()
	dir := dataDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		messageBox("Next TS", "Não consegui criar a pasta de dados:\n"+dir+"\n\n"+err.Error())
		return
	}
	openLog(dir)
	fileLog("---- Next TS %s iniciando ----", Version)
	instPath := filepath.Join(dir, "instancia.json")

	// Já aberto? Só mostra a janela de novo.
	if b, err := os.ReadFile(instPath); err == nil {
		var inst instanceInfo
		if json.Unmarshal(b, &inst) == nil && inst.Port > 0 && pingInstance(inst) == Version {
			url := fmt.Sprintf("http://127.0.0.1:%d/?t=%s", inst.Port, inst.Token)
			if os.Getenv("NEXTTS_NO_BROWSER") == "" {
				_ = openWindow(url, filepath.Join(dir, "janela"))
			}
			fileLog("já estava aberto; janela reaberta")
			return
		}
		// Versão antiga ainda aberta: pede para ela fechar e abre esta.
		if inst.Port > 0 && pingInstance(inst) != "" {
			quitInstance(inst)
			fileLog("versão anterior fechada")
		}
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		messageBox("Next TS", "Não consegui abrir a janela do app:\n"+err.Error())
		return
	}
	port := ln.Addr().(*net.TCPAddr).Port
	tok := make([]byte, 16)
	_, _ = rand.Read(tok)
	token := hex.EncodeToString(tok)
	ib, _ := json.Marshal(instanceInfo{Port: port, Token: token})
	_ = os.WriteFile(instPath, ib, 0o600)
	url := fmt.Sprintf("http://127.0.0.1:%d/?t=%s", port, token)
	fmt.Println("URL " + url) // usado pelos testes

	app := NewApp(&Store{dir: dir})
	srv := newServer(app, port, token)
	go func() { _ = http.Serve(ln, srv) }()

	// Conecta sozinho (com a chave salva) assim que o TeamSpeak estiver aberto.
	go app.AutoLoop(autoEvery())

	if os.Getenv("NEXTTS_NO_BROWSER") == "" {
		if err := openWindow(url, filepath.Join(dir, "janela")); err != nil {
			messageBox("Next TS", "Não consegui abrir a janela. Abra este endereço no navegador:\n"+url)
		}
	}
	srv.waitQuit()
	app.Shutdown()
	_ = os.Remove(instPath)
	fileLog("encerrado")
}

// pingInstance devolve a versão do app já aberto ("" se não houver nenhum).
// autoEvery: intervalo das tentativas automáticas de conexão (os testes usam menos).
func autoEvery() time.Duration {
	if d, err := time.ParseDuration(os.Getenv("NEXTTS_AUTO_EVERY")); err == nil && d > 0 {
		return d
	}
	return 3 * time.Second
}

func pingInstance(inst instanceInfo) string {
	c := http.Client{Timeout: 1500 * time.Millisecond}
	req, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/api/ping", inst.Port), nil)
	req.Header.Set("X-Token", inst.Token)
	resp, err := c.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var v struct {
		App     string `json:"app"`
		Version string `json:"version"`
	}
	if resp.StatusCode != 200 || json.NewDecoder(resp.Body).Decode(&v) != nil || v.App != "nextts" {
		return ""
	}
	if v.Version == "" {
		return "antiga"
	}
	return v.Version
}

func quitInstance(inst instanceInfo) {
	c := http.Client{Timeout: 2 * time.Second}
	req, _ := http.NewRequest("POST", fmt.Sprintf("http://127.0.0.1:%d/api/quit", inst.Port), strings.NewReader("{}"))
	req.Header.Set("X-Token", inst.Token)
	req.Header.Set("Content-Type", "application/json")
	if resp, err := c.Do(req); err == nil {
		resp.Body.Close()
	}
	for i := 0; i < 20 && pingInstance(inst) != ""; i++ {
		time.Sleep(150 * time.Millisecond)
	}
}

// ---------- Servidor local da janela ----------

type server struct {
	app      *App
	port     int
	token    string
	mu       sync.Mutex
	lastSeen time.Time
	byeAt    time.Time
	everSeen bool
	quit     chan struct{}
	once     sync.Once
}

func newServer(app *App, port int, token string) *server {
	return &server{app: app, port: port, token: token, quit: make(chan struct{}), lastSeen: time.Now()}
}

func (s *server) doQuit() { s.once.Do(func() { close(s.quit) }) }

// waitQuit: fecha quando a janela é fechada (ou se nenhuma janela aparecer).
func (s *server) waitQuit() {
	start := time.Now()
	triedFallback := false
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-s.quit:
			return
		case <-t.C:
		}
		s.mu.Lock()
		seen, last, bye := s.everSeen, s.lastSeen, s.byeAt
		s.mu.Unlock()
		if !bye.IsZero() && last.Before(bye.Add(time.Millisecond)) && time.Since(bye) > 5*time.Second {
			fileLog("janela fechada")
			return
		}
		if !seen && os.Getenv("NEXTTS_NO_BROWSER") == "" {
			if !triedFallback && time.Since(start) > 40*time.Second {
				triedFallback = true
				_ = openDefaultBrowser(fmt.Sprintf("http://127.0.0.1:%d/?t=%s", s.port, s.token))
			}
			if time.Since(start) > 3*time.Minute {
				fileLog("nenhuma janela abriu; encerrando")
				return
			}
		}
	}
}

func (s *server) touch() {
	s.mu.Lock()
	s.lastSeen = time.Now()
	s.everSeen = true
	s.mu.Unlock()
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Só aceita a própria janela (protege contra sites tentando falar com o app).
	if r.Host != fmt.Sprintf("127.0.0.1:%d", s.port) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.URL.Path == "/" {
		if r.URL.Query().Get("t") != s.token {
			http.Error(w, "Abra o app pelo arquivo Next TS.exe", http.StatusForbidden)
			return
		}
		b, _ := webFS.ReadFile("web/index.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; img-src 'self' data:; connect-src 'self'")
		w.Write(b)
		return
	}
	if r.URL.Path == "/favicon.ico" || r.URL.Path == "/icone.png" {
		b, _ := webFS.ReadFile("web/icone.png")
		w.Header().Set("Content-Type", "image/png")
		w.Write(b)
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/api/") {
		http.NotFound(w, r)
		return
	}
	tok := r.Header.Get("X-Token")
	if tok == "" {
		tok = r.URL.Query().Get("t") // sendBeacon não envia cabeçalhos
	}
	if tok != s.token {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if r.URL.Path != "/api/bye" && r.URL.Path != "/api/ping" {
		s.touch()
	}
	if r.Method == http.MethodPost && r.URL.Path != "/api/bye" &&
		!strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	var err error
	switch r.URL.Path {
	case "/api/ping":
		writeJSON(w, map[string]string{"app": "nextts", "version": Version})
		return
	case "/api/state":
		writeJSON(w, s.app.State())
		return
	case "/api/catalog":
		writeJSON(w, Catalog())
		return
	case "/api/connect":
		var body struct {
			Key      string `json:"key"`
			Port     int    `json:"port"`
			Remember bool   `json:"remember"`
		}
		if err = readJSON(r, &body); err == nil {
			err = s.app.Connect(body.Key, body.Port, body.Remember)
		}
	case "/api/refresh":
		var body struct {
			Handler int `json:"handler"`
		}
		if err = readJSON(r, &body); err == nil {
			err = s.app.Refresh(body.Handler)
		}
	case "/api/peek":
		var body struct {
			Cid   int    `json:"cid"`
			Code  string `json:"code"`
			CodeB string `json:"codeB"`
		}
		if err = readJSON(r, &body); err == nil {
			writeJSON(w, s.app.Peek(body.Cid, body.Code, body.CodeB))
			return
		}
	case "/api/start":
		var p StartParams
		if err = readJSON(r, &p); err == nil {
			err = s.app.Start(p)
		}
	case "/api/select":
		var body struct {
			Cid    int    `json:"cid"`
			BotUID string `json:"botUID"`
		}
		if err = readJSON(r, &body); err == nil {
			err = s.app.Select(body.Cid, body.BotUID)
		}
	case "/api/preset/save":
		var body struct {
			Name   string `json:"name"`
			Cid    int    `json:"cid"`
			BotUID string `json:"botUID"`
		}
		if err = readJSON(r, &body); err == nil {
			err = s.app.SavePreset(body.Name, body.Cid, body.BotUID)
		}
	case "/api/preset/delete":
		var body struct {
			Name string `json:"name"`
		}
		if err = readJSON(r, &body); err == nil {
			err = s.app.DeletePreset(body.Name)
		}
	case "/api/stop":
		s.app.Stop("Parado por você.")
	case "/api/forget":
		s.app.ForgetKey()
	case "/api/openrec":
		err = s.app.OpenRecDir()
	case "/api/bye":
		s.mu.Lock()
		s.byeAt = time.Now()
		s.mu.Unlock()
	case "/api/quit":
		s.app.Stop("App fechado.")
		writeJSON(w, map[string]any{"ok": true})
		go func() { time.Sleep(300 * time.Millisecond); s.doQuit() }()
		return
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(w, map[string]any{"ok": false, "error": capitalize(err.Error())})
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	return strings.ToUpper(string(r[0])) + string(r[1:])
}

func readJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 64*1024))
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("pedido inválido")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}
