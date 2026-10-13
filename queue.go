package main

// Leitura da lista do canal Claimeds publicada pelo x3tBot.
//
// Formato observado (texto visível no TeamSpeak):
//   c1 - [01:30] Livraria de Fire (Tier 3): Kira Zinha | Next: Real Calvon +1
//   a1 - [00:22] Cobra Castelo (Tier 1): Jairo Slayer | Next: Lord Tenchi
//   d7 - [00:06] DT -1 (Tier 1): Elite'Nitrox
//
// Respawn livre = o código não aparece na lista. Nesse caso NUNCA se envia nada.
// Qualquer linha que não seja entendida com segurança também nunca libera envio.

import (
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
)

var (
	reImg        = regexp.MustCompile(`(?is)\[img(?:=[^\]]*)?\].*?\[/img\]`)
	reBlockTags  = regexp.MustCompile(`(?i)\[/?(?:table|tr|th|center|left|right|list|hr|quote|spoiler|code)(?:=[^\]]*)?\]|\[\*\]|\[br\s*/?\]|<br\s*/?>`)
	reCellTags   = regexp.MustCompile(`(?i)\[/?td(?:=[^\]]*)?\]`)
	reInlineTags = regexp.MustCompile(`(?i)\[/?(?:b|i|u|s|color|size|font|url|time)(?:=[^\]]*)?\]`)
	reNextMarker = regexp.MustCompile(`(?i)\bNext\s*:`)
	reNextWord   = regexp.MustCompile(`(?i)\bNext\b`)
	reExtra      = regexp.MustCompile(`\s*\+\s*([1-9][0-9]{0,2})\s*$`)
	reTimer      = regexp.MustCompile(`^\[(\d{1,3}):([0-5]\d)\]\s*`)
	reLastUpdate = regexp.MustCompile(`(?i)last\s*update\s*:\s*(\d{4}-\d{2}-\d{2}\s+\d{1,2}:\d{2}(?::\d{2})?)`)
	reCode       = regexp.MustCompile(`^[A-Z]{1,3}[0-9]{1,3}$`)
	reAnyLine    = regexp.MustCompile(`(?i)^\s*(?:[•*·]\s*)?[a-z]{1,3}[0-9]{1,3}\s*[-–—]\s*\S`)
)

// VisibleText remove BBCode/HTML e devolve o texto como aparece na tela.
func VisibleText(desc string) string {
	t := reImg.ReplaceAllString(desc, "")
	t = reBlockTags.ReplaceAllString(t, "\n")
	t = reCellTags.ReplaceAllString(t, "\t")
	t = reInlineTags.ReplaceAllString(t, "")
	t = html.UnescapeString(t)
	t = strings.NewReplacer(
		"\r\n", "\n", "\r", "\n",
		"\u00a0", " ", "\u2007", " ", "\u202f", " ",
		"\u200b", "", "\u200c", "", "\u200d", "", "\u2060", "", "\ufeff", "",
		"\u200e", "", "\u200f", "",
	).Replace(t)
	return t
}

// NormalizeCode deixa o código em maiúsculas e confere o formato (ex.: C1, AB2, D15).
func NormalizeCode(code string) (string, bool) {
	c := strings.ToUpper(strings.TrimSpace(code))
	return c, reCode.MatchString(c)
}

// QueueState descreve a situação de um respawn na lista.
type QueueState struct {
	Code      string `json:"code"`
	Present   bool   `json:"present"`   // código aparece na lista (alguém está no respawn)
	Valid     bool   `json:"valid"`     // linha entendida com segurança
	Count     int    `json:"count"`     // pessoas no next (0, 1, 2...)
	Occupant  string `json:"occupant"`  // quem está no respawn (com nome do respawn, sem o tempo)
	First     string `json:"first"`     // primeiro da fila de next
	Line      string `json:"line"`      // linha completa como aparece
	Reason    string `json:"reason"`    // por que não é válida
	HasTimer  bool   `json:"hasTimer"`  // a linha mostra o tempo que falta, ex.: [01:45]
	Remaining int    `json:"remaining"` // minutos que faltam para quem está caçando sair
}

func lineRegexp(code string) *regexp.Regexp {
	// Começa com o código, seguido de hífen/traço/dois-pontos. "c1" não casa "c10" nem "ac1".
	return regexp.MustCompile(`(?i)^\s*(?:[•*·]\s*)?` + regexp.QuoteMeta(code) + `\s*[-–—:]\s*(.*)$`)
}

// reTag: etiqueta de tempo depois do nome do next, ex.: "[Espera ~01:39]" ou "[Fim em ~03:14]".
var reTag = regexp.MustCompile(`\s*\[[^\[\]]*\]\s*$`)

// cleanNext limpa o nome de uma vaga do next. ok=false se o nome não for confiável.
func cleanNext(seg string) (string, bool) {
	seg = strings.TrimSpace(seg)
	for i := 0; i < 3 && reTag.MatchString(seg); i++ {
		seg = strings.TrimSpace(reTag.ReplaceAllString(seg, ""))
	}
	if seg == "" || seg == "-" || seg == "—" || seg == "?" || strings.Contains(seg, "|") || strings.Contains(seg, "[") {
		return "", false
	}
	return seg, true
}

