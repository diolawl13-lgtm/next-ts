package main

// Cliente mínimo do ClientQuery do TeamSpeak 3 (plugin que vem com o TS3).
// Conecta somente em 127.0.0.1. Uma conexão; comandos em sequência.

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

var ErrDisconnected = errors.New("a conexão com o TeamSpeak foi perdida")

const cmdTimeout = 4 * time.Second

var escPairs = []string{
	"\\", `\\`, "/", `\/`, " ", `\s`, "|", `\p`,
	"\a", `\a`, "\b", `\b`, "\f", `\f`, "\n", `\n`, "\r", `\r`, "\t", `\t`, "\v", `\v`,
}

var escaper = strings.NewReplacer(escPairs...)

// Escape aplica o escape do protocolo do TeamSpeak.
func Escape(s string) string { return escaper.Replace(s) }

// Unescape desfaz o escape do protocolo do TeamSpeak.
func Unescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' || i+1 >= len(s) {
			b.WriteByte(c)
			continue
		}
		i++
		switch s[i] {
		case '\\':
			b.WriteByte('\\')
		case '/':
			b.WriteByte('/')
		case 's':
			b.WriteByte(' ')
		case 'p':
			b.WriteByte('|')
		case 'a':
			b.WriteByte('\a')
		case 'b':
			b.WriteByte('\b')
		case 'f':
			b.WriteByte('\f')
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case 'v':
			b.WriteByte('\v')
		default:
			b.WriteByte('\\')
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// ParseRows lê "a=1 b=2|a=3 b=4" em uma lista de mapas.
func ParseRows(line string) []map[string]string {
	var rows []map[string]string
	for _, part := range strings.Split(line, "|") {
		row := map[string]string{}
		for _, tok := range strings.Split(part, " ") {
			if tok == "" {
				continue
			}
			k, v, found := strings.Cut(tok, "=")
			if found {
				row[k] = Unescape(v)
			} else {
				row[k] = ""
			}
		}
		if len(row) > 0 {
			rows = append(rows, row)
		}
	}
	return rows
}

// QueryError é um erro devolvido pelo TeamSpeak ("error id=... msg=...").
type QueryError struct {
	ID  int
	Msg string
}

func (e *QueryError) Error() string {
	return fmt.Sprintf("TeamSpeak respondeu: %s (código %d)", e.Msg, e.ID)
}

// AuthError: o TeamSpeak recusou a API Key (não adianta tentar de novo sozinho).
type AuthError struct{ Msg string }

func (e *AuthError) Error() string {
	return "o TeamSpeak recusou a API Key. Copie de novo a chave nas configurações do ClientQuery (" + e.Msg + ")"
}

// Notify é uma notificação assíncrona (ex.: mensagem de texto recebida).
type Notify struct {
	Event string
	Data  map[string]string
}

type Query struct {
	conn     net.Conn
	cmdMu    sync.Mutex
	lines    chan string
	done     chan struct{}
	closeErr sync.Once
	OnNotify func(Notify)
}

// DialQuery conecta e autentica com a API Key.
func DialQuery(port int, apiKey string, onNotify func(Notify)) (*Query, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, errors.New("cole a API Key do ClientQuery")
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 3*time.Second)
	if err != nil {
		return nil, fmt.Errorf("não consegui falar com o TeamSpeak na porta %d. O TeamSpeak 3 está aberto e o plugin ClientQuery está ativado?", port)
	}
	q := &Query{conn: conn, lines: make(chan string, 256), done: make(chan struct{}), OnNotify: onNotify}
	go q.readLoop()

	// Banner: "TS3 Client", "Welcome to the TeamSpeak 3 ClientQuery...", ..., "selected schandlerid=N"
	first, err := q.nextLine(time.Now().Add(3 * time.Second))
	if err != nil || first != "TS3 Client" {
		q.Close()
		return nil, fmt.Errorf("a porta %d respondeu, mas não é o ClientQuery do TeamSpeak 3", port)
	}
	deadline := time.Now().Add(1500 * time.Millisecond)
	for {
		l, err := q.nextLine(deadline)
		if err != nil || strings.HasPrefix(l, "selected ") {
			break // banner terminou (ou não veio a última linha; tudo bem)
		}
	}
	if _, err := q.Command("auth apikey=" + Escape(apiKey)); err != nil {
		q.Close()
		var qe *QueryError
		if errors.As(err, &qe) {
			return nil, &AuthError{Msg: qe.Msg}
		}
		return nil, err
	}
	// Respostas do x3tBot no privado (só para mostrar na janela).
	_, _ = q.Command("clientnotifyregister schandlerid=0 event=notifytextmessage")
	_, _ = q.Command("clientnotifyregister schandlerid=0 event=notifyclientpoke")
	return q, nil
}

