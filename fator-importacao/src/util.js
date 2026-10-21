// Conversões pt-BR explícitas: a máquina alvo roda em inglês, nada aqui pode
// depender de locale do sistema (já produziu data trocada e número mal lido).

export function parseBR(v) {
  if (typeof v === 'number') return Number.isFinite(v) ? v : 0
  if (v == null) return 0
  // No CSV legado o separador decimal é TAB e o de milhar é ponto.
  const t = String(v).replace(/\t/g, ',').replace(/[^\d,.-]/g, '')
  if (!/\d/.test(t)) return 0
  const n = Number(t.replace(/\./g, '').replace(',', '.'))
  return Number.isFinite(n) ? n : 0
}

// Aceita Date (vindo do .xlsx, já sem ambiguidade), "03/08/2026", "03/08/26"
// e "03/08/2026 00:00:00". Sempre dia/mês/ano, nunca mês/dia.
export function parseDateBR(v) {
  if (v instanceof Date) return Number.isNaN(v.getTime()) ? null : v
  const m = String(v ?? '').trim().match(/^(\d{1,2})[/\-.](\d{1,2})[/\-.](\d{2,4})/)
  if (!m) return null
  const [, d, mo, y] = m
  const ano = y.length === 2 ? 2000 + Number(y) : Number(y)
  const dia = Number(d), mes = Number(mo)
  const dt = new Date(Date.UTC(ano, mes - 1, dia))
  return dt.getUTCDate() === dia && dt.getUTCMonth() === mes - 1 ? dt : null
}

export function fmtDateBR(d) {
  if (!(d instanceof Date)) return ''
  const p = (n) => String(n).padStart(2, '0')
  return `${p(d.getUTCDate())}/${p(d.getUTCMonth() + 1)}/${d.getUTCFullYear()}`
}

// Cabeçalhos variam entre .csv e .xlsx (acento, caixa, espaço sobrando).
export function normKey(s) {
  return String(s ?? '').normalize('NFD').replace(/[̀-ͯ]/g, '')
    .trim().toLowerCase().replace(/\s+/g, ' ')
}

export function fmtBRL(n) {
  return (Number(n) || 0).toLocaleString('pt-BR', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
}

export function fmtPct(n) {
  return ((Number(n) || 0) * 100).toFixed(1).replace('.', ',') + '%'
}
