// Rejoindre : comprendre, essayer, devenir fondateur (les 3 étapes du README), puis les documents du
// dépôt dans les quatre langues (P2 : lus sur GitHub, jamais recopiés). Site public seulement.

import { h } from "../lib/dom.js";
import { icon } from "../lib/icons.js";
import { pageHead } from "../lib/ui.js";
import { modeOf, docURL, docFor, external, DOC_LANGS, REPO, CONTACT } from "../lib/site.js";
import { prelaunchBanner } from "./home.js";
import { LANG_NAMES } from "../lib/strings.js";

// Libellé « Lire » dans la langue du document (le lecteur choisit la langue qu'il lit).
const READ = { fr: "Lire", en: "Read", ja: "読む", zh: "阅读" };
const DOC_ROWS = ["founders", "short", "guide", "architecture"];

function copyButton(t, text) {
  const b = h("button", { class: "link", type: "button", onClick: async () => {
    try {
      await navigator.clipboard.writeText(text);
      b.textContent = t("join.copied");
      setTimeout(() => (b.textContent = t("join.copy")), 2000);
    } catch { /* presse-papiers refusé : l'adresse reste sélectionnable */ }
  } }, t("join.copy"));
  return b;
}

const ext = () => icon("external");

export function renderJoin(app) {
  const { t, lang } = app;
  const mode = modeOf(app.status);
  const step = (n, title, ...body) => h("li", { class: "panel join-step" }, h("span", { class: "n", "aria-hidden": "true" }, String(n)),
    h("div", {}, h("h2", {}, title), ...body));
  const docsTable = h("div", { class: "docs" },
    h("table", {},
      h("thead", {}, h("tr", {}, h("th", { scope: "col" }, t("join.docs.doc")), DOC_LANGS.map((l) => h("th", { scope: "col", lang: l }, LANG_NAMES[l])))),
      h("tbody", {}, DOC_ROWS.map((d) => h("tr", {}, h("th", { scope: "row" }, t(`join.doc.${d}`)),
        DOC_LANGS.map((l) => {
          const url = docURL(d, l);
          return url ? h("td", { lang: l }, external(url, `${READ[l]} `, ext())) : h("td", { class: "none" }, h("span", { "aria-label": t("join.docs.none") }, "—"));
        }))))));
  return h("div", { class: "page join" },
    mode === "prelaunch" ? prelaunchBanner(t) : null,
    pageHead(t("nav.join"), t("join.title")),
    h("ol", { class: "join-steps" },
      step(1, t("join.understand"), h("p", { class: "muted" }, t("join.understandText")),
        h("p", { class: "links" }, DOC_LANGS.map((l) => external(docURL("short", l), `${LANG_NAMES[l]} `, ext())))),
      step(2, t("join.try"), h("p", { class: "muted" }, t("join.tryText")),
        h("pre", { class: "code" }, h("code", {}, "go build -o etl ./cmd/etl")),
        h("p", { class: "links" }, external(docFor("guide", lang), `${t("join.guide")} `, ext()), external(REPO, `${t("join.source")} `, ext()))),
      step(3, t("join.found"), h("p", { class: "muted" }, t("join.foundText")),
        h("p", { class: "contact-line" }, h("span", { class: "mono" }, CONTACT), copyButton(t, CONTACT)))),
    mode === "public" ? h("p", { class: "callout info" }, icon("info"), h("span", {}, t("join.walletOnly"), " ", h("a", { href: "#/wallet" }, t("join.walletLink")))) : null,
    h("section", { class: "panel flush" },
      h("div", { class: "panel-head pad" }, h("h2", {}, t("join.docs.title")), h("span", { class: "fine" }, t("join.docs.note"))),
      docsTable));
}
