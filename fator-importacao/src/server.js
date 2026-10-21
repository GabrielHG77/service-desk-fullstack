import { createServer } from 'node:http'
import { readFile, writeFile, mkdir, stat } from 'node:fs/promises'
import { existsSync } from 'node:fs'
import { join, dirname, basename, isAbsolute } from 'node:path'
import { spawn } from 'node:child_process'
import { consolidar, varrer, FAIXA } from './consolidar.js'
import { abrir, nrefsExistentes } from './planilha.js'
import { fmtBRL, fmtPct } from './util.js'
import UI from './ui.gen.js'

const PADRAO = { raiz: 'Y:\\BR10\\Procurement\\Public\\Teste fator importação\\', planilha: '' }

// Rodando como .exe portátil, a config fica ao lado dele; rodando por node, no cwd.
const APP_DIR = /^(node|bun)(\.exe)?$/i.test(basename(process.execPath)) ? process.cwd() : dirname(process.execPath)
const argRaiz = process.argv.find((a) => a.startsWith('--raiz='))?.slice(7)
const CONFIG = process.argv.find((a) => a.startsWith('--config='))?.slice(9) ?? join(APP_DIR, 'config.json')

// O arquivo guarda só o que o usuário escolheu. `planilha` ausente significa
// "derivar da raiz": assim trocar a raiz não deixa a planilha apontando para a
// pasta antiga.
async function lerConfigBruta() {
  let cfg = { ...PADRAO }
  try { cfg = { ...cfg, ...JSON.parse(await readFile(CONFIG, 'utf8')) } } catch { /* primeira execução */ }
  if (argRaiz) cfg.raiz = argRaiz
  return cfg
}

async function lerConfig() {
  const cfg = await lerConfigBruta()
  if (!cfg.planilha) cfg.planilha = join(cfg.raiz, 'ConsolidadoPath.xlsx')
  else if (!isAbsolute(cfg.planilha)) cfg.planilha = join(cfg.raiz, cfg.planilha)
  return cfg
}

const gravarConfig = (cfg) => writeFile(CONFIG, JSON.stringify(cfg, null, 2), 'utf8')

const historico = [] // { id, quando, dryRun, resultado, texto }

async function estado() {
  const cfg = await lerConfig()
  let pedidos = [], erro = null
  const dados = join(cfg.raiz, 'dados')
  try {
    // Caminho errado não pode parecer "nada a fazer": a tela ficaria vazia.
    await stat(dados)
    pedidos = await varrer(cfg.raiz)
  } catch (e) {
    erro = e.code === 'ENOENT' ? `a pasta ${dados} não existe` : `não deu para ler ${dados}: ${e.message}`
  }

  let jaNaPlanilha = { AIR: new Set(), SEA: new Set() }
  try {
    const wb = await abrir(cfg.planilha)
    jaNaPlanilha = { AIR: nrefsExistentes(wb, 'AIR'), SEA: nrefsExistentes(wb, 'SEA') }
  } catch (e) { erro = (erro ? erro + ' · ' : '') + `planilha de destino: ${e.message}` }

  const porMes = new Map()
  for (const p of pedidos) {
    const m = porMes.get(p.mes) ?? { mes: p.mes, total: 0, existentes: 0, novos: 0 }
    m.total++
    if (jaNaPlanilha[p.modal].has(p.nref)) m.existentes++; else m.novos++
    porMes.set(p.mes, m)
  }
  return {
    cfg, erro, faixa: FAIXA,
    meses: [...porMes.values()].sort((a, b) => a.mes.localeCompare(b.mes)),
    historico: historico.map(({ id, quando, dryRun, resultado }) => ({
      id, quando, dryRun,
      adicionados: resultado.adicionados.length, pulados: resultado.pulados.length, erros: resultado.erros.length,
    })).reverse(),
  }
}

function relatorio(r, cfg, quando) {
  const L = [`Execução ${quando}${r.dryRun ? ' (conferência, nada gravado)' : ''}`,
    `Pasta de dados: ${join(cfg.raiz, 'dados')}`, `Planilha: ${cfg.planilha}`, '',
    `Adicionados: ${r.adicionados.length} · Pulados (já na planilha): ${r.pulados.length} · Pastas com erro: ${r.erros.length}`, '']
  for (const m of ['AIR', 'SEA']) {
    const t = r.totais[m]
    if (!t.pedidos) continue
    L.push(`${m}: ${t.pedidos} pedidos, ${t.linhas} linhas de material, CIF R$ ${fmtBRL(t.cif)}, ` +
      `custo R$ ${fmtBRL(t.total)}, fator ponderado ${fmtPct(t.fatorPonderado)}, ` +
      `fator logístico ${fmtPct(t.fatorLogistico)}`)
  }
  if (r.erros.length) {
    L.push('', 'Pastas que falharam:')
    for (const e of r.erros) L.push(`  ${e.pasta} -> ${e.motivo}`)
  }
  const falhasPdf = r.adicionados.flatMap((p) => p.pdfs.falhas.map((f) => `  ${p.nref}: ${f.arquivo} -> ${f.motivo}`))
  if (falhasPdf.length) L.push('', 'PDFs que não abriram (pedido gravado sem eles):', ...falhasPdf)
  if (r.avisos.length) {
    L.push('', `Fora da faixa ${FAIXA.min * 100}%–${FAIXA.max * 100}% (fator alto em pedido pequeno é esperado):`)
    for (const a of r.avisos) L.push(`  ${a.nref} [${a.modal}] CIF R$ ${fmtBRL(a.cif)} -> ${fmtPct(a.fator)}`)
  }
  return L.join('\n')
}

