import { readFile, readdir } from 'node:fs/promises'
import { join } from 'node:path'
import { parseBR } from './util.js'

// Os regex do spec usam lookbehind de tamanho variável. Aqui viram grupo de
// captura: mesma semântica (acha o rótulo, pega o primeiro número depois dele)
// e sem depender de como o motor avalia lookbehind para trás. Corpo idêntico.
// Sensíveis a maiúsculas, de propósito.
const RX = {
  honorarios: /Despacho Importação[\s\S]*?SUBTOTAL TRIBUTÁVEL[^\d]*([\d.]+,\d{2})/,
  armazenagem: /Armazenagem - destino[^\d]*([\d.]+,\d{2})/g,
  marinha: /Marinha Mercante[^\d]*([\d.]+,\d{2})/,
  rodoviario: /Transporte Rodoviário[^\d]*([\d.]+,\d{2})/,
  freteIntl: /SUBTOTAL NÃO TRIBUTÁVEL[^\d]*([\d.]+,\d{2})/,
  origem: /Taxa Administrativa[\s\S]*?SUBTOTAL TRIBUTÁVEL[^\d]*([\d.]+,\d{2})/,
}

const MARCA_NACIONAL = 'Armazenagem - destino'
const MARCA_DESPACHANTE = 'DESPACHANTES ADUANEIROS'

const ZERO = () => ({ honorarios: 0, armazenagem: 0, marinha: 0, rodoviario: 0, freteIntl: 0, origem: 0 })

function um(rx, texto) {
  const m = texto.match(rx)
  return m ? parseBR(m[1]) : 0
}

// Reconstrói linhas a partir das posições dos fragmentos: os regex dependem da
// ordem em que rótulo e número aparecem, e getTextContent devolve fora de ordem.
export function layoutTexto(itens) {
  const linhas = []
  for (const it of itens) {
    if (!it.str) continue
    const x = it.transform[4], y = it.transform[5]
    let linha = linhas.find((l) => Math.abs(l.y - y) <= 2)
    if (!linha) { linha = { y, partes: [] }; linhas.push(linha) }
    linha.partes.push({ x, fim: x + (it.width || 0), s: it.str })
  }
  linhas.sort((a, b) => b.y - a.y)
  return linhas.map((l) => {
    l.partes.sort((a, b) => a.x - b.x)
    let s = '', fimAnterior = null
    for (const p of l.partes) {
      // Sem folga = mesma palavra quebrada em fragmentos; com folga = espaço.
      if (fimAnterior !== null && p.x - fimAnterior > 0.8) s += ' '
      s += p.s
      fimAnterior = p.fim
    }
    return s.replace(/[ \t]+/g, ' ').trim()
  }).filter(Boolean).join('\n')
}

export async function pdfTexto(caminho) {
  const { getDocument } = await import('pdfjs-dist/legacy/build/pdf.mjs')
  const data = new Uint8Array(await readFile(caminho))
  const doc = await getDocument({ data, isEvalSupported: false, disableFontFace: true, useSystemFonts: false }).promise
  try {
    const paginas = []
    for (let p = 1; p <= doc.numPages; p++) {
      const page = await doc.getPage(p)
      paginas.push(layoutTexto((await page.getTextContent()).items))
      page.cleanup()
    }
    return paginas.join('\n')
  } finally {
    await doc.destroy()
  }
}

// Nota nacional (tem `Armazenagem - destino`) traz os custos de destino;
// a internacional traz frete internacional e despesas na origem.
export function extrairCustos(texto) {
  const c = ZERO()
  if (texto.includes(MARCA_DESPACHANTE)) return { ...c, ignorada: 'despachante' }
  if (texto.includes(MARCA_NACIONAL)) {
    c.honorarios = um(RX.honorarios, texto)
    // A única que repete: uma cobrança por período de permanência. Somar todas.
    for (const m of texto.matchAll(RX.armazenagem)) c.armazenagem += parseBR(m[1])
    c.marinha = um(RX.marinha, texto)
    c.rodoviario = um(RX.rodoviario, texto)
    return { ...c, tipo: 'nacional' }
  }
  c.freteIntl = um(RX.freteIntl, texto)
  c.origem = um(RX.origem, texto)
  return { ...c, tipo: 'internacional' }
}

// Extratos de DI/DUIMP repetem valores já contabilizados: contagem dobrada.
export const ehExtrato = (nome) => /extrato/i.test(nome)

export async function custosDoPedido(dir) {
  const arquivos = (await readdir(dir)).filter((f) => f.toLowerCase().endsWith('.pdf') && !ehExtrato(f)).sort()
  const total = ZERO()
  const usados = [], ignorados = [], falhas = []
  for (const f of arquivos) {
    let c
    try {
      c = extrairCustos(await pdfTexto(join(dir, f)))
    } catch (e) {
      falhas.push({ arquivo: f, motivo: e.message })
      continue
    }
    if (c.ignorada) { ignorados.push(f); continue }
    for (const k of Object.keys(total)) total[k] += c[k]
    usados.push({ arquivo: f, tipo: c.tipo })
  }
  return { ...total, usados, ignorados, falhas }
}
