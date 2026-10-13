package main

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Regras pedidas (versão 1.3):
//   - verifica a lista a cada 0,5 s;
//   - só pega o 1º next, e só quando ALGUÉM ENTRA na hunt (o que já estava lá
//     quando você clicou em Iniciar não é pego);
//   - nunca envia com o respawn livre;
//   - janela de início (opcional): hora atual + tempo que falta tem que cair na janela;
//   - Plano A falhou (pegaram o next antes, ou o horário ficou fora da janela):
//     passa para o Plano B e não volta. Plano B falhou: para;
//   - espera de 1 s a 2 s (aleatório), confere de novo e envia;
//   - envia UMA ÚNICA VEZ e para.
const (
	PollInterval = 500 * time.Millisecond
	DelayMin     = 1000 * time.Millisecond
	DelayMax     = 2000 * time.Millisecond
	logLimit     = 300
)

type LogEntry struct {
	Time string `json:"time"`
	Text string `json:"text"`
	Kind string `json:"kind"` // info, ok, warn, err, bot
}

// MonitorView é o que a janela mostra sobre o monitoramento.
type MonitorView struct {
	Running      bool   `json:"running"`
	Phase        string `json:"phase"` // idle, watching, pending, sent, tested, stopped, error, waitingts, failed, ended
	Code         string `json:"code"`  // respawn vigiado agora
	Plan         string `json:"plan"`  // "A" ou "B"
	CodeA        string `json:"codeA"`
	CodeB        string `json:"codeB"`  // vazio = sem Plano B
	Window       string `json:"window"` // "20:00 – 22:30" (vazio = sem janela)
	Calc         string `json:"calc"`   // última conta de horário feita
	EndsAt       string `json:"endsAt"` // quando quem está caçando sai (hora atual + tempo que falta)
	TestMode     bool   `json:"testMode"`
	Status       string `json:"status"`
	Line         string `json:"line"`
	Present      bool   `json:"present"`
	Valid        bool   `json:"valid"`
	Count        int    `json:"count"`
	LastUpdate   string `json:"lastUpdate"`
	SecondsSince int    `json:"secondsSinceChange"` // desde a última mudança da lista
	PendingMs    int    `json:"pendingMs"`          // falta para enviar (se aguardando)
	StartedAt    string `json:"startedAt"`
	lastChange   time.Time
	lastDesc     string
	pendingAt    time.Time
	sending      bool // o comando está saindo agora (Parar não pode mais dizer "nada foi enviado")
	cancel       context.CancelFunc
	runID        int
}

type App struct {
	mu sync.Mutex // protege o estado abaixo
	op sync.Mutex // uma operação de cada vez na conexão com o TS

	store *Store
	set   Settings

	q           *Query
	apiKey      string
	connErr     string
	connecting  bool
	lastDial    time.Time
	autoBlocked bool // chave recusada: não tenta sozinho até o usuário agir
	autoFailing bool // já avisou que o TS não respondeu (não repete o aviso)
	autoWaiting bool // esperando o TS para conectar sozinho

	servers  []ServerInfo
	channels []ChannelInfo
	clients  []ClientInfo
	handler  int
	cid      int
	botUID   string

	mon   MonitorView
	runs  int
	log   []LogEntry
	notif chan Notify
	rng   *rand.Rand
}

func NewApp(store *Store) *App {
	a := &App{store: store, notif: make(chan Notify, 64), rng: rand.New(rand.NewSource(time.Now().UnixNano()))}
	a.set = store.Load()
	a.mon.Phase = "idle"
	a.mon.Code = a.set.Code
	go a.notifyLoop()
	return a
}

func (a *App) addLog(kind, format string, args ...any) {
	e := LogEntry{Time: now().Format("15:04:05"), Text: fmt.Sprintf(format, args...), Kind: kind}
	a.log = append(a.log, e)
	if len(a.log) > logLimit {
		a.log = a.log[len(a.log)-logLimit:]
	}
	fileLog("[%s] %s", kind, e.Text)
}

func (a *App) Log(kind, format string, args ...any) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.addLog(kind, format, args...)
}

// onNotify roda na leitura da conexão: nunca pode travar.
func (a *App) onNotify(n Notify) {
	select {
	case a.notif <- n:
	default:
	}
}

func (a *App) notifyLoop() {
	for n := range a.notif {
		private := n.Event == "notifytextmessage" && n.Data["targetmode"] == "1"
		poke := n.Event == "notifyclientpoke"
		if !private && !poke {
			continue
		}
		a.mu.Lock()
		if a.botUID != "" && n.Data["invokeruid"] == a.botUID {
			msg := strings.TrimSpace(VisibleText(n.Data["msg"]))
			verb := "respondeu"
			if poke {
				verb = "cutucou (poke)"
			}
			a.addLog("bot", "%s %s: %s", n.Data["invokername"], verb, msg)
		}
		a.mu.Unlock()
	}
}

// ---------- Conexão ----------

// Connect conecta com a chave informada (ou a salva, se key == ""). Pedido do usuário.
func (a *App) Connect(key string, port int, remember bool) error {
	return a.connect(key, port, remember, false)
}