async function rodar(body) {
  const cfg = await lerConfig()
  const quando = new Date().toISOString().replace('T', ' ').slice(0, 19)
  const resultado = await consolidar({
    raiz: cfg.raiz, planilha: cfg.planilha,
    meses: body.meses ?? [], dryRun: !!body.dryRun, reprocessar: !!body.reprocessar,
  })
  const texto = relatorio(resultado, cfg, quando)
  const id = String(historico.length + 1)
  historico.push({ id, quando, dryRun: resultado.dryRun, resultado, texto })
  try {
    await mkdir(join(APP_DIR, 'logs'), { recursive: true })
    await writeFile(join(APP_DIR, 'logs', `run-${quando.replace(/[: ]/g, '-')}.txt`), texto, 'utf8')
  } catch { /* log em arquivo é um extra, não pode derrubar a execução */ }
  return { id, quando, ...resultado, texto }
}

async function json(req) {
  const chunks = []
  for await (const c of req) chunks.push(c)
  return chunks.length ? JSON.parse(Buffer.concat(chunks).toString('utf8')) : {}
}

const server = createServer(async (req, res) => {
  const send = (code, obj) => {
    res.writeHead(code, { 'content-type': 'application/json; charset=utf-8' })
    res.end(JSON.stringify(obj))
  }
  try {
    const url = new URL(req.url, 'http://localhost')
    if (url.pathname === '/') {
      res.writeHead(200, { 'content-type': 'text/html; charset=utf-8' })
      return res.end(UI)
    }
    if (url.pathname === '/api/estado') return send(200, await estado())
    if (url.pathname === '/api/config' && req.method === 'POST') {
      const body = await json(req)
      const cfg = await lerConfigBruta()
      const dado = (k) => typeof body[k] === 'string' && body[k].trim()
      if (dado('raiz')) {
        cfg.raiz = body.raiz.trim()
        if (!dado('planilha')) delete cfg.planilha // volta a derivar da raiz nova
      }
      if (dado('planilha')) cfg.planilha = body.planilha.trim()
      await gravarConfig(cfg)
      return send(200, await estado())
    }
    if (url.pathname === '/api/rodar' && req.method === 'POST') return send(200, await rodar(await json(req)))
    if (url.pathname.startsWith('/api/log/')) {
      const h = historico.find((x) => x.id === url.pathname.slice(9))
      return h ? send(200, h) : send(404, { erro: 'execução não encontrada' })
    }
    send(404, { erro: 'rota desconhecida' })
  } catch (e) {
    send(500, { erro: e.message })
  }
})

const navegadorPadrao = (url) => spawn('cmd', ['/c', 'start', '', url], { detached: true, stdio: 'ignore' }).unref()

// Janela própria: sem barra de endereço, sem abas, ícone próprio na barra de
// tarefas. Tem que parecer aplicativo, não site aberto no navegador.
function abrirApp(url) {
  if (process.argv.includes('--no-open')) return
  if (process.platform !== 'win32') {
    const cmd = process.platform === 'darwin' ? ['open', [url]] : ['xdg-open', [url]]
    try { spawn(cmd[0], cmd[1], { detached: true, stdio: 'ignore' }).unref() } catch { /* abre na mão */ }
    return
  }
  // LOCALAPPDATA entra na busca porque numa máquina sem admin o Edge e o Chrome
  // costumam estar instalados por usuário, fora de Program Files. Mesma ordem
  // que o mqtt-panel usa nesta empresa.
  const exe = ['PROGRAMFILES(X86)', 'PROGRAMFILES', 'LOCALAPPDATA']
    .flatMap((v) => ['Microsoft\\Edge\\Application\\msedge.exe', 'Google\\Chrome\\Application\\chrome.exe']
      .map((rel) => (process.env[v] ? join(process.env[v], rel) : '')))
    .find((p) => p && existsSync(p))

  if (!exe) return navegadorPadrao(url) // sem Edge nem Chrome: pelo menos abre

  // Perfil só do app, fora do OneDrive: não mistura com a sessão do usuário e
  // garante uma janela de verdade em vez de uma aba na janela já aberta.
  const perfil = join(process.env.LOCALAPPDATA || APP_DIR, 'FatorImportacao', 'janela')
  const janela = spawn(exe, [`--app=${url}`, '--window-size=1180,920',
    `--user-data-dir=${perfil}`, '--no-first-run', '--no-default-browser-check'], { stdio: 'ignore' })
  janela.on('error', () => navegadorPadrao(url))
  // Fechou a janela, fechou o app: nada de processo órfão segurando a planilha.
  janela.on('exit', () => process.exit(0))
}

const PORTA = Number(process.env.PORT) || 7317
const URL_APP = `http://127.0.0.1:${PORTA}/`

// Segunda execução não sobe outro servidor: traz a janela da que já está aberta.
server.on('error', (e) => {
  if (e.code !== 'EADDRINUSE') throw e
  console.log('O app já está aberto; abrindo a janela da instância existente.')
  abrirApp(URL_APP)
})

server.listen(PORTA, '127.0.0.1', () => {
  console.log(`Fator de importação em ${URL_APP}`)
  console.log(`Configuração: ${CONFIG}`)
  abrirApp(URL_APP)
})

export { server }
