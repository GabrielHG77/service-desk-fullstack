import { readFile } from 'node:fs/promises'
import { basename, extname } from 'node:path'
import ExcelJS from 'exceljs'
import { parseBR, parseDateBR, normKey } from './util.js'

// Só estas colunas são lidas. O arquivo traz dezenas de outras (Fob, Seguro,
// ICMS, PIS, Peso Liquido, NCM...) e elas são ignoradas de propósito:
// PIS/COFINS/ICMS são recuperáveis e não entram no custo de importação.
const COLS = {
  di: 'Nr.DI/Duimp',
  data: 'Data DI/Duimp',
  pedido: 'Pedido',
  descricao: 'Descricao',
  qtde: 'Qtde',
  unidade: 'Unidade',
  cif: 'CIF Total',
  ii: 'II Recolhido',
  siscomex: 'Tx. Siscomex',
  exportador: 'Exportador',
}

function decode(buf) {
  try {
    return new TextDecoder('utf-8', { fatal: true }).decode(buf).replace(/^﻿/, '')
  } catch {
    // Sistema legado brasileiro: quase sempre cp1252.
    try { return new TextDecoder('windows-1252').decode(buf) } catch { return buf.toString('latin1') }
  }
}

// shortcut: não trata campo entre aspas contendo quebra de linha; as exportações
// do legado não produzem isso. Se aparecer, trocar por um parser de CSV de verdade.
function splitCSV(linha) {
  const out = []
  let cur = '', q = false
  for (let i = 0; i < linha.length; i++) {
    const c = linha[i]
    if (q) {
      if (c === '"') { if (linha[i + 1] === '"') { cur += '"'; i++ } else q = false }
      else cur += c
    } else if (c === '"') q = true
    else if (c === ';') { out.push(cur); cur = '' }
    else cur += c
  }
  out.push(cur)
  return out
}

// `Pedido` aparece nas posições 4 e 45 com o mesmo valor: fica a primeira.
// Cabeçalhos vazios do fim do arquivo são descartados.
function mapearColunas(cabecalho) {
  const idx = {}
  cabecalho.forEach((nome, i) => {
    const k = normKey(nome)
    if (k && !(k in idx)) idx[k] = i
  })
  const faltando = []
  const campo = {}
  for (const [alias, nome] of Object.entries(COLS)) {
    const i = idx[normKey(nome)]
    if (i === undefined) faltando.push(nome)
    else campo[alias] = i
  }
  if (faltando.length) throw new Error(`colunas ausentes: ${faltando.join(', ')}`)
  return campo
}

function cellText(v) {
  if (v == null) return ''
  if (v instanceof Date) return v
  if (typeof v === 'object') {
    if (Array.isArray(v.richText)) return v.richText.map((t) => t.text).join('')
    if ('result' in v) return v.result
    if ('text' in v) return v.text
    return ''
  }
  return v
}

function montarItem(valor) {
  const descricao = String(cellText(valor('descricao')) ?? '').trim()
  if (!descricao) return null // linhas em branco do fim do arquivo
  return {
    di: String(cellText(valor('di')) ?? '').trim(),
    data: parseDateBR(cellText(valor('data'))),
    pedido: String(cellText(valor('pedido')) ?? '').trim(),
    descricao,
    qtde: parseBR(cellText(valor('qtde'))),
    unidade: String(cellText(valor('unidade')) ?? '').trim(),
    cif: parseBR(cellText(valor('cif'))),
    ii: parseBR(cellText(valor('ii'))),
    siscomex: parseBR(cellText(valor('siscomex'))),
    exportador: String(cellText(valor('exportador')) ?? '').trim(),
  }
}

async function lerCSV(caminho) {
  const texto = decode(await readFile(caminho))
  const linhas = texto.split(/\r?\n/)
  const cabecalho = splitCSV(linhas[0] ?? '')
  const campo = mapearColunas(cabecalho)
  const itens = []
  for (const linha of linhas.slice(1)) {
    if (!linha.trim()) continue
    const celulas = splitCSV(linha)
    const item = montarItem((alias) => celulas[campo[alias]])
    if (item) itens.push(item)
  }
  return itens
}

async function lerXLSX(caminho) {
  const wb = new ExcelJS.Workbook()
  await wb.xlsx.readFile(caminho)
  const ws = wb.worksheets[0]
  if (!ws) throw new Error('planilha de materiais sem abas')
  const cabecalho = (ws.getRow(1).values ?? []).slice(1).map((v) => cellText(v))
  const campo = mapearColunas(cabecalho)
  const itens = []
  ws.eachRow((row, n) => {
    if (n === 1) return
    const item = montarItem((alias) => row.getCell(campo[alias] + 1).value)
    if (item) itens.push(item)
  })
  return itens
}

// O nome do arquivo sem extensão é a N/Ref. (B2600037349.CSV -> B2600037349).
export function nrefDe(caminho) {
  return basename(caminho, extname(caminho))
}

export async function lerMateriais(caminho) {
  const ext = extname(caminho).toLowerCase()
  const itens = ext === '.csv' ? await lerCSV(caminho) : await lerXLSX(caminho)
  return { nref: nrefDe(caminho), itens }
}
