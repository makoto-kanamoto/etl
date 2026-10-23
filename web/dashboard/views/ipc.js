// Page IPC : la valeur de 1 ETL et d'où elle vient. IPC en vigueur, au lancement, courbe de l'IPC
// adopté trimestre après trimestre (en-têtes de la chaîne), vote en cours face au seuil des 2/3,
// lecture de ce nœud et « relire la source » (session opérateur). Aucune saisie de valeur (D7).

import { h, svg, clear, put, legible } from "../lib/dom.js";
import { formatRate } from "../lib/amount.js";
import { localeOf } from "../lib/strings.js";
import { message, pageHead } from "../lib/ui.js";
import { TOKEN_KEY } from "./operator.js";

function token() {
  try {
    return sessionStorage.getItem(TOKEN_KEY);
  } catch {
    return null;
  }
}

// inseePage : page lisible de la série INSEE lue par le nœud (URL de l'API BDM), sinon null.
export function inseePage(sourceURL) {
  const m = /^https:\/\/api\.insee\.fr\/series\/BDM\/V1\/data\/SERIES_BDM\/(\d+)$/.exec(sourceURL ?? "");
  return m ? `https://www.insee.fr/fr/statistiques/serie/${m[1]}` : null;
}

// missingVotes : voix qui manquent au triplet le plus soutenu pour atteindre le seuil (0 : atteint).
export function missingVotes(votes, quorum) {
  return Math.max(0, quorum - votes);
}

// chartScale : bornes de l'axe vertical, arrondies au point d'indice, avec une marge.
export function chartScale(values) {
  const lo = Math.min(...values), hi = Math.max(...values);
  const pad = Math.max(500, Math.round((hi - lo) * 0.15)); // valeurs × 1000 : 0,5 point au moins
  return [Math.floor((lo - pad) / 1000) * 1000, Math.ceil((hi + pad) / 1000) * 1000];
}

export function chart(app, points, index) {
  const { t } = app;
  const W = 560, H = 230, L = 74, R = 18, T = 22, B = 34;
  const [lo, hi] = chartScale(points.map((p) => p.value));
  const n = points.length;
  const x = (i) => (n === 1 ? (L + W - R) / 2 : L + ((W - L - R) * i) / (n - 1));
  const y = (v) => T + ((H - T - B) * (hi - v)) / (hi - lo);
  const step = Math.max(1000, Math.ceil((hi - lo) / 4000) * 1000);
  const grid = [];
  for (let v = lo; v <= hi; v += step) {
    grid.push(svg("line", { class: "grid", x1: L, x2: W - R, y1: y(v), y2: y(v) }), svg("text", { x: L - 8, y: y(v) + 4, "text-anchor": "end" }, index(v)));
  }
  const every = Math.ceil(n / 6);
  const xl = points.map((p, i) => (i % every === 0 || i === n - 1)
    ? svg("text", { x: x(i), y: H - 8, "text-anchor": n === 1 ? "middle" : i === 0 ? "start" : i === n - 1 ? "end" : "middle" }, p.period) : null);
  const d = points.map((p, i) => `${i ? "L" : "M"}${x(i).toFixed(1)},${y(p.value).toFixed(1)}`).join("");
  const last = points[n - 1];
  return legible(svg("svg", { class: "chart", viewBox: `0 0 ${W} ${H}`, role: "img", "aria-label": t("ipc.chart.alt") },
    ...grid, ...xl,
    n > 1 ? svg("path", { class: "area", d: `${d} L${x(n - 1)},${H - B} L${x(0)},${H - B} Z` }) : null,
    n > 1 ? svg("path", { class: "line", d }) : null,
    svg("line", { class: "launch", x1: x(0), x2: x(0), y1: T, y2: H - B }),
    svg("text", { class: "launch-label", x: x(0) + 6, y: T + 10 }, t("ipc.chart.launch", { value: index(points[0].value) })),
    svg("circle", { class: "dot", cx: x(n - 1), cy: y(last.value), r: 5 }),
    n > 1 ? svg("text", { class: "last-label", x: x(n - 1) - 8, y: y(last.value) - 10, "text-anchor": "end" }, index(last.value)) : null), W);
}

