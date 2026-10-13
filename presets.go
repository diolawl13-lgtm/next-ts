package main

// Atalhos de claim: trocar com um clique de onde ler (canal) e para quem mandar (bot).
// Ex.: "x3tBot" = canal Claimeds + x3tBot Kalibra;
//      "MissClick Bot Test" = canal do claim de teste + MissClick Bot Test.

import (
	"errors"
	"strings"
)

const maxPresets = 8

// suggestPresets procura pares canal+bot que fazem sentido e ainda não existem.
// Não toca em atalhos que o usuário apagou. Precisa de a.mu travado.
func (a *App) suggestPresets(chans []ChannelInfo, clients []ClientInfo, hasList map[int]bool) bool {
	removed := map[string]bool{}
	for _, k := range a.set.PresetsRemoved {
		removed[k] = true
	}
	exists := map[string]bool{}
	for _, p := range a.set.Presets {
		exists[presetKey(p.ChannelName, p.BotUID)] = true
	}
	changed := false
	add := func(name string, ch ChannelInfo, bot ClientInfo) {
		k := presetKey(ch.Name, bot.UID)
		if exists[k] || removed[k] || len(a.set.Presets) >= maxPresets || bot.UID == "" {
			return
		}
		for _, p := range a.set.Presets {
			if strings.EqualFold(p.Name, name) {
				return // já existe um atalho com esse nome (talvez criado pelo usuário)
			}
		}
		exists[k] = true
		a.set.Presets = append(a.set.Presets, Preset{Name: name, ChannelName: ch.Name, BotUID: bot.UID, BotName: bot.Name})
		changed = true
	}
	// O x3tBot primeiro, para o atalho dele aparecer antes dos outros.
	ordered := make([]ClientInfo, 0, len(clients))
	for _, c := range clients {
		if strings.Contains(strings.ToLower(c.Name), "x3t") {
			ordered = append(ordered, c)
		}
	}
	for _, c := range clients {
		if !strings.Contains(strings.ToLower(c.Name), "x3t") {
			ordered = append(ordered, c)
		}
	}
	for _, bot := range ordered {
		lb := strings.ToLower(bot.Name)
		if strings.Contains(lb, "x3t") {
			// x3tBot: o canal "Claimeds" (nome exato), mesmo que hoje esteja sem lista.
			for _, ch := range chans {
				if ln := lettersOnly(ch.Name); ln == "claimeds" || ln == "claimed" {
					add("x3tBot", ch, bot)
					break
				}
			}
			continue
		}
		if !strings.Contains(lb, "bot") {
			continue
		}
		// Outro bot: um canal de claim que tenha a palavra que distingue o bot (ex.: "missclick").
		words := distinctiveWords(bot.Name)
		if len(words) == 0 {
			continue
		}
		var best *ChannelInfo
		for i, ch := range chans {
			ln := lettersOnly(ch.Name)
			if !strings.Contains(ln, "claim") {
				continue
			}
			for _, w := range words {
				if strings.Contains(ln, w) {
					if best == nil || (hasList[ch.Cid] && !hasList[best.Cid]) {
						best = &chans[i]
					}
					break
				}
			}
		}
		if best != nil {
			add(bot.Name, *best, bot)
		}
	}
	return changed
}

// distinctiveWords: palavras do nome que identificam o bot (ignora "bot", "test" etc.).
func distinctiveWords(name string) []string {
	skip := map[string]bool{"bot": true, "test": true, "teste": true, "the": true, "claim": true, "claimed": true}
	var out []string
	for _, f := range strings.FieldsFunc(strings.ToLower(name), func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r < 128
	}) {
		w := lettersOnly(f)
		if len(w) >= 4 && !skip[w] {
			out = append(out, w)
		}
	}
	return out
}

