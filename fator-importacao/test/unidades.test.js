import { test } from 'node:test'
import assert from 'node:assert/strict'
import { parseBR, parseDateBR, fmtDateBR } from '../src/util.js'
import { extrairCustos, layoutTexto, ehExtrato } from '../src/pdf.js'

test('TAB como separador decimal no CSV legado', () => {
  assert.equal(parseBR('668\t14'), 668.14)
  assert.equal(parseBR('  1.278\t261048'), 1278.261048)
  assert.equal(parseBR('1.234,56'), 1234.56)
  assert.equal(parseBR(1234.56), 1234.56)
  assert.equal(parseBR(''), 0)
  assert.equal(parseBR(null), 0)
  assert.equal(parseBR('-'), 0)
})

// A própria linha se verifica: II Recolhido / CIF Total tem que bater com % II.
test('conferência do CSV: II/CIF bate com a coluna % II', () => {
  assert.equal(+(parseBR('  120\t27') / parseBR('  668\t14') * 100).toFixed(2), 18.00)
  assert.equal(+(parseBR('1.046\t57') / parseBR('5.232\t85') * 100).toFixed(2), 20.00)
})

test('data sempre dia/mês/ano, nunca mês/dia', () => {
  assert.equal(fmtDateBR(parseDateBR('06/04/2026')), '06/04/2026') // 6 de abril, não 4 de junho
  assert.equal(parseDateBR('06/04/2026').getUTCMonth(), 3)
  assert.equal(fmtDateBR(parseDateBR('03/08/26')), '03/08/2026')   // ano de 2 dígitos do CSV
  assert.equal(fmtDateBR(parseDateBR('03/08/2026 00:00:00')), '03/08/2026') // hora grudada
  assert.equal(parseDateBR('31/02/2026'), null)
  assert.equal(parseDateBR(''), null)
  const d = new Date(Date.UTC(2026, 7, 3))
  assert.equal(fmtDateBR(parseDateBR(d)), '03/08/2026') // Date vindo do .xlsx passa reto
})

const NOTA_NACIONAL = `
NOTA DE DEBITO SSZ2602015
Despacho Importação
Honorários de despacho 1.417,79
SUBTOTAL TRIBUTÁVEL 1.417,79
Armazenagem - destino 3.203,43
Armazenagem - destino 338,63
Armazenagem - destino 4.828,19
Marinha Mercante 2.551,00
Transporte Rodoviário 980,45
`

const NOTA_INTERNACIONAL = `
NOTA DE DEBITO INTERNACIONAL
Frete marítimo 12.000,00
SUBTOTAL NÃO TRIBUTÁVEL 12.000,00
Taxa Administrativa 150,00
Handling origem 123,50
SUBTOTAL TRIBUTÁVEL 273,50
`

test('armazenagem soma todas as cobranças, não só a primeira', () => {
  const c = extrairCustos(NOTA_NACIONAL)
  assert.equal(c.tipo, 'nacional')
  assert.equal(+c.armazenagem.toFixed(2), 8370.25) // 3.203,43 + 338,63 + 4.828,19
  assert.notEqual(c.armazenagem, 3203.43)
  assert.equal(c.honorarios, 1417.79)
  assert.equal(c.marinha, 2551.00)
  assert.equal(c.rodoviario, 980.45)
  assert.equal(c.freteIntl, 0) // nota nacional não traz frete internacional
})

test('nota internacional traz frete e despesas na origem', () => {
  const c = extrairCustos(NOTA_INTERNACIONAL)
  assert.equal(c.tipo, 'internacional')
  assert.equal(c.freteIntl, 12000)
  assert.equal(c.origem, 273.50) // um dos dois valores esperados no marítimo
  assert.equal(c.armazenagem, 0)
})

test('nota do despachante é descartada inteira', () => {
  const c = extrairCustos('ACME DESPACHANTES ADUANEIROS LTDA\n' + NOTA_NACIONAL)
  assert.equal(c.ignorada, 'despachante')
  assert.equal(c.armazenagem, 0)
})

test('razão social com ARMAZENAGEM não vira nota nacional', () => {
  // `Armazenagem - destino` é a marca; o nome da empresa não casa com ela.
  const c = extrairCustos('ARMAZENAGEM GERAL DO PORTO LTDA\n' + NOTA_INTERNACIONAL)
  assert.equal(c.tipo, 'internacional')
  assert.equal(c.armazenagem, 0)
  assert.equal(c.freteIntl, 12000)
})

test('extratos de DI/DUIMP são descartados pelo nome', () => {
  assert.ok(ehExtrato('Extrato DI 26-0001.pdf'))
  assert.ok(ehExtrato('EXTRATO DUIMP.pdf'))
  assert.ok(!ehExtrato('SSZ2602015.pdf'))
})

test('valor ausente entra como zero, não como erro', () => {
  const c = extrairCustos('Armazenagem - destino 10,00\nnada mais aqui')
  assert.equal(c.honorarios, 0)
  assert.equal(c.marinha, 0)
  assert.equal(c.rodoviario, 0)
  assert.equal(c.armazenagem, 10)
})

test('layout reconstrói a linha na ordem rótulo → número', () => {
  // getTextContent devolve fora de ordem e quebra palavras em fragmentos.
  const itens = [
    { str: '3.203,43', width: 40, transform: [1, 0, 0, 1, 400, 700] },
    { str: 'Armazena', width: 48, transform: [1, 0, 0, 1, 50, 700] },
    { str: 'gem', width: 18, transform: [1, 0, 0, 1, 98, 700] },
    { str: '- destino', width: 40, transform: [1, 0, 0, 1, 120, 700] },
    { str: 'Marinha Mercante', width: 80, transform: [1, 0, 0, 1, 50, 680] },
    { str: '2.551,00', width: 40, transform: [1, 0, 0, 1, 400, 681] }, // mesma linha visual
  ]
  const txt = layoutTexto(itens)
  assert.equal(txt, 'Armazenagem - destino 3.203,43\nMarinha Mercante 2.551,00')
  assert.equal(extrairCustos(txt).armazenagem, 3203.43)
})
