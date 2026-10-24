// Site public (etalon-etl.fr) : routes de l'explorateur, liens vers la documentation du dépôt,
// formulations « il y a … », liens vers les pages de l'explorateur.

import { h } from "./dom.js";
import { identiconSvg } from "./identicon.js";
import { short } from "./ui.js";

// Navigation de chaque mode : dashboard de l'opérateur, site public, site public avant le lancement.
export const NAVS = {
  dashboard: [["", "home", "nav.home"], ["wallet", "wallet", "nav.wallet"], ["escrow", "escrow", "nav.escrow"], ["ipc", "ipc", "nav.ipc"], ["operator", "operator", "nav.operator"]],
  public: [["", "home", "nav.home"], ["wallet", "wallet", "nav.wallet"], ["escrow", "escrow", "nav.escrow"], ["explorer", "explorer", "nav.explorer"], ["ipc", "ipc", "nav.ipc"], ["join", "join", "nav.join"]],
  prelaunch: [["", "home", "nav.home"], ["join", "join", "nav.join"]],
};
export function modeOf(status) {
  if (status?.public !== true) return "dashboard";
  return status.launched === false ? "prelaunch" : "public";
}

// Site public : relecture de l'état toutes les 30 secondes (D4), pas de flux SSE.
export const POLL_MS = 30_000;

// Dépôt public : la documentation est lue sur GitHub (décision P2), jamais recopiée par le site.
export const REPO = "https://github.com/makoto-kanamoto/etl";
const DOCS = {
  short: { fr: "docs/fr/MANIFESTE_COURT.md", en: "docs/en/SHORT_MANIFESTO.md", ja: "docs/ja/SHORT_MANIFESTO.md", zh: "docs/zh/SHORT_MANIFESTO.md" },
  founders: { fr: "docs/fr/MANIFESTE_FONDATEUR.md", en: "docs/en/FOUNDERS_MANIFESTO.md", ja: "docs/ja/FOUNDERS_MANIFESTO.md", zh: "docs/zh/FOUNDERS_MANIFESTO.md" },
  guide: { fr: "docs/fr/GUIDE_OPERATEUR.md", en: "docs/en/OPERATOR_GUIDE.md" },
  architecture: { fr: "docs/ARCHITECTURE.md" },
};
export const DOC_LANGS = ["fr", "en", "ja", "zh"];
export const CONTACT = "makoto.kanamoto@proton.me";

// docURL : adresse du document doc dans la langue lang, null s'il n'existe pas dans cette langue.
export function docURL(doc, lang) {
  const path = DOCS[doc]?.[lang];
  return path ? `${REPO}/blob/main/${path}` : null;
}
// docFor : le document dans la langue de l'interface, à défaut en anglais, puis en français.
export function docFor(doc, lang) {
  return docURL(doc, lang) ?? docURL(doc, "en") ?? docURL(doc, "fr");
}
export const DOC_KEYS = Object.keys(DOCS);

// parseRoute découpe « #/explorer/block/42 » en { page: "explorer", params: ["block", "42"] }.
export function parseRoute(hash) {
  const parts = hash.replace(/^#\/?/, "").split("/").map((p) => {
    try {
      return decodeURIComponent(p);
    } catch {
      return p;
    }
  });
  return { page: parts[0] ?? "", params: parts.slice(1).filter((p) => p !== "") };
}

// Adresses des pages de l'explorateur.
export const route = {
  explorer: () => "#/explorer",
  block: (height) => `#/explorer/block/${height}`,
  address: (a) => `#/explorer/address/${encodeURIComponent(a)}`,
  tx: (id) => `#/explorer/tx/${encodeURIComponent(id)}`,
  escrow: (id) => `#/explorer/escrow/${encodeURIComponent(id)}`,
  search: (q) => `#/explorer/search/${encodeURIComponent(q)}`,
};

// searchTarget : page ouverte par une recherche réussie (réponse de /api/search).
export function searchTarget(r) {
  switch (r?.type) {
    case "block": return route.block(r.height);
    case "address": return route.address(r.address);
    case "tx": return route.tx(r.id);
    case "escrow": return route.escrow(r.id);
    default: return null;
  }
}

// ago : durée écoulée depuis une heure Unix, en minutes, heures ou jours (pour « il y a … ») ; une heure
// dans le futur (horloge du visiteur en retard) donne « il y a 1 min ».
export function ago(t, then, now = Date.now() / 1000) {
  const s = Math.floor(now - then);
  if (s < 3600) return t("ago.min", { n: Math.max(1, Math.floor(s / 60)) });
  if (s < 2 * 86400) return t("ago.h", { n: Math.floor(s / 3600) });
  return t("ago.d", { n: Math.floor(s / 86400) });
}

// shortHex abrège une empreinte de 64 caractères.
export const shortHex = (x) => `${x.slice(0, 10)}…${x.slice(-6)}`;

// addrLink : motif + adresse abrégée, lien vers la page de l'adresse.
export function addrLink(address, label) {
  return h("a", { class: "alink", href: route.address(address) }, identiconSvg(address, "idc"), h("span", {}, label ?? short(address)));
}

// external : lien vers un autre site (documentation), ouvert à part, sans référent.
export function external(href, text, icon) {
  return h("a", { class: "extlink", href, target: "_blank", rel: "noopener noreferrer" }, text, icon ?? null);
}