// SavePreset grava o canal + bot escolhidos agora como um atalho com nome.
func (a *App) SavePreset(name string, cid int, botUID string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("dê um nome ao atalho")
	}
	if len([]rune(name)) > 30 {
		return errors.New("nome do atalho muito longo (até 30 letras)")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	ch, bot, err := a.findPair(cid, botUID)
	if err != nil {
		return err
	}
	p := Preset{Name: name, ChannelName: ch.Name, BotUID: bot.UID, BotName: bot.Name}
	for i, old := range a.set.Presets {
		if strings.EqualFold(old.Name, name) || presetKey(old.ChannelName, old.BotUID) == presetKey(p.ChannelName, p.BotUID) {
			a.set.Presets[i] = p
			a.saveSettings()
			a.addLog("ok", "Atalho \"%s\" atualizado: canal %s, comando para %s.", name, ch.Name, bot.Name)
			return nil
		}
	}
	if len(a.set.Presets) >= maxPresets {
		return errors.New("limite de 8 atalhos; apague um antes")
	}
	a.set.Presets = append(a.set.Presets, p)
	a.saveSettings()
	a.addLog("ok", "Atalho \"%s\" salvo: canal %s, comando para %s.", name, ch.Name, bot.Name)
	return nil
}

// DeletePreset apaga um atalho (e não sugere de novo).
func (a *App) DeletePreset(name string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i, p := range a.set.Presets {
		if p.Name == name {
			a.set.Presets = append(a.set.Presets[:i], a.set.Presets[i+1:]...)
			a.set.PresetsRemoved = append(a.set.PresetsRemoved, presetKey(p.ChannelName, p.BotUID))
			if len(a.set.PresetsRemoved) > 50 {
				a.set.PresetsRemoved = a.set.PresetsRemoved[len(a.set.PresetsRemoved)-50:]
			}
			a.saveSettings()
			a.addLog("info", "Atalho \"%s\" apagado.", name)
			return nil
		}
	}
	return errors.New("atalho não encontrado")
}

// Select grava a escolha de canal e bot feita na janela (para lembrar na próxima vez).
func (a *App) Select(cid int, botUID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.mon.Running {
		return errors.New("pare o monitoramento antes de mudar isso")
	}
	for _, c := range a.channels {
		if c.Cid == cid {
			a.cid = cid
			a.set.ChannelName = c.Name
			a.set.ManualChannel = true
		}
	}
	for _, c := range a.clients {
		if botUID != "" && c.UID == botUID {
			a.botUID = botUID
			a.set.BotUID, a.set.BotName = c.UID, c.Name
		}
	}
	a.saveSettings()
	return nil
}

// findPair acha canal e bot atuais. Precisa de a.mu travado.
func (a *App) findPair(cid int, botUID string) (ChannelInfo, ClientInfo, error) {
	var ch ChannelInfo
	var bot ClientInfo
	for _, c := range a.channels {
		if c.Cid == cid {
			ch = c
		}
	}
	for _, c := range a.clients {
		if botUID != "" && c.UID == botUID {
			bot = c
		}
	}
	if ch.Cid == 0 {
		return ch, bot, errors.New("escolha o canal da lista primeiro")
	}
	if bot.UID == "" {
		return ch, bot, errors.New("escolha quem recebe o comando primeiro")
	}
	return ch, bot, nil
}

// PresetView é o atalho como a janela mostra: com os números atuais do servidor.
type PresetView struct {
	Preset
	Cid       int  `json:"cid"`       // 0 = canal não existe neste servidor
	BotOnline bool `json:"botOnline"` // o bot está conectado agora
}

// presetViews: precisa de a.mu travado.
func (a *App) presetViews() []PresetView {
	out := make([]PresetView, 0, len(a.set.Presets))
	for _, p := range a.set.Presets {
		v := PresetView{Preset: p}
		for _, c := range a.channels {
			if c.Name == p.ChannelName {
				v.Cid = c.Cid
			}
		}
		for _, c := range a.clients {
			if c.UID == p.BotUID {
				v.BotOnline = true
			}
		}
		out = append(out, v)
	}
	return out
}
