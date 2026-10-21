import { copyFile, stat } from 'node:fs/promises'
import ExcelJS from 'exceljs'

// Linha de material: Mercadoria preenchida, Modal vazio.
// Linha de total:    o contrário. É o que separa os dois níveis depois.
const CABECALHO = {
  SEA: ['N/Ref.', 'Data', 'Nr.DI/Duimp', 'Ref. Cliente', 'Origem', 'Exportador', 'Modal',
    'Mercadoria', 'Qtde', 'Unidade de Medida', 'Valor BRL', 'II (R$)', 'Tx. Siscomex (R$)',
    'Valor Frete Rodoviário', 'MARINHA MERCANTE', 'Despesas na Origem', 'Armazenagem (R$)',
    'Honorarios (R$)', 'Frete Maritímo (R$)', 'Outros (R$)', 'Total (R$)', 'Percentual Custo de Importação'],
  // Aéreo não tem Marinha Mercante: tudo dali para a direita anda uma coluna.
  AIR: ['N/Ref.', 'Data', 'Nr.DI/Duimp', 'Ref. Cliente', 'Origem', 'Exportador', 'Modal',
    'Mercadoria', 'Qtde', 'Unidade de Medida', 'Valor BRL', 'II (R$)', 'Tx. Siscomex (R$)',
    'Valor Frete Rodoviário', 'Despesas na Origem', 'Armazenagem (R$)',
    'Honorarios (R$)', 'Frete Aéreo (R$)', 'Outros (R$)', 'Total (R$)', 'Percentual Custo de Importação'],
}

// Índice 1-based das colunas que mudam entre os modais.
const LAYOUT = {
  SEA: { rodoviario: 14, marinha: 15, origem: 16, armazenagem: 17, honorarios: 18, freteIntl: 19, outros: 20, total: 21, pct: 22 },
  AIR: { rodoviario: 14, marinha: 0, origem: 15, armazenagem: 16, honorarios: 17, freteIntl: 18, outros: 19, total: 20, pct: 21 },
}

const DINHEIRO = '#,##0.00'

export async function abrir(caminho) {
  const wb = new ExcelJS.Workbook()
  try {
    const s = await stat(caminho)
    // Planilha de 0 bytes: o ExcelJS estoura com erro críptico. Recria.
    if (s.size > 0) await wb.xlsx.readFile(caminho)
  } catch (e) {
    if (e.code !== 'ENOENT') throw e
  }
  for (const modal of ['AIR', 'SEA']) {
    const ws = wb.getWorksheet(modal) ?? wb.addWorksheet(modal)
    if (!ws.getRow(1).getCell(1).value) {
      ws.getRow(1).values = CABECALHO[modal]
      ws.getRow(1).font = { bold: true }
    }
  }
  return wb
}

// A N/Ref. na coluna A é a chave: se já aparece na aba, o pedido é pulado inteiro.
export function nrefsExistentes(wb, modal) {
  const ws = wb.getWorksheet(modal)
  const set = new Set()
  if (!ws) return set
  ws.eachRow((row, n) => {
    if (n === 1) return
    const v = row.getCell(1).value
    if (v) set.add(String(v).trim())
  })
  return set
}

export function linhasDeNrefs(wb, modal, nrefs) {
  const ws = wb.getWorksheet(modal)
  const alvo = new Set(nrefs)
  const linhas = []
  if (!ws) return linhas
  ws.eachRow((row, n) => {
    if (n === 1) return
    const v = row.getCell(1).value
    if (v && alvo.has(String(v).trim())) linhas.push(n)
  })
  return linhas
}

export function removerLinhas(wb, modal, linhas) {
  const ws = wb.getWorksheet(modal)
  if (!ws) return 0
  for (const n of [...linhas].sort((a, b) => b - a)) ws.spliceRows(n, 1)
  return linhas.length
}

function setMoeda(cell, valor) {
  cell.value = valor
  cell.numFmt = DINHEIRO
}

export function gravarPedido(wb, pedido) {
  const modal = pedido.modal
  const ws = wb.getWorksheet(modal)
  const L = LAYOUT[modal]

  for (const it of pedido.itens) {
    const r = ws.addRow([])
    r.getCell(1).value = pedido.nref
    if (it.data) {
      r.getCell(2).value = it.data
      r.getCell(2).numFmt = 'dd/mm/yyyy' // formato explícito: a máquina está em inglês
    }
    r.getCell(3).value = it.di
    r.getCell(4).value = it.pedido
    r.getCell(8).value = it.descricao
    r.getCell(9).value = it.qtde
    r.getCell(10).value = it.unidade
    setMoeda(r.getCell(11), it.cif)
    setMoeda(r.getCell(12), it.ii)
    setMoeda(r.getCell(13), it.siscomex)
  }

  const t = ws.addRow([])
  const n = t.number
  t.getCell(1).value = pedido.nref
  t.getCell(4).value = pedido.itens[0]?.pedido ?? ''
  t.getCell(6).value = pedido.exportador
  t.getCell(7).value = modal
  t.getCell(9).value = pedido.somas.qtde
  setMoeda(t.getCell(11), pedido.somas.cif)
  setMoeda(t.getCell(12), pedido.somas.ii)
  setMoeda(t.getCell(13), pedido.somas.siscomex)
  setMoeda(t.getCell(L.rodoviario), pedido.custos.rodoviario)
  if (L.marinha) setMoeda(t.getCell(L.marinha), pedido.custos.marinha)
  setMoeda(t.getCell(L.origem), pedido.custos.origem)
  setMoeda(t.getCell(L.armazenagem), pedido.custos.armazenagem)
  setMoeda(t.getCell(L.honorarios), pedido.custos.honorarios)
  setMoeda(t.getCell(L.freteIntl), pedido.custos.freteIntl)
  // Outros fica vazia de propósito: a analista digita ali à mão. Zero obrigaria
  // a apagar antes. Como Total é fórmula até Outros, o que ela digitar entra.
  const col = (i) => ws.getColumn(i).letter
  t.getCell(L.total).value = { formula: `SUM(${col(12)}${n}:${col(L.outros)}${n})`, result: pedido.total }
  t.getCell(L.total).numFmt = DINHEIRO
  t.getCell(L.pct).value = { formula: `IF(${col(11)}${n}=0,"",${col(L.total)}${n}/${col(11)}${n})`, result: pedido.fator }
  t.getCell(L.pct).numFmt = '0.0%'
  return n
}

export async function salvar(wb, caminho) {
  try {
    const s = await stat(caminho)
    if (s.size > 0) await copyFile(caminho, caminho.replace(/\.xlsx$/i, '.bak.xlsx'))
  } catch (e) {
    if (e.code !== 'ENOENT') throw e
  }
  await wb.xlsx.writeFile(caminho)
}

export { CABECALHO, LAYOUT }
