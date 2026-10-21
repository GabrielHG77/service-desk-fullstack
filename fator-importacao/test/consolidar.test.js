import { test } from 'node:test'
import assert from 'node:assert/strict'
import { mkdtemp, mkdir, writeFile, rm, stat } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import ExcelJS from 'exceljs'
import { consolidar, varrer } from '../src/consolidar.js'
import { lerMateriais } from '../src/materiais.js'
import { CABECALHO, LAYOUT } from '../src/planilha.js'

// PDF mínimo com texto não comprimido: o pdfjs lê e o layout() reconstrói.
function pdfComLinhas(linhas) {
  const esc = (s) => s.replace(/[\\()]/g, (c) => '\\' + c)
  const txt = linhas.map((l, i) => `BT /F1 10 Tf 50 ${740 - i * 18} Td (${esc(l)}) Tj ET`).join('\n')
  const objs = [
    '<< /Type /Catalog /Pages 2 0 R >>',
    '<< /Type /Pages /Kids [3 0 R] /Count 1 >>',
    '<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>',
    `<< /Length ${Buffer.byteLength(txt, 'latin1')} >>\nstream\n${txt}\nendstream`,
    '<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>',
  ]
  let pdf = '%PDF-1.4\n'
  const off = []
  objs.forEach((o, i) => { off.push(pdf.length); pdf += `${i + 1} 0 obj\n${o}\nendobj\n` })
  const xref = pdf.length
  pdf += `xref\n0 ${objs.length + 1}\n0000000000 65535 f \n` +
    off.map((o) => String(o).padStart(10, '0') + ' 00000 n \n').join('') +
    `trailer\n<< /Size ${objs.length + 1} /Root 1 0 R >>\nstartxref\n${xref}\n%%EOF`
  return Buffer.from(pdf, 'latin1')
}

// `Pedido` aparece duas vezes e o cabeçalho termina com colunas sem nome.
const CAB_CSV = ['Nr.DI/Duimp', 'Data DI/Duimp', 'Fob', 'Pedido', 'Descricao', 'Qtde', 'Unidade',
  'CIF Total', 'II Recolhido', '% II', 'PIS Recolhido', 'ICMS', 'Tx. Siscomex', 'Exportador', 'Pedido', '', '']

// Campos com TAB como decimal e espaço à esquerda, como o legado exporta.
function linhaCSV(c) {
  return [c.di, c.data, '  500\t00', c.pedido, c.descricao, c.qtde, 'PC',
    c.cif, c.ii, '18,00', '  99\t99', '  88\t88', c.siscomex, c.exportador, c.pedido, '', ''].join(';')
}

