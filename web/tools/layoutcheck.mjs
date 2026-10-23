// Contrôle de mise en page du dashboard dans Chrome (sans fenêtre) : chaque page, six largeurs d'écran
// (1440 à 360 px), thèmes clair et sombre. Signale : page plus large que l'écran, élément qui dépasse
// à droite, contenu coupé ou barre de défilement interne, texte coupé dans un champ, texte d'un
// graphique hors de son cadre, trop petit à l'écran (< 9,5 px) ou superposé à un autre, erreurs de la
// console et refus de la CSP.
//
//   go run -tags dev ./cmd/etl-dev &                       (ou un vrai nœud)
//   node web/tools/layoutcheck.mjs [http://localhost:3001] [répertoire de captures]
//   ETL_LANG=ja node web/tools/layoutcheck.mjs              (langue de l'interface : fr, en, ja, zh)
//
// Node 20 : ajouter --experimental-websocket. Chrome doit être installé (CHROME pour un autre chemin).
// Code de sortie 1 si un problème est trouvé. Hors de « node --test » : il faut Chrome et un nœud.

import { spawn } from "node:child_process";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const BASE = process.argv[2] ?? "http://localhost:3001";
const SHOTS = process.argv[3];
const LANG = process.env.ETL_LANG ?? "";
const CHROME = process.env.CHROME ?? "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome";
const PORT = 9400 + Math.floor(Math.random() * 400);
const ROUTES = ["", "wallet", "escrow", "ipc", "operator"];
const WIDTHS = [1440, 1280, 1024, 768, 390, 360];
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// Mesures faites dans la page.
const PROBE = `(() => {
  const out = [], vw = document.documentElement.clientWidth;
  if (document.documentElement.scrollWidth > vw + 1) out.push("page plus large que l'écran : " + document.documentElement.scrollWidth + " > " + vw);
  const name = (e) => e.tagName.toLowerCase() + (typeof e.className === "string" && e.className.trim() ? "." + e.className.trim().split(/\\s+/).join(".") : "");
  for (const e of document.querySelectorAll("body *")) {
    if (e.closest("svg") && e.tagName.toLowerCase() !== "svg") continue;
    const cs = getComputedStyle(e);
    if (cs.display === "none" || cs.visibility === "hidden") continue;
    const r = e.getBoundingClientRect();
    if (!r.width) continue;
    if (r.right > vw + 1 && cs.position !== "fixed") out.push("dépasse à droite : " + name(e) + " (" + Math.round(r.right) + " > " + vw + ")");
    const tag = e.tagName.toLowerCase();
    if (/(hidden|auto|scroll|clip)/.test(cs.overflowX + cs.overflowY) && !["canvas", "svg", "textarea", "input", "select"].includes(tag)) {
      if (e.scrollWidth > e.clientWidth + 1) out.push("contenu coupé ou défilant en largeur : " + name(e));
      if (e.scrollHeight > e.clientHeight + 1) out.push("contenu coupé ou défilant en hauteur : " + name(e));
    }
    if (tag === "input" && ["text", "password"].includes(e.type) && e.value && e.scrollWidth > e.clientWidth + 1) out.push("texte coupé dans un champ : " + name(e));
  }
  for (const s of document.querySelectorAll("svg[viewBox]")) {
    const vb = s.viewBox.baseVal;
    const scale = s.getBoundingClientRect().width / vb.width;
    for (const t of s.querySelectorAll("text")) {
      const px = parseFloat(getComputedStyle(t).fontSize) * scale;
      if (!t.classList.contains("watermark")) {
        const r = t.getBoundingClientRect();
        for (const u of s.querySelectorAll("text")) {
          if (u === t || u.classList.contains("watermark") || !(t.compareDocumentPosition(u) & Node.DOCUMENT_POSITION_FOLLOWING)) continue;
          const q = u.getBoundingClientRect();
          if (r.width && q.width && r.left < q.right - 1 && q.left < r.right - 1 && r.top < q.bottom - 1 && q.top < r.bottom - 1) out.push("textes superposés : « " + t.textContent + " » et « " + u.textContent + " »");
        }
      }
      if (px < 9.5) out.push("texte de graphique trop petit (" + px.toFixed(1) + " px) : « " + t.textContent + " »");
      const b = t.getBBox();
      if (b.x < vb.x - 1 || b.y < vb.y - 1 || b.x + b.width > vb.x + vb.width + 1 || b.y + b.height > vb.y + vb.height + 1) out.push("texte hors de son graphique : « " + t.textContent + " »");
    }
  }
  return [...new Set(out)];
})()`;

