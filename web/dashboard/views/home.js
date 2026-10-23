// Accueil : la valeur de 1 ETL et pourquoi elle tient, le prochain bloc sur une règle de 24 h, l'état
// du nœud et du réseau.

import { h, svg, legible } from "../lib/dom.js";
import { formatRate, formatETL } from "../lib/amount.js";
import { localeOf } from "../lib/strings.js";

const DAY = 86400;
// Fenêtre de lecture de l'IPC par les nœuds (internal/oracle : 20:00 + 150 min, UTC).
const IPC_WINDOW = [20 * 3600, 22.5 * 3600];
// Simulation affichée sur l'accueil : hausse des prix de 2 % par an pendant 10 ans.
export const SIM_RATE = 0.02;
export const SIM_YEARS = 10;

const locale = localeOf;

// priceChange compare l'IPC courant à celui du lancement : sens et variation en %, deux décimales.
export function priceChange(ipc, base, lang) {
  if (!base || ipc === base) return { dir: "same", pct: "0" };
  const hundredths = (BigInt(Math.abs(ipc - base)) * 10000n + BigInt(base) / 2n) / BigInt(base);
  const pct = `${hundredths / 100n}${lang === "fr" ? "," : "."}${(hundredths % 100n).toString().padStart(2, "0")}`;
  return { dir: ipc > base ? "up" : "down", pct };
}

// simulation : ce que permettent d'acheter 100 € laissés sur un compte, année par année.
export function simulation(years = SIM_YEARS, rate = SIM_RATE) {
  return Array.from({ length: years + 1 }, (_, y) => 100 / Math.pow(1 + rate, y));
}

// countdown découpe une durée (secondes) en heures, minutes, secondes ; jamais négative.
export function countdown(seconds) {
  const s = Math.max(0, Math.floor(seconds));
  return { h: Math.floor(s / 3600), m: Math.floor((s % 3600) / 60), s: s % 60 };
}

export function countdownText(t, seconds) {
  const c = countdown(seconds);
  const pad = (n) => String(n).padStart(2, "0");
  if (c.h) return t("home.countdown.h", { h: c.h, m: pad(c.m), s: pad(c.s) });
  if (c.m) return t("home.countdown.m", { m: c.m, s: pad(c.s) });
  return t("home.countdown.s", { s: c.s });
}

const ipcText = (v, lang) => (v / 1000).toLocaleString(locale(lang), { minimumFractionDigits: 2, maximumFractionDigits: 3 });

// --- graphique de simulation ---

function simChart(t) {
  const W = 520, H = 230, L = 46, R = 14, T = 14, B = 34, LO = 75;
  const eur = simulation();
  const x = (y) => L + ((W - L - R) * y) / SIM_YEARS;
  const yv = (v) => T + ((H - T - B) * (100 - v)) / (100 - LO);
  const line = (vals) => vals.map((v, i) => `${i ? "L" : "M"}${x(i).toFixed(1)},${yv(v).toFixed(1)}`).join("");
  const grid = [100, 95, 90, 85, 80, 75].flatMap((v) => [
    svg("line", { class: "grid", x1: L, x2: W - R, y1: yv(v), y2: yv(v) }),
    svg("text", { x: L - 8, y: yv(v) + 4, "text-anchor": "end" }, String(v)),
  ]);
  const xl = [0, 5, 10].map((y) =>
    svg("text", { x: x(y), y: H - 8, "text-anchor": y === 0 ? "start" : y === SIM_YEARS ? "end" : "middle" },
      y === 0 ? t("home.sim.now") : t("home.sim.year", { n: y })));
  const last = eur[SIM_YEARS];
  return legible(svg("svg", { class: "chart", viewBox: `0 0 ${W} ${H}`, role: "img", "aria-label": t("home.sim.alt") },
    ...grid, ...xl,
    svg("path", { class: "area-loss", d: `${line(eur)} L${x(SIM_YEARS)},${yv(LO)} L${x(0)},${yv(LO)} Z` }),
    svg("text", { class: "watermark", x: (L + W - R) / 2, y: yv(87), "text-anchor": "middle" }, t("home.sim.watermark")),
    svg("path", { class: "line-eur", d: line(eur) }),
    svg("path", { class: "line-etl", d: line(eur.map(() => 100)) }),
    svg("circle", { class: "dot-etl", cx: x(SIM_YEARS), cy: yv(100), r: 4.5 }),
    svg("circle", { class: "dot-eur", cx: x(SIM_YEARS), cy: yv(last), r: 4.5 }),
    svg("text", { class: "lbl-etl", x: x(SIM_YEARS) - 8, y: yv(100) + 18, "text-anchor": "end" }, `${t("home.sim.etl")} : 100`),
    svg("text", { class: "lbl-eur", x: x(SIM_YEARS) - 8, y: yv(last) + 18, "text-anchor": "end" }, `100 € : ${Math.round(last)}`),
  ), W);
}