async function arvore() {
  const raiz = await mkdtemp(join(tmpdir(), 'fator-'))
  const sea = join(raiz, 'dados', '2026-08', 'SEA', 'pedido-sea')
  const air = join(raiz, 'dados', '2026-08', 'air', 'pedido-air') // modal em minúscula
  await mkdir(sea, { recursive: true })
  await mkdir(air, { recursive: true })

  await writeFile(join(sea, 'B26TESTE01.CSV'), [CAB_CSV.join(';'),
    linhaCSV({ di: '26/0000001-1', data: '03/08/26', pedido: 'PO-1', descricao: 'PARAFUSO', qtde: '10', cif: '  668\t14', ii: '  120\t27', siscomex: '  154\t23', exportador: 'ACME GMBH' }),
    linhaCSV({ di: '26/0000001-1', data: '03/08/26', pedido: 'PO-1', descricao: 'PORCA', qtde: '5', cif: '5.232\t85', ii: '1.046\t57', siscomex: '  0\t00', exportador: 'ACME GMBH' }),
    linhaCSV({ di: '', data: '', pedido: '', descricao: '', qtde: '', cif: '', ii: '', siscomex: '', exportador: '' }), // linha fantasma do fim
    '',
  ].join('\r\n'), 'latin1')

  await writeFile(join(sea, 'SSZ2602015.pdf'), pdfComLinhas([
    'Despacho Importação',
    'SUBTOTAL TRIBUTÁVEL 1.417,79',
    'Armazenagem - destino 3.203,43',
    'Armazenagem - destino 338,63',
    'Armazenagem - destino 4.828,19',
    'Marinha Mercante 500,00',
    'Transporte Rodoviário 200,00',
  ]))
  await writeFile(join(sea, 'INTL-001.pdf'), pdfComLinhas([
    'SUBTOTAL NÃO TRIBUTÁVEL 1.000,00',
    'Taxa Administrativa 50,00',
    'SUBTOTAL TRIBUTÁVEL 273,50',
  ]))
  // Estes dois têm que ser descartados: contagem dobrada.
  await writeFile(join(sea, 'Extrato DI 26-0001.pdf'), pdfComLinhas(['Armazenagem - destino 9.999,99']))
  await writeFile(join(sea, 'desp.pdf'), pdfComLinhas(['ACME DESPACHANTES ADUANEIROS LTDA', 'Armazenagem - destino 7.777,77']))

  const wb = new ExcelJS.Workbook()
  const ws = wb.addWorksheet('Dados')
  ws.addRow(['Nr.DI/Duimp', 'Data DI/Duimp', 'Pedido', 'Descrição', 'Qtde', 'Unidade', 'CIF Total', 'II Recolhido', 'Tx. Siscomex', 'Exportador', 'NCM'])
  ws.addRow(['26/0000002-2', new Date(Date.UTC(2026, 3, 6)), 'PO-2', 'ROLAMENTO', 2, 'PC', 10000, 1800, 154.23, 'BETA AG', '8482.10.10'])
  ws.addRow([null, null, null, '', null, null, null, null, null, null, null])
  await wb.xlsx.writeFile(join(air, 'B26TESTE02.xlsx'))
  await writeFile(join(air, 'nota.pdf'), pdfComLinhas([
    'Despacho Importação', 'SUBTOTAL TRIBUTÁVEL 1.417,79',
    'Armazenagem - destino 100,00', 'Transporte Rodoviário 50,00',
  ]))

  return { raiz, planilha: join(raiz, 'ConsolidadoPath.xlsx') }
}

