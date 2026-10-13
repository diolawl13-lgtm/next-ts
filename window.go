package main

// Janela de início da hunt: só pega o next se a hunt for começar dentro dela.
//
// A hora de início é calculada assim: hora atual (relógio do Windows) + o tempo
// que falta para quem está caçando sair, ex.: 18:30 + [01:45] = 20:15.
// A conta é feita minuto a minuto e as pontas contam como dentro (20:00 e 22:30).

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"time"
)

var reHM = regexp.MustCompile(`^\s*([01]?\d|2[0-3])\s*[:hH.]\s*([0-5]\d)\s*$`)

// ParseHM lê "20:00" (também aceita "20h00" e "20.00") e devolve minutos do dia.
func ParseHM(s string) (int, bool) {
	m := reHM.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	h, _ := strconv.Atoi(m[1])
	mi, _ := strconv.Atoi(m[2])
	return h*60 + mi, true
}

// Window é a janela já posicionada no calendário (início e fim exatos, ao minuto).
type Window struct {
	On         bool
	Start, End time.Time // fim incluído: vale até HH:MM:59
	Text       string    // "20:00 – 22:30"
}

// ResolveWindow acha a janela em vigor a partir de `now`: a de ontem (se passou da
// meia-noite e ainda não acabou) ou a de hoje. Se a de hoje já acabou, dá erro.
func ResolveWindow(startHM, endHM string, now time.Time) (Window, error) {
	s, ok1 := ParseHM(startHM)
	e, ok2 := ParseHM(endHM)
	if !ok1 || !ok2 {
		return Window{}, errors.New("horário da janela inválido. Use o formato 20:00")
	}
	text := FmtHM(s) + " – " + FmtHM(e)
	nowMin := now.Truncate(time.Minute)
	for _, day := range []int{-1, 0} {
		base := time.Date(now.Year(), now.Month(), now.Day()+day, 0, 0, 0, 0, now.Location())
		ws := base.Add(time.Duration(s) * time.Minute)
		we := base.Add(time.Duration(e) * time.Minute)
		if e < s {
			we = we.AddDate(0, 0, 1) // passa da meia-noite
		}
		if !we.Before(nowMin) {
			return Window{On: true, Start: ws, End: we, Text: text}, nil
		}
	}
	return Window{}, fmt.Errorf("a janela %s de hoje já acabou. Ajuste o horário ou desligue a janela", text)
}

// Contains: a hora `t` (ao minuto) está dentro da janela.
func (w Window) Contains(t time.Time) bool {
	tm := t.Truncate(time.Minute)
	return !tm.Before(w.Start) && !tm.After(w.End)
}

// Ended: já passou do último minuto da janela (nenhuma hunt consegue mais começar nela).
func (w Window) Ended(now time.Time) bool {
	return now.Truncate(time.Minute).After(w.End)
}

// ---------- Relógio ----------

// clockOffset só é usado nos testes automáticos (NEXTTS_TEST_CLOCK=HH:MM:SS),
// para simular outro horário. No uso normal é zero: vale o relógio do Windows.
var clockOffset time.Duration

func initClock() {
	v := os.Getenv("NEXTTS_TEST_CLOCK")
	if v == "" {
		return
	}
	t, err := time.ParseInLocation("15:04:05", v, time.Local)
	if err != nil {
		return
	}
	n := time.Now()
	want := time.Date(n.Year(), n.Month(), n.Day(), t.Hour(), t.Minute(), t.Second(), 0, time.Local)
	clockOffset = want.Sub(n)
}

func now() time.Time { return time.Now().Add(clockOffset) }
