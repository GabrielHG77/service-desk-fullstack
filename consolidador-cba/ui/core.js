// Consolidação da aba Data: o mesmo que a consulta do Power Query (ProcessarArquivo + Combinado + Reordered Columns).
// Sem tela e sem acesso a disco: recebe os bytes de cada Excel e devolve a tabela pronta. Roda no app e no Node (testes).
(function (root) {
"use strict";

const DEFAULT_COLUMNS = [
  "Sales Doc.", "Item", "Created On", "Arquivo Origem", "SOrg.", "Material", "Order Quantity",
  "AT_MCP2_CTR_CRD_TYP", "AT_MCP2_PIT_M", "AT_MCP2_RD_POW_M", "AT_MCP2_MOT_POW_M",
  "AT_MCP2_FKT_ANG", "AT_MCP2_GEA_01_RAT", "AT_MCP2_BTF_LEN_M",
  "AT_MCP2_RD_VOT", "CC_VBAP_MATNR", "AT_MCP2_RD_INT_TYP", "AT_MCP2_SEN_SPLR_01",
  "AT_MCP2_BLT_TYP", "CC_VBAP_KWMENG", "AT_MCP2_MOD_LEN_M"
];
const BASE_COLS = ["Sales Doc.", "Item", "Created On", "Arquivo Origem", "SOrg.", "Material", "Order Quantity"];
const NUM_COLS = new Set(["Sales Doc.", "Item", "Order Quantity"]);
const SRC = {
  salesDoc: "Sales Doc.", item: "Item", createdOn: "Created On", sorg: "SOrg.", material: "Material",
  qty: "Order Quantity", charNo: "Internal char no.", charVal: "Characteristic Value", valFrom: "Val from"
};
const EPOCH = Date.UTC(1899, 11, 30);

function defaultSettings() {
  return { sheet: "Data", decimal: ",", columns: DEFAULT_COLUMNS.slice(), extras: false, repeated: "first" };
}

// ---------- conversões ----------

const norm = (s) => String(s ?? "").trim().replace(/\s+/g, " ").toLowerCase();
const blank = (v) => v == null || (typeof v === "string" && v.trim() === "");
const userErr = (msg) => Object.assign(new Error(msg), { user: true });

function fmtNum(n, dec) {
  const s = String(Number(n.toPrecision(15)));
  return dec === "," ? s.replace(".", ",") : s;
}

// Lê número escrito como texto ("7,5", "1.000", "12.5"), usando o separador escolhido.
function parseNum(v, dec) {
  if (typeof v === "number") return Number.isFinite(v) ? v : null;
  if (v == null || typeof v === "boolean") return null;
  let s = String(v).trim().replace(/[\s ]/g, "");
  if (!s) return null;
  // Exportação crua do SAP: " 1,2340000000000000E+01" (notação científica, com vírgula ou ponto).
  const sci = s.match(/^(-?\d+)(?:[.,](\d+))?[eE]([+-]?\d+)$/);
  if (sci) return Number(`${sci[1]}.${sci[2] || "0"}e${sci[3]}`);
  const th = dec === "," ? "." : ",";
  if (new RegExp("^-?\\d{1,3}(\\" + th + "\\d{3})+(\\" + dec + "\\d+)?$").test(s)) s = s.split(th).join("");
  s = s.replace(dec, ".");
  if (/^-?\d+(\.\d+)?$/.test(s)) return Number(s);
  s = s.replace(",", ".");
  if (/^-?\d+(\.\d+)?$/.test(s)) return Number(s);
  return null;
}

function textOf(v, dec) {
  if (blank(v)) return null;
  if (typeof v === "number") return fmtNum(v, dec);
  return String(v).trim();
}

// Sales Doc. e Item viram inteiros, como no Int64.Type da consulta ("0012345" -> 12345).
function keyNum(v, dec) {
  if (blank(v)) return null;
  if (typeof v === "number") return v;
  const s = String(v).trim();
  if (/^\d+$/.test(s)) return Number(s);
  return parseNum(s, dec) ?? s;
}

function qtyOf(v, dec) {
  if (blank(v)) return null;
  return parseNum(v, dec) ?? String(v).trim();
}

function ymdToSerial(y, m, d) {
  if (y < 100) y += y < 70 ? 2000 : 1900;
  const t = Date.UTC(y, m - 1, d);
  const dt = new Date(t);
  if (dt.getUTCFullYear() !== y || dt.getUTCMonth() !== m - 1 || dt.getUTCDate() !== d) return undefined;
  return Math.round((t - EPOCH) / 86400000);
}

// Created On: data do Excel, ou texto dd/mm/aaaa, dd.mm.aaaa, aaaa-mm-dd. Devolve undefined se não for data.
function toSerial(v, date1904) {
  if (blank(v)) return null;
  if (typeof v === "number") return Number.isFinite(v) ? Math.floor(v) + (date1904 ? 1462 : 0) : undefined;
  const s = String(v).trim();
  let m = s.match(/^(\d{1,2})[\/.\-](\d{1,2})[\/.\-](\d{4}|\d{2})(?=$|[\sT])/);
  if (m) return ymdToSerial(+m[3], +m[2], +m[1]);
  m = s.match(/^(\d{4})[\/.\-](\d{1,2})[\/.\-](\d{1,2})(?=$|[\sT])/);
  if (m) return ymdToSerial(+m[1], +m[2], +m[3]);
  if (/^\d{5}(\.\d+)?$/.test(s)) return Math.floor(Number(s));
  return undefined;
}

function serialToText(n) {
  const d = new Date(EPOCH + n * 86400000);
  const p = (x) => String(x).padStart(2, "0");
  return p(d.getUTCDate()) + "/" + p(d.getUTCMonth() + 1) + "/" + d.getUTCFullYear();
}

// ---------- leitura do Excel ----------

function readError(err) {
  const m = String((err && err.message) || err);
  if (/password|encrypt/i.test(m)) return "O arquivo está protegido por senha.";
  return "Não foi possível abrir o arquivo. Ele pode estar corrompido ou não ser um Excel válido.";
}

const REQUIRED = ["salesDoc", "item", "charNo"];
const headerOf = (aoa) => (aoa.find((r) => Array.isArray(r) && r.some((c) => !blank(c))) || []).map(norm);
const hasColumns = (head) => REQUIRED.every((k) => head.includes(norm(SRC[k])));

function readWorkbook(XLSX, bytes, sheet) {
  const data = bytes instanceof Uint8Array ? bytes : new Uint8Array(bytes);
  // .xlsx/.xlsm são ZIP ("PK"); com senha o Excel grava em outro formato (D0 CF 11 E0).
  if (data[0] === 0xd0 && data[1] === 0xcf && data[2] === 0x11 && data[3] === 0xe0) throw userErr("O arquivo está protegido por senha. Abra no Excel, tire a senha e salve de novo.");
  if (data[0] !== 0x50 || data[1] !== 0x4b) throw userErr("Não foi possível abrir o arquivo. Ele pode estar corrompido ou não ser um Excel válido.");
  let names;
  try { names = XLSX.read(data, { type: "array", bookSheets: true }).SheetNames; }
  catch (err) { throw userErr(readError(err)); }
  let actual = names.find((n) => n === sheet) ?? names.find((n) => norm(n) === norm(sheet));
  let note = null;
  if (!actual) {
    // Sem a aba com esse nome (alguém salvou o mês com a aba renomeada): vale a primeira aba com as colunas da consulta.
    let peek;
    try { peek = XLSX.read(data, { type: "array", sheetRows: 20, cellText: false, cellHTML: false, cellFormula: false }); }
    catch (err) { throw userErr(readError(err)); }
    actual = names.find((n) => peek.Sheets[n] && hasColumns(headerOf(XLSX.utils.sheet_to_json(peek.Sheets[n], { header: 1, raw: true, defval: null, blankrows: false }))));
    if (!actual) throw userErr(`Não tem a aba "${sheet}", e nenhuma outra aba tem as colunas Sales Doc., Item e Internal char no. Abas do arquivo: ${names.join(", ") || "nenhuma"}.`);
    note = `A aba se chama "${actual}" em vez de "${sheet}". Foi lida assim mesmo, porque tem as colunas certas.`;
  }
  let wb;
  try { wb = XLSX.read(data, { type: "array", sheets: actual, cellText: false, cellHTML: false, cellFormula: false }); }
  catch (err) { throw userErr(readError(err)); }
  const ws = wb.Sheets[actual];
  const aoa = ws ? XLSX.utils.sheet_to_json(ws, { header: 1, raw: true, defval: null, blankrows: false }) : [];
  return { aoa, date1904: !!(wb.Workbook && wb.Workbook.WBProps && wb.Workbook.WBProps.date1904), note };
}

// ---------- a transformação (equivale à função ProcessarArquivo) ----------

function processSheet(aoa, fileName, date1904, opt) {
  const dec = opt.decimal;
  const msgs = [];
  const rows = aoa.filter((r) => Array.isArray(r) && r.some((c) => !blank(c)));
  if (!rows.length) throw userErr(`A aba "${opt.sheet}" está vazia.`);

  const head = rows[0].map(norm);
  const ix = {};
  for (const k in SRC) ix[k] = head.indexOf(norm(SRC[k]));
  const missing = ["salesDoc", "item", "charNo"].filter((k) => ix[k] < 0).map((k) => SRC[k]);
  if (ix.charVal < 0 && ix.valFrom < 0) missing.push("Characteristic Value ou Val from");
  if (missing.length) throw userErr("Faltam colunas na aba: " + missing.join(", ") + ".");
  const optional = ["createdOn", "sorg", "material", "qty", "charVal", "valFrom"].filter((k) => ix[k] < 0).map((k) => SRC[k]);
  if (optional.length) msgs.push({ level: "warn", text: "Colunas não encontradas (ficam em branco): " + optional.join(", ") + "." });

  const get = (r, k) => (ix[k] < 0 ? null : r[ix[k]] ?? null);
  const groups = new Map();
  const chars = [];
  const seen = new Set();
  const all = opt.repeated === "all";
  // Só conta repetição de característica que vai para a tabela: listas como CC_SDCOM_VKOND ficam de fora de qualquer jeito.
  const wanted = opt.extras || !opt.columns ? null : new Set(opt.columns);
  const examples = [];
  let badDates = 0, repeats = 0;

  for (let i = 1; i < rows.length; i++) {
    const r = rows[i];
    const code = textOf(get(r, "charNo"), dec);
    if (!code) continue; // Filtered Rows: sem "Internal char no."

    // Final value: Characteristic Value; se vazio, Val from em texto; se os dois vazios, "".
    let fin = textOf(get(r, "charVal"), dec);
    if (!fin) {
      const vf = get(r, "valFrom");
      if (blank(vf)) fin = "";
      else {
        const n = parseNum(vf, dec);
        fin = n == null ? String(vf).trim() : fmtNum(n, dec);
      }
    }

    let co = toSerial(get(r, "createdOn"), date1904);
    if (co === undefined) { badDates++; co = null; }
    const sd = keyNum(get(r, "salesDoc"), dec);
    const it = keyNum(get(r, "item"), dec);
    const so = textOf(get(r, "sorg"), dec);
    const ma = textOf(get(r, "material"), dec);
    const qt = qtyOf(get(r, "qty"), dec);

    // Pivot: uma linha por item da ordem, agrupando só pelas colunas que vão para o resultado.
    const key = JSON.stringify([sd, it, co, so, ma, qt]);
    let g = groups.get(key);
    if (!g) {
      g = { "Sales Doc.": sd, "Item": it, "Created On": co, "Arquivo Origem": fileName, "SOrg.": so, "Material": ma, "Order Quantity": qt, c: new Map() };
      groups.set(key, g);
    }
    if (!seen.has(code)) { seen.add(code); chars.push(code); }
    // A mesma característica de novo no mesmo item: a consulta ficava com o primeiro valor (List.First).
    // Com "Todos", os valores diferentes ficam juntos na célula.
    const prev = g.c.get(code);
    if (prev === undefined || prev === "") g.c.set(code, fin);
    else if (fin !== "" && !prev.split(" | ").includes(fin)) {
      if (!wanted || wanted.has(code)) repeats++;
      if ((!wanted || wanted.has(code)) && examples.length < 2) examples.push(`Sales Doc. ${sd}, item ${it}, ${code}: "${prev.split(" | ")[0]}" e "${fin}"`);
      if (all) g.c.set(code, prev + " | " + fin);
    }
  }

  if (badDates) msgs.push({ level: "warn", text: badDates === 1
    ? '1 linha com "Created On" que não é uma data. A data ficou em branco.'
    : `${badDates} linhas com "Created On" que não é uma data. As datas ficaram em branco.` });
  if (repeats) {
    const n = repeats.toLocaleString("pt-BR");
    const ex = ` Exemplo: ${examples.join("; ")}.`;
    msgs.push({ level: "info", text: all
      ? `${n} ${repeats === 1 ? "vez" : "vezes"} a mesma característica veio de novo no mesmo item com outro valor. Os valores ficaram juntos na célula, separados por " | ".${ex}`
      : `${n} ${repeats === 1 ? "valor ficou" : "valores ficaram"} de fora: a mesma característica vem mais de uma vez no mesmo item, com valores diferentes, e fica o primeiro (a consulta do Power Query fazia igual).${ex} Para ver todos os valores, mude em Ajustes > Característica repetida.` });
  }
  if (!groups.size) msgs.push({ level: "warn", text: 'Nenhuma linha com "Internal char no." preenchido.' });
  return { rows: [...groups.values()], chars, msgs };
}

// Junta todos os arquivos e aplica a ordem de colunas (Combinado + Reordered Columns).
function combine(parts, s) {
  const rows = parts.flatMap((p) => p.rows);
  const charOrder = [];
  const seen = new Set();
  for (const p of parts) for (const c of p.chars) if (!seen.has(c)) { seen.add(c); charOrder.push(c); }
  const existing = new Set([...BASE_COLS, ...charOrder]);
  const listed = new Set(s.columns);
  const cols = s.columns.filter((c) => existing.has(c));
  const extras = charOrder.filter((c) => !listed.has(c) && !BASE_COLS.includes(c));
  if (s.extras) cols.push(...extras);
  return { cols, rows, extras };
}

const cellValue = (row, col) => (BASE_COLS.includes(col) ? row[col] : row.c.get(col)) ?? null;

function displayValue(row, col, dec) {
  const v = cellValue(row, col);
  if (v == null) return "";
  if (col === "Created On" && typeof v === "number") return serialToText(v);
  if (typeof v === "number") return fmtNum(v, dec);
  return String(v);
}

// ---------- saída ----------

function buildXlsx(XLSX, res) {
  const aoa = [res.cols.slice()];
  for (const r of res.rows) aoa.push(res.cols.map((c) => cellValue(r, c)));
  const ws = XLSX.utils.aoa_to_sheet(aoa);
  const ci = res.cols.indexOf("Created On");
  if (ci >= 0) {
    for (let r = 1; r < aoa.length; r++) {
      const cell = ws[XLSX.utils.encode_cell({ r, c: ci })];
      if (cell && cell.t === "n") cell.z = "dd/mm/yyyy";
    }
  }
  ws["!cols"] = res.cols.map((c, i) => {
    let w = c.length;
    for (let r = 1; r < Math.min(aoa.length, 500); r++) {
      const v = aoa[r][i];
      if (v != null) w = Math.max(w, i === ci ? 10 : String(v).length);
    }
    return { wch: Math.min(48, w + 2) };
  });
  if (ws["!ref"]) ws["!autofilter"] = { ref: ws["!ref"] };
  const wb = XLSX.utils.book_new();
  XLSX.utils.book_append_sheet(wb, ws, "Consolidado");
  return new Uint8Array(XLSX.write(wb, { bookType: "xlsx", type: "array", compression: true }));
}

function delimited(res, dec, sep) {
  const q = (v) => (/[";\t\r\n]/.test(v) || v.includes(sep) ? '"' + v.replace(/"/g, '""') + '"' : v);
  const lines = [res.cols.map(q).join(sep)];
  for (const r of res.rows) lines.push(res.cols.map((c) => q(displayValue(r, c, dec))).join(sep));
  return lines.join("\r\n");
}

// ---------- exemplo (dados inventados, gravados como Excel de verdade) ----------

const SAMPLE_HEAD = ["Sales Doc.", "Item", "Created On", "SOrg.", "Material", "Order Quantity", "Internal char no.", "Characteristic Value", "Val from"];
const sampleItem = (sd, it, co, mat, qty, chars) => chars.map(([c, cv, vf]) => [sd, it, co, "BR10", mat, qty, c, cv ?? null, vf ?? null]);
const SAMPLE_FILES = [
  { rel: "Pedidos_2026-01.xlsx", aoa: [SAMPLE_HEAD,
    ...sampleItem(2000154312, 10, 46027, "MCP2-30-1000", 1, [
      ["AT_MCP2_CTR_CRD_TYP", "MCB-200"], ["AT_MCP2_PIT_M", null, 1.2], ["AT_MCP2_RD_POW_M", null, 11],
      ["AT_MCP2_MOT_POW_M", null, 7.5], ["AT_MCP2_FKT_ANG", null, 30], ["AT_MCP2_GEA_01_RAT", "1:24"],
      ["AT_MCP2_BTF_LEN_M", null, 6.8], ["AT_MCP2_RD_VOT", "380 V"], ["AT_MCP2_HND_COL", "Preto"]]),
    ...sampleItem(2000154312, 20, 46027, "MCP2-35-800", 2, [
      ["AT_MCP2_CTR_CRD_TYP", "MCB-200"], ["AT_MCP2_PIT_M", null, 1.1], ["AT_MCP2_MOT_POW_M", null, 5.5],
      ["AT_MCP2_FKT_ANG", null, 35], ["AT_MCP2_RD_VOT", "380 V"], ["AT_MCP2_BLT_TYP", "Lisa"]]),
    [2000154312, 30, 46027, "BR10", "MCP2-35-800", 1, null, "linha sem característica", null],
    ...sampleItem("2000154398", 10, "12/01/2026", "MCP2-30-1000", 1, [
      ["AT_MCP2_CTR_CRD_TYP", "MCB-300"], ["AT_MCP2_PIT_M", null, 1.3], ["AT_MCP2_RD_POW_M", null, 15],
      ["AT_MCP2_MOT_POW_M", null, 11], ["AT_MCP2_FKT_ANG", null, 30], ["AT_MCP2_GEA_01_RAT", "1:28"],
      ["AT_MCP2_RD_VOT", "440 V"], ["AT_MCP2_HND_COL", "Inox"]])
  ] },
  { rel: "fevereiro/Pedidos_2026-02.xlsx", aoa: [SAMPLE_HEAD,
    ...sampleItem("2000155020", 10, "03.02.2026", "MCP2-30-1200", 1, [
      ["AT_MCP2_CTR_CRD_TYP", "MCB-300"], ["AT_MCP2_PIT_M", null, 1.25], ["AT_MCP2_RD_POW_M", null, 18.5],
      ["AT_MCP2_MOT_POW_M", null, 15], ["AT_MCP2_FKT_ANG", null, 30], ["AT_MCP2_BTF_LEN_M", null, 7.2],
      ["AT_MCP2_RD_VOT", "380 V"], ["AT_MCP2_SEN_SPLR_01", "Fornecedor A"], ["AT_MCP2_MOD_LEN_M", null, 2.4]])
  ] },
  { rel: "fevereiro/~$Pedidos_2026-02.xlsx", temp: true }
];

function sampleBytes(XLSX, aoa) {
  const wb = XLSX.utils.book_new();
  XLSX.utils.book_append_sheet(wb, XLSX.utils.aoa_to_sheet(aoa), "Data");
  return new Uint8Array(XLSX.write(wb, { bookType: "xlsx", type: "array" }));
}

const api = {
  DEFAULT_COLUMNS, BASE_COLS, NUM_COLS, SAMPLE_FILES, defaultSettings,
  fmtNum, parseNum, toSerial, serialToText, readWorkbook, readError, processSheet, combine,
  cellValue, displayValue, buildXlsx, delimited, sampleBytes
};
root.CBA = api;
if (typeof module === "object" && module.exports) module.exports = api;
})(typeof window !== "undefined" ? window : globalThis);
