package main

import (
	"testing"
	"time"
)

// Texto igual ao das capturas de tela do canal Claimeds.
const sampleVisible = `Last update: 2026-09-28 18:02:13 America/Sao_Paulo

a1 - [00:22] Cobra Castelo (Tier 1): Jairo Slayer | Next: Lord Tenchi
c1 - [01:30] Livraria de Fire (Tier 3): Kira Zinha | Next: Real Calvon +1
D2 - [01:51] Plague -1 (Tier 1): Guardian'Henry
d7 - [00:06] DT -1 (Tier 1): Elite'Nitrox
d9 - [00:52] Pumin -1,-2 (Tier 1): Jamaica Tank | Next: White Wolff
d15 - [00:35] Caminho Ferumbras (Tier 1): Guiixd | Next: Goutheer CEL
v7 - [00:07] Bulltaur Lair -2(Tier 1): Krunnit | Next: Red Vesgo +1
y11 - [01:20] Norcferatu Catacombs Dir(Tier 1): Kubzkoutz`

// Mesma lista com BBCode, como o TeamSpeak entrega pela ClientQuery.
const sampleBB = `[center][img]https://x3tbot.com/claimed.png[/img][/center]
[i][size=8]Last update: 2026-09-28 18:02:13 America/Sao_Paulo[/size][/i]
[b]a1 - [color=#c8a000][00:22][/color] Cobra Castelo (Tier 1): [url=client://0/abc=~Jairo]Jairo Slayer[/url][/b] | Next: [b][color=#1f4e9b]Lord Tenchi[/color][/b]
[b]c1 - [color=#c8a000][01:30][/color] Livraria de Fire (Tier 3): [color=blue]Kira Zinha[/color][/b] | Next: [b]Real Calvon[/b] +1
[b]D2 - [color=#c8a000][01:51][/color] Plague -1 (Tier 1): Guardian&#39;Henry[/b]
[b]c10 - [00:10] Teste: Fulano[/b]
[b]d7 - [00:06] DT -1 (Tier 1): Elite'Nitrox[/b]`

func TestParseSample(t *testing.T) {
	cases := []struct {
		desc, code string
		present    bool
		valid      bool
		count      int
		first      string
	}{
		{sampleVisible, "A1", true, true, 1, "Lord Tenchi"},
		{sampleVisible, "C1", true, true, 2, "Real Calvon"},
		{sampleVisible, "D2", true, true, 0, ""},
		{sampleVisible, "D7", true, true, 0, ""},
		{sampleVisible, "D15", true, true, 1, "Goutheer CEL"},
		{sampleVisible, "D1", false, false, 0, ""}, // não confundir com D15
		{sampleVisible, "C2", false, false, 0, ""},
		{sampleVisible, "Y1", false, false, 0, ""}, // não confundir com Y11
		{sampleVisible, "Y11", true, true, 0, ""},
		{sampleBB, "A1", true, true, 1, "Lord Tenchi"},
		{sampleBB, "C1", true, true, 2, "Real Calvon"},
		{sampleBB, "D2", true, true, 0, ""},
		{sampleBB, "C10", true, true, 0, ""},
		{sampleBB, "D7", true, true, 0, ""},
	}
	for _, c := range cases {
		st := ParseQueue(c.desc, c.code)
		if st.Present != c.present || st.Valid != c.valid || st.Count != c.count || st.First != c.first {
			t.Errorf("%s: got %+v", c.code, st)
		}
	}
	st := ParseQueue(sampleBB, "D2")
	if st.Occupant != "Plague -1 (Tier 1): Guardian'Henry" {
		t.Errorf("occupant: %q", st.Occupant)
	}
	if lu := LastUpdate(sampleBB); lu != "2026-09-28 18:02:13" {
		t.Errorf("last update: %q", lu)
	}
}

func TestEligible(t *testing.T) {
	free := ParseQueue(sampleVisible, "C2")
	zero := ParseQueue(sampleVisible, "D7")
	one := ParseQueue(sampleVisible, "A1")
	two := ParseQueue(sampleVisible, "C1")
	for _, pos := range []int{1, 2} {
		if Eligible(free, pos) {
			t.Fatalf("respawn livre nunca pode ser elegível (pos %d)", pos)
		}
	}
	if !Eligible(zero, 1) || Eligible(one, 1) || Eligible(two, 1) {
		t.Fatal("regra do 1º next errada")
	}
	if Eligible(zero, 2) || !Eligible(one, 2) || Eligible(two, 2) {
		t.Fatal("regra do 2º next errada")
	}
}