// connect: quiet = tentativa automática (só avisa a primeira falha).
func (a *App) connect(key string, port int, remember, quiet bool) error {
	a.mu.Lock()
	if a.mon.Running {
		a.mu.Unlock()
		return errors.New("pare o monitoramento antes de trocar a conexão")
	}
	if a.connecting {
		a.mu.Unlock()
		return errors.New("já estou conectando, aguarde um instante")
	}
	if strings.TrimSpace(key) == "" {
		key = a.apiKey
	}
	if strings.TrimSpace(key) == "" {
		key = a.set.savedKey()
	}
	if strings.TrimSpace(key) == "" {
		if quiet {
			a.autoBlocked = true // não há chave que funcione: espera o usuário colar
		}
		a.mu.Unlock()
		return errors.New("cole a API Key do ClientQuery")
	}
	if port <= 0 || port > 65535 {
		port = 25639
	}
	a.connecting = true
	a.mu.Unlock()

	a.op.Lock()
	err := a.dialAndLoad(key, port)
	a.op.Unlock()

	a.mu.Lock()
	defer a.mu.Unlock()
	a.connecting = false
	if err != nil {
		a.connErr = err.Error()
		var ae *AuthError
		switch {
		case errors.As(err, &ae):
			a.autoBlocked, a.autoWaiting = true, false
			a.addLog("err", "Não conectou: %s", err.Error())
		case quiet:
			if !a.autoFailing {
				a.autoFailing = true
				a.addLog("warn", "Não consegui falar com o TeamSpeak agora (%s). Vou conectar sozinho assim que ele estiver aberto e no servidor.", err.Error())
			}
		default:
			a.addLog("err", "Não conectou: %s", err.Error())
		}
		return err
	}
	a.connErr = ""
	a.autoBlocked, a.autoFailing, a.autoWaiting = false, false, false
	a.apiKey = strings.TrimSpace(key)
	a.set.Port = port
	a.set.Remember = remember
	if remember {
		if err := a.set.setSavedKey(a.apiKey); err != nil {
			a.addLog("warn", "Conectou, mas não consegui guardar a chave neste PC: %s", err.Error())
		}
	} else {
		a.set.KeyProtected = ""
	}
	a.saveSettings()
	me := ""
	for _, s := range a.servers {
		if s.Handler == a.handler {
			me = s.MyName + " · " + s.Name
		}
	}
	a.addLog("ok", "Conectado ao TeamSpeak (%s).", me)
	return nil
}

// AutoLoop conecta sozinho quando o TeamSpeak estiver aberto (ao abrir o app
// ou depois de o TS fechar). Não age durante o monitoramento, que tem a sua
// própria reconexão, nem depois de a chave ser recusada.
func (a *App) AutoLoop(every time.Duration) {
	for {
		a.tryAuto()
		time.Sleep(every)
	}
}

func (a *App) tryAuto() {
	a.mu.Lock()
	connected := a.q != nil && a.q.Alive()
	hasKey := a.apiKey != "" || a.set.KeyProtected != ""
	a.autoWaiting = !connected && !a.mon.Running && !a.autoBlocked && hasKey
	go_ := a.autoWaiting && !a.connecting
	remember, port := a.set.KeyProtected != "", a.set.Port
	a.mu.Unlock()
	if go_ {
		_ = a.connect("", port, remember, true)
	}
}

// dialAndLoad: precisa de a.op travado.
func (a *App) dialAndLoad(key string, port int) error {
	a.mu.Lock()
	old := a.q
	a.q = nil
	a.mu.Unlock()
	if old != nil {
		old.Close()
	}
	q, err := DialQuery(port, key, a.onNotify)
	if err != nil {
		return err
	}
	servers, err := q.Servers()
	if err != nil {
		q.Close()
		return err
	}
	if len(servers) == 0 {
		q.Close()
		return errors.New("o TeamSpeak está aberto, mas não está conectado a nenhum servidor. Entre no servidor da guild e tente de novo")
	}
	a.mu.Lock()
	a.q = q
	a.servers = servers
	h := servers[0].Handler
	for _, s := range servers {
		if s.Name == a.set.ServerName {
			h = s.Handler
		}
	}
	a.handler = h
	a.mu.Unlock()
	return a.loadLists(q, h)
}

// loadLists carrega canais e pessoas do servidor escolhido. Precisa de a.op travado.
func (a *App) loadLists(q *Query, handler int) error {
	if err := q.Use(handler); err != nil {
		return err
	}
	chans, err := q.Channels()
	if err != nil {
		return err
	}
	clients, err := q.Clients()
	if err != nil {
		return err
	}
	sort.SliceStable(clients, func(i, j int) bool {
		bi, bj := looksLikeBot(clients[i].Name), looksLikeBot(clients[j].Name)
		if bi != bj {
			return bi
		}
		return strings.ToLower(clients[i].Name) < strings.ToLower(clients[j].Name)
	})
	best, hasList, err := a.pickChannel(q, chans)
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.handler = handler
	a.channels = chans
	a.clients = clients
	a.cid = best
	// Bot: o salvo (pela identidade); senão o x3tBot; senão o primeiro com "bot" no nome.
	a.botUID = ""
	for _, c := range clients {
		if a.set.BotUID != "" && c.UID == a.set.BotUID {
			a.botUID = c.UID
			break
		}
	}
	for _, want := range []string{"x3t", "bot"} {
		for _, c := range clients {
			if a.botUID == "" && strings.Contains(strings.ToLower(c.Name), want) {
				a.botUID = c.UID
			}
		}
	}
	if a.suggestPresets(chans, clients, hasList) {
		a.saveSettings()
	}
	return nil
}