// ParseQueue lê a situação do respawn `code` na descrição.
//
// Aceita os dois formatos do x3tBot:
//
//	antigo: "c1 - [01:30] Livraria de Fire (Tier 3): Kira | Next: Calvon +1"          (+1 = mais uma pessoa)
//	novo:   "R9 - [01:39] Upper Roshamuul (Tier 1): Nowlolah | Next: Shawn [Espera ~01:39] | Next: Billy [Espera ~04:09]"
//
// Cada "Next:" é uma pessoa; a "/" junta nomes da mesma vaga e não soma.
func ParseQueue(desc, code string) QueueState {
	st := QueueState{Code: code, Reason: "Respawn livre (não aparece na lista)"}
	re := lineRegexp(code)
	var bodies, lines []string
	for _, ln := range strings.Split(VisibleText(desc), "\n") {
		if m := re.FindStringSubmatch(ln); m != nil {
			bodies = append(bodies, strings.TrimSpace(m[1]))
			lines = append(lines, strings.TrimSpace(ln))
		}
	}
	if len(bodies) == 0 {
		return st
	}
	st.Present = true
	if len(bodies) > 1 {
		st.Line = lines[0]
		st.Reason = "O código aparece mais de uma vez na lista; aguardando leitura clara"
		return st
	}
	body := bodies[0]
	st.Line = lines[0]
	st.Reason = "Formato da linha não reconhecido"
	if body == "" {
		return st
	}
	markers := reNextMarker.FindAllStringIndex(body, -1)
	occupant, first, count := "", "", 0
	if len(markers) == 0 {
		if reNextWord.MatchString(body) {
			return st
		}
		occupant = body
	} else {
		occupant = strings.TrimRight(strings.TrimSpace(body[:markers[0][0]]), " |\t")
		if occupant == "" {
			return st
		}
		for i, mk := range markers {
			end := len(body)
			last := i == len(markers)-1
			if !last {
				end = markers[i+1][0]
			}
			seg := strings.TrimSpace(body[mk[1]:end])
			if !last {
				// Entre um "Next:" e outro precisa ter a barra "|" separando.
				if !strings.HasSuffix(seg, "|") {
					return st
				}
				seg = strings.TrimSpace(strings.TrimSuffix(seg, "|"))
			}
			extra := 0
			if last {
				if m := reExtra.FindStringSubmatchIndex(seg); m != nil {
					extra, _ = strconv.Atoi(seg[m[2]:m[3]])
					seg = strings.TrimSpace(seg[:m[0]])
				}
			}
			name, ok := cleanNext(seg)
			if !ok || strings.Contains(name, "+") {
				return st
			}
			if i == 0 {
				first = name
			}
			count += 1 + extra
		}
	}
	if m := reTimer.FindStringSubmatchIndex(occupant); m != nil {
		h, _ := strconv.Atoi(occupant[m[2]:m[3]])
		mi, _ := strconv.Atoi(occupant[m[4]:m[5]])
		st.HasTimer, st.Remaining = true, h*60+mi
		occupant = strings.TrimSpace(occupant[m[1]:])
	}
	if occupant == "" {
		return st
	}
	st.Valid, st.Count, st.First, st.Occupant, st.Reason = true, count, first, occupant, ""
	return st
}

// Hunter devolve só os nomes de quem está caçando (sem o nome do respawn).
func Hunter(st QueueState) string {
	who := st.Occupant
	if i := strings.LastIndex(who, ": "); i >= 0 {
		who = strings.TrimSpace(who[i+2:])
	}
	return who
}

// FmtHM escreve minutos como "01:45".
func FmtHM(min int) string {
	return fmt.Sprintf("%02d:%02d", min/60, min%60)
}

// Eligible: a posição escolhida está disponível agora.
// 1º next = alguém no respawn e fila vazia. 2º next = exatamente uma pessoa na fila.
// Respawn livre (ausente) nunca é elegível.
func Eligible(st QueueState, position int) bool {
	return st.Present && st.Valid && st.Count == position-1
}

// LastUpdate devolve o horário "Last update" que o x3tBot escreve na lista, se houver.
func LastUpdate(desc string) string {
	if m := reLastUpdate.FindStringSubmatch(VisibleText(desc)); m != nil {
		return m[1]
	}
	return ""
}

// ListSize conta quantas linhas de respawn a lista tem (0 = canal sem lista).
func ListSize(desc string) int {
	n := 0
	for _, ln := range strings.Split(VisibleText(desc), "\n") {
		if reAnyLine.MatchString(ln) {
			n++
		}
	}
	return n
}

// Summarize descreve a situação do respawn em uma linha curta, para a janela.
func Summarize(st QueueState, position int) string {
	if !st.Present {
		return "Agora: livre (não está na lista)."
	}
	if !st.Valid {
		return "Agora: " + st.Reason + "."
	}
	fila := "ninguém no next"
	switch {
	case st.Count == 1:
		fila = "1 no next (" + st.First + ")"
	case st.Count > 1:
		fila = strconv.Itoa(st.Count) + " no next"
	}
	return "Agora: " + Hunter(st) + " no respawn, " + fila + "."
}

// HasList: a descrição parece a lista do x3tBot (tem "Last update" ou linhas de respawn).
func HasList(desc string) bool { return LastUpdate(desc) != "" || ListSize(desc) > 0 }