func TestUnsafeLinesNeverEligible(t *testing.T) {
	bad := []string{
		"c1 - ",                                   // vazia
		"c1 - [01:00] Livraria: Fulano | Next:",   // next vazio
		"c1 - [01:00] Livraria: Fulano | Next: -", // next indefinido
		"c1 - [01:00] Livraria: Fulano | Next: A | Next: B",
		"c1 - [01:00] Livraria: Fulano Next B",          // palavra Next sem dois-pontos
		"c1 - [01:00]",                                  // sem ocupante
		"c1 - [01:00] X: Fulano\nc1 - [00:10] X: Outro", // duplicada
		"c1 - Next: Fulano",                             // sem ocupante antes do Next
	}
	for _, d := range bad {
		st := ParseQueue(d, "C1")
		if Eligible(st, 1) || Eligible(st, 2) {
			t.Errorf("linha insegura liberou envio: %q -> %+v", d, st)
		}
	}
}

func TestOtherFormats(t *testing.T) {
	// Traço longo, espaços extras, maiúsculas, CRLF, espaço não separável.
	d := "Last update: 2026-10-06 9:05\r\n  C1 — [00:59]\u00a0Livraria de Fire (Tier 3): Kira | Next: Lord Calvon + 2\r\n"
	st := ParseQueue(d, "c1")
	if !st.Valid || st.Count != 3 || st.First != "Lord Calvon" {
		t.Fatalf("%+v", st)
	}
	if LastUpdate(d) != "2026-10-06 9:05" {
		t.Fatal(LastUpdate(d))
	}
}

func TestNormalizeCode(t *testing.T) {
	for _, ok := range []string{"c1", " C1 ", "ab2", "D15", "GI1", "T9"} {
		if _, v := NormalizeCode(ok); !v {
			t.Errorf("%q deveria ser válido", ok)
		}
	}
	for _, bad := range []string{"", "1", "C", "C1 X", "!next c1", "C-1", "ABCD1"} {
		if _, v := NormalizeCode(bad); v {
			t.Errorf("%q deveria ser inválido", bad)
		}
	}
}

func TestEscapeRoundTrip(t *testing.T) {
	for _, s := range []string{"!next C1", `a\b/c|d e`, "linha1\nlinha2\tx", "Você • ✦"} {
		if got := Unescape(Escape(s)); got != s {
			t.Errorf("%q -> %q", s, got)
		}
	}
	if Escape("!next C1") != `!next\sC1` {
		t.Error(Escape("!next C1"))
	}
	rows := ParseRows(`clid=1 client_nickname=x3tBot\sKalibra client_unique_identifier=ab\/c=|clid=2 client_nickname=Eu`)
	if len(rows) != 2 || rows[0]["client_nickname"] != "x3tBot Kalibra" || rows[0]["client_unique_identifier"] != "ab/c=" {
		t.Errorf("%v", rows)
	}
}

func TestListSizeAndSummary(t *testing.T) {
	if n := ListSize(sampleVisible); n != 8 {
		t.Errorf("linhas: %d", n)
	}
	if ListSize("") != 0 || ListSize("Bem-vindo ao canal de testes") != 0 {
		t.Error("canal sem lista deveria ter 0")
	}
	if s := Summarize(ParseQueue(sampleVisible, "C1"), 1); s != "Agora: Kira Zinha no respawn, 2 no next." {
		t.Error(s)
	}
	if s := Summarize(ParseQueue(sampleVisible, "D7"), 1); s != "Agora: Elite'Nitrox no respawn, ninguém no next." {
		t.Error(s)
	}
	// "Next: A / B" = uma vaga só (a "/" junta nomes); quem soma pessoa é o "+1".
	st := ParseQueue("d14 - [01:07] HFF Seal (Tier 1): Ironkin | Next: Dudu Overlord / Pedrinho", "D14")
	if !st.Valid || st.Count != 1 || !Eligible(st, 2) {
		t.Errorf("A / B deveria ser 1 no next: %+v", st)
	}
	st = ParseQueue("d14 - [01:07] HFF Seal (Tier 1): Ironkin | Next: Dudu Overlord / Pedrinho +1", "D14")
	if st.Count != 2 || Eligible(st, 1) || Eligible(st, 2) {
		t.Errorf("A / B +1 deveria ser 2 no next: %+v", st)
	}
	// O ocupante pode ter "/" (party no respawn): não conta na fila.
	st = ParseQueue("d14 - [01:07] HFF: Ironkin / Fulano | Next: Dudu", "D14")
	if !st.Valid || st.Count != 1 {
		t.Errorf("barra no ocupante: %+v", st)
	}
}