// pickChannel escolhe o canal da lista de claims. Ordem de preferência:
//  1. o canal que o usuário escolheu de propósito (lembrado pelo nome);
//  2. um canal com a lista de verdade na descrição;
//  3. o nome ("Claimeds"). Canais de erro/regras/histórico perdem pontos.
//
// Devolve também quais canais conferidos têm lista. Precisa de a.op travado.
func (a *App) pickChannel(q *Query, chans []ChannelInfo) (int, map[int]bool, error) {
	a.mu.Lock()
	saved, manual := a.set.ChannelName, a.set.ManualChannel
	a.mu.Unlock()
	hasList := map[int]bool{}
	best, bestScore, checked := 0, -1<<30, 0
	for _, c := range chans {
		ln := lettersOnly(c.Name)
		isSaved := saved != "" && c.Name == saved
		if !strings.Contains(ln, "claim") && !isSaved {
			continue
		}
		sc := 0
		if ln == "claimeds" || ln == "claimed" {
			sc += 20
		}
		for _, bad := range []string{"test", "erro", "report", "regra", "history", "historico", "requerimento"} {
			if strings.Contains(ln, bad) {
				sc -= 50
			}
		}
		if isSaved {
			sc += 10
			if manual {
				sc += 1000
			}
		}
		if checked < 20 {
			checked++
			d, err := q.Description(c.Cid)
			if errors.Is(err, ErrDisconnected) {
				return 0, nil, err
			}
			if err == nil && HasList(d) {
				hasList[c.Cid] = true
				sc += 100
			}
		}
		if sc > bestScore {
			best, bestScore = c.Cid, sc
		}
	}
	return best, hasList, nil
}

// PeekView mostra, antes de iniciar, o que o app enxerga no canal escolhido.
type PeekView struct {
	Ok         bool   `json:"ok"`
	Error      string `json:"error,omitempty"`
	HasList    bool   `json:"hasList"`
	Lines      int    `json:"lines"`
	LastUpdate string `json:"lastUpdate"`
	Summary    string `json:"summary"`
	Line       string `json:"line"`
	SummaryB   string `json:"summaryB"`
}

// describe: "Agora: Kira no respawn (sai às 20:15), ninguém no next."
func describe(st QueueState, t time.Time) string {
	s := Summarize(st, 1)
	if st.Present && st.Valid && st.HasTimer {
		who := Hunter(st)
		s = strings.Replace(s, who+" no respawn", fmt.Sprintf("%s no respawn (falta %s, sai às %s)", who, FmtHM(st.Remaining), endsAt(st, t)), 1)
	}
	return s
}

func endsAt(st QueueState, t time.Time) string {
	return t.Add(time.Duration(st.Remaining) * time.Minute).Format("15:04")
}

func (a *App) Peek(cid int, code, codeB string) PeekView {
	a.mu.Lock()
	q, handler, running := a.q, a.handler, a.mon.Running
	a.mu.Unlock()
	if running {
		return PeekView{Error: "monitorando"}
	}
	if q == nil || !q.Alive() {
		return PeekView{Error: "Sem conexão com o TeamSpeak."}
	}
	a.op.Lock()
	defer a.op.Unlock()
	if err := q.Use(handler); err != nil {
		return PeekView{Error: "O TeamSpeak não respondeu."}
	}
	d, err := q.Description(cid)
	if err != nil {
		return PeekView{Error: "Não consegui ler este canal."}
	}
	pv := PeekView{Ok: true, HasList: HasList(d), Lines: ListSize(d), LastUpdate: LastUpdate(d)}
	t := now()
	if c, ok := NormalizeCode(code); ok && pv.HasList {
		st := ParseQueue(d, c)
		pv.Summary, pv.Line = describe(st, t), st.Line
	}
	if c, ok := NormalizeCode(codeB); ok && pv.HasList {
		pv.SummaryB = describe(ParseQueue(d, c), t)
	}
	return pv
}

