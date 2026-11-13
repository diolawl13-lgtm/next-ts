package main

// Tempo personalizado do claim (1.7.0): "!next C1 2:10" pede 2 horas e 10 minutos de
// hunt no C1. Desligado, o comando continua "!next C1". Quem escolhe a duração é o
// usuário: o app não aplica limite de ranking (só confere se o número faz sentido).

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
)

// maxDurHours: o campo de horas tem 2 algarismos (só para não aceitar um número
// digitado errado, como 210). Não é limite de ranking.
const maxDurHours = 99

// ValidateDuration confere a duração escolhida.
func ValidateDuration(h, m int) error {
	switch {
	case h < 0 || m < 0:
		return errors.New("a duração não pode ser negativa")
	case m > 59:
		return errors.New("os minutos vão de 0 a 59")
	case h == 0 && m == 0:
		return errors.New("a duração não pode ser zero")
	case h > maxDurHours:
		return fmt.Errorf("as horas vão até %d", maxDurHours)
	}
	return nil
}

// FmtDuration: "2:10", "0:05", "10:00" (minutos sempre com dois algarismos).
func FmtDuration(h, m int) string { return fmt.Sprintf("%d:%02d", h, m) }

// NextCommand monta o comando que vai para o bot: "!next C1" ou, com o tempo
// personalizado ligado, "!next C1 2:10".
func NextCommand(code string, useDuration bool, h, m int) string {
	if !useDuration {
		return "!next " + code
	}
	return "!next " + code + " " + FmtDuration(h, m)
}

// Resposta do bot: "... Tempo reservado: 2:00."
var reReserved = regexp.MustCompile(`(?i)tempo\s+reservado\s*:?\s*(\d{1,2}):(\d{2})`)

// reservedTime devolve o tempo reservado que o bot informou (ok=false se não informou).
func reservedTime(msg string) (h, m int, ok bool) {
	r := reReserved.FindStringSubmatch(msg)
	if r == nil {
		return 0, 0, false
	}
	h, _ = strconv.Atoi(r[1])
	m, _ = strconv.Atoi(r[2])
	return h, m, true
}
