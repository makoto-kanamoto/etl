// Page IPC : valeur en vigueur et taux de 1 ETL, triplets en attente face au quorum, dernière lecture
// de ce nœud, et « relire la source maintenant » (session opérateur). Aucune saisie de valeur (D7).

import { h, clear, put } from "../lib/dom.js";
import { formatRate } from "../lib/amount.js";
import { TOKEN_KEY } from "./operator.js";

function msg(kind, text) {
  return h("p", { class: `msg ${kind}`, role: kind === "error" ? "alert" : "status" }, text);
}

function token() {
  try {
    return sessionStorage.getItem(TOKEN_KEY);
  } catch {
    return null;
  }
}

export function renderIPC(app) {
  const { t, lang } = app;
  let notice = null; // message conservé à travers le rechargement de la page
  const root = h("div", { class: "grid" }, h("p", { class: "muted" }, "…"));
  const locale = lang === "fr" ? "fr-FR" : "en-GB";
  const index = (v) => (v / 1000).toLocaleString(locale, { minimumFractionDigits: 2, maximumFractionDigits: 3 });
  const when = (s) => new Date(s * 1000).toLocaleString(locale, { dateStyle: "medium", timeStyle: "short" });

  const load = async () => {
    let d;
    try {
      d = await app.api.ipc();
    } catch {
      return put(clear(root), msg("error", t("error.network")));
    }
        const out = h("div");
    if (notice) put(out, notice);
    notice = null;
    const pending = d.pending.length
      ? h("ul", { class: "tallies" }, d.pending.map((p) => {
          const pct = Math.min(100, Math.round((100 * p.votes) / d.quorum));
          return h("li", {},
            h("span", { class: "strong" }, `${p.record.period} = ${index(p.record.value)}`),
            h("span", { class: "bar", role: "img", "aria-label": t("ipc.votes", { votes: p.votes, quorum: d.quorum }) }, h("span", { class: "fill", "data-pct": pct })),
            h("span", { class: "small muted" }, t("ipc.votes", { votes: p.votes, quorum: d.quorum })),
            h("span", { class: "mono small muted" }, p.record.source_sha256.slice(0, 16) + "…"),
          );
        }))
      : h("p", { class: "muted" }, t("ipc.pendingEmpty"));
    const o = d.oracle;
    const oracle = o
      ? [
          h("p", {}, o.last_attempt ? t("ipc.lastRead", { time: when(o.last_attempt) }) : t("ipc.never")),
          o.read ? h("p", {}, `${t("ipc.read", { period: o.read.period, value: index(o.read.value) })} — ${o.voted ? t("ipc.voted") : t("ipc.notVoted")}`) : null,
          o.error ? msg("error", t("ipc.error", { error: o.error })) : null,
          o.next_read ? h("p", { class: "note" }, t("ipc.next", { time: when(o.next_read) })) : null,
        ]
      : null;
    const readNow = token()
      ? h("button", { onClick: async () => {
          try {
                        await app.api.operator(token()).ipcRead();
            notice = msg("ok", t("ipc.done"));
            load();
          } catch (e) {
            put(clear(out), msg("error", e.message === "network" ? t("error.network") : e.message));
          }
        } }, t("ipc.readNow"))
      : h("p", { class: "note" }, h("a", { href: "#/operator" }, t("ipc.needToken")));
    put(clear(root), 
      h("section", { class: "card stat" }, h("h3", {}, t("ipc.current")), h("p", { class: "value" }, index(d.current.value)), h("p", { class: "note" }, d.current.period)),
      h("section", { class: "card stat" }, h("h3", {}, t("ipc.base")), h("p", { class: "value" }, index(d.base.value)), h("p", { class: "note" }, d.base.period)),
      h("section", { class: "card stat" }, h("h3", {}, t("ipc.rate")), h("p", { class: "value" }, `${formatRate(d.current.value, d.base.value, lang)} €`), h("p", { class: "note" }, t("home.rateNote"))),
      h("section", { class: "card wide" }, h("h3", {}, t("ipc.source")), h("p", { class: "mono small" }, d.current.source_sha256)),
      h("section", { class: "card wide" }, h("h3", {}, t("ipc.pending")), pending),
      h("section", { class: "card wide" }, h("h3", {}, t("ipc.oracle")), h("p", { class: "note" }, t("ipc.auto")), oracle, h("div", { class: "actions" }, readNow), out),
    );
    for (const f of root.querySelectorAll(".fill")) f.style.width = `${f.dataset.pct}%`;
  };
  app.onBlock = load;
  load();
  return root;
}
