// Accueil : état du nœud et de la chaîne, IPC et valeur de 1 ETL.

import { h } from "../lib/dom.js";
import { formatRate } from "../lib/amount.js";

function card(title, value, note) {
  return h("section", { class: "card stat" }, h("h3", {}, title), h("p", { class: "value" }, value), note ? h("p", { class: "note" }, note) : null);
}

export function duration(seconds, lang) {
  const s = Math.max(0, Math.round(seconds));
  const hrs = Math.floor(s / 3600);
  const min = Math.floor((s % 3600) / 60);
  return lang === "fr" ? `${hrs} h ${String(min).padStart(2, "0")}` : `${hrs}h ${String(min).padStart(2, "0")}m`;
}

export function renderHome(app) {
  const { status: st, lang, t } = app;
  if (!st) return h("p", { class: "muted" }, t("error.network"));
  const next = new Date(st.next_block * 1000);
  const nextLocal = next.toLocaleString(lang === "fr" ? "fr-FR" : "en-GB", { weekday: "short", hour: "2-digit", minute: "2-digit" });
  const ipc = st.ipc.value / 1000;
  const node = st.node.address ? `${st.node.address} — ${t(`status.${st.node.status}`)}` : t("home.passive");
  return h(
    "div",
    { class: "grid" },
    card(t("home.height"), String(st.height)),
    card(t("home.nextBlock"), nextLocal, t("home.in", { time: duration(st.next_block - Date.now() / 1000, lang) })),
    card(t("home.active"), String(st.active_nodes)),
    st.peers ? card(t("home.peers"), String(st.peers.in + st.peers.out), t("home.peersValue", st.peers)) : null,
    card(t("home.ipc"), ipc.toLocaleString(lang === "fr" ? "fr-FR" : "en-GB", { minimumFractionDigits: 2 }), t("home.ipcPeriod", { period: st.ipc.period })),
    card(t("home.rate"), `${formatRate(st.ipc.value, st.base_ipc.value, lang)} €`, t("home.rateNote")),
    h("section", { class: "card wide" }, h("h3", {}, t("home.node")), h("p", { class: "mono" }, node), h("p", { class: "note" }, t("home.chain", { chain: st.chain_id, version: st.version }))),
  );
}
