import { readdir } from 'node:fs/promises'
import { join, extname } from 'node:path'
import { custosDoPedido } from './pdf.js'
import { lerMateriais, nrefDe } from './materiais.js'
import * as planilha from './planilha.js'

const FAIXA = { min: 0.24, max: 0.30 }

async function subpastas(dir) {
  try {
    return (await readdir(dir, { withFileTypes: true })).filter((d) => d.isDirectory()).map((d) => d.name).sort()
  } catch (e) {
    if (e.code === 'ENOENT' || e.code === 'ENOTDIR') return []
    throw e
  }
}

// dados\<mês>\<modal>\<pedido>\  — modal em qualquer caixa.
export async function varrer(raiz) {
  const base = join(raiz, 'dados')
  const pedidos = []
  for (const mes of await subpastas(base)) {
    for (const modalDir of await subpastas(join(base, mes))) {
      const modal = modalDir.toUpperCase()
      if (modal !== 'AIR' && modal !== 'SEA') continue
      for (const nome of await subpastas(join(base, mes, modalDir))) {
        pedidos.push({ mes, modal, nref: nome, dir: join(base, mes, modalDir, nome) })
      }
    }
  }
  return pedidos
}

async function arquivoDeMateriais(dir) {
  const nomes = await readdir(dir)
  const cand = nomes.filter((f) => ['.xlsx', '.csv'].includes(extname(f).toLowerCase()) && !f.startsWith('~$'))
  if (!cand.length) throw new Error('nenhum arquivo de materiais (.xlsx/.csv) na pasta')
  if (cand.length > 1) throw new Error(`mais de um arquivo de materiais: ${cand.join(', ')}`)
  return join(dir, cand[0])
}

export async function processarPedido(p) {
  const arq = await arquivoDeMateriais(p.dir)
  const { itens } = await lerMateriais(arq)
  if (!itens.length) throw new Error('arquivo de materiais sem linhas com Descricao')
  const custos = await custosDoPedido(p.dir)

  const somas = itens.reduce((a, it) => ({
    qtde: a.qtde + it.qtde, cif: a.cif + it.cif, ii: a.ii + it.ii, siscomex: a.siscomex + it.siscomex,
  }), { qtde: 0, cif: 0, ii: 0, siscomex: 0 })

  if (!(somas.cif > 0)) throw new Error('CIF Total somado é zero — conversão de número suspeita')

  // Só II e Tx. Siscomex. PIS/COFINS/ICMS são recuperáveis e ficam de fora.
  const logistico = custos.rodoviario + custos.marinha + custos.origem +
    custos.armazenagem + custos.honorarios + custos.freteIntl
  const total = somas.ii + somas.siscomex + logistico

  return {
    ...p,
    nref: nrefDe(arq), // o nome do arquivo manda, não o da pasta
    itens, custos, somas, total,
    fator: total / somas.cif,
    fatorLogistico: logistico / somas.cif,
    exportador: itens.find((i) => i.exportador)?.exportador ?? '',
    pdfs: { usados: custos.usados, ignorados: custos.ignorados, falhas: custos.falhas },
  }
}

export async function consolidar({ raiz, planilha: destino, meses, dryRun = false, reprocessar = false }) {
  const alvo = new Set(meses ?? [])
  const todos = await varrer(raiz)
  const wb = await planilha.abrir(destino)
  const existentes = { AIR: planilha.nrefsExistentes(wb, 'AIR'), SEA: planilha.nrefsExistentes(wb, 'SEA') }

  const candidatos = todos.filter((p) => alvo.has(p.mes))

  if (reprocessar) {
    for (const modal of ['AIR', 'SEA']) {
      const nrefs = candidatos.filter((p) => p.modal === modal).map((p) => p.nref)
      planilha.removerLinhas(wb, modal, planilha.linhasDeNrefs(wb, modal, nrefs))
      existentes[modal] = planilha.nrefsExistentes(wb, modal)
    }
  }

  const adicionados = [], pulados = [], erros = []
  for (const p of candidatos) {
    if (existentes[p.modal].has(p.nref)) { pulados.push(p.nref); continue }
    try {
      const pedido = await processarPedido(p)
      if (existentes[p.modal].has(pedido.nref)) { pulados.push(pedido.nref); continue }
      planilha.gravarPedido(wb, pedido)
      existentes[p.modal].add(pedido.nref)
      adicionados.push(pedido)
    } catch (e) {
      erros.push({ pasta: p.dir, mes: p.mes, modal: p.modal, nref: p.nref, motivo: e.message })
    }
  }

  const precisaGravar = adicionados.length > 0 || reprocessar
  if (!dryRun && precisaGravar) await planilha.salvar(wb, destino)

  return {
    dryRun, gravado: !dryRun && precisaGravar,
    adicionados: adicionados.map(resumo), pulados, erros,
    avisos: adicionados.filter((p) => p.fator < FAIXA.min || p.fator > FAIXA.max).map(resumo),
    totais: agregar(adicionados),
  }
}

const resumo = (p) => ({
  nref: p.nref, mes: p.mes, modal: p.modal, cif: p.somas.cif, total: p.total,
  fator: p.fator, fatorLogistico: p.fatorLogistico, itens: p.itens.length,
  pdfs: p.pdfs,
})

// Fator ponderado: soma dos custos / soma dos CIF. A média simples dá o mesmo
// peso a um pedido de R$ 4,5 mil e a um de R$ 925 mil, e engana.
function agregar(pedidos) {
  const out = {}
  for (const modal of ['AIR', 'SEA']) {
    const ps = pedidos.filter((p) => p.modal === modal)
    const cif = ps.reduce((a, p) => a + p.somas.cif, 0)
    const total = ps.reduce((a, p) => a + p.total, 0)
    const imposto = ps.reduce((a, p) => a + p.somas.ii + p.somas.siscomex, 0)
    out[modal] = {
      pedidos: ps.length,
      linhas: ps.reduce((a, p) => a + p.itens.length, 0),
      cif, total,
      fatorPonderado: cif ? total / cif : 0,
      fatorLogistico: cif ? (total - imposto) / cif : 0,
    }
  }
  return out
}

export { FAIXA }
