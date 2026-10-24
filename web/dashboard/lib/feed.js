// Historique d'une adresse (portefeuille, page d'une adresse dans l'explorateur) : transferts,
// mouvements d'échange et récompenses, du plus récent au plus ancien. Sur le site public, blocs,
// adresses et échanges ouvrent leur page dans l'explorateur.

import { h, clear, put } from "./dom.js";
import { formatETL } from "./amount.js";
import { icon } from "./icons.js";
import { short } from "./ui.js";
import { route, shortHex } from "./site.js";

// feedRows : lignes de l'historique hist vu depuis address, triées du bloc le plus récent au plus ancien.
export function feedRows(hist, address) {
  return [
    ...hist.transactions.map((tx) => {
      const out = tx.from === address;
      return { height: tx.height, out, kind: out ? "out" : "in", peer: out ? tx.to : tx.from, amount: tx.amount, fee: out ? tx.fee : 0n, tx: tx.id };
    }),
    ...hist.escrow.map((m) => ({ height: m.height, out: m.out, kind: "lock", escrowKind: m.kind, amount: m.amount, escrow: m.escrow })),
    ...(hist.rewards ?? []).map((r) => ({ height: r.height, out: false, kind: "reward", amount: r.amount })),
  ].sort((a, b) => b.height - a.height);
}

// weave insère node à la place de MARK dans un texte traduit : l'ordre des mots de chaque langue est
// respecté (« Reçu de X », « X から受け取り »).
export const MARK = "\u0001";
export function weave(text, node) {
  const i = text.indexOf(MARK);
  return i < 0 ? [text] : [text.slice(0, i), node, text.slice(i + MARK.length)];
}

// renderFeed affiche les lignes dans el (vide : « aucune transaction »). addContact : bouton « ajouter
// aux contacts » sous une adresse inconnue (portefeuille seulement).
export function renderFeed(app, el, rows, { addContact = null, empty = "history.empty" } = {}) {
  const { t, lang } = app;
  const links = app.status?.public === true;
  if (!rows.length) return put(clear(el), h("p", { class: "muted" }, t(empty)));
  return put(clear(el), h("ul", {}, rows.map((r) => {
    const name = r.peer ? app.book?.name(r.peer) : null;
    const peerText = name ?? short(r.peer ?? "");
    const peer = r.peer && links ? h("a", { href: route.address(r.peer) }, peerText) : peerText;
    let title;
    if (r.peer) title = weave(t(r.out ? "activity.to" : "activity.from", { who: MARK }), peer);
    else if (r.kind === "lock") title = [t(`history.escrow.${r.escrowKind}`), ...(links ? [" · ", h("a", { class: "mono", href: route.escrow(r.escrow) }, shortHex(r.escrow))] : [])];
    else title = [t("activity.reward")];
    const blockText = t("history.block", { n: r.height });
    const detail = [links ? h("a", { href: route.block(r.height) }, blockText) : blockText];
    if (r.peer && name) detail.push(" · ", short(r.peer));
    if (r.fee) detail.push(" · ", t("activity.fee", { fee: formatETL(r.fee, lang) }));
    if (r.tx && links) detail.push(" · ", h("a", { class: "mono", href: route.tx(r.tx) }, t("history.txLink")));
    const add = r.peer && !name && addContact ? h("button", { class: "link small", type: "button", onClick: () => addContact(r.peer) }, t("contacts.add")) : null;
    return h("li", {},
      h("span", { class: `ico ${r.kind}` }, icon(r.kind)),
      h("div", { class: "row-text" }, h("div", { class: "t" }, title), h("div", { class: "d" }, detail), add),
      h("div", { class: `a${r.out ? "" : " plus"}` }, `${r.out ? "−" : "+"}${formatETL(r.amount, lang)}`, h("small", {}, "ETL")));
  })));
}