// --- règle graduée du jour (UTC) ---

export function rulePosition(unixSeconds) {
  return (((unixSeconds % DAY) + DAY) % DAY) / DAY;
}

function dayRule(t, now, blockTime) {
  const W = 600;
  const x = (frac) => (W * frac).toFixed(1);
  const parts = [];
  parts.push(svg("rect", { class: "win", x: x(IPC_WINDOW[0] / DAY), y: 22, width: x((IPC_WINDOW[1] - IPC_WINDOW[0]) / DAY), height: 8 }));
  for (let i = 0; i <= 24; i++) {
    const major = i % 6 === 0;
    parts.push(svg("line", { class: major ? "tick maj" : "tick", x1: x(i / 24), x2: x(i / 24), y1: major ? 14 : 20, y2: 30 }));
    if (major) parts.push(svg("text", { x: x(i / 24), y: 46, "text-anchor": i === 0 ? "start" : i === 24 ? "end" : "middle" },
      t("home.rule.hour", { h: String(i).padStart(2, "0") })));
  }
  const b = rulePosition(blockTime);
  parts.push(svg("rect", { class: "blk", x: (W * b - 3).toFixed(1), y: 6, width: 6, height: 26, rx: 2 }));
  parts.push(svg("text", { class: "blk-label", x: x(b), y: 2, "text-anchor": b > 0.95 ? "end" : "middle" }, t("home.rule.blockShort")));
  const nowLine = svg("line", { class: "now", x1: x(rulePosition(now)), x2: x(rulePosition(now)), y1: 8, y2: 32 });
  parts.push(nowLine);
  const el = legible(svg("svg", { viewBox: `0 -28 ${W} 86`, "aria-hidden": "true" }, ...parts), W);
  el.moveNow = (n) => { nowLine.setAttribute("x1", x(rulePosition(n))); nowLine.setAttribute("x2", x(rulePosition(n))); };
  return el;
}

// --- vue ---

function issueText(t, issue, st) {
  if (issue === "clock") return t("home.clock", { s: ((st.clock_offset_ms ?? 0) / 1000).toFixed(1) });
  return t(`home.issue.${issue}`);
}

function healthPanel(app) {
  const { status: st, t } = app;
  const issues = st.health?.issues ?? [];
  const chip = h("span", { class: issues.length ? "chip warn" : "chip good" }, t(issues.length ? "home.health.bad" : "home.health.ok"));
  return { chip, list: issues.length ? h("ul", { class: "issues", role: "alert" }, issues.map((i) => h("li", {}, issueText(t, i, st)))) : null };
}

function nodePanel(app) {
  const { status: st, t } = app;
  const status = st.node.address ? h("span", { class: st.node.status === "admitted" ? "chip good" : "chip neutral" }, t(`status.${st.node.status}`)) : null;
  const body = [];
  if (st.node.address) {
    const copy = h("button", { class: "link", type: "button", onClick: async () => {
      try {
        await navigator.clipboard.writeText(st.node.address);
        copy.textContent = t("home.copied");
        setTimeout(() => (copy.textContent = t("home.copy")), 2000);
      } catch { /* presse-papiers refusé : l'adresse reste sélectionnable */ }
    } }, t("home.copy"));
    body.push(h("p", { class: "addr" }, st.node.address), copy);
    if (st.node.examined) body.push(h("p", { class: "fine" }, t("home.participation", { n: st.node.participated, of: st.node.examined })));
    if (st.node.last_reward !== undefined) body.push(h("p", { class: "fine" }, t("home.lastReward", { amount: formatETL(BigInt(st.node.last_reward), app.lang) })));
  } else {
    body.push(h("p", { class: "muted" }, t("home.passive")));
  }
  return h("section", { class: "panel" },
    h("div", { class: "panel-head" }, h("h2", {}, t("home.node")), status),
    ...body,
    h("p", { class: "fine" }, t("home.chain", { chain: st.chain_id, version: st.version })));
}

