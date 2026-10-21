package main

// Conversões pt-BR explícitas: a máquina alvo roda em inglês, nada aqui pode
// depender de locale do sistema (já produziu data trocada e número mal lido).

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// ParseBR lê o formato brasileiro. No CSV legado o separador decimal é um TAB
// e o de milhar é ponto; os campos ainda vêm preenchidos com espaços.
// Valor ausente ou ilegível vira zero, nunca erro.
func ParseBR(s string) float64 {
	limpo := strings.Map(func(r rune) rune {
		switch {
		case r == '\t':
			return ','
		case r >= '0' && r <= '9', r == ',', r == '.', r == '-':
			return r
		}
		return -1
	}, s)
	if !strings.ContainsFunc(limpo, unicode.IsDigit) {
		return 0
	}
	limpo = strings.ReplaceAll(limpo, ".", "")
	limpo = strings.Replace(limpo, ",", ".", 1)
	n, err := strconv.ParseFloat(limpo, 64)
	if err != nil {
		return 0
	}
	return n
}

// ParseDataBR lê sempre dia/mês/ano, nunca mês/dia. Aceita ano de dois dígitos
// (o .csv traz 03/08/26) e descarta hora grudada ("03/08/2026 00:00:00").
func ParseDataBR(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	campos := strings.FieldsFunc(s, func(r rune) bool { return r == '/' || r == '-' || r == '.' })
	if len(campos) < 3 {
		return time.Time{}, false
	}
	anoTxt := strings.Fields(campos[2])
	if len(anoTxt) == 0 {
		return time.Time{}, false
	}
	dia, err1 := strconv.Atoi(strings.TrimSpace(campos[0]))
	mes, err2 := strconv.Atoi(strings.TrimSpace(campos[1]))
	ano, err3 := strconv.Atoi(anoTxt[0])
	if err1 != nil || err2 != nil || err3 != nil {
		return time.Time{}, false
	}
	if len(anoTxt[0]) == 2 {
		ano += 2000
	}
	d := time.Date(ano, time.Month(mes), dia, 0, 0, 0, 0, time.UTC)
	// Rejeita 31/02 em vez de deixar o Go normalizar para 03/03.
	if d.Day() != dia || int(d.Month()) != mes {
		return time.Time{}, false
	}
	return d, true
}

func FmtDataBR(d time.Time) string { return d.Format("02/01/2006") }

// NormKey compara cabeçalhos sem se importar com acento, caixa ou espaço
// sobrando: os nomes variam entre o .csv e o .xlsx.
func NormKey(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		if a, ok := semAcento[r]; ok {
			b.WriteRune(a)
		} else {
			b.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

var semAcento = map[rune]rune{
	'á': 'a', 'à': 'a', 'ã': 'a', 'â': 'a', 'ä': 'a',
	'é': 'e', 'ê': 'e', 'è': 'e', 'ë': 'e',
	'í': 'i', 'î': 'i', 'ì': 'i', 'ï': 'i',
	'ó': 'o', 'õ': 'o', 'ô': 'o', 'ò': 'o', 'ö': 'o',
	'ú': 'u', 'û': 'u', 'ù': 'u', 'ü': 'u',
	'ç': 'c', 'ñ': 'n',
}

// FmtBRL formata 1234.5 como "1.234,50" sem depender do locale da máquina.
func FmtBRL(n float64) string {
	s := strconv.FormatFloat(n, 'f', 2, 64)
	sinal := ""
	if strings.HasPrefix(s, "-") {
		sinal, s = "-", s[1:]
	}
	inteiro, decimal, _ := strings.Cut(s, ".")
	var b strings.Builder
	for i, d := range inteiro {
		if i > 0 && (len(inteiro)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(d)
	}
	return sinal + b.String() + "," + decimal
}

func FmtPct(n float64) string {
	return strings.Replace(fmt.Sprintf("%.1f", n*100), ".", ",", 1) + "%"
}