func (q *Query) readLoop() {
	defer close(q.done)
	r := bufio.NewReaderSize(q.conn, 64*1024)
	for {
		raw, err := r.ReadString('\n')
		line := strings.Trim(raw, "\r\n")
		if line != "" {
			if strings.HasPrefix(line, "notify") {
				ev, payload, _ := strings.Cut(line, " ")
				if q.OnNotify != nil {
					for _, row := range ParseRows(payload) {
						q.OnNotify(Notify{Event: ev, Data: row})
					}
				}
			} else {
				select {
				case q.lines <- line:
				default: // ninguém lendo e fila cheia: descarta
				}
			}
		}
		if err != nil {
			return
		}
	}
}

func (q *Query) nextLine(deadline time.Time) (string, error) {
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case l := <-q.lines:
		return l, nil
	case <-q.done:
		// Ainda pode haver linhas na fila.
		select {
		case l := <-q.lines:
			return l, nil
		default:
		}
		return "", ErrDisconnected
	case <-timer.C:
		return "", errors.New("o TeamSpeak não respondeu a tempo")
	}
}

// Alive informa se a conexão ainda está aberta.
func (q *Query) Alive() bool {
	select {
	case <-q.done:
		return false
	default:
		return true
	}
}

func (q *Query) Close() {
	q.closeErr.Do(func() { q.conn.Close() })
}

// Command envia um comando já montado e devolve as linhas de dados.
func (q *Query) Command(cmd string) ([]map[string]string, error) {
	q.cmdMu.Lock()
	defer q.cmdMu.Unlock()
	if !q.Alive() {
		return nil, ErrDisconnected
	}
	// Linhas soltas de antes (não deveriam existir) seriam confundidas com a resposta.
	for drained := false; !drained; {
		select {
		case <-q.lines:
		default:
			drained = true
		}
	}
	q.conn.SetWriteDeadline(time.Now().Add(cmdTimeout))
	if _, err := q.conn.Write([]byte(cmd + "\n")); err != nil {
		q.Close()
		return nil, ErrDisconnected
	}
	deadline := time.Now().Add(cmdTimeout)
	var rows []map[string]string
	for {
		l, err := q.nextLine(deadline)
		if err != nil {
			// Sem resposta: a sequência de respostas ficou incerta. Não reutilizar.
			q.Close()
			if errors.Is(err, ErrDisconnected) {
				return nil, ErrDisconnected
			}
			return nil, err
		}
		if strings.HasPrefix(l, "error ") {
			st := ParseRows(strings.TrimPrefix(l, "error "))
			if len(st) == 0 {
				return nil, errors.New("resposta inválida do TeamSpeak")
			}
			id, _ := strconv.Atoi(st[0]["id"])
			if id != 0 {
				return nil, &QueryError{ID: id, Msg: st[0]["msg"]}
			}
			return rows, nil
		}
		rows = append(rows, ParseRows(l)...)
	}
}

// ---- Comandos de alto nível ----

type ServerInfo struct {
	Handler int    `json:"handler"`
	Name    string `json:"name"`
	MyName  string `json:"myName"`
	MyClid  int    `json:"-"`
}