function stat(label, value, note) {
  return h("div", { class: "stat" }, h("span", { class: "label" }, label), h("span", { class: "v" }, value), note ? h("span", { class: "s" }, note) : null);
}

export function renderHome(app) {
  const { status: st, lang, t } = app;
  if (!st) return h("p", { class: "msg error" }, t("error.network"));

  const change = priceChange(st.ipc.value, st.base_ipc.value, lang);
  const explain = change.dir === "same"
    ? t("home.rateSame", { base: ipcText(st.base_ipc.value, lang), period: st.base_ipc.period })
    : t(change.dir === "up" ? "home.rateUp" : "home.rateDown", { pct: change.pct, base: ipcText(st.base_ipc.value, lang), now: ipcText(st.ipc.value, lang) });

  const hero = h("section", { class: "hero" },
    h("div", { class: "hero-value" },
      h("span", { class: "label" }, t("home.rateLabel")),
      h("p", { class: "eq" }, h("span", {}, "1 ETL"), h("span", { class: "unit" }, "="), h("em", {}, `${formatRate(st.ipc.value, st.base_ipc.value, lang)} €`)),
      h("p", { class: "explain" }, explain),
      h("div", { class: "engrave", "aria-hidden": "true" })),
    h("div", { class: "hero-sim" },
      h("span", { class: "sim" }, t("home.sim.badge")),
      h("span", { class: "label" }, t("home.sim.title")),
      simChart(t),
      h("div", { class: "legend" },
        h("span", {}, h("i", { class: "key-etl" }), t("home.sim.etl")),
        h("span", {}, h("i", { class: "key-eur" }), t("home.sim.eur")))));

  const nextAt = new Date(st.next_block * 1000);
  const cd = h("span", { class: "countdown" }, countdownText(t, st.next_block - Date.now() / 1000));
  // Heure du bloc du réseau (23:00 UTC) ; le compte à rebours suit le prochain bloc annoncé par le nœud.
  const rule = dayRule(t, Date.now() / 1000, st.block_time);
  const blockUTC = new Date(st.block_time * 1000).toISOString().slice(11, 16);
  const health = healthPanel(app);
  const nextPanel = h("section", { class: "panel dayrule" },
    h("div", { class: "panel-head" }, h("h2", {}, t("home.next")), health.chip),
    h("div", { class: "next-row" }, cd,
      h("span", { class: "muted" }, t("home.nextAt", {
        day: nextAt.toLocaleDateString(locale(lang), { weekday: "long" }),
        time: nextAt.toLocaleTimeString(locale(lang), { hour: "2-digit", minute: "2-digit" }),
      }))),
    rule,
    h("div", { class: "key" },
      h("span", {}, h("i", { class: "key-now" }), t("home.rule.now")),
      h("span", {}, h("i", { class: "key-win" }), t("home.rule.ipc")),
      h("span", {}, h("i", { class: "key-blk" }), t("home.rule.block", { time: blockUTC }))),
    health.list);

  // Compte à rebours en direct, arrêté par app.render au changement de page. Bloc attendu et non
  // signalé par le flux d'événements : un seul rafraîchissement de l'état.
  let refreshed = false;
  app.tick = setInterval(() => {
    const now = Date.now() / 1000;
    cd.textContent = countdownText(t, st.next_block - now);
    rule.moveNow(now);
    if (!refreshed && now >= st.next_block + 5 && app.refresh) {
      refreshed = true;
      app.refresh();
    }
  }, 1000);

  const stats = h("section", { class: "stats" },
    stat(t("home.blocks"), st.height.toLocaleString(locale(lang)), t("home.blocksNote")),
    stat(t("home.active"), String(st.active_nodes), t("home.activeNote")),
    st.peers ? stat(t("home.peers"), String(st.peers.in + st.peers.out), t("home.peersValue", st.peers)) : null,
    stat(t("home.ipc"), ipcText(st.ipc.value, lang), t("home.ipcPeriod", { period: st.ipc.period })),
    st.supply !== undefined ? stat(t("home.supply"), formatETL(BigInt(st.supply), lang), t("home.supplyNote")) : null);

  return h("div", { class: "page" },
    h("div", { class: "page-head" }, h("div", {}, h("span", { class: "label" }, t("home.kicker")), h("h1", {}, t("home.title")))),
    hero,
    h("div", { class: "split" }, nextPanel, nodePanel(app)),
    stats);
}
