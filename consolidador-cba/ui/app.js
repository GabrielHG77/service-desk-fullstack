// Tela do Consolidador CBA, organizada como um app SwiftUI:
// - AppModel é o view model "@Observable": cada view registra as propriedades que lê e só é redesenhada quando uma delas muda;
// - as fontes de dados (pasta real e exemplo) e a ponte com o Windows entram por injeção, como @Environment;
// - views pequenas e separadas (barra lateral, toolbar, cards, tabela, sheet de ajustes), e a tabela é "lazy":
//   só desenha as linhas visíveis, como um LazyVStack.
"use strict";
(() => {
const { CBA, XLSX } = window;

const $ = (id) => document.getElementById(id);
const esc = (s) => String(s).replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
const tick = () => new Promise((r) => setTimeout(r, 0));
const fmtInt = (n) => n.toLocaleString("pt-BR");
const plural = (n, one, many) => `${fmtInt(n)} ${n === 1 ? one : many}`;
const fold = (s) => s.normalize("NFD").replace(/[̀-ͯ]/g, "").toLowerCase();
const collator = new Intl.Collator("pt-BR", { numeric: true, sensitivity: "base" });
const baseName = (p) => p.split(/[\\/]/).filter(Boolean).pop() || p;
const themeOf = (t) => (t === "light" || t === "dark" ? t : "auto");
function today() {
  const d = new Date();
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
}

// Chave de um item na planilha, igual ao planilha.go: "0005940235", 5940235 e "5940235" são o mesmo pedido.
function keyPart(v) {
  if (v == null) return "";
  const s = String(v).trim();
  if (!s) return "";
  const n = Number(s.replace(",", "."));
  if (Number.isFinite(n) && Number.isInteger(n) && Math.abs(n) < 1e15) return String(n);
  return s.replace(/^0+/, "");
}
function rowKey(r) {
  const d = keyPart(r["Sales Doc."]), i = keyPart(r.Item);
  return d || i ? d + "|" + i : "";
}

// Ícones no estilo SF Symbols.
const ICON = {
  folder: '<path fill="currentColor" d="M2 5.75C2 4.78 2.78 4 3.75 4h3.6c.46 0 .9.19 1.23.52L9.9 5.8h6.35c.97 0 1.75.78 1.75 1.75v7.7c0 .97-.78 1.75-1.75 1.75H3.75C2.78 17 2 16.22 2 15.25z"/>',
  table: '<rect x="2.75" y="3.75" width="14.5" height="12.5" rx="2.2" fill="none" stroke="currentColor" stroke-width="1.5"/><path stroke="currentColor" stroke-width="1.5" d="M2.75 8h14.5M2.75 12.1h14.5M7.6 8v8.25M12.4 8v8.25"/>',
  doc: '<path fill="none" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round" d="M5.5 2.75h5.4l3.6 3.6v9.4c0 .83-.67 1.5-1.5 1.5H5.5c-.83 0-1.5-.67-1.5-1.5V4.25c0-.83.67-1.5 1.5-1.5z"/><path fill="none" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round" d="M10.75 2.9v3.6h3.6"/>',
  ok: '<circle cx="10" cy="10" r="7.5" fill="currentColor"/><path fill="none" stroke="#fff" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" d="m6.6 10.2 2.3 2.3 4.5-4.8"/>',
  warn: '<path fill="currentColor" d="M8.68 3.2a1.5 1.5 0 0 1 2.64 0l6.2 11.1A1.5 1.5 0 0 1 16.2 16.5H3.8a1.5 1.5 0 0 1-1.32-2.2z"/><path stroke="#fff" stroke-width="1.7" stroke-linecap="round" d="M10 7.4v3.8"/><circle cx="10" cy="13.7" r="1" fill="#fff"/>',
  error: '<path fill="currentColor" d="M7.1 2.5h5.8l4.6 4.6v5.8l-4.6 4.6H7.1l-4.6-4.6V7.1z"/><path stroke="#fff" stroke-width="1.7" stroke-linecap="round" d="m7.6 7.6 4.8 4.8m0-4.8-4.8 4.8"/>',
  info: '<circle cx="10" cy="10" r="7.5" fill="currentColor"/><path stroke="#fff" stroke-width="1.7" stroke-linecap="round" d="M10 9.2v4.6"/><circle cx="10" cy="6.6" r="1" fill="#fff"/>',
  skip: '<circle cx="10" cy="10" r="6.75" fill="none" stroke="currentColor" stroke-width="1.5"/><path stroke="currentColor" stroke-width="1.5" stroke-linecap="round" d="M6.9 10h6.2"/>',
  chevron: '<path fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" d="m7.5 4.5 5.5 5.5-5.5 5.5"/>',
  up: '<path fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" d="m5 12.5 5-5 5 5"/>',
  down: '<path fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" d="m5 7.5 5 5 5-5"/>'
};
const icon = (name, cls = "") => `<svg class="sym ${cls}" viewBox="0 0 20 20" aria-hidden="true">${ICON[name]}</svg>`;
const STATUS_ICON = {
  ok: () => icon("ok", "tint-green"), warn: () => icon("warn", "tint-orange"), error: () => icon("error", "tint-red"),
  temp: () => icon("skip", "tint-gray"), pending: () => icon("doc", "tint-gray"), reading: () => '<span class="spinner"></span>'
};

// ---------- Observation ----------

// Como @Observable: rastreia cada propriedade; uma view registra as que lê e é redesenhada uma vez por mudança.
function observable(obj, keys) {
  const values = {};
  const subs = new Map();
  let dirty = new Set();
  let queued = false;
  const flush = () => {
    queued = false;
    const changed = dirty;
    dirty = new Set();
    const run = new Set();
    for (const k of changed) for (const view of subs.get(k) || []) run.add(view);
    for (const view of run) view();
  };
  for (const k of keys) {
    values[k] = obj[k];
    Object.defineProperty(obj, k, {
      enumerable: true,
      get: () => values[k],
      set: (v) => {
        if (Object.is(values[k], v)) return;
        values[k] = v;
        dirty.add(k);
        if (!queued) { queued = true; queueMicrotask(flush); }
      }
    });
  }
  obj.observe = (reads, view) => {
    for (const k of reads) {
      if (!subs.has(k)) subs.set(k, new Set());
      subs.get(k).add(view);
    }
    view();
  };
}

// ---------- dependências (injetadas no modelo, como @Environment) ----------

// Ponte com o programa do Windows: pasta, leitura dos arquivos e janelas de salvar.
class NativeBridge {
  constructor(token) { this.token = token; }
  async call(method, path, body, raw) {
    const headers = { "X-Token": this.token };
    if (body && !raw) headers["Content-Type"] = "application/json";
    let res;
    try {
      res = await fetch(path, { method, headers, body: raw ? body : body ? JSON.stringify(body) : undefined });
    } catch (err) {
      throw Object.assign(new Error("O app perdeu a conexão com o programa. Feche e abra de novo."), { transient: true });
    }
    if (!res.ok) {
      let msg = `Erro ${res.status}.`;
      try { msg = (await res.json()).error || msg; } catch (e) { /* sem corpo */ }
      throw Object.assign(new Error(msg), { user: true, transient: res.status === 409 });
    }
    return res;
  }
  async json(method, path, body) {
    const res = await this.call(method, path, body);
    return res.status === 204 ? null : res.json();
  }
  config() { return this.json("GET", "/api/config"); }
  saveConfig(c) { return this.json("PUT", "/api/config", c); }
  async pickFolder() { const r = await this.json("POST", "/api/pick-folder"); return r.cancelled ? null : r.path; }
  scan(path) { return this.json("GET", "/api/scan?path=" + encodeURIComponent(path)); }
  async read(id) { const res = await this.call("GET", "/api/file?id=" + encodeURIComponent(id)); return new Uint8Array(await res.arrayBuffer()); }
  async exportFile(name, kind, bytes) {
    const res = await this.call("POST", `/api/export?kind=${kind}&name=${encodeURIComponent(name)}`, bytes, true);
    const r = await res.json();
    return r.cancelled ? null : r.path;
  }
  open(path) { return this.json("POST", "/api/open", { path }); }
  reveal(path) { return this.json("POST", "/api/reveal", { path }); }
  ping() { return this.call("POST", "/api/ping").catch(() => {}); }
  planilha() { return this.json("GET", "/api/planilha"); }
  async choosePlanilha() { const r = await this.json("POST", "/api/planilha/escolher"); return r.cancelled ? null : r; }
  gravar(body) { return this.json("POST", "/api/planilha/gravar", body); }
  organizar(columns, backup) { return this.json("POST", "/api/planilha/organizar", { columns, backup }); }
}

// Duas fontes com o mesmo formato, como um repositório com uma implementação real e uma de exemplo (a "preview").
class FolderSource {
  constructor(native, path) { this.kind = "folder"; this.native = native; this.path = path; }
  list() { return this.native.scan(this.path); }
  read(file) { return this.native.read(file.id); }
}

class SampleSource {
  constructor() { this.kind = "sample"; }
  async list() {
    const files = CBA.SAMPLE_FILES.map((s, i) => ({ id: "s" + (i + 1), rel: s.rel, name: baseName(s.rel), size: 0, modified: 0, temp: !!s.temp }));
    return { folder: "", files, other: 0, truncated: false };
  }
  async read(file) { return CBA.sampleBytes(XLSX, CBA.SAMPLE_FILES.find((s) => s.rel === file.rel).aoa); }
}

// ---------- view model ----------

class AppModel {
  constructor(native) {
    this.native = native;
    this.source = null;
    this.cache = new Map();
    this.run = 0;
    this.phase = "start";      // start | empty | failed | ready
    this.mode = "none";        // none | folder | sample
    this.folder = "";
    this.failure = "";
    this.files = [];
    this.other = 0;
    this.truncated = false;
    this.busy = false;
    this.progress = "";
    this.result = null;
    this.updatedAt = null;
    this.selection = null;     // null = todos os arquivos; senão, os caminhos relativos marcados (estáveis entre leituras)
    this.anchor = null;        // último arquivo clicado, para o Shift+clique
    this.search = "";
    this.sort = null;          // { col, dir: "asc" | "desc" }
    // o tema já vem na página (data-theme), para não piscar claro antes de ler os ajustes
    this.settings = { ...CBA.defaultSettings(), theme: themeOf(document.documentElement.dataset.theme) };
    this.settingsOpen = false;
    this.toast = null;
    this.planilha = null;      // { path, exists, rows, keys: Map "doc|item" -> arquivo, error }
    this.show = "all";         // all | new | saved: filtro da tabela
    this.confirm = null;       // pergunta aberta antes de regravar
    observable(this, ["phase", "mode", "folder", "failure", "files", "busy", "progress", "result", "updatedAt",
      "selection", "search", "sort", "settings", "settingsOpen", "toast", "planilha", "show", "confirm"]);
  }

  // Como .task {}: roda uma vez quando a tela aparece.
  async start() {
    try {
      const c = await this.native.config();
      const d = CBA.defaultSettings();
      this.settings = {
        sheet: c.sheet || d.sheet,
        decimal: c.decimal === "." ? "." : ",",
        columns: Array.isArray(c.columns) && c.columns.length ? c.columns : d.columns,
        extras: !!c.extras,
        repeated: c.repeated === "all" ? "all" : "first",
        backup: c.backup !== false,
        theme: themeOf(c.theme)
      };
      await this.loadPlanilha();
      if (c.folder) return this.openFolder(c.folder);
    } catch (err) {
      this.notify(err.message, "error");
    }
    this.phase = "empty";
  }

  async chooseFolder() {
    try {
      const path = await this.native.pickFolder();
      if (path) await this.openFolder(path);
    } catch (err) {
      this.notify(err.message, "error");
    }
  }

  async openFolder(path) {
    this.source = new FolderSource(this.native, path);
    this.mode = "folder";
    this.folder = path;
    this.selection = null;
    await this.refresh();
  }

  async showSample() {
    this.source = new SampleSource();
    this.mode = "sample";
    this.folder = "";
    this.selection = null;
    await this.refresh();
  }

  async refresh() {
    if (!this.source) return this.chooseFolder();
    const run = ++this.run;
    this.busy = true;
    this.progress = "Lendo a pasta…";
    if (this.planilha) await this.loadPlanilha(); // alguém pode ter gravado nela desde a última vez
    let list;
    try {
      list = await this.source.list();
    } catch (err) {
      if (run !== this.run) return;
      this.busy = false;
      this.progress = "";
      this.failure = err.message;
      this.phase = "failed";
      return;
    }
    if (run !== this.run) return;
    this.other = list.other;
    this.truncated = !!list.truncated;
    this.files = list.files.map((f) => ({ ...f, status: f.temp ? "temp" : "pending", note: "", rows: 0 }));
    if (this.selection) {
      const left = this.selection.filter((rel) => this.files.some((f) => f.rel === rel));
      this.selection = left.length || !this.selection.length ? left : null;
    }
    this.phase = "ready";
    await this.consolidate(run);
  }

  async consolidate(run = ++this.run) {
    const s = this.settings;
    const files = this.files.map((f) => ({ ...f }));
    const todo = files.filter((f) => !f.temp);
    const parts = [];
    const msgs = [];
    this.busy = true;
    for (let i = 0; i < todo.length; i++) {
      const f = todo[i];
      const key = [this.mode, f.rel, f.size, f.modified, s.sheet].join("\u0000");
      let data = this.cache.get(key);
      if (!data) {
        this.progress = `Lendo ${i + 1} de ${todo.length}: ${f.name}`;
        f.status = "reading";
        this.files = files.map((x) => ({ ...x }));
        await tick();
        if (run !== this.run) return;
        let bytes = null;
        try { bytes = await this.source.read(f); }
        catch (err) { data = { error: err.message, transient: true }; }
        if (run !== this.run) return;
        if (bytes) {
          try { data = CBA.readWorkbook(XLSX, bytes, s.sheet); }
          catch (err) { data = { error: err.user ? err.message : CBA.readError(err) }; }
        }
        if (!data.transient) this.cache.set(key, data); // um arquivo bloqueado agora pode abrir no próximo Atualizar
      }
      if (data.error) {
        f.status = "error"; f.note = data.error; f.rows = 0;
        msgs.push({ level: "error", file: f.rel, text: data.error });
        continue;
      }
      try {
        const p = CBA.processSheet(data.aoa, f.name, data.date1904, s);
        for (const r of p.rows) r.file = f.rel;
        parts.push(p);
        if (data.note) p.msgs.unshift({ level: "info", text: data.note });
        f.status = p.msgs.some((m) => m.level === "warn") ? "warn" : "ok";
        f.rows = p.rows.length;
        for (const m of p.msgs) msgs.push({ ...m, file: f.rel });
      } catch (err) {
        const text = err.user ? err.message : "Erro inesperado: " + err.message;
        f.status = "error"; f.note = text; f.rows = 0;
        msgs.push({ level: "error", file: f.rel, text });
      }
    }

    const res = CBA.combine(parts, s);
    res.fileCount = parts.length;
    // Os arquivos do SAP trazem centenas de características; a lista de colunas escolhe as que vão para o Excel,
    // como a consulta fazia. Isso é uma nota, não um aviso, e o botão de incluir só aparece quando são poucas.
    const n = res.extras.length;
    if (n) {
      const few = n <= 10;
      const names = few ? res.extras.join(", ") : res.extras.slice(0, 5).join(", ") + "…";
      msgs.push({
        level: "info",
        text: s.extras
          ? `${plural(n, "característica fora da lista entrou", "características fora da lista entraram")} no fim da tabela: ${names}.`
          : n === 1
            ? `1 característica dos arquivos não está na lista de colunas e ficou de fora: ${names}.`
            : `${fmtInt(n)} características dos arquivos não estão na lista de colunas e ficaram de fora, como na consulta do Power Query: ${names}.${few ? "" : " Para levar todas, ligue Ajustes > Incluir características fora da lista."}`,
        addExtras: !s.extras && few
      });
    }
    for (const f of files) if (f.temp) msgs.push({ level: "info", file: f.rel, text: "Ignorado: é o arquivo temporário que o Excel cria enquanto alguém está com o original aberto." });
    if (this.other) msgs.push({ level: "info", text: `${plural(this.other, "arquivo de outro tipo foi ignorado", "arquivos de outros tipos foram ignorados")}. Só entram .xlsx e .xlsm.` });
    if (this.truncated) msgs.push({ level: "warn", text: "A pasta tem arquivos demais e só uma parte foi lida. Escolha a pasta DadosCBA, não o drive inteiro." });
    res.msgs = msgs;

    this.files = files;
    this.result = res;
    this.busy = false;
    this.progress = "";
    this.updatedAt = new Date();
  }

  updateSettings(patch) {
    this.settings = { ...this.settings, ...patch };
    clearTimeout(this.saveTimer);
    this.saveTimer = setTimeout(() => this.native.saveConfig(this.settings).catch((err) => this.notify(err.message, "error")), 300);
    if (this.phase !== "ready" || Object.keys(patch).every((k) => k === "backup" || k === "theme")) return;
    // A chave ou o campo mudam na hora; a tabela é refeita logo depois, para a tela poder mostrar a mudança antes
    // (com os arquivos do SAP, refazer leva um ou dois segundos, e com as características fora da lista, mais).
    this.busy = true;
    this.progress = "Aplicando os ajustes…";
    clearTimeout(this.applyTimer);
    this.applyTimer = setTimeout(() => this.consolidate(), 40);
  }
  resetSettings() { this.updateSettings(CBA.defaultSettings()); }
  includeExtras() { if (this.result) this.updateSettings({ columns: [...this.settings.columns, ...this.result.extras] }); }

  toggleSort(col) {
    const s = this.sort;
    this.sort = !s || s.col !== col ? { col, dir: "asc" } : s.dir === "asc" ? { col, dir: "desc" } : null;
  }

  // ---------- a planilha consolidada (como o ConsolidadoPath.xlsx do Fator de Importação) ----------

  async loadPlanilha() {
    try { this.setPlanilha(await this.native.planilha()); }
    catch (err) { this.notify(err.message, "error"); }
  }

  setPlanilha(p) {
    this.planilha = p && p.path
      ? {
          path: p.path, exists: !!p.exists, rows: p.rows || 0, keys: new Map(Object.entries(p.keys || {})), error: p.error || "",
          sheet: p.sheet || "", headerRow: p.headerRow || 0, header: p.header || [], tables: p.tables || [], sheets: p.sheets || []
        }
      : null;
    if (!this.planilha) this.show = "all";
  }

  async choosePlanilha() {
    try {
      const p = await this.native.choosePlanilha();
      if (p) this.setPlanilha(p);
    } catch (err) {
      this.notify(err.message, "error");
    }
  }

  // Itens que ainda não estão na planilha: o primeiro de cada Sales Doc. + Item, na ordem dos arquivos.
  pending() {
    const res = this.result, pl = this.planilha;
    if (this.pendKey && this.pendKey[0] === res && this.pendKey[1] === pl) return this.pend;
    const out = { rows: [], set: new Set(), byFile: new Map() };
    if (res && pl && !pl.error) {
      const seen = new Set();
      for (const r of res.rows) {
        const k = rowKey(r);
        if (!k || pl.keys.has(k) || seen.has(k)) continue;
        seen.add(k);
        out.rows.push(r);
        out.set.add(r);
        out.byFile.set(r.file, (out.byFile.get(r.file) || 0) + 1);
      }
    }
    this.pendKey = [res, pl];
    this.pend = out;
    return out;
  }

  isSaved(r) { return !!(this.planilha && this.planilha.keys.has(rowKey(r))); }

  canWrite() {
    const cols = this.result ? this.result.cols : [];
    if (!cols.includes("Sales Doc.") || !cols.includes("Item")) {
      this.notify("Para gravar na planilha, a lista de colunas (em Ajustes) precisa ter Sales Doc. e Item.", "error");
      return false;
    }
    return this.mode === "folder" && !!this.planilha && !this.planilha.error && !this.busy;
  }

  // O padrão do app: as colunas da lista (as que a planilha ou os arquivos têm), na ordem da lista.
  standardColumns() {
    const s = this.settings, res = this.result, pl = this.planilha;
    const inFile = new Set((pl ? pl.header : []).map(CBA.canon));
    const inData = new Set(res ? res.cols : []);
    let cols = s.columns.filter((c) => inFile.has(CBA.canon(c)) || inData.has(c));
    if (s.extras && res) cols = cols.concat(res.extras.filter((c) => !cols.includes(c)));
    return cols;
  }

  // Em que a planilha escolhida difere do padrão do app: nomes na linha 1, as colunas do app com os nomes do app
  // e na ordem do app, e as que o app não conhece depois delas.
  layoutDiff() {
    const pl = this.planilha;
    if (!pl || pl.error || !pl.exists || !pl.sheet || !this.result) return null;
    const std = this.standardColumns();
    const stdC = std.map(CBA.canon);
    const head = pl.header.map((h) => String(h).trim());
    const list = (names) => names.slice(0, 3).join(", ") + (names.length > 3 ? "…" : ".");
    const items = [];
    if (pl.headerRow > 1) items.push(`Os nomes das colunas estão na linha ${pl.headerRow}, não na 1 (${plural(pl.headerRow - 1, "linha", "linhas")} acima ${pl.headerRow - 1 === 1 ? "sai" : "saem"}).`);
    const found = [], renamed = [];
    head.forEach((h, i) => {
      const k = h ? stdC.indexOf(CBA.canon(h)) : -1;
      if (k < 0) return;
      found.push({ k, i });
      if (std[k] !== h) renamed.push(`${h} → ${std[k]}`);
    });
    if (renamed.length) items.push(`${plural(renamed.length, "coluna tem", "colunas têm")} outro nome: ${list(renamed)}`);
    const missing = std.filter((c, k) => !found.some((f) => f.k === k));
    if (missing.length) items.push(`${plural(missing.length, "coluna do app falta", "colunas do app faltam")}: ${list(missing)}`);
    if (!found.every((f, j) => j === 0 || f.k > found[j - 1].k)) items.push("As colunas do app estão em outra ordem.");
    const unknown = head.map((h, i) => (h && stdC.indexOf(CBA.canon(h)) < 0 ? i : -1)).filter((i) => i >= 0);
    const lastKnown = found.length ? found[found.length - 1].i : -1;
    const middle = unknown.filter((i) => i < lastKnown).map((i) => head[i]);
    if (middle.length) items.push(`${plural(middle.length, "coluna que o app não conhece está", "colunas que o app não conhece estão")} no meio das do app: ${list(middle)}`);
    return { items, std, kept: unknown.map((i) => head[i]) };
  }

  backupNote(which) {
    return this.settings.backup
      ? `A versão ${which} fica numa cópia .bak.xlsx.`
      : "Sem cópia de segurança: a mudança vai direto para a planilha (a cópia .bak está desligada em Ajustes).";
  }

  askOrganizar() {
    const d = this.layoutDiff();
    if (!d || !d.items.length) return;
    const pl = this.planilha;
    const keep = d.kept.length ? `\n\nAs colunas que o app não conhece (${d.kept.slice(0, 4).join(", ")}${d.kept.length > 4 ? "…" : ""}) ficam, depois das do app.` : "";
    const tables = pl.tables.length ? ` A tabela ${pl.tables.join(", ")} é refeita com o mesmo nome.` : "";
    this.confirm = {
      title: `Organizar a aba ${pl.sheet} no padrão do app?`,
      text: d.items.map((i) => "• " + i).join("\n") + keep +
        `\n\nOs dados não mudam, só de lugar.${tables} As outras abas não são tocadas. ${this.backupNote("de agora")}`,
      yes: "Organizar",
      run: () => this.organizar()
    };
  }

  async organizar() {
    if (!this.canWrite()) return;
    this.busy = true;
    this.progress = "Organizando a planilha…";
    try {
      const out = await this.native.organizar(this.layoutDiff().std, this.settings.backup);
      await this.loadPlanilha();
      const bits = [plural(out.rows, "item", "itens")];
      if (out.renamed) bits.push(plural(out.renamed, "coluna renomeada", "colunas renomeadas"));
      if (out.added.length) bits.push(plural(out.added.length, "coluna nova", "colunas novas"));
      if (out.kept.length) bits.push(`${out.kept.slice(0, 3).join(", ")}${out.kept.length > 3 ? "…" : ""} no fim`);
      if (out.formulas) bits.push(plural(out.formulas, "fórmula virou valor", "fórmulas viraram valores"));
      this.notify(`Aba ${out.sheet} organizada no padrão do app: ${bits.join(", ")}.`, "saved", out.path);
    } catch (err) {
      this.notify(err.message, "error");
    } finally {
      this.busy = false;
      this.progress = "";
    }
  }

  async gravar() {
    const rows = this.pending().rows.filter((r) => this.picks(r.file));
    if (!rows.length || !this.canWrite()) return;
    const out = await this.write(rows, false);
    if (out) this.notify(`${plural(out.added, "item novo gravado", "itens novos gravados")} em ${baseName(out.path)}.${out.backup ? ` A versão anterior ficou em ${baseName(out.backup)}.` : ""}`, "saved", out.path);
  }

  askRegravar() {
    const rels = this.selection;
    if (!rels || !rels.length || !this.planilha) return;
    const saved = this.savedIn(rels);
    const fresh = this.pendingIn(rels);
    const what = rels.length === 1 ? this.fileName(rels[0]) : plural(rels.length, "arquivo marcado", "arquivos marcados");
    this.confirm = {
      title: `Regravar ${what}?`,
      text: `${plural(saved, "item que já está na planilha recebe", "itens que já estão na planilha recebem")} os valores de agora` +
        (fresh ? ` e ${plural(fresh, "item novo entra", "itens novos entram")} no fim` : "") +
        `. As outras abas e as colunas escritas à mão não mudam. ${this.backupNote("anterior")}`,
      yes: "Regravar",
      run: () => this.regravar(rels)
    };
  }

  async regravar(rels) {
    if (!this.canWrite()) return;
    const seen = new Set();
    const rows = this.result.rows.filter((r) => {
      const k = rowKey(r);
      if (!rels.includes(r.file) || !k || seen.has(k)) return false;
      seen.add(k);
      return true;
    });
    const out = await this.write(rows, true);
    const what = rels.length === 1 ? this.fileName(rels[0]) + " regravado" : plural(rels.length, "arquivo regravado", "arquivos regravados");
    if (out) this.notify(`${what}: ${plural(out.updated, "item atualizado", "itens atualizados")}, ${plural(out.added, "novo", "novos")}.`, "saved", out.path);
  }

  // ---------- seleção de arquivos (como a seleção múltipla de uma List) ----------

  selectable() { return this.files.filter((f) => !f.temp).map((f) => f.rel); }
  isAll() { return this.selection === null; }
  picks(rel) { return this.selection === null || this.selection.includes(rel); }
  setSelection(set) {
    const all = this.selectable();
    const list = all.filter((rel) => set.has(rel));
    this.selection = list.length === all.length && list.length > 0 ? null : list;
  }
  selectOnly(rel) {
    this.anchor = rel === "all" ? null : rel;
    this.selection = rel === "all" ? null : [rel];
  }
  toggle(rel) {
    const set = new Set(this.selection === null ? this.selectable() : this.selection);
    if (set.has(rel)) set.delete(rel); else set.add(rel);
    this.anchor = rel;
    this.setSelection(set);
  }
  selectRange(rel) {
    const all = this.selectable();
    const a = all.indexOf(this.anchor), b = all.indexOf(rel);
    if (a < 0 || b < 0) return this.toggle(rel);
    const set = new Set(this.selection === null ? [] : this.selection);
    for (let i = Math.min(a, b); i <= Math.max(a, b); i++) set.add(all[i]);
    this.setSelection(set);
  }
  selectionLabel() {
    if (this.selection === null) return "Todos os arquivos";
    if (this.selection.length === 1) return this.fileName(this.selection[0]);
    if (!this.selection.length) return "Nenhum arquivo marcado";
    return plural(this.selection.length, "arquivo marcado", "arquivos marcados");
  }
  pendingIn(rels) { const b = this.pending().byFile; return (rels || this.selectable()).reduce((n, rel) => n + (b.get(rel) || 0), 0); }
  savedIn(rels) { return this.result ? this.result.rows.filter((r) => (rels ? rels.includes(r.file) : true) && this.isSaved(r)).length : 0; }

  async write(rows, replace) {
    const res = this.result;
    this.busy = true;
    this.progress = "Gravando na planilha…";
    try {
      const out = await this.native.gravar({ columns: res.cols, rows: rows.map((r) => res.cols.map((c) => CBA.cellValue(r, c))), replace, backup: this.settings.backup });
      await this.loadPlanilha();
      return out;
    } catch (err) {
      this.notify(err.message, "error");
      return null;
    } finally {
      this.busy = false;
      this.progress = "";
    }
  }

  // Derivado e memorizado: só recalcula quando resultado, seleção, busca, ordem, filtro ou planilha mudam.
  visibleRows() {
    const res = this.result;
    if (!res) return [];
    const key = [res, this.selection, this.search, this.sort, this.show, this.planilha];
    if (this.visKey && key.every((v, i) => v === this.visKey[i])) return this.vis;
    let rows = res.rows;
    if (this.selection !== null) { const set = new Set(this.selection); rows = rows.filter((r) => set.has(r.file)); }
    if (this.show === "new") { const set = this.pending().set; rows = rows.filter((r) => set.has(r)); }
    if (this.show === "saved") rows = rows.filter((r) => this.isSaved(r));
    const q = fold(this.search.trim());
    if (q) {
      const dec = this.settings.decimal;
      rows = rows.filter((r) => (r.text ??= fold(res.cols.map((c) => CBA.displayValue(r, c, dec)).join("\u0001"))).includes(q));
    }
    if (this.sort) {
      const { col, dir } = this.sort;
      const m = dir === "asc" ? 1 : -1;
      const dec = this.settings.decimal;
      const empty = (v) => v == null || v === "";
      rows = rows.slice().sort((a, b) => {
        const va = CBA.cellValue(a, col), vb = CBA.cellValue(b, col);
        if (empty(va) || empty(vb)) return empty(va) === empty(vb) ? 0 : empty(va) ? 1 : -1;
        const na = typeof va === "number" ? va : CBA.parseNum(va, dec);
        const nb = typeof vb === "number" ? vb : CBA.parseNum(vb, dec);
        if (na != null && nb != null) return (na - nb) * m;
        return collator.compare(String(va), String(vb)) * m;
      });
    }
    this.visKey = key;
    this.vis = rows;
    return rows;
  }

  fileName(rel) { const f = this.files.find((x) => x.rel === rel); return f ? f.name : baseName(rel); }

  async exportFile(kind) {
    if (!this.result || !this.result.rows.length) return;
    const name = `Consolidado_CBA_${today()}.${kind}`;
    try {
      const bytes = kind === "xlsx"
        ? CBA.buildXlsx(XLSX, this.result)
        : new TextEncoder().encode("﻿" + CBA.delimited(this.result, this.settings.decimal, ";"));
      const path = await this.native.exportFile(name, kind, bytes);
      if (path) this.notify(`${baseName(path)} foi salvo.`, "saved", path);
    } catch (err) {
      this.notify(err.message, "error");
    }
  }

  async copyTable() {
    if (!this.result || !this.result.rows.length) return;
    const tsv = CBA.delimited(this.result, this.settings.decimal, "\t");
    const done = () => this.notify(`Tabela copiada (${plural(this.result.rows.length, "linha", "linhas")}). Cole no Excel com Ctrl+V.`, "ok");
    try {
      await navigator.clipboard.writeText(tsv);
      done();
    } catch (err) {
      const ta = document.createElement("textarea");
      ta.value = tsv;
      document.body.appendChild(ta);
      ta.select();
      const ok = document.execCommand("copy");
      ta.remove();
      if (ok) done(); else this.notify("Não foi possível copiar. Use Exportar Excel.", "error");
    }
  }

  async openSaved(how) {
    const path = this.toast && this.toast.path;
    if (!path) return;
    try { await (how === "reveal" ? this.native.reveal(path) : this.native.open(path)); }
    catch (err) { this.notify(err.message, "error"); }
  }

  notify(text, kind = "info", path = null) { this.toast = { text, kind, path }; }
}

// ---------- views ----------

function FolderCard(model) {
  const el = $("folder-card");
  model.observe(["mode", "folder"], () => {
    if (model.mode === "sample") {
      el.innerHTML = `${icon("folder", "sample")}<strong>Exemplo</strong><span class="path">Dados inventados, só para mostrar o resultado.</span>
        <button class="link" type="button" data-action="choose">Escolher pasta…</button>`;
    } else if (model.folder) {
      const parts = model.folder.split(/[\\/]/).filter(Boolean);
      const name = parts.pop() || model.folder;
      const parent = model.folder.slice(0, model.folder.length - name.length).replace(/[\\/]+$/, "");
      el.innerHTML = `${icon("folder")}<strong title="${esc(model.folder)}">${esc(name)}</strong><span class="path" title="${esc(model.folder)}">${esc(parent)}</span>
        <button class="link" type="button" data-action="choose">Trocar pasta…</button>`;
    } else {
      el.innerHTML = `${icon("folder", "tint-gray")}<strong>Nenhuma pasta</strong><span class="path">Escolha a pasta DadosCBA.</span>
        <button class="link" type="button" data-action="choose">Escolher pasta…</button>`;
    }
  });
}

function PlanilhaCard(model) {
  const el = $("planilha-card");
  const sheetIcon = () => icon("table", "sheet");
  model.observe(["planilha", "result", "settings"], () => {
    const pl = model.planilha;
    if (!pl) {
      el.innerHTML = `${icon("table", "tint-gray")}<strong>Nenhuma planilha</strong><span class="path">Escolha onde gravar o consolidado. Cada atualização acrescenta só os itens novos.</span>
        <button class="link" type="button" data-action="choose-planilha">Escolher planilha…</button>`;
      return;
    }
    const name = baseName(pl.path);
    const parent = pl.path.slice(0, pl.path.length - name.length).replace(/[\\/]+$/, "");
    const where = pl.sheet && pl.sheet.toLowerCase() !== "consolidado" ? ` · aba ${esc(pl.sheet)}` : "";
    const status = pl.error
      ? `<span class="status err">${esc(pl.error)}</span>`
      : !pl.exists
        ? `<span class="status">Ainda não existe: é criada na primeira gravação.</span>`
        : !pl.sheet
          ? `<span class="status">Nenhuma aba tem Sales Doc. e Item: os dados vão para uma aba Consolidado nova.</span>`
          : `<span class="status">${plural(pl.rows, "item gravado", "itens gravados")}${where}</span>`;
    const diff = model.layoutDiff();
    const organize = diff && diff.items.length
      ? `<span class="status warn">${icon("warn", "tint-orange")}Organizada diferente do app</span>
         <button class="link" type="button" data-action="ask-organizar">Ver diferenças e organizar…</button>`
      : "";
    el.innerHTML = `${sheetIcon()}<strong title="${esc(pl.path)}">${esc(name)}</strong><span class="path" title="${esc(pl.path)}">${esc(parent)}</span>${status}${organize}
      <button class="link" type="button" data-action="choose-planilha">Trocar planilha…</button>`;
  });
}

function FileList(model) {
  const el = $("file-list");
  model.observe(["files", "selection", "result", "planilha"], () => {
    if (!model.files.length) { el.innerHTML = ""; return; }
    const pend = model.pending();
    const badge = (n) => (n ? `<span class="new-badge" title="${plural(n, "item novo", "itens novos")} para gravar">+${fmtInt(n)}</span>` : "");
    const total = model.result ? model.result.rows.length : 0;
    const all = model.isAll();
    let h = row("all", icon("table", "tint-blue"), "Todos os arquivos", "", total ? fmtInt(total) : "", "Todos os arquivos (marca todos)", badge(pend.rows.length), all, null);
    h += '<li class="divider" role="separator"></li>';
    for (const f of model.files) {
      const dir = f.rel.includes("/") ? f.rel.slice(0, f.rel.lastIndexOf("/") + 1) : "";
      const count = f.status === "ok" || f.status === "warn" ? fmtInt(f.rows) : "";
      const tip = f.rel + (f.note ? " — " + f.note : f.status === "temp" ? " — arquivo temporário, ignorado" : "") + "\nCtrl+clique ou a caixinha marca mais de um.";
      const check = f.temp ? "" : `<input class="pick" type="checkbox" tabindex="-1" data-pick="${esc(f.rel)}" aria-label="Marcar ${esc(f.name)}"${model.picks(f.rel) ? " checked" : ""}>`;
      h += row(f.rel, STATUS_ICON[f.status](), f.name, dir, count, tip, badge(pend.byFile.get(f.rel) || 0), !all && model.picks(f.rel), check);
    }
    el.innerHTML = h;
    $("files-count").textContent = all || !model.selection ? "" : `${fmtInt(model.selection.length)} de ${fmtInt(model.selectable().length)}`;
    function row(key, sym, name, dir, count, tip, extra, sel, check) {
      return `<li role="option" aria-selected="${sel}" data-select="${esc(key)}" title="${esc(tip)}">${check || ""}${sym}` +
        `<span class="name">${dir ? `<span class="dim">${esc(dir)}</span>` : ""}${esc(name)}</span>${extra || `<span class="count">${count}</span>`}</li>`;
    }
  });
  el.setAttribute("aria-multiselectable", "true");
  el.addEventListener("click", (ev) => {
    const box = ev.target.closest("[data-pick]");
    if (box) { model.toggle(box.dataset.pick); return; }
    const li = ev.target.closest("[data-select]");
    if (!li) return;
    const rel = li.dataset.select;
    if (rel !== "all" && (ev.ctrlKey || ev.metaKey)) model.toggle(rel);
    else if (rel !== "all" && ev.shiftKey) model.selectRange(rel);
    else model.selectOnly(rel);
  });
  el.addEventListener("mousedown", (ev) => { if (ev.shiftKey) ev.preventDefault(); }); // sem selecionar texto no Shift+clique
  el.addEventListener("keydown", (ev) => {
    if (ev.key !== "ArrowDown" && ev.key !== "ArrowUp") return;
    ev.preventDefault();
    const keys = ["all", ...model.selectable()];
    const cur = model.isAll() ? "all" : model.anchor || (model.selection[0] ?? "all");
    const i = keys.indexOf(cur) + (ev.key === "ArrowDown" ? 1 : -1);
    if (i >= 0 && i < keys.length) {
      if (ev.shiftKey && keys[i] !== "all") { model.toggle(keys[i]); }
      else model.selectOnly(keys[i]);
    }
    queueMicrotask(() => { const c = el.querySelector(`[data-select="${CSS.escape(model.anchor || "all")}"]`); if (c) c.scrollIntoView({ block: "nearest" }); });
  });
}

function Toolbar(model) {
  model.observe(["selection", "files", "result", "busy", "progress", "updatedAt", "phase", "mode", "planilha"], () => {
    const ready = model.phase === "ready";
    $("title").textContent = !ready ? "Consolidador CBA" : model.selectionLabel();
    const sub = $("subtitle");
    if (model.busy) {
      sub.innerHTML = `<span class="spinner"></span><span>${esc(model.progress)}</span>`;
    } else if (ready && model.result) {
      const r = model.result;
      const t = model.updatedAt;
      const at = t ? ` · atualizado às ${String(t.getHours()).padStart(2, "0")}:${String(t.getMinutes()).padStart(2, "0")}` : "";
      sub.textContent = `${plural(r.fileCount, "arquivo lido", "arquivos lidos")} · ${plural(r.rows.length, "linha", "linhas")}${at}`;
    } else {
      sub.textContent = "";
    }
    const hasRows = !!(ready && model.result && model.result.rows.length);
    // Botão principal: escolher a planilha, gravar os novos, ou nada a fazer.
    const primary = $("primary");
    const n = model.pendingIn(model.isAll() ? null : model.selection);
    const pl = model.planilha;
    let label = "Gravar na planilha", action = "gravar", enabled = false, tip = "";
    if (model.mode === "sample") tip = "Os dados de exemplo não são gravados.";
    else if (!pl) { label = "Escolher planilha…"; action = "choose-planilha"; enabled = ready; }
    else if (pl.error) tip = pl.error;
    else if (n) { label = `Gravar ${plural(n, "novo", "novos")}`; enabled = hasRows; tip = `Acrescenta na planilha só os itens ${model.isAll() ? "" : "dos arquivos marcados "}que ainda não estão nela (Ctrl+S).`; }
    else if (hasRows) { label = "Planilha em dia"; tip = model.isAll() ? "Todos os itens da pasta já estão na planilha." : "Os itens dos arquivos marcados já estão na planilha."; }
    primary.dataset.action = action;
    primary.innerHTML = (label === "Planilha em dia" ? icon("ok") : "") + `<span>${esc(label)}</span>`;
    primary.disabled = !enabled || model.busy;
    primary.title = tip;
    for (const b of document.querySelectorAll('#more-menu [data-action="export"]')) b.disabled = !hasRows;
    $("refresh").disabled = model.busy || model.mode === "none";
    $("search").disabled = !ready;
    for (const b of document.querySelectorAll('#more-menu [data-action="csv"], #more-menu [data-action="copy"]')) b.disabled = !hasRows;
  });
  let typing;
  $("search").addEventListener("input", (ev) => {
    clearTimeout(typing);
    typing = setTimeout(() => { model.search = ev.target.value; }, 120);
  });
}

function MoreMenu() {
  const btn = $("more");
  const menu = $("more-menu");
  const show = (open) => { menu.hidden = !open; btn.setAttribute("aria-expanded", String(open)); };
  btn.addEventListener("click", (ev) => { ev.stopPropagation(); show(menu.hidden); if (!menu.hidden) menu.querySelector("button:not(:disabled)")?.focus(); });
  document.addEventListener("click", () => show(false));
  document.addEventListener("keydown", (ev) => { if (ev.key === "Escape") show(false); });
}

function Screens(model) {
  model.observe(["phase", "failure"], () => {
    $("empty").hidden = model.phase !== "empty";
    $("failed").hidden = model.phase !== "failed";
    $("loaded").hidden = model.phase !== "ready";
    $("failure-text").textContent = model.failure;
  });
  model.observe(["mode"], () => { $("sample-banner").hidden = model.mode !== "sample"; });
}

function Stats(model) {
  model.observe(["result", "selection", "planilha"], () => {
    const r = model.result;
    if (!r) { $("stats").innerHTML = ""; return; }
    const all = model.isAll();
    const rows = all ? r.rows.length : r.rows.filter((x) => model.picks(x.file)).length;
    const msgs = all ? r.msgs : r.msgs.filter((m) => m.file && model.picks(m.file));
    const errors = msgs.filter((m) => m.level === "error").length;
    const warns = msgs.filter((m) => m.level === "warn").length;
    const attention = errors + warns;
    const tint = errors ? "tint-red" : warns ? "tint-orange" : "";
    const stat = (label, value, cls = "", note = "") => `<div class="card stat"><span>${label}</span><strong class="${cls}">${value}</strong>${note ? `<small>${note}</small>` : ""}</div>`;
    const one = !all && model.selection.length === 1 ? model.selection[0] : null;
    const file = one ? model.files.find((f) => f.rel === one) : null;
    const SITUATION = { ok: ["Lido", "tint-green"], warn: ["Lido com aviso", "tint-orange"], error: ["Não lido", "tint-red"], temp: ["Ignorado", ""] };
    const [situation, sitTint] = file ? SITUATION[file.status] || ["—", ""] : ["", ""];
    $("stats").innerHTML =
      stat("Linhas", fmtInt(rows)) +
      (() => {
        if (!model.planilha || model.planilha.error) return stat("Novos", "—");
        const n = model.pendingIn(all ? null : model.selection);
        const saved = model.savedIn(all ? null : model.selection);
        return stat("Novos para gravar", n ? fmtInt(n) : "Nenhum", n ? "tint-blue" : "", saved ? `${fmtInt(saved)} já na planilha` : "");
      })() +
      (all ? stat("Arquivos lidos", fmtInt(r.fileCount)) : one ? stat("Situação", situation, sitTint) : stat("Arquivos marcados", `${fmtInt(model.selection.length)} de ${fmtInt(model.selectable().length)}`)) +
      stat(errors ? "Erros e avisos" : "Avisos", attention ? fmtInt(attention) : "Nenhum", tint);
  });
}

function Issues(model) {
  const box = $("issues");
  let lastResult = null;
  model.observe(["result", "selection"], () => {
    const r = model.result;
    const msgs = !r ? [] : model.isAll() ? r.msgs : r.msgs.filter((m) => m.file && model.picks(m.file));
    box.hidden = !msgs.length;
    if (!msgs.length) return;
    const order = { error: 0, warn: 1, info: 2 };
    const sorted = msgs.slice().sort((a, b) => order[a.level] - order[b.level]);
    const errors = msgs.filter((m) => m.level === "error").length;
    const warns = msgs.filter((m) => m.level === "warn").length;
    const parts = [errors && plural(errors, "erro", "erros"), warns && plural(warns, "aviso", "avisos"), !errors && !warns && plural(msgs.length, "observação", "observações")].filter(Boolean);
    $("issues-summary").innerHTML = `${icon(errors ? "error" : warns ? "warn" : "info", errors ? "tint-red" : warns ? "tint-orange" : "tint-blue")}` +
      `<span>Erros e avisos</span><span class="count">${parts.join(" · ")}</span>${icon("chevron", "chev")}`;
    const ICONS = { error: icon("error", "tint-red"), warn: icon("warn", "tint-orange"), info: icon("info", "tint-blue") };
    $("issues-list").innerHTML = sorted.map((m) =>
      `<li>${ICONS[m.level]}<span>${m.file ? `<span class="file">${esc(model.fileName(m.file))}</span>: ` : ""}${esc(m.text)}` +
      `${m.addExtras ? '<button class="link" type="button" data-action="include-extras">Incluir na lista de colunas</button>' : ""}</span></li>`
    ).join("");
    if (r !== lastResult) { box.open = errors > 0 || warns > 0; lastResult = r; }
  });
}

// Tabela "lazy", como um LazyVStack: só as linhas visíveis existem no documento.
class LazyTable {
  constructor(model) {
    this.model = model;
    this.scroll = $("table-scroll");
    this.table = $("grid");
    this.rowH = 30;
    this.headH = 34;
    this.rows = [];
    this.range = [-1, -1];
    this.scroll.addEventListener("scroll", () => this.schedule(), { passive: true });
    new ResizeObserver(() => this.schedule(true)).observe(this.scroll);
    this.table.tHead.addEventListener("click", (ev) => {
      const b = ev.target.closest("[data-col]");
      if (b) model.toggleSort(b.dataset.col);
    });
    model.observe(["result", "selection", "search", "sort", "show", "planilha"], () => this.update());
  }

  update() {
    const m = this.model;
    const res = m.result;
    if (!res) return;
    if (res !== this.res) { this.res = res; this.widths = this.measure(res); }
    this.rows = m.visibleRows();
    this.scroll.scrollTop = 0;
    this.layout();
    const empty = $("table-empty");
    empty.hidden = this.rows.length > 0;
    if (!this.rows.length) {
      const q = m.search.trim();
      empty.innerHTML = q
        ? `<strong>Nenhum resultado para “${esc(q)}”</strong><span>Confira a grafia ou busque outro termo.</span>`
        : m.selection && !m.selection.length
          ? `<strong>Nenhum arquivo marcado</strong><span>Marque os arquivos na barra lateral, ou clique em Todos os arquivos.</span>`
          : m.show !== "all"
            ? `<strong>Nenhuma linha ${m.show === "new" ? "nova" : "já gravada"}</strong><span>${m.show === "new" ? "Tudo o que está marcado já está na planilha." : "Nada do que está marcado está na planilha ainda."}</span>`
            : !m.isAll()
              ? `<strong>${m.selection.length === 1 ? "Este arquivo não entrou" : "Estes arquivos não entraram"} na tabela</strong><span>O motivo está em Erros e avisos, acima.</span>`
          : `<strong>Nenhuma linha</strong><span>Nenhum arquivo trouxe linhas com "Internal char no." preenchido. Veja os erros e avisos acima.</span>`;
    }
    this.range = [-1, -1];
    this.cols = [-1, -1];
    this.paint();
  }

  measure(res) {
    const dec = this.model.settings.decimal;
    const sample = res.rows.slice(0, 800);
    return res.cols.map((c) => {
      const code = !CBA.BASE_COLS.includes(c);
      let w = c.length * (code ? 7.4 : 7.1) + 40;
      for (const r of sample) w = Math.max(w, CBA.displayValue(r, c, dec).length * 7.2 + 28);
      return Math.round(Math.min(340, Math.max(76, w)));
    });
  }

  // Larguras fixas por coluna: com elas dá para desenhar só as colunas visíveis (como as linhas) e o resto da
  // largura vira uma célula vazia de cada lado. Com as características fora da lista são mais de mil colunas.
  layout() {
    this.left = [0];
    for (const w of this.widths) this.left.push(this.left[this.left.length - 1] + w);
    this.table.style.width = this.left[this.left.length - 1] + "px";
    this.table.querySelector("colgroup").innerHTML = this.widths.map((w) => `<col style="width:${w}px">`).join("");
  }

  visibleCols() {
    const n = this.res.cols.length;
    if (n <= 40) return [0, n];
    const x0 = this.scroll.scrollLeft, x1 = x0 + this.scroll.clientWidth;
    let c0 = 0;
    while (c0 < n && this.left[c0 + 1] <= x0) c0++;
    let c1 = c0;
    while (c1 < n && this.left[c1] < x1) c1++;
    return [Math.max(0, c0 - 3), Math.min(n, c1 + 3)];
  }

  renderHead(c0, c1) {
    const { cols } = this.res;
    const sort = this.model.sort;
    const pad = (k) => (k > 0 ? `<th class="hsp" colspan="${k}" aria-hidden="true"></th>` : "");
    this.table.tHead.innerHTML = "<tr>" + pad(c0) + cols.slice(c0, c1).map((c) => {
      const cls = [CBA.BASE_COLS.includes(c) ? "" : "code", CBA.NUM_COLS.has(c) ? "n" : ""].join(" ").trim();
      const s = sort && sort.col === c ? icon(sort.dir === "asc" ? "up" : "down") : "";
      const aria = sort && sort.col === c ? (sort.dir === "asc" ? "ascending" : "descending") : "none";
      return `<th scope="col" aria-sort="${aria}"${cls ? ` class="${cls}"` : ""}><button type="button" data-col="${esc(c)}" title="Ordenar por ${esc(c)}">${esc(c)}${s}</button></th>`;
    }).join("") + pad(cols.length - c1) + "</tr>";
  }

  schedule(force) {
    if (force) { this.range = [-1, -1]; this.cols = [-1, -1]; }
    if (this.raf) return;
    this.raf = requestAnimationFrame(() => { this.raf = 0; this.paint(); });
  }

  paint() {
    if (!this.res) return;
    const n = this.rows.length;
    const cols = this.res.cols;
    const top = Math.max(0, this.scroll.scrollTop - this.headH);
    const start = Math.max(0, Math.floor(top / this.rowH) - 10);
    const end = Math.min(n, start + Math.ceil(this.scroll.clientHeight / this.rowH) + 20);
    const [c0, c1] = this.visibleCols();
    if (start === this.range[0] && end === this.range[1] && c0 === this.cols[0] && c1 === this.cols[1]) return;
    if (c0 !== this.cols[0] || c1 !== this.cols[1]) this.renderHead(c0, c1);
    this.range = [start, end];
    this.cols = [c0, c1];
    const shown = cols.slice(c0, c1);
    const lead = c0 > 0 ? `<td class="hsp" colspan="${c0}"></td>` : "";
    const tail = c1 < cols.length ? `<td class="hsp" colspan="${cols.length - c1}"></td>` : "";
    const dec = this.model.settings.decimal;
    const pend = this.model.pending().set;
    const spacer = (rows) => `<tr class="spacer" aria-hidden="true"><td colspan="${cols.length}" style="height:${rows * this.rowH}px"></td></tr>`;
    let h = start ? spacer(start) : "";
    for (let i = start; i < end; i++) {
      const r = this.rows[i];
      const cls = [i % 2 ? "alt" : "", pend.has(r) ? "new" : ""].join(" ").trim();
      h += `<tr${cls ? ` class="${cls}"` : ""}${pend.has(r) ? ' title="Novo: ainda não está na planilha"' : ""}>` + lead + shown.map((c) => {
        const t = CBA.displayValue(r, c, dec);
        const n = CBA.NUM_COLS.has(c);
        return `<td${n ? ' class="n"' : ""}${t.length > 36 ? ` title="${esc(t)}"` : ""}>${esc(t)}</td>`;
      }).join("") + tail + "</tr>";
    }
    if (end < n) h += spacer(n - end);
    this.table.tBodies[0].innerHTML = h;
  }
}

function SettingsSheet(model) {
  const back = $("settings");
  const sheetInput = $("opt-sheet");
  const columns = $("opt-columns");
  const extras = $("opt-extras");
  let lastFocus = null;
  model.observe(["settingsOpen", "settings"], () => {
    const s = model.settings;
    if (document.activeElement !== sheetInput) sheetInput.value = s.sheet;
    if (document.activeElement !== columns) columns.value = s.columns.join("\n");
    extras.checked = s.extras;
    $("opt-backup").checked = s.backup;
    document.documentElement.dataset.theme = s.theme;
    for (const b of document.querySelectorAll("#opt-theme [data-theme-choice]")) b.setAttribute("aria-checked", String(b.dataset.themeChoice === s.theme));
    for (const b of document.querySelectorAll("#opt-decimal [data-decimal]")) b.setAttribute("aria-checked", String(b.dataset.decimal === s.decimal));
    for (const b of document.querySelectorAll("#opt-repeated [data-repeated]")) b.setAttribute("aria-checked", String(b.dataset.repeated === s.repeated));
    const open = model.settingsOpen;
    if (open && back.hidden) { lastFocus = document.activeElement; back.hidden = false; sheetInput.focus(); }
    if (!open && !back.hidden) { back.hidden = true; if (lastFocus) lastFocus.focus(); }
  });
  sheetInput.addEventListener("change", () => model.updateSettings({ sheet: sheetInput.value.trim() || "Data" }));
  let typing;
  columns.addEventListener("input", () => {
    clearTimeout(typing);
    typing = setTimeout(() => {
      const cols = [];
      for (const line of columns.value.split(/\r?\n/)) { const c = line.trim(); if (c && !cols.includes(c)) cols.push(c); }
      model.updateSettings({ columns: cols });
    }, 500);
  });
  extras.addEventListener("change", () => model.updateSettings({ extras: extras.checked }));
  $("opt-backup").addEventListener("change", (ev) => model.updateSettings({ backup: ev.target.checked }));
  $("opt-theme").addEventListener("click", (ev) => {
    const b = ev.target.closest("[data-theme-choice]");
    if (b) model.updateSettings({ theme: b.dataset.themeChoice });
  });
  $("opt-decimal").addEventListener("click", (ev) => {
    const b = ev.target.closest("[data-decimal]");
    if (b) model.updateSettings({ decimal: b.dataset.decimal });
  });
  $("opt-repeated").addEventListener("click", (ev) => {
    const b = ev.target.closest("[data-repeated]");
    if (b) model.updateSettings({ repeated: b.dataset.repeated });
  });
  back.addEventListener("click", (ev) => { if (ev.target === back) model.settingsOpen = false; });
}

// Em cima da tabela: Todas / Novas / Já gravadas, para conferir antes de gravar, e Regravar o arquivo aberto.
function TableBar(model) {
  model.observe(["planilha", "show", "selection", "result", "mode"], () => {
    const pl = model.planilha;
    $("table-bar").hidden = !pl || !!pl.error || model.mode !== "folder";
    for (const b of document.querySelectorAll("#show-filter [data-show]")) b.setAttribute("aria-checked", String(b.dataset.show === model.show));
    const rels = model.selection;
    $("regravar").hidden = !rels || !rels.length || !pl || model.savedIn(rels) === 0;
    $("regravar").textContent = rels && rels.length > 1 ? `Regravar os ${fmtInt(rels.length)} arquivos marcados na planilha…` : "Regravar este arquivo na planilha…";
  });
  $("show-filter").addEventListener("click", (ev) => {
    const b = ev.target.closest("[data-show]");
    if (b) model.show = b.dataset.show;
  });
}

// Como um .alert do SwiftUI: pergunta antes de uma ação que muda a planilha.
function ConfirmAlert(model) {
  const back = $("confirm");
  model.observe(["confirm"], () => {
    const c = model.confirm;
    back.hidden = !c;
    if (!c) return;
    $("confirm-title").textContent = c.title;
    $("confirm-text").textContent = c.text;
    $("confirm-yes").textContent = c.yes;
    $("confirm-yes").focus();
  });
  back.addEventListener("click", (ev) => { if (ev.target === back) model.confirm = null; });
}

function Toast(model) {
  const el = $("toast");
  let timer;
  model.observe(["toast"], () => {
    clearTimeout(timer);
    const t = model.toast;
    el.hidden = !t;
    if (!t) return;
    const sym = t.kind === "error" ? icon("error", "tint-red") : t.kind === "saved" || t.kind === "ok" ? icon("ok", "tint-green") : icon("info", "tint-blue");
    const acts = t.kind === "saved"
      ? `<span class="acts"><button class="btn small prominent" type="button" data-action="open-saved">Abrir no Excel</button><button class="btn small" type="button" data-action="reveal-saved">Mostrar na pasta</button></span>`
      : "";
    el.innerHTML = `${sym}<span>${esc(t.text)}</span>${acts}`;
    timer = setTimeout(() => { model.toast = null; }, t.kind === "error" ? 14000 : 9000);
  });
}

// ---------- montagem ----------

function mount(model) {
  FolderCard(model);
  FileList(model);
  Toolbar(model);
  MoreMenu();
  Screens(model);
  Stats(model);
  Issues(model);
  new LazyTable(model);
  SettingsSheet(model);
  PlanilhaCard(model);
  TableBar(model);
  ConfirmAlert(model);
  Toast(model);

  const actions = {
    choose: () => model.chooseFolder(),
    sample: () => model.showSample(),
    refresh: () => model.refresh(),
    export: () => model.exportFile("xlsx"),
    gravar: () => model.gravar(),
    "choose-planilha": () => model.choosePlanilha(),
    "ask-regravar": () => model.askRegravar(),
    "ask-organizar": () => model.askOrganizar(),
    "confirm-yes": () => { const c = model.confirm; model.confirm = null; if (c) c.run(); },
    "confirm-no": () => { model.confirm = null; },
    csv: () => model.exportFile("csv"),
    copy: () => model.copyTable(),
    settings: () => { model.settingsOpen = true; },
    "close-settings": () => { model.settingsOpen = false; },
    "reset-settings": () => model.resetSettings(),
    "include-extras": () => model.includeExtras(),
    "open-saved": () => model.openSaved("open"),
    "reveal-saved": () => model.openSaved("reveal")
  };
  document.addEventListener("click", (ev) => {
    const el = ev.target.closest("[data-action]");
    if (el && !el.disabled && actions[el.dataset.action]) actions[el.dataset.action]();
  });

  document.addEventListener("keydown", (ev) => {
    const k = ev.key.toLowerCase();
    if (ev.key === "F5" || (ev.ctrlKey && k === "r")) { ev.preventDefault(); if (!model.busy && model.mode !== "none") model.refresh(); }
    else if (ev.ctrlKey && k === "f") { ev.preventDefault(); $("search").focus(); $("search").select(); }
    else if (ev.ctrlKey && k === "e") { ev.preventDefault(); if (model.result && model.result.rows.length) model.exportFile("xlsx"); }
    else if (ev.ctrlKey && k === "s") { ev.preventDefault(); if (!$("primary").disabled && $("primary").dataset.action === "gravar") model.gravar(); }
    else if (ev.key === "Escape" && model.confirm) model.confirm = null;
    else if (ev.ctrlKey && k === ",") { ev.preventDefault(); model.settingsOpen = true; }
    else if (ev.key === "Escape" && model.settingsOpen) model.settingsOpen = false;
    else if (ev.ctrlKey && (k === "p" || k === "u")) ev.preventDefault();
  });
  $("path-form").addEventListener("submit", (ev) => {
    ev.preventDefault();
    const path = $("path-input").value.trim().replace(/^"(.*)"$/, "$1"); // o "Copiar como caminho" do Explorer vem com aspas
    if (path) model.openFolder(path);
  });
  // Arquivos soltos na janela não podem trocar a tela.
  window.addEventListener("dragover", (ev) => ev.preventDefault());
  window.addEventListener("drop", (ev) => ev.preventDefault());
}

const token = document.querySelector('meta[name="token"]').content;
const native = new NativeBridge(token);
const model = new AppModel(native);
mount(model);
model.start();
native.ping();
setInterval(() => native.ping(), 10000);
window.cbaModel = model; // para os testes
})();