// Formato novo do x3tBot (prints de 08 e 09/10/2026).
const sampleNew = `Last update: 2026-10-09 18:02:13 America/Sao_Paulo
D7 - [00:15] DT -1 (Tier 1): Gnorge Talento Ruro | Next: bruneo berserker/emma Reilly [Espera ~00:15]
R9 - [01:39] Upper Roshamuul (Tier 1): Nowlolah Wolentah | Next: Shawn Shooter [Espera ~01:39] | Next: Billy Noval [Espera ~04:09]
V7 - [01:14] Bulltaur Lair -2 (Tier 1): Hardyz Ek | Next: Red Vesgo [Fim em ~03:14]
B3 - [01:16] Gloom Pillars: Arthy Zephyr
AB1 - [02:30] Teste Grande (Tier 2): Fulano`

func TestNewFormat(t *testing.T) {
	cases := []struct {
		code       string
		count, rem int
		first, who string
	}{
		{"D7", 1, 15, "bruneo berserker/emma Reilly", "Gnorge Talento Ruro"},
		{"R9", 2, 99, "Shawn Shooter", "Nowlolah Wolentah"},
		{"V7", 1, 74, "Red Vesgo", "Hardyz Ek"},
		{"B3", 0, 76, "", "Arthy Zephyr"},
		{"AB1", 0, 150, "", "Fulano"},
	}
	for _, c := range cases {
		st := ParseQueue(sampleNew, c.code)
		if !st.Valid || st.Count != c.count || st.First != c.first || !st.HasTimer || st.Remaining != c.rem || Hunter(st) != c.who {
			t.Errorf("%s: %+v", c.code, st)
		}
	}
	// Código em minúsculas na escolha também acha a linha em maiúsculas.
	if st := ParseQueue(sampleNew, "r9"); st.Count != 2 {
		t.Errorf("r9: %+v", st)
	}
	// O tempo antigo também é lido.
	if st := ParseQueue(sampleVisible, "C1"); !st.HasTimer || st.Remaining != 90 {
		t.Errorf("tempo antigo: %+v", st)
	}
	bad := []string{
		"R9 - [01:00] X: A | Next: B Next: C",       // dois Next sem barra
		"R9 - [01:00] X: A | Next: [Espera ~01:00]", // só a etiqueta, sem nome
		"R9 - [01:00] X: A | Next: B | Next:",       // segundo vazio
	}
	for _, d := range bad {
		if st := ParseQueue(d, "R9"); st.Valid {
			t.Errorf("deveria ser inválida: %q -> %+v", d, st)
		}
	}
}

func TestWindow(t *testing.T) {
	loc := time.Local
	at := func(h, m int) time.Time { return time.Date(2026, 10, 9, h, m, 30, 0, loc) }
	w, err := ResolveWindow("20:00", "22:30", at(18, 30))
	if err != nil {
		t.Fatal(err)
	}
	if !w.Contains(at(20, 15)) || !w.Contains(at(20, 0)) || !w.Contains(at(22, 30)) || w.Contains(at(22, 31)) || w.Contains(at(19, 59)) {
		t.Error("contains errado")
	}
	if w.Ended(at(22, 30)) || !w.Ended(at(22, 31)) {
		t.Error("ended errado")
	}
	if _, err := ResolveWindow("20:00", "22:30", at(23, 0)); err == nil {
		t.Error("janela que já acabou deveria dar erro")
	}
	// Passa da meia-noite: 23:00 – 01:00.
	w, err = ResolveWindow("23:00", "01:00", at(0, 30))
	if err != nil || !w.Contains(at(0, 45)) || w.Contains(at(1, 1)) || !w.Contains(time.Date(2026, 10, 8, 23, 10, 0, 0, loc)) {
		t.Errorf("meia-noite (madrugada): %v %+v", err, w)
	}
	w, err = ResolveWindow("23:00", "01:00", at(21, 0))
	if err != nil || !w.Contains(at(23, 30)) || !w.Contains(time.Date(2026, 10, 10, 0, 59, 0, 0, loc)) {
		t.Errorf("meia-noite (noite): %v %+v", err, w)
	}
	for _, bad := range []string{"", "25:00", "20", "aa:bb", "20:60"} {
		if _, ok := ParseHM(bad); ok {
			t.Errorf("%q deveria ser inválido", bad)
		}
	}
	if v, ok := ParseHM("20h05"); !ok || v != 20*60+5 {
		t.Error("20h05")
	}
}