func lettersOnly(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func looksLikeBot(name string) bool {
	n := strings.ToLower(name)
	return strings.Contains(n, "x3t") || strings.Contains(n, "bot")
}

// SelectServer troca o servidor (aba do TS) e recarrega canais e pessoas.
func (a *App) Refresh(handler int) error {
	a.mu.Lock()
	if a.mon.Running {
		a.mu.Unlock()
		return errors.New("pare o monitoramento antes de mudar isso")
	}
	q := a.q
	key, port := a.apiKey, a.set.Port
	a.mu.Unlock()
	a.op.Lock()
	defer a.op.Unlock()
	if q == nil || !q.Alive() {
		if key == "" {
			return errors.New("conecte ao TeamSpeak primeiro")
		}
		return a.dialAndLoad(key, port)
	}
	servers, err := q.Servers()
	if err != nil {
		return err
	}
	if len(servers) == 0 {
		return errors.New("o TeamSpeak não está conectado a nenhum servidor")
	}
	ok := false
	for _, s := range servers {
		ok = ok || s.Handler == handler
	}
	if !ok {
		handler = servers[0].Handler
	}
	a.mu.Lock()
	a.servers = servers
	a.mu.Unlock()
	return a.loadLists(q, handler)
}

// ---------- Monitoramento ----------

type StartParams struct {
	Handler     int    `json:"handler"`
	Cid         int    `json:"cid"`
	BotUID      string `json:"botUID"`
	Code        string `json:"code"`  // Plano A
	CodeB       string `json:"codeB"` // Plano B (opcional)
	WindowOn    bool   `json:"windowOn"`
	WindowStart string `json:"windowStart"`
	WindowEnd   string `json:"windowEnd"`
	TestMode    bool   `json:"testMode"`
}

type runParams struct {
	StartParams
	serverName, channelName, botName string
	win                              Window
	id                               int
}

func (a *App) Start(p StartParams) error {
	code, ok := NormalizeCode(p.Code)
	if !ok {
		return errors.New("digite um código de respawn válido para o Plano A, por exemplo C1")
	}
	codeB := ""
	if strings.TrimSpace(p.CodeB) != "" {
		if codeB, ok = NormalizeCode(p.CodeB); !ok {
			return errors.New("o código do Plano B está inválido (exemplo: D7). Para não usar Plano B, deixe \"Sem Plano B\"")
		}
		if codeB == code {
			return errors.New("o Plano B é o mesmo respawn do Plano A. Escolha outro ou deixe \"Sem Plano B\"")
		}
	}
	var win Window
	if p.WindowOn {
		w, err := ResolveWindow(p.WindowStart, p.WindowEnd, now())
		if err != nil {
			return err
		}
		win = w
	}
	if pv := a.Peek(p.Cid, code, ""); pv.Ok && !pv.HasList {
		return errors.New("o canal escolhido está sem a lista de respawns. Escolha o canal certo (ex.: Claimeds) e clique nele uma vez no TeamSpeak")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.mon.Running {
		return errors.New("o monitoramento já está ligado")
	}
	if a.q == nil || !a.q.Alive() {
		return errors.New("conecte ao TeamSpeak primeiro")
	}
	if p.Handler != a.handler {
		return errors.New("a lista de servidores mudou; clique em Atualizar")
	}
	rp := runParams{StartParams: p, win: win}
	rp.Code, rp.CodeB = code, codeB
	for _, s := range a.servers {
		if s.Handler == p.Handler {
			rp.serverName = s.Name
		}
	}
	for _, c := range a.channels {
		if c.Cid == p.Cid {
			rp.channelName = c.Name
		}
	}
	for _, c := range a.clients {
		if c.UID == p.BotUID && p.BotUID != "" {
			rp.botName = c.Name
		}
	}
	if rp.channelName == "" {
		return errors.New("escolha o canal da lista (Claimeds)")
	}
	if rp.botName == "" {
		return errors.New("escolha quem recebe o comando (ex.: x3tBot)")
	}
	a.cid, a.botUID = p.Cid, p.BotUID
	a.set.ServerName, a.set.ChannelName, a.set.BotUID, a.set.BotName = rp.serverName, rp.channelName, p.BotUID, rp.botName
	a.set.ManualChannel = true
	a.set.Code, a.set.CodeB = code, codeB
	a.set.WindowOn = p.WindowOn
	if p.WindowOn {
		a.set.WindowStart, a.set.WindowEnd = p.WindowStart, p.WindowEnd
	}
	a.saveSettings()

	a.runs++
	rp.id = a.runs
	ctx, cancel := context.WithCancel(context.Background())
	a.mon = MonitorView{Running: true, Phase: "watching", Code: code, Plan: "A", CodeA: code, CodeB: codeB, Window: win.Text,
		TestMode: p.TestMode, Status: "Lendo a lista...", StartedAt: now().Format("15:04:05"), cancel: cancel, runID: rp.id}
	extra := ""
	if codeB != "" {
		extra += " Plano B: " + codeB + "."
	}
	if win.On {
		extra += " Só pego se a hunt começar entre " + win.Text + "."
	}
	if p.TestMode {
		extra += " MODO TESTE: não vai enviar nada."
	}
	a.addLog("info", "Iniciado: Plano A %s, 1º next.%s", code, extra)
	go a.run(ctx, rp)
	return nil
}

func (a *App) Stop(reason string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.mon.Running {
		return
	}
	if a.mon.sending {
		// O comando já saiu (ou está saindo): o resultado do envio aparece em seguida.
		a.addLog("info", "O comando já estava sendo enviado quando você clicou em Parar; veja o resultado logo abaixo.")
		return
	}
	a.mon.cancel()
	a.mon.Running = false
	a.mon.Phase = "stopped"
	a.mon.PendingMs = 0
	a.mon.pendingAt = time.Time{}
	a.mon.Status = "Parado. Nada foi enviado."
	a.addLog("info", "%s Nada foi enviado.", reason)
}

// update altera a visão só se esta execução ainda for a atual.
func (a *App) update(id int, f func(m *MonitorView)) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.mon.Running || a.mon.runID != id {
		return false
	}
	f(&a.mon)
	return true
}

func (a *App) finish(id int, phase, status, kind, logText string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.mon.runID != id || !a.mon.Running {
		return
	}
	a.mon.cancel()
	a.mon.Running = false
	a.mon.sending = false
	a.mon.Phase = phase
	a.mon.Status = status
	a.mon.PendingMs = 0
	a.mon.pendingAt = time.Time{}
	a.addLog(kind, "%s", logText)
}

func (a *App) randomDelay() time.Duration {
	a.mu.Lock()
	defer a.mu.Unlock()
	return DelayMin + time.Duration(a.rng.Int63n(int64(DelayMax-DelayMin)+1))
}

// planState guarda o que o app já viu no respawn do plano atual.
type planState struct {
	code         string
	baseSet      bool   // já leu a situação inicial
	lastKey      string // quem estava caçando na última leitura com alguém (não esquece quando fica livre)
	lastRem      int
	lastHasTimer bool
}

// huntKey identifica "quem está na hunt": muda quando alguém novo entra.
func huntKey(st QueueState) string {
	if !st.Present {
		return ""
	}
	return st.Occupant
}

func (a *App) run(ctx context.Context, p runParams) {
	plans := []string{p.Code}
	if p.CodeB != "" {
		plans = append(plans, p.CodeB)
	}
	idx := 0
	ps := planState{code: plans[0]}
	letter := func() string { return string(rune('A' + idx)) }

	var timer *time.Timer
	var timerC <-chan time.Time
	var detectedAt time.Time
	entryKey, fireTries := "", 0
	stopTimer := func() {
		if timer != nil {
			timer.Stop()
		}
		timer, timerC, fireTries = nil, nil, 0
	}
	defer stopTimer()
	backToWatching := func() {
		a.update(p.id, func(m *MonitorView) { m.Phase = "watching"; m.PendingMs = 0; m.pendingAt = time.Time{} })
	}
	cancelPending := func(why string) {
		if timer == nil {
			return
		}
		stopTimer()
		backToWatching()
		a.Log("warn", "Envio cancelado: %s Continuo vigiando o Plano %s (%s).", why, letter(), ps.code)
	}
	// fail: o plano atual falhou. Passa para o B (sem voltar) ou encerra. true = acabou.
	fail := func(why string) bool {
		stopTimer()
		if idx+1 < len(plans) {
			old := ps.code
			idx++
			ps = planState{code: plans[idx]}
			a.update(p.id, func(m *MonitorView) {
				m.Phase, m.Plan, m.Code, m.PendingMs, m.pendingAt = "watching", letter(), ps.code, 0, time.Time{}
				m.Status = "Passando para o Plano B..."
				m.Line, m.Present, m.Valid, m.Count, m.EndsAt = "", false, false, 0, ""
			})
			a.Log("warn", "Plano A (%s) falhou: %s Passando para o Plano B (%s).", old, why, ps.code)
			return false
		}
		status := fmt.Sprintf("Plano %s (%s) falhou: %s Nada foi enviado.", letter(), ps.code, why)
		if len(plans) > 1 {
			status = fmt.Sprintf("Plano B (%s) também falhou: %s Nada foi enviado.", ps.code, why)
		}
		a.finish(p.id, "failed", status, "warn", status+" Monitoramento encerrado.")
		return true
	}
	waiting := func(st QueueState) string {
		t := now()
		switch {
		case !st.Present:
			return ps.code + " está livre. Esperando alguém entrar na hunt."
		case st.Count > 0:
			return describe(st, t) + " Esperando a próxima pessoa entrar na hunt."
		default:
			return describe(st, t) + " Esse eu não pego: espero a próxima pessoa entrar na hunt."
		}
	}

	// step trata uma leitura da lista para o plano atual. Devolve (acabou, reprocessar).
	step := func(desc string) (bool, bool) {
		st := ParseQueue(desc, ps.code)
		t := now()
		a.update(p.id, func(m *MonitorView) {
			m.Line, m.Present, m.Valid, m.Count = st.Line, st.Present, st.Valid, st.Count
			m.EndsAt = ""
			if st.Present && st.Valid && st.HasTimer {
				m.EndsAt = endsAt(st, t)
			}
		})
		readable := !st.Present || st.Valid
		k := huntKey(st)
		if !ps.baseSet {
			if !readable {
				a.update(p.id, func(m *MonitorView) { m.Status = st.Reason + ". Não envio nada assim." })
				return false, false
			}
			ps.baseSet, ps.lastKey, ps.lastRem, ps.lastHasTimer = true, k, st.Remaining, st.HasTimer
			if st.Present {
				a.Log("info", "Plano %s (%s): %s está na hunt agora; esse eu não pego. Espero a próxima pessoa entrar.", letter(), ps.code, Hunter(st))
			} else {
				a.Log("info", "Plano %s (%s): respawn livre agora. Espero alguém entrar na hunt.", letter(), ps.code)
			}
			a.update(p.id, func(m *MonitorView) { m.Status = waiting(st) })
			return false, false
		}
		if !readable {
			// Leitura duvidosa: não muda nada (um envio pendente é conferido na hora).
			a.update(p.id, func(m *MonitorView) { m.Status = st.Reason + ". Não envio nada assim." })
			return false, false
		}
		// Entrou alguém: outra pessoa na hunt, ou a mesma com o tempo pulando para cima (claim novo).
		// Se a linha sumir por um instante e voltar com a mesma pessoa, NÃO é entrada.
		entered := st.Present && (k != ps.lastKey || (st.HasTimer && ps.lastHasTimer && st.Remaining > ps.lastRem+5))
		if st.Present {
			ps.lastKey, ps.lastRem, ps.lastHasTimer = k, st.Remaining, st.HasTimer
		}
		if timer != nil {
			switch {
			case !st.Present:
				cancelPending("o respawn ficou livre antes do envio.")
			case entered:
				cancelPending("entrou outra pessoa na hunt antes do envio.")
			case st.Count > 0:
				if fail(st.First + " pegou o next antes de mim.") {
					return true, false
				}
				return false, true
			default:
				a.update(p.id, func(m *MonitorView) { m.Status = "Enviando em instantes..." })
				return false, false
			}
		}
		if !entered {
			a.update(p.id, func(m *MonitorView) { m.Status = waiting(st) })
			return false, false
		}
		// Alguém entrou na hunt.
		hunter := Hunter(st)
		if st.Count > 0 {
			if fail(hunter + " entrou na hunt, mas " + st.First + " já pegou o next antes de mim.") {
				return true, false
			}
			return false, true
		}
		calc := ""
		if st.HasTimer {
			calc = fmt.Sprintf("Agora %s + falta %s = sua hunt começaria às %s", t.Format("15:04"), FmtHM(st.Remaining), endsAt(st, t))
		}
		if p.win.On {
			if !st.HasTimer {
				a.Log("warn", "%s entrou em %s, mas não consegui ler o tempo que falta na linha. Sem ele não dá para conferir a janela, então não envio. Continuo vigiando.", hunter, ps.code)
				a.update(p.id, func(m *MonitorView) { m.Status = waiting(st) })
				return false, false
			}
			start := t.Add(time.Duration(st.Remaining) * time.Minute)
			if !p.win.Contains(start) {
				c := calc + " → fora da janela " + p.win.Text
				a.update(p.id, func(m *MonitorView) { m.Calc = c })
				if fail(fmt.Sprintf("%s entrou na hunt. %s.", hunter, c)) {
					return true, false
				}
				return false, true
			}
			calc += " → dentro da janela " + p.win.Text
		}
		d := a.randomDelay()
		detectedAt = time.Now()
		timer = time.NewTimer(d)
		timerC = timer.C
		fireTries = 0
		entryKey = k
		at := detectedAt.Add(d)
		a.update(p.id, func(m *MonitorView) {
			m.Phase, m.pendingAt, m.Calc = "pending", at, calc
			m.Status = "Disponível! Enviando em instantes..."
		})
		msg := fmt.Sprintf("%s entrou em %s (Plano %s)!", hunter, ps.code, letter())
		if calc != "" {
			msg += " " + calc + "."
		}
		a.Log("info", "%s Enviando o 1º next em %s s...", msg, secs(d))
		return false, false
	}

	ticker := time.NewTicker(PollInterval)
	defer ticker.Stop()
	for {
		desc, ok := a.poll(ctx, p)
		if ctx.Err() != nil {
			return
		}
		if p.win.On && timer == nil && p.win.Ended(now()) {
			st := fmt.Sprintf("Janela encerrada (%s). Nada foi enviado.", p.win.Text)
			a.finish(p.id, "ended", st, "info", st+" Monitoramento parado.")
			return
		}
		if ok {
			for again := true; again; {
				var done bool
				done, again = step(desc)
				if done {
					return
				}
			}
		} else if timer != nil && fireTries == 0 {
			// Sem leitura confiável agora: o envio só sai se a conferência final der certo.
			a.update(p.id, func(m *MonitorView) { m.Status = "Conferindo a lista..." })
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-timerC:
			timer, timerC = nil, nil
			// Confere de novo, com leitura nova, antes de enviar.
			desc, ok := a.poll(ctx, p)
			if ctx.Err() != nil {
				return
			}
			st := ParseQueue(desc, ps.code)
			if !ok || (st.Present && !st.Valid) {
				fireTries++
				if fireTries <= 6 {
					timer = time.NewTimer(PollInterval)
					timerC = timer.C
					continue
				}
				fireTries = 0
				backToWatching()
				a.Log("warn", "Envio cancelado: não consegui confirmar a lista na hora de enviar. Continuo vigiando o Plano %s (%s).", letter(), ps.code)
				continue
			}
			fireTries = 0
			switch {
			case !st.Present:
				backToWatching()
				a.Log("warn", "Envio cancelado: na conferência final o %s estava livre. Continuo vigiando.", ps.code)
				continue
			case huntKey(st) != entryKey:
				backToWatching() // a próxima leitura trata quem entrou
				a.Log("warn", "Envio cancelado: na conferência final já era outra pessoa na hunt do %s. Continuo vigiando.", ps.code)
				continue
			case st.Count > 0:
				if fail(st.First + " pegou o next antes de mim.") {
					return
				}
				continue
			}
			a.send(ctx, p, ps.code, time.Since(detectedAt))
			return
		}
	}
}

// poll lê a descrição do canal uma vez. ok=false quando não dá para confiar na leitura.
func (a *App) poll(ctx context.Context, p runParams) (string, bool) {
	a.op.Lock()
	defer a.op.Unlock()
	if ctx.Err() != nil {
		return "", false
	}
	a.mu.Lock()
	q, handler, cid := a.q, a.handler, a.cid // podem mudar após reconectar
	a.mu.Unlock()
	if q == nil || !q.Alive() {
		a.reconnect(p)
		return "", false
	}
	fail := func(status string) (string, bool) {
		a.update(p.id, func(m *MonitorView) { m.Phase = phaseKeep(m.Phase, "waitingts"); m.Status = status })
		return "", false
	}
	if err := q.Use(handler); err != nil {
		return fail("O TeamSpeak não respondeu. Tentando de novo...")
	}
	if err := q.Connected(); err != nil {
		if errors.Is(err, ErrDisconnected) {
			return fail("Conexão com o TeamSpeak perdida. Tentando reconectar...")
		}
		return fail("O TeamSpeak está desconectado do servidor. Esperando voltar (não envio nada assim).")
	}
	desc, err := q.Description(cid)
	if err != nil {
		return fail("Não consegui ler a lista do canal. Tentando de novo...")
	}
	if strings.TrimSpace(VisibleText(desc)) == "" {
		return fail("A lista está vazia aqui. No TeamSpeak, clique uma vez no canal " + p.channelName + ".")
	}
	lu := LastUpdate(desc)
	a.update(p.id, func(m *MonitorView) {
		if m.Phase == "waitingts" {
			m.Phase = "watching"
		}
		if desc != m.lastDesc {
			m.lastDesc = desc
			m.lastChange = time.Now()
		}
		m.LastUpdate = lu
	})
	return desc, true
}

func secs(d time.Duration) string {
	return strings.Replace(fmt.Sprintf("%.1f", d.Seconds()), ".", ",", 1)
}

func phaseKeep(cur, next string) string {
	if cur == "pending" {
		return cur
	}
	return next
}

// reconnect tenta voltar a falar com o TS a cada 3 s. Precisa de a.op travado.
func (a *App) reconnect(p runParams) {
	a.mu.Lock()
	if time.Since(a.lastDial) < 3*time.Second {
		a.mu.Unlock()
		return
	}
	a.lastDial = time.Now()
	key, port := a.apiKey, a.set.Port
	a.mu.Unlock()
	a.update(p.id, func(m *MonitorView) {
		m.Phase = "waitingts"
		m.Status = "Conexão com o TeamSpeak perdida. Tentando reconectar..."
	})
	q, err := DialQuery(port, key, a.onNotify)
	if err != nil {
		return
	}
	servers, err := q.Servers()
	if err != nil {
		q.Close()
		return
	}
	// Reencontra o mesmo servidor e canal pelos nomes (os números podem mudar).
	h := 0
	for _, s := range servers {
		if s.Name == p.serverName {
			h = s.Handler
		}
	}
	if h == 0 {
		q.Close()
		return
	}
	_ = q.Use(h)
	chans, err := q.Channels()
	if err != nil {
		q.Close()
		return
	}
	cid := 0
	for _, c := range chans {
		if c.Name == p.channelName {
			cid = c.Cid
		}
	}
	if cid == 0 {
		q.Close()
		return
	}
	a.mu.Lock()
	a.q, a.servers, a.handler, a.channels, a.cid = q, servers, h, chans, cid
	running := a.mon.Running && a.mon.runID == p.id
	a.mu.Unlock()
	if running {
		a.Log("ok", "Reconectado ao TeamSpeak.")
	}
}

func (a *App) send(ctx context.Context, p runParams, code string, waited time.Duration) {
	message := "!next " + code
	a.op.Lock()
	defer a.op.Unlock()
	if ctx.Err() != nil {
		return
	}
	a.mu.Lock()
	q, handler := a.q, a.handler
	a.mu.Unlock()
	if q == nil || !q.Alive() {
		a.finish(p.id, "error", "Falhou: conexão perdida na hora do envio. Nada foi enviado.", "err", "Conexão com o TeamSpeak perdida na hora do envio. Nada foi enviado.")
		return
	}
	// Garante a aba certa do TeamSpeak antes de procurar o bot e enviar.
	if err := q.Use(handler); err != nil {
		a.finish(p.id, "error", "Falhou: o TeamSpeak não respondeu na hora do envio. Nada foi enviado.", "err", "O TeamSpeak não respondeu na hora do envio. Nada foi enviado.")
		return
	}
	// Procura o bot pela identidade (o número dele muda se ele reconectar).
	clients, err := q.Clients()
	if err != nil {
		a.finish(p.id, "error", "Falhou: não consegui ver quem está no servidor. Nada foi enviado.", "err", "Não consegui listar o servidor na hora do envio. Nada foi enviado.")
		return
	}
	clid := 0
	for _, c := range clients {
		if c.UID == p.BotUID {
			clid = c.Clid
		}
	}
	if clid == 0 {
		a.finish(p.id, "error", "Falhou: "+p.botName+" não está no servidor agora. Nada foi enviado.", "err", p.botName+" não está no servidor agora. Nada foi enviado.")
		return
	}
	if p.TestMode {
		a.finish(p.id, "tested", fmt.Sprintf("Teste concluído: eu teria enviado \"%s\" para %s.", message, p.botName),
			"ok", fmt.Sprintf("MODO TESTE: teria enviado \"%s\" para %s (após %s s). Nada foi enviado.", message, p.botName, secs(waited)))
		return
	}
	// Último instante antes de enviar: se clicaram em Parar, não envia.
	a.mu.Lock()
	still := a.mon.Running && a.mon.runID == p.id && ctx.Err() == nil
	if still {
		a.mon.sending = true
	}
	a.mu.Unlock()
	if !still {
		return
	}
	err = q.SendPrivate(clid, message)
	switch {
	case err == nil:
		a.finish(p.id, "sent", fmt.Sprintf("Enviado: \"%s\" para %s às %s.", message, p.botName, now().Format("15:04:05")),
			"ok", fmt.Sprintf("ENVIADO \"%s\" para %s (%s s depois de a pessoa entrar na hunt). Monitoramento encerrado.", message, p.botName, secs(waited)))
	case errors.As(err, new(*QueryError)):
		a.finish(p.id, "error", "O TeamSpeak recusou o envio. Nada foi enviado.", "err", "O TeamSpeak recusou o envio: "+err.Error())
	default:
		a.finish(p.id, "error", "A conexão caiu durante o envio. Confira o privado de "+p.botName+".", "err",
			"A conexão caiu durante o envio; não sei se a mensagem saiu. Confira o privado de "+p.botName+" antes de tentar de novo.")
	}
}

// ---------- Estado para a janela ----------

type StateView struct {
	Connected   bool          `json:"connected"`
	Connecting  bool          `json:"connecting"`
	ConnErr     string        `json:"connErr"`
	HasSavedKey bool          `json:"hasSavedKey"`
	HasKey      bool          `json:"hasKey"` // há chave (salva ou desta sessão) para reconectar
	AutoWaiting bool          `json:"autoWaiting"`
	AutoBlocked bool          `json:"autoBlocked"`
	Remember    bool          `json:"remember"`
	Port        int           `json:"port"`
	Servers     []ServerInfo  `json:"servers"`
	Handler     int           `json:"handler"`
	Channels    []ChannelInfo `json:"channels"`
	Cid         int           `json:"cid"`
	Clients     []ClientInfo  `json:"clients"`
	BotUID      string        `json:"botUID"`
	Code        string        `json:"code"`
	CodeB       string        `json:"codeB"`
	WindowOn    bool          `json:"windowOn"`
	WindowStart string        `json:"windowStart"`
	WindowEnd   string        `json:"windowEnd"`
	Now         string        `json:"now"` // relógio usado (o do Windows)
	Monitor     MonitorView   `json:"monitor"`
	Log         []LogEntry    `json:"log"`
	Presets     []PresetView  `json:"presets"`
	Version     string        `json:"version"`
}

func (a *App) State() StateView {
	a.mu.Lock()
	defer a.mu.Unlock()
	m := a.mon
	if !m.lastChange.IsZero() {
		m.SecondsSince = int(time.Since(m.lastChange).Seconds())
	}
	if m.Running && !m.pendingAt.IsZero() {
		if ms := int(time.Until(m.pendingAt).Milliseconds()); ms > 0 {
			m.PendingMs = ms
		}
	}
	logs := make([]LogEntry, len(a.log))
	copy(logs, a.log)
	return StateView{
		Connected: a.q != nil && a.q.Alive() && !a.connecting, Connecting: a.connecting, ConnErr: a.connErr, // "conectado" só com as listas carregadas
		HasSavedKey: a.set.KeyProtected != "", HasKey: a.set.KeyProtected != "" || a.apiKey != "",
		AutoWaiting: a.autoWaiting && !a.autoBlocked && !(a.q != nil && a.q.Alive()), AutoBlocked: a.autoBlocked, Remember: a.set.Remember, Port: a.set.Port,
		Servers: a.servers, Handler: a.handler, Channels: a.channels, Cid: a.cid, Clients: a.clients, BotUID: a.botUID,
		Code: a.set.Code, CodeB: a.set.CodeB, WindowOn: a.set.WindowOn, WindowStart: a.set.WindowStart, WindowEnd: a.set.WindowEnd,
		Now:     now().Format("15:04:05"),
		Monitor: m, Log: logs, Presets: a.presetViews(), Version: Version,
	}
}

func (a *App) ForgetKey() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.set.KeyProtected = ""
	a.set.Remember = false
	a.saveSettings()
	a.addLog("info", "Chave apagada deste PC.")
}

// saveSettings: precisa de a.mu travado.
func (a *App) saveSettings() {
	if err := a.store.Save(a.set); err != nil {
		a.addLog("warn", "Não consegui salvar as preferências: %s", err.Error())
	}
}