test('ponta a ponta: varre, consolida, não duplica e não grava em dry-run', async (t) => {
  const { raiz, planilha } = await arvore()
  t.after(() => rm(raiz, { recursive: true, force: true }))

  const pedidos = await varrer(raiz)
  assert.equal(pedidos.length, 2)
  assert.deepEqual(pedidos.map((p) => p.modal).sort(), ['AIR', 'SEA']) // modal em minúscula também conta

  const dry = await consolidar({ raiz, planilha, meses: ['2026-08'], dryRun: true })
  assert.equal(dry.erros.length, 0, JSON.stringify(dry.erros))
  assert.equal(dry.adicionados.length, 2)
  await assert.rejects(stat(planilha), 'dry-run não pode tocar a planilha')

  const sea = dry.adicionados.find((p) => p.modal === 'SEA')
  assert.equal(sea.nref, 'B26TESTE01') // nome do arquivo, não da pasta
  assert.equal(sea.itens, 2)           // linha fantasma pulada
  // CIF 668,14 + 5.232,85 = 5.900,99 ; II 120,27 + 1.046,57 ; Siscomex 154,23
  // Custos: armazenagem 8.370,25 (as três cobranças) + honorários 1.417,79
  //       + marinha 500 + rodoviário 200 + frete 1.000 + origem 273,50
  const logistico = 8370.25 + 1417.79 + 500 + 200 + 1000 + 273.50
  assert.equal(+sea.total.toFixed(2), +(120.27 + 1046.57 + 154.23 + logistico).toFixed(2))
  assert.equal(+sea.cif.toFixed(2), 5900.99)
  assert.equal(+(sea.total - sea.cif * sea.fator).toFixed(6), 0)
  assert.equal(+(sea.fatorLogistico * sea.cif).toFixed(2), +logistico.toFixed(2))

  const r1 = await consolidar({ raiz, planilha, meses: ['2026-08'] })
  assert.equal(r1.adicionados.length, 2)
  assert.equal(r1.gravado, true)
  await assert.doesNotReject(stat(planilha))

  // Reexecução: a N/Ref. bloqueia regravação.
  const r2 = await consolidar({ raiz, planilha, meses: ['2026-08'] })
  assert.equal(r2.adicionados.length, 0)
  assert.deepEqual(r2.pulados.sort(), ['B26TESTE01', 'B26TESTE02'])

  const wb = new ExcelJS.Workbook()
  await wb.xlsx.readFile(planilha)
  const wsSea = wb.getWorksheet('SEA')
  assert.deepEqual(wsSea.getRow(1).values.slice(1), CABECALHO.SEA)
  assert.equal(wsSea.actualRowCount, 4) // cabeçalho + 2 materiais + 1 total
  assert.equal(wb.getWorksheet('AIR').actualRowCount, 3)

  // Linha de material: Mercadoria preenchida, Modal vazio. Total: o contrário.
  const mat = wsSea.getRow(2), tot = wsSea.getRow(4)
  assert.equal(mat.getCell(8).value, 'PARAFUSO')
  assert.ok(!mat.getCell(7).value)
  assert.ok(!tot.getCell(8).value)
  assert.equal(tot.getCell(7).value, 'SEA')
  assert.equal(tot.getCell(6).value, 'ACME GMBH')
  assert.equal(+tot.getCell(11).value.toFixed(2), 5900.99)
  assert.equal(tot.getCell(LAYOUT.SEA.armazenagem).value, 8370.25)
  assert.equal(tot.getCell(LAYOUT.SEA.honorarios).value, 1417.79)
  assert.equal(tot.getCell(LAYOUT.SEA.origem).value, 273.50)
  // Outros fica sempre vazia: a analista digita ali à mão.
  assert.equal(tot.getCell(LAYOUT.SEA.outros).value, null)
  assert.equal(tot.getCell(LAYOUT.SEA.total).value.formula, 'SUM(L4:T4)')
  assert.equal(tot.getCell(LAYOUT.SEA.pct).value.formula, 'IF(K4=0,"",U4/K4)')
  // Data gravada como 3 de agosto, não 8 de março.
  assert.equal(mat.getCell(2).value.getUTCDate(), 3)
  assert.equal(mat.getCell(2).value.getUTCMonth(), 7)
  assert.equal(mat.getCell(2).numFmt, 'dd/mm/yyyy')

  const air = wb.getWorksheet('AIR').getRow(3)
  assert.equal(air.getCell(7).value, 'AIR')
  assert.equal(air.getCell(LAYOUT.AIR.armazenagem).value, 100)
  assert.equal(air.getCell(LAYOUT.AIR.total).value.formula, 'SUM(L3:S3)')
  // 06/04/2026 do .xlsx continua 6 de abril.
  assert.equal(wb.getWorksheet('AIR').getRow(2).getCell(2).value.getUTCMonth(), 3)

  // Backup antes de gravar por cima; reprocessar apaga e regrava sem duplicar.
  await consolidar({ raiz, planilha, meses: ['2026-08'], reprocessar: true })
  await assert.doesNotReject(stat(planilha.replace(/\.xlsx$/, '.bak.xlsx')))
  const wb2 = new ExcelJS.Workbook()
  await wb2.xlsx.readFile(planilha)
  assert.equal(wb2.getWorksheet('SEA').actualRowCount, 4)
})

test('CSV: coluna Pedido repetida e colunas sem nome não quebram a leitura', async (t) => {
  const { raiz } = await arvore()
  t.after(() => rm(raiz, { recursive: true, force: true }))
  const { nref, itens } = await lerMateriais(join(raiz, 'dados', '2026-08', 'SEA', 'pedido-sea', 'B26TESTE01.CSV'))
  assert.equal(nref, 'B26TESTE01')
  assert.equal(itens.length, 2)
  assert.equal(itens[0].pedido, 'PO-1')
  assert.equal(itens[0].cif, 668.14)
  assert.equal(itens[0].exportador, 'ACME GMBH')
})

test('pasta sem arquivo de materiais vira erro registrado, não derruba a execução', async (t) => {
  const { raiz, planilha } = await arvore()
  t.after(() => rm(raiz, { recursive: true, force: true }))
  await mkdir(join(raiz, 'dados', '2026-08', 'SEA', 'pedido-vazio'), { recursive: true })
  const r = await consolidar({ raiz, planilha, meses: ['2026-08'], dryRun: true })
  assert.equal(r.adicionados.length, 2)
  assert.equal(r.erros.length, 1)
  assert.match(r.erros[0].motivo, /nenhum arquivo de materiais/)
})