// Linhas exatas do print de 13/10/2026 (claim MissClick Bot Test): tempo com espaços
// dentro dos colchetes e o estado "Aceitar".
const samplePrint1310 = `A2 - [ 00:50 ] Cobra Subsolo (Tier 1): Caipyranho
C1 - [ 01:55 ] Livraria de Fire (Tier 3): Lord Tenchi | Next: Real Calvon [ Fim em ~03:55 ]
D8 - [ 01:22 ] DT -2 (Tier 1): Jamaica Tank | Next: Projekkt [ Fim em ~02:17 ]
R1 - [ 00:26 ] West (Tier 1): Rhoten | Next: Apoloooo [ Fim em ~02:26 ] | Next: Megazord
R9 - [ Aceitar 00:03 ] Upper Roshamuul (Tier 1): Next: Alxoff — aguardando aceitação
Y11 - [ 01:21 ] Norcferatu Catacombs Direita (Tier 1): Maarpz`

func TestPrint1310(t *testing.T) {
	cases := []struct {
		code       string
		count, rem int
		first, who string
	}{
		{"A2", 0, 50, "", "Caipyranho"},
		{"C1", 1, 115, "Real Calvon", "Lord Tenchi"},
		{"D8", 1, 82, "Projekkt", "Jamaica Tank"},
		{"R1", 2, 26, "Apoloooo", "Rhoten"},
		{"Y11", 0, 81, "", "Maarpz"},
	}
	for _, c := range cases {
		st := ParseQueue(samplePrint1310, c.code)
		if !st.Valid || st.Accepting || st.Count != c.count || st.First != c.first || !st.HasTimer || st.Remaining != c.rem || Hunter(st) != c.who {
			t.Errorf("%s: %+v", c.code, st)
		}
	}
	// O tempo não pode ficar grudado em quem caça: senão, a cada minuto parece que entrou alguém.
	a := ParseQueue("C1 - [ 02:28 ] Livraria de Fire (Tier 3): Lord Tenchi", "C1")
	b := ParseQueue("C1 - [ 02:27 ] Livraria de Fire (Tier 3): Lord Tenchi", "C1")
	if huntKey(a) != huntKey(b) || !a.HasTimer || a.Remaining != 148 {
		t.Errorf("tempo grudado no nome: %q / %q", huntKey(a), huntKey(b))
	}
	// Aceitar: ninguém caçando, Alxoff (sem o "— aguardando aceitação") está aceitando.
	st := ParseQueue(samplePrint1310, "R9")
	if !st.Valid || !st.Accepting || st.HasTimer || st.Count != 1 || st.First != "Alxoff" || Hunter(st) != "" || huntKey(st) != "" || st.AcceptText != "Aceitar 00:03" {
		t.Errorf("R9 aceitar: %+v", st)
	}
	if s := Summarize(st, 1); s != "Agora: ninguém caçando; Alxoff está aceitando o respawn (Aceitar 00:03)." {
		t.Errorf("resumo: %q", s)
	}
	// Outras variações de espaço e palavra desconhecida (nunca vale como tempo).
	if st := ParseQueue("C1 - [1:05] X: Fulano", "C1"); !st.HasTimer || st.Remaining != 65 {
		t.Errorf("sem zero: %+v", st)
	}
	if st := ParseQueue("C1 - [  01 : 05  ] X: Fulano", "C1"); !st.HasTimer || st.Remaining != 65 || Hunter(st) != "Fulano" {
		t.Errorf("muitos espaços: %+v", st)
	}
	for _, d := range []string{
		"C1 - [ Pausa 00:10 ] X (Tier 1): Fulano",
		"C1 - [ Aceitar 00:03 ] X (Tier 1): Fulano", // "Aceitar" sem ninguém na fila: estranho, não confia
	} {
		if st := ParseQueue(d, "C1"); st.Valid {
			t.Errorf("deveria ser inválida: %q -> %+v", d, st)
		}
	}
}