type ChannelInfo struct {
	Cid  int    `json:"cid"`
	Name string `json:"name"`
}

type ClientInfo struct {
	Clid int    `json:"clid"`
	Name string `json:"name"`
	UID  string `json:"uid"`
}

func (q *Query) Use(handler int) error {
	_, err := q.Command("use schandlerid=" + strconv.Itoa(handler))
	return err
}

// Servers lista as abas do TS conectadas a algum servidor.
func (q *Query) Servers() ([]ServerInfo, error) {
	rows, err := q.Command("serverconnectionhandlerlist")
	if err != nil {
		return nil, err
	}
	var out []ServerInfo
	for _, r := range rows {
		h, err := strconv.Atoi(r["schandlerid"])
		if err != nil || h <= 0 {
			continue
		}
		if err := q.Use(h); err != nil {
			if errors.Is(err, ErrDisconnected) {
				return nil, err
			}
			continue
		}
		who, err := q.Command("whoami")
		if err != nil || len(who) == 0 {
			if errors.Is(err, ErrDisconnected) {
				return nil, err
			}
			continue // aba sem servidor conectado
		}
		clid, _ := strconv.Atoi(who[0]["clid"])
		if clid <= 0 {
			continue
		}
		info := ServerInfo{Handler: h, MyClid: clid}
		if sv, err := q.Command("servervariable virtualserver_name"); err == nil && len(sv) > 0 {
			info.Name = sv[0]["virtualserver_name"]
		}
		if info.Name == "" {
			info.Name = "Servidor " + strconv.Itoa(h)
		}
		if me, err := q.Command("clientvariable clid=" + strconv.Itoa(clid) + " client_nickname"); err == nil && len(me) > 0 {
			info.MyName = me[0]["client_nickname"]
		}
		out = append(out, info)
	}
	return out, nil
}

// Connected confere se a aba selecionada continua ligada ao servidor.
func (q *Query) Connected() error {
	who, err := q.Command("whoami")
	if err != nil {
		return err
	}
	if len(who) == 0 {
		return errors.New("aba do TeamSpeak sem servidor")
	}
	if c, _ := strconv.Atoi(who[0]["clid"]); c <= 0 {
		return errors.New("aba do TeamSpeak sem servidor")
	}
	return nil
}

func (q *Query) Channels() ([]ChannelInfo, error) {
	rows, err := q.Command("channellist")
	if err != nil {
		return nil, err
	}
	var out []ChannelInfo
	for _, r := range rows {
		cid, err := strconv.Atoi(r["cid"])
		if err != nil || cid <= 0 {
			continue
		}
		out = append(out, ChannelInfo{Cid: cid, Name: r["channel_name"]})
	}
	return out, nil
}

func (q *Query) Clients() ([]ClientInfo, error) {
	rows, err := q.Command("clientlist -uid")
	if err != nil {
		return nil, err
	}
	var out []ClientInfo
	for _, r := range rows {
		clid, err := strconv.Atoi(r["clid"])
		if err != nil || clid <= 0 {
			continue
		}
		out = append(out, ClientInfo{Clid: clid, Name: r["client_nickname"], UID: r["client_unique_identifier"]})
	}
	return out, nil
}

// Description lê a descrição do canal (a lista de claimeds).
func (q *Query) Description(cid int) (string, error) {
	rows, err := q.Command("channelvariable cid=" + strconv.Itoa(cid) + " channel_description")
	if err != nil {
		return "", err
	}
	for _, r := range rows {
		if r["cid"] == strconv.Itoa(cid) {
			return r["channel_description"], nil
		}
	}
	if len(rows) == 1 {
		return rows[0]["channel_description"], nil
	}
	return "", errors.New("o TeamSpeak não devolveu a descrição do canal")
}

// SendPrivate manda uma mensagem privada para o cliente clid.
func (q *Query) SendPrivate(clid int, msg string) error {
	_, err := q.Command("sendtextmessage targetmode=1 target=" + strconv.Itoa(clid) + " msg=" + Escape(msg))
	return err
}
