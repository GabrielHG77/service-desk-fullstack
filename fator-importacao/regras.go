package main

import "fmt"

// Componente é uma parcela do custo de importação que pode entrar ou sair da
// conta. PIS, COFINS e ICMS não estão aqui de propósito: são recuperáveis, a
// empresa credita depois, e incluí-los já fez o fator aparecer entre 69% e 200%.
type Componente struct {
	Chave   string `json:"chave"`
	Rotulo  string `json:"rotulo"`
	Imposto bool   `json:"imposto"` // separa o fator cheio do fator logístico
}

var Componentes = []Componente{
	{"ii", "II Recolhido", true},
	{"siscomex", "Tx. Siscomex", true},
	{"rodoviario", "Frete rodoviário", false},
	{"marinha", "Marinha mercante", false},
	{"origem", "Despesas na origem", false},
	{"armazenagem", "Armazenagem", false},
	{"honorarios", "Honorários", false},
	{"freteIntl", "Frete internacional", false},
}

type Faixa struct {
	Min float64 `json:"min"`
	Max float64 `json:"max"`
}

type Regras struct {
	Faixa Faixa    `json:"faixa"`
	Custo []string `json:"custo"` // chaves de Componentes que entram no Total
}

// RegrasPadrao é o que o levantamento define: faixa de 24% a 30% e todos os
// oito componentes no custo.
func RegrasPadrao() Regras {
	todos := make([]string, 0, len(Componentes))
	for _, c := range Componentes {
		todos = append(todos, c.Chave)
	}
	return Regras{Faixa: Faixa{0.24, 0.30}, Custo: todos}
}

func ehComponente(chave string) bool {
	for _, c := range Componentes {
		if c.Chave == chave {
			return true
		}
	}
	return false
}

// Normalizar descarta chave desconhecida e conserta faixa invertida ou vazia,
// para uma configuração editada à mão não derrubar o app.
func (r Regras) Normalizar() Regras {
	p := RegrasPadrao()
	out := Regras{Faixa: r.Faixa}
	for _, k := range r.Custo {
		if ehComponente(k) && !out.Usa(k) {
			out.Custo = append(out.Custo, k)
		}
	}
	if len(out.Custo) == 0 {
		out.Custo = p.Custo
	}
	if out.Faixa.Min < 0 || out.Faixa.Max <= 0 || out.Faixa.Min >= out.Faixa.Max {
		out.Faixa = p.Faixa
	}
	return out
}

func (r Regras) Usa(chave string) bool {
	for _, k := range r.Custo {
		if k == chave {
			return true
		}
	}
	return false
}

func (r Regras) Padrao() bool {
	p := RegrasPadrao()
	return r.Faixa == p.Faixa && len(r.Custo) == len(p.Custo)
}

// Total soma só os componentes escolhidos. `s` traz os impostos do arquivo de
// materiais; `c` traz o que saiu das notas em PDF.
func (r Regras) Total(s Somas, c Custos) float64 {
	valor := map[string]float64{
		"ii": s.II, "siscomex": s.Siscomex,
		"rodoviario": c.Rodoviario, "marinha": c.Marinha, "origem": c.Origem,
		"armazenagem": c.Armazenagem, "honorarios": c.Honorarios, "freteIntl": c.FreteIntl,
	}
	var t float64
	for _, k := range r.Custo {
		t += valor[k]
	}
	return t
}

// Imposto é a parte do total que é tributo, para separar o fator logístico.
func (r Regras) Imposto(s Somas) float64 {
	var t float64
	if r.Usa("ii") {
		t += s.II
	}
	if r.Usa("siscomex") {
		t += s.Siscomex
	}
	return t
}

// ColunasDe devolve as colunas da planilha, 1-based, que a fórmula do Total
// precisa somar neste modal. Marinha mercante não existe no aéreo.
func (r Regras) ColunasDe(modal string) []int {
	L := layouts[modal]
	coluna := map[string]int{
		"ii": 12, "siscomex": 13,
		"rodoviario": L.rodoviario, "marinha": L.marinha, "origem": L.origem,
		"armazenagem": L.armazenagem, "honorarios": L.honorarios, "freteIntl": L.freteIntl,
	}
	var out []int
	for _, c := range Componentes { // ordem das colunas, não a da configuração
		if r.Usa(c.Chave) && coluna[c.Chave] > 0 {
			out = append(out, coluna[c.Chave])
		}
	}
	return append(out, L.outros) // Outros entra sempre: é o campo manual da analista
}

// FormulaTotal monta a soma da linha. Com tudo ligado as colunas são contíguas
// e vira um SUM, que é o que a analista espera ver ao clicar na célula.
func (r Regras) FormulaTotal(modal string, linha int) string {
	cols := r.ColunasDe(modal)
	contiguo := true
	for i := 1; i < len(cols); i++ {
		if cols[i] != cols[i-1]+1 {
			contiguo = false
			break
		}
	}
	if contiguo && len(cols) > 1 {
		return fmt.Sprintf("SUM(%s%d:%s%d)", colNome(cols[0]), linha, colNome(cols[len(cols)-1]), linha)
	}
	s := ""
	for i, c := range cols {
		if i > 0 {
			s += "+"
		}
		s += fmt.Sprintf("%s%d", colNome(c), linha)
	}
	return s
}
