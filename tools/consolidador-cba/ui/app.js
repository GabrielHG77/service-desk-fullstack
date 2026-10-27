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
function today() {
  const d = new Date();
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
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
    this.selection = "all";    // "all" ou o caminho relativo do arquivo (identificador estável entre leituras)
    this.search = "";
    this.sort = null;          // { col, dir: "asc" | "desc" }
    this.settings = CBA.defaultSettings();
    this.settingsOpen = false;
    this.toast = null;
    observable(this, ["phase", "mode", "folder", "failure", "files", "busy", "progress", "result", "updatedAt",
      "selection", "search", "sort", "settings", "settingsOpen", "toast"]);
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
        extras: !!c.extras
      };
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
    this.selection = "all";
    await this.refresh();
  }

  async showSample() {
    this.source = new SampleSource();
    this.mode = "sample";
    this.folder = "";
    this.selection = "all";
    await this.refresh();
  }

  async refresh() {
    if (!this.source) return this.chooseFolder();
    const run = ++this.run;
    this.busy = true;
    this.progress = "Lendo a pasta…";
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
    if (this.selection !== "all" && !this.files.some((f) => f.rel === this.selection)) this.selection = "all";
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
        f.status = p.msgs.length ? "warn" : "ok";
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
    if (res.extras.length) {
      msgs.push({
        level: s.extras ? "info" : "warn",
        text: s.extras
          ? `${plural(res.extras.length, "característica fora da lista entrou", "características fora da lista entraram")} no fim da tabela: ${res.extras.join(", ")}.`
          : res.extras.length === 1
            ? `1 característica não está na lista de colunas e ficou de fora: ${res.extras[0]}.`
            : `${fmtInt(res.extras.length)} características não estão na lista de colunas e ficaram de fora: ${res.extras.join(", ")}.`,
        addExtras: !s.extras
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
    if (this.phase === "ready") this.consolidate();
  }
  resetSettings() { this.updateSettings(CBA.defaultSettings()); }
  includeExtras() { if (this.result) this.updateSettings({ columns: [...this.settings.columns, ...this.result.extras] }); }

  toggleSort(col) {
    const s = this.sort;
    this.sort = !s || s.col !== col ? { col, dir: "asc" } : s.dir === "asc" ? { col, dir: "desc" } : null;
  }

  // Derivado e memorizado: só recalcula quando resultado, seleção, busca ou ordem mudam.
  visibleRows() {
    const res = this.result;
    if (!res) return [];
    const key = [res, this.selection, this.search, this.sort];
    if (this.visKey && key.every((v, i) => v === this.visKey[i])) return this.vis;
    let rows = res.rows;
    if (this.selection !== "all") rows = rows.filter((r) => r.file === this.selection);
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

function FileList(model) {
  const el = $("file-list");
  model.observe(["files", "selection", "result"], () => {
    if (!model.files.length) { el.innerHTML = ""; return; }
    const total = model.result ? model.result.rows.length : 0;
    let h = row("all", icon("table", "tint-blue"), "Todos os arquivos", "", total ? fmtInt(total) : "", "Todos os arquivos");
    h += '<li class="divider" role="separator"></li>';
    for (const f of model.files) {
      const dir = f.rel.includes("/") ? f.rel.slice(0, f.rel.lastIndexOf("/") + 1) : "";
      const count = f.status === "ok" || f.status === "warn" ? fmtInt(f.rows) : "";
      const tip = f.rel + (f.note ? " — " + f.note : f.status === "temp" ? " — arquivo temporário, ignorado" : "");
      h += row(f.rel, STATUS_ICON[f.status](), f.name, dir, count, tip);
    }
    el.innerHTML = h;
    function row(key, sym, name, dir, count, tip) {
      const sel = model.selection === key;
      return `<li role="option" aria-selected="${sel}" data-select="${esc(key)}" title="${esc(tip)}">${sym}` +
        `<span class="name">${dir ? `<span class="dim">${esc(dir)}</span>` : ""}${esc(name)}</span><span class="count">${count}</span></li>`;
    }
  });
  el.addEventListener("click", (ev) => {
    const li = ev.target.closest("[data-select]");
    if (li) model.selection = li.dataset.select;
  });
  el.addEventListener("keydown", (ev) => {
    if (ev.key !== "ArrowDown" && ev.key !== "ArrowUp") return;
    ev.preventDefault();
    const keys = ["all", ...model.files.map((f) => f.rel)];
    const i = keys.indexOf(model.selection) + (ev.key === "ArrowDown" ? 1 : -1);
    if (i >= 0 && i < keys.length) model.selection = keys[i];
    queueMicrotask(() => { const cur = el.querySelector('[aria-selected="true"]'); if (cur) cur.scrollIntoView({ block: "nearest" }); });
  });
}

function Toolbar(model) {
  model.observe(["selection", "files", "result", "busy", "progress", "updatedAt", "phase", "mode"], () => {
    const ready = model.phase === "ready";
    $("title").textContent = !ready ? "Consolidador CBA" : model.selection === "all" ? "Todos os arquivos" : model.fileName(model.selection);
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
    $("export").disabled = !hasRows || model.busy;
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
  model.observe(["result", "selection"], () => {
    const r = model.result;
    if (!r) { $("stats").innerHTML = ""; return; }
    const all = model.selection === "all";
    const rows = all ? r.rows.length : r.rows.filter((x) => x.file === model.selection).length;
    const msgs = all ? r.msgs : r.msgs.filter((m) => m.file === model.selection);
    const errors = msgs.filter((m) => m.level === "error").length;
    const warns = msgs.filter((m) => m.level === "warn").length;
    const attention = errors + warns;
    const tint = errors ? "tint-red" : warns ? "tint-orange" : "";
    const stat = (label, value, cls = "") => `<div class="card stat"><span>${label}</span><strong class="${cls}">${value}</strong></div>`;
    const file = all ? null : model.files.find((f) => f.rel === model.selection);
    const SITUATION = { ok: ["Lido", "tint-green"], warn: ["Lido com aviso", "tint-orange"], error: ["Não lido", "tint-red"], temp: ["Ignorado", ""] };
    const [situation, sitTint] = file ? SITUATION[file.status] || ["—", ""] : ["", ""];
    $("stats").innerHTML =
      stat("Linhas", fmtInt(rows)) +
      stat("Colunas", fmtInt(r.cols.length)) +
      (all ? stat("Arquivos lidos", fmtInt(r.fileCount)) : stat("Situação", situation, sitTint)) +
      stat(errors ? "Erros e avisos" : "Avisos", attention ? fmtInt(attention) : "Nenhum", tint);
  });
}

function Issues(model) {
  const box = $("issues");
  let lastResult = null;
  model.observe(["result", "selection"], () => {
    const r = model.result;
    const msgs = !r ? [] : model.selection === "all" ? r.msgs : r.msgs.filter((m) => m.file === model.selection);
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
    model.observe(["result", "selection", "search", "sort"], () => this.update());
  }

  update() {
    const m = this.model;
    const res = m.result;
    if (!res) return;
    if (res !== this.res) { this.res = res; this.widths = this.measure(res); }
    this.rows = m.visibleRows();
    this.scroll.scrollTop = 0;
    this.renderHead();
    const empty = $("table-empty");
    empty.hidden = this.rows.length > 0;
    if (!this.rows.length) {
      const q = m.search.trim();
      empty.innerHTML = q
        ? `<strong>Nenhum resultado para “${esc(q)}”</strong><span>Confira a grafia ou busque outro termo.</span>`
        : `<strong>Nenhuma linha</strong><span>Nenhum arquivo trouxe linhas com "Internal char no." preenchido. Veja os erros e avisos acima.</span>`;
    }
    this.range = [-1, -1];
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

  renderHead() {
    const { cols } = this.res;
    const sort = this.model.sort;
    this.table.style.width = this.widths.reduce((a, b) => a + b, 0) + "px";
    this.table.querySelector("colgroup").innerHTML = this.widths.map((w) => `<col style="width:${w}px">`).join("");
    this.table.tHead.innerHTML = "<tr>" + cols.map((c) => {
      const cls = [CBA.BASE_COLS.includes(c) ? "" : "code", CBA.NUM_COLS.has(c) ? "n" : ""].join(" ").trim();
      const s = sort && sort.col === c ? icon(sort.dir === "asc" ? "up" : "down") : "";
      const aria = sort && sort.col === c ? (sort.dir === "asc" ? "ascending" : "descending") : "none";
      return `<th scope="col" aria-sort="${aria}"${cls ? ` class="${cls}"` : ""}><button type="button" data-col="${esc(c)}" title="Ordenar por ${esc(c)}">${esc(c)}${s}</button></th>`;
    }).join("") + "</tr>";
  }

  schedule(force) {
    if (force) this.range = [-1, -1];
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
    if (start === this.range[0] && end === this.range[1]) return;
    this.range = [start, end];
    const dec = this.model.settings.decimal;
    const spacer = (rows) => `<tr class="spacer" aria-hidden="true"><td colspan="${cols.length}" style="height:${rows * this.rowH}px"></td></tr>`;
    let h = start ? spacer(start) : "";
    for (let i = start; i < end; i++) {
      const r = this.rows[i];
      h += `<tr${i % 2 ? ' class="alt"' : ""}>` + cols.map((c) => {
        const t = CBA.displayValue(r, c, dec);
        const n = CBA.NUM_COLS.has(c);
        return `<td${n ? ' class="n"' : ""}${t.length > 36 ? ` title="${esc(t)}"` : ""}>${esc(t)}</td>`;
      }).join("") + "</tr>";
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
    for (const b of document.querySelectorAll("#opt-decimal [data-decimal]")) b.setAttribute("aria-checked", String(b.dataset.decimal === s.decimal));
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
  $("opt-decimal").addEventListener("click", (ev) => {
    const b = ev.target.closest("[data-decimal]");
    if (b) model.updateSettings({ decimal: b.dataset.decimal });
  });
  back.addEventListener("click", (ev) => { if (ev.target === back) model.settingsOpen = false; });
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
  Toast(model);

  const actions = {
    choose: () => model.chooseFolder(),
    sample: () => model.showSample(),
    refresh: () => model.refresh(),
    export: () => model.exportFile("xlsx"),
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
    else if (ev.ctrlKey && k === "e") { ev.preventDefault(); if (!$("export").disabled) model.exportFile("xlsx"); }
    else if (ev.ctrlKey && k === ",") { ev.preventDefault(); model.settingsOpen = true; }
    else if (ev.key === "Escape" && model.settingsOpen) model.settingsOpen = false;
    else if (ev.ctrlKey && (k === "p" || k === "s" || k === "u")) ev.preventDefault();
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