export function renderIPC(app) {
  const { t, lang } = app;
  let notice = null; // message conservé à travers le rechargement de la page
  const body = h("div", { class: "stack" }, h("p", { class: "muted" }, "…"));
  const verify = h("span", {});
  const root = h("div", { class: "page ipc" }, pageHead(t("ipc.kicker"), t("ipc.title"), verify), body);
  const locale = localeOf(lang);
  const index = (v) => (v / 1000).toLocaleString(locale, { minimumFractionDigits: 2, maximumFractionDigits: 3 });
  const when = (s) => new Date(s * 1000).toLocaleString(locale, { dateStyle: "medium", timeStyle: "short" });
  const date = (s) => new Date(s * 1000).toLocaleDateString(locale, { dateStyle: "medium" });
  const stat = (label, value, sub) => h("section", { class: "panel stat" }, h("span", { class: "label" }, label), h("span", { class: "v" }, value), h("span", { class: "s" }, sub));

  const load = async () => {
    let d;
    try {
      d = await app.api.ipc();
    } catch {
      return put(clear(body), message("error", t("error.network")));
    }
    const page = inseePage(d.source_url);
    put(clear(verify), page ? h("a", { class: "button", href: page, target: "_blank", rel: "noopener noreferrer" }, t("ipc.verify")) : null);
    const hist = d.history?.length ? d.history : [{ ...d.base, height: 0, time: 0 }];
    const adopted = hist[hist.length - 1];

    const top = d.pending.slice().sort((a, b) => b.votes - a.votes)[0];
    const gauge = top ? (() => {
      const miss = missingVotes(top.votes, d.quorum);
      const fill = h("span", { class: "fill" });
      fill.style.width = `${Math.min(100, (100 * top.votes) / Math.max(1, d.active_nodes))}%`;
      const need = h("span", { class: "need" });
      need.style.left = `${Math.min(100, (100 * d.quorum) / Math.max(1, d.active_nodes))}%`;
      return h("section", { class: "panel" },
        h("div", { class: "panel-head" }, h("h2", {}, t("ipc.vote.title", { period: top.record.period })),
          h("span", { class: miss ? "chip warn" : "chip good" }, miss ? t(miss === 1 ? "ipc.vote.missing1" : "ipc.vote.missingN", { n: miss }) : t("ipc.vote.reached"))),
        h("p", { class: "vote-value" }, h("span", { class: "big" }, index(top.record.value)),
          h("span", { class: "muted" }, t("ipc.vote.by", { votes: top.votes, active: d.active_nodes }))),
        h("div", { class: "gauge", role: "img", "aria-label": t("ipc.votes", { votes: top.votes, quorum: d.quorum }) }, fill, need),
        h("p", { class: "fine" }, t("ipc.vote.rule", { quorum: d.quorum })),
        d.pending.length > 1 ? h("p", { class: "fine" }, t("ipc.vote.others", { list: d.pending.filter((p) => p !== top).map((p) => `${p.record.period} = ${index(p.record.value)} (${p.votes})`).join(" · ") })) : null);
    })() : h("section", { class: "panel" }, h("h2", {}, t("ipc.pending")), h("p", { class: "muted" }, t("ipc.pendingEmpty")));

    const out = h("div");
    if (notice) put(out, notice);
    notice = null;
    const o = d.oracle;
    const readNow = token()
      ? h("button", { type: "button", onClick: async () => {
          try {
            await app.api.operator(token()).ipcRead();
            notice = message("ok", t("ipc.done"));
            load();
          } catch (e) {
            put(clear(out), message("error", e.message === "network" ? t("error.network") : e.message));
          }
        } }, t("ipc.readNow"))
      : h("p", { class: "fine" }, h("a", { href: "#/operator" }, t("ipc.needToken")));
    const oracle = h("section", { class: "panel" },
      h("h2", {}, t("ipc.oracle")),
      h("p", { class: "fine" }, t("ipc.auto")),
      o ? h("dl", { class: "pair" },
        h("dt", {}, t("ipc.lastReadLabel")), h("dd", {}, o.last_attempt ? when(o.last_attempt) : t("ipc.never")),
        o.read ? [h("dt", {}, t("ipc.readLabel")), h("dd", {}, `${o.read.period} = ${index(o.read.value)} · ${o.voted ? t("ipc.voted") : t("ipc.notVoted")}`)] : null,
        o.next_read ? [h("dt", {}, t("ipc.nextLabel")), h("dd", {}, when(o.next_read))] : null) : h("p", { class: "muted" }, t("ipc.passive")),
      o?.error ? message("error", t("ipc.error", { error: o.error })) : null,
      o ? readNow : null,
      out);

    put(clear(body),
      h("div", { class: "grid-3" },
        stat(t("ipc.current"), index(d.current.value), adopted.height === 0
          ? t("ipc.baseNote", { period: d.current.period })
          : t("ipc.adoptedOn", { period: d.current.period, date: date(adopted.time) })),
        stat(t("ipc.base"), index(d.base.value), t("ipc.baseNote", { period: d.base.period })),
        stat(t("ipc.rate"), `${formatRate(d.current.value, d.base.value, lang)} €`, `${index(d.current.value)} ÷ ${index(d.base.value)}`)),
      h("section", { class: "panel" },
        h("div", { class: "panel-head" }, h("h2", {}, t("ipc.chart.title")), h("span", { class: "fine" }, t("ipc.chart.note"))),
        chart(app, hist, index),
        hist.length === 1 ? h("p", { class: "fine" }, t("ipc.chart.single")) : null),
      h("div", { class: "split even" }, gauge, oracle),
      h("section", { class: "panel" }, h("h2", {}, t("ipc.source")), h("p", { class: "addr" }, d.current.source_sha256), h("p", { class: "fine" }, t("ipc.sourceNote"))));
  };
  app.onBlock = load;
  load();
  return root;
}
