// Point d'entrée du dashboard : navigation, langue, état du nœud, notifications (SSE), verrouillage
// automatique du portefeuille.

import { h, clear } from "./lib/dom.js";
import { t as translate, LANGS } from "./lib/strings.js";
import { makeApi } from "./lib/api.js";
import { Session, AUTO_LOCK_MS } from "./lib/session.js";
import { formatETL } from "./lib/amount.js";
import { detectLang } from "../i18n/i18n.js";
import { renderHome } from "./views/home.js";
import { renderWallet } from "./views/wallet.js";

const LANG_KEY = "etl.lang";

function storedLang() {
  try {
    const l = localStorage.getItem(LANG_KEY);
    return LANGS.includes(l) ? l : null;
  } catch {
    return null;
  }
}

const app = {
  lang: storedLang() ?? (LANGS.includes(detectLang()) ? detectLang() : "en"),
  api: makeApi(),
  status: null,
  session: null,
  autoLocked: false,
  onBlock: null,
  t: (key, vars) => translate(app.lang, key, vars),
  render,
};

const main = h("main", { id: "main", tabindex: "-1" });
const dot = h("span", { class: "dot", "aria-hidden": "true" });
const nodeState = h("span", { class: "node-state" }, dot, h("span", { class: "label" }));
const toasts = h("div", { class: "toasts", "aria-live": "polite" });

function header() {
  const select = h("select", { "aria-label": app.t("lang.label"), onChange: (e) => {
    app.lang = e.target.value;
    try { localStorage.setItem(LANG_KEY, app.lang); } catch { /* préférence non retenue */ }
    document.documentElement.lang = app.lang;
    build();
  } }, LANGS.map((l) => h("option", { value: l, selected: l === app.lang }, l.toUpperCase())));
  const link = (route, key) => h("a", { href: `#/${route}`, "aria-current": currentRoute() === route ? "page" : null }, app.t(key));
  return h("header", {},
    h("div", { class: "brand" }, h("strong", {}, app.t("app.title")), h("span", { class: "muted" }, app.t("app.subtitle"))),
    h("nav", {}, link("", "nav.home"), link("wallet", "nav.wallet")),
    h("div", { class: "tools" }, nodeState, select),
  );
}

function currentRoute() {
  return location.hash.replace(/^#\/?/, "");
}

function updateNodeState() {
  const st = app.status;
  const label = nodeState.querySelector(".label");
  if (!st) {
    dot.className = "dot off";
    label.textContent = app.t("node.offline");
  } else if (st.halted) {
    dot.className = "dot halted";
    label.textContent = app.t("node.halted");
  } else {
    dot.className = "dot on";
    label.textContent = `${app.t("node.online")} · #${st.height}`;
  }
}

function render() {
  app.onBlock = null;
  clear(main).append(currentRoute() === "wallet" ? renderWallet(app) : renderHome(app));
  updateNodeState();
}

function build() {
  document.body.replaceChildren(header(), main, toasts);
  document.title = `${app.t("app.title")} — ${app.t("app.subtitle")}`;
  render();
}

function toast(text) {
  const el = h("div", { class: "toast" }, text);
  toasts.append(el);
  setTimeout(() => el.remove(), 8000);
}

async function refreshStatus() {
  try {
    app.status = await app.api.status();
  } catch {
    app.status = null;
  }
  updateNodeState();
}

function listen() {
  const es = new EventSource("/api/events");
  es.addEventListener("block", async (ev) => {
    const block = JSON.parse(ev.data);
    await refreshStatus();
    const me = app.session?.isUnlocked() ? app.session.address() : null;
    if (me) {
      for (const tx of block.txs) {
        if (tx.from === me) toast(app.t("toast.confirmed", { n: block.height }));
        else if (tx.to === me) toast(app.t("toast.received", { amount: formatETL(BigInt(tx.amount), app.lang), n: block.height }));
      }
    }
    if (app.onBlock) app.onBlock();
    else if (currentRoute() === "") render();
  });
  es.onerror = () => refreshStatus();
}

async function start() {
  document.documentElement.lang = app.lang;
  await refreshStatus();
  app.session = new Session(localStorage, app.status?.hrp ?? "etl");
  for (const ev of ["pointerdown", "keydown"]) addEventListener(ev, () => app.session.isUnlocked() && app.session.touch(), { passive: true });
  setInterval(() => {
    if (app.session.check()) {
      app.autoLocked = true;
      if (currentRoute() === "wallet") render();
    }
  }, Math.min(30_000, AUTO_LOCK_MS));
  addEventListener("hashchange", () => (build(), main.focus()));
  build();
  listen();
  setInterval(refreshStatus, 30_000);
}

start();