class Chrome {
  constructor() {
    this.profile = mkdtempSync(join(tmpdir(), "etl-layout-"));
    this.proc = spawn(CHROME, ["--headless=new", `--remote-debugging-port=${PORT}`, `--user-data-dir=${this.profile}`, "--no-first-run",
      "--no-default-browser-check", "about:blank"], { stdio: "ignore" });
    this.seq = 0;
    this.pending = new Map();
    this.errors = [];
  }
  async connect() {
    for (let i = 0; i < 50; i++) {
      try {
        const page = (await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json()).find((t) => t.type === "page");
        if (page) {
          this.ws = new WebSocket(page.webSocketDebuggerUrl);
          await new Promise((r) => (this.ws.onopen = r));
          this.ws.onmessage = (m) => this.onMessage(JSON.parse(m.data));
          for (const d of ["Page", "Runtime", "Log"]) await this.send(`${d}.enable`);
          return;
        }
      } catch { /* Chrome démarre */ }
      await sleep(200);
    }
    throw new Error("Chrome injoignable (variable CHROME ?)");
  }
  onMessage(m) {
    if (m.id && this.pending.has(m.id)) {
      const p = this.pending.get(m.id);
      this.pending.delete(m.id);
      m.error ? p.rej(new Error(m.error.message)) : p.res(m.result);
    } else if (m.method === "Runtime.exceptionThrown") this.errors.push("exception : " + m.params.exceptionDetails.exception?.description);
    else if (m.method === "Runtime.consoleAPICalled" && ["error", "warning"].includes(m.params.type)) this.errors.push("console : " + m.params.args.map((a) => a.value ?? a.description).join(" "));
    else if (m.method === "Log.entryAdded" && ["error", "warning"].includes(m.params.entry.level)) this.errors.push("journal : " + m.params.entry.text);
  }
  send(method, params = {}) {
    const id = ++this.seq;
    this.ws.send(JSON.stringify({ id, method, params }));
    return new Promise((res, rej) => this.pending.set(id, { res, rej }));
  }
  async ev(expr) {
    const r = await this.send("Runtime.evaluate", { expression: expr, awaitPromise: true, returnByValue: true });
    if (r.exceptionDetails) throw new Error(r.exceptionDetails.exception?.description ?? "évaluation");
    return r.result.value;
  }
  // close arrête Chrome, attend sa fin (il écrit encore dans son profil), puis supprime le profil.
  async close() {
    try { this.ws?.close(); } catch { /* déjà fermé */ }
    const exited = new Promise((r) => this.proc.once("exit", r));
    this.proc.kill();
    await Promise.race([exited, sleep(5000)]);
    try {
      rmSync(this.profile, { recursive: true, force: true, maxRetries: 5, retryDelay: 200 });
    } catch { /* profil temporaire : le système le nettoiera */ }
  }
}

const c = new Chrome();
let problems = 0;
try {
  await c.connect();
  if (LANG) {
    await c.send("Page.navigate", { url: `${BASE}/` });
    await sleep(800);
    await c.ev(`localStorage.setItem("etl.lang", ${JSON.stringify(LANG)}), true`);
    await c.send("Page.reload");
    await sleep(1500);
    const shown = await c.ev("document.documentElement.lang");
    if (!shown.startsWith(LANG)) throw new Error(`langue non appliquée : ${shown} au lieu de ${LANG}`);
  }
  for (const theme of ["light", "dark"]) {
    await c.send("Emulation.setEmulatedMedia", { features: [{ name: "prefers-color-scheme", value: theme }] });
    for (const w of WIDTHS) {
      await c.send("Emulation.setDeviceMetricsOverride", { width: w, height: 900, deviceScaleFactor: 1, mobile: w < 800 });
      for (const route of ROUTES) {
        await c.send("Page.navigate", { url: `${BASE}/#/${route}` });
        await sleep(400);
        await c.ev(`new Promise((r) => { const t = Date.now(); (function w() { document.querySelector("main")?.children.length || Date.now() - t > 5000 ? r() : setTimeout(w, 50); })(); })`);
        await c.ev("document.fonts.ready.then(() => true)");
        await sleep(300);
        const found = [...(await c.ev(PROBE)), ...c.errors.splice(0)];
        if (found.length) {
          problems += found.length;
          console.log(`${theme} ${w}px #/${route} :\n  ${found.join("\n  ")}`);
        }
        if (SHOTS) {
          const r = await c.send("Page.captureScreenshot", { format: "png", captureBeyondViewport: true });
          writeFileSync(join(SHOTS, `${LANG ? LANG + "-" : ""}${theme}-${w}-${route || "home"}.png`), Buffer.from(r.data, "base64"));
        }
      }
    }
  }
  const fonts = await c.ev(`[...new Set([...document.fonts].filter((f) => f.status === "loaded").map((f) => f.family))].join(", ")`);
  console.log(`${LANG ? "[" + LANG + "] " : ""}${ROUTES.length} pages × ${WIDTHS.length} largeurs × 2 thèmes : ${problems ? problems + " problème(s)" : "aucun problème"} · polices chargées : ${fonts || "aucune"}`);
} catch (e) {
  console.log("ÉCHEC :", e.message);
  problems++;
} finally {
  await c.close();
}
process.exit(problems ? 1 : 0);
