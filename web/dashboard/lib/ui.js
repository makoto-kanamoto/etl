// Éléments d'affichage communs aux pages (portefeuille, échanges, opérateur).

import { h } from "./dom.js";
import { identiconSvg } from "./identicon.js";

// short abrège une adresse ; group la découpe en groupes de 4 pour la relire.
export const short = (a) => `${a.slice(0, 8)}…${a.slice(-4)}`;
export const group = (a) => a.match(/.{1,4}/g).join(" ");

// capitalize : majuscule initiale (« l'arbitre demande » en tête de phrase).
export const capitalize = (s) => s.charAt(0).toLocaleUpperCase() + s.slice(1);

export function message(kind, text) {
  return h("p", { class: `msg ${kind}`, role: kind === "error" ? "alert" : "status" }, text);
}

export function actionRow(...buttons) {
  return h("div", { class: "actions" }, ...buttons);
}

export function pageHead(kicker, title, ...extra) {
  return h("div", { class: "page-head" }, h("div", {}, h("span", { class: "label" }, kicker), h("h1", {}, title)), ...extra);
}

// steps : étapes numérotées, l'étape courante en évidence, les précédentes cochées.
export function steps(labels, current) {
  return h("ol", { class: "steps" }, labels.map((l, i) => h("li", { class: i < current ? "done" : i === current ? "on" : null, "aria-current": i === current ? "step" : null }, l)));
}

// who : motif de l'adresse, nom du contact (ou « Mon portefeuille »), sinon l'adresse abrégée.
// sub : ligne secondaire (false : aucune) ; bare : toujours l'adresse, jamais le nom.
export function who(app, address, sub, big = false, bare = false) {
  const mine = app.session?.hasVault() && address === app.session.address();
  const name = bare ? null : mine ? app.t("who.mine") : app.book?.name(address) ?? null;
  const line = sub === false ? null : sub !== undefined ? h("div", { class: "who-sub" }, sub) : h("div", { class: name ? "who-sub mono" : "who-sub" }, name ? short(address) : app.t("who.unnamed"));
  return h("div", { class: "who-line" }, identiconSvg(address, big ? "idc lg" : "idc"),
    h("div", { class: "who-text" }, h("div", { class: name ? "who-name" : "who-name mono" }, name ?? short(address)), line));
}
