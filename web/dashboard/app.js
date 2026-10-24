// Point d'entrée du dashboard et du site public : navigation, langue, état du nœud, notifications
// (flux SSE sur le dashboard ; relecture toutes les 30 s sur le site public), verrouillage automatique
// du portefeuille. Le mode vient de /api/status : public (site etalon-etl.fr) et launched (réseau lancé).

import { h, clear } from "./lib/dom.js";
import { logo, icon } from "./lib/icons.js";
import { t as translate, LANGS, LANG_NAMES, HTML_LANG } from "./lib/strings.js";
import { makeApi } from "./lib/api.js";
import { Session, AUTO_LOCK_MS } from "./lib/session.js";
import { formatETL } from "./lib/amount.js";
import { detectLang } from "../i18n/i18n.js";
import { renderHome } from "./views/home.js";
import { renderWallet } from "./views/wallet.js";
import { renderEscrow } from "./views/escrow.js";
import { renderOperator } from "./views/operator.js";
import { renderIPC } from "./views/ipc.js";
import { renderExplorer } from "./views/explorer.js";
import { renderJoin } from "./views/join.js";
import { parseRoute, ago, NAVS, modeOf, POLL_MS } from "./lib/site.js";

const VIEWS = { "": renderHome, wallet: renderWallet, escrow: renderEscrow, ipc: renderIPC, operator: renderOperator, explorer: renderExplorer, join: renderJoin };


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
  tick: null, // minuterie d'une vue (compte à rebours), arrêtée à chaque changement de page
  t: (key, vars) => translate(app.lang, key, vars),
  render,
  refresh: async () => (await refreshStatus(), render()),
};

const main = h("main", { id: "main", tabindex: "-1" });
const toasts = h("div", { class: "toasts", "aria-live": "polite" });
// État du nœud : affiché dans la barre latérale (ordinateur) et dans la barre du haut (téléphone).
const nodeStates = [0, 1].map(() => h("span", { class: "node-state" }, h("span", { class: "dot", "aria-hidden": "true" }), h("span", { class: "state-text" })));

const routes = () => NAVS[modeOf(app.status)];

function langSelect() {
  return h("select", { "aria-label": app.t("lang.label"), onChange: (e) => {
    app.lang = e.target.value;
    try { localStorage.setItem(LANG_KEY, app.lang); } catch { /* préférence non retenue */ }
    document.documentElement.lang = HTML_LANG[app.lang];
    build();
  } }, LANGS.map((l) => h("option", { value: l, selected: l === app.lang }, LANG_NAMES[l])));
}

function brand() {
  return h("a", { class: "brand", href: "#/" }, logo(), h("span", { class: "name" }, app.t("app.title")));
}

function sidebar() {
  const link = ([route, ico, key]) => h("a", { href: `#/${route}`, "aria-current": currentRoute() === route ? "page" : null }, icon(ico), h("span", {}, app.t(key)));
  const items = routes();
  return h("aside", { class: "side" },
    brand(),
    h("nav", { class: `nav n${items.length}`, "aria-label": app.t("nav.label") }, items.map(link)),
    h("div", { class: "side-foot" }, nodeStates[0], h("span", { class: "fine" }, subtitle()), langSelect()));
}

// subtitle : « Tableau de bord du nœud », ou réseau et nom du site public.
function subtitle() {
  return modeOf(app.status) === "dashboard" ? app.t("app.subtitle") : `${app.status.chain_id} · ${location.hostname}`;
}

function topbar() {
  return h("header", { class: "topbar" }, brand(), h("div", { class: "tools" }, nodeStates[1], langSelect()));
}

// currentRoute : la page affichée (premier segment de l'adresse) ; une page absente de la navigation
// du mode (opérateur sur le site public, explorateur avant le lancement) ramène à l'accueil.
function currentRoute() {
  const { page } = parseRoute(location.hash);
  return routes().some(([r]) => r === page) ? page : "";
}

function updateNodeState() {
  const st = app.status;
  for (const el of nodeStates) {
    const [dot, label] = el.children;
    if (st && modeOf(st) === "prelaunch") {
      dot.className = "dot pending";
      label.textContent = app.t("site.notLaunched");
    } else if (st && modeOf(st) === "public") {
      dot.className = "dot on";
      label.textContent = app.t("site.lastBlock", { n: st.height, ago: ago(app.t, st.tip_time) });
    } else if (!st) {
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
}

function render() {
  app.onBlock = null;
  clearInterval(app.tick);
  app.tick = null;
  app.route = parseRoute(location.hash);
  clear(main).append((VIEWS[currentRoute()] ?? renderHome)(app));
  updateNodeState();
}

function build() {
  document.body.replaceChildren(h("div", { class: "shell" }, sidebar(), h("div", { class: "content" }, topbar(), main)), toasts);
  document.title = modeOf(app.status) === "dashboard" ? `${app.t("app.title")} — ${app.t("app.subtitle")}` : `${app.t("app.title")} — ${app.t("site.tagline")}`;
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

// onNewBlock : état relu, notifications des transactions du portefeuille ouvert, page mise à jour.
function onNewBlock(block) {
  const me = app.session?.isUnlocked() ? app.session.address() : null;
  if (me) {
    for (const tx of block.txs) {
      if (tx.from === me) toast(app.t("toast.confirmed", { n: block.height }));
      else if (tx.to === me) toast(app.t("toast.received", { amount: formatETL(BigInt(tx.amount), app.lang), n: block.height }));
    }
  }
  if (app.onBlock) app.onBlock();
  else if (currentRoute() === "") render();
}

// poll (site public) : relit l'état ; à chaque nouveau bloc (5 au plus d'un coup), lit son contenu.
async function poll() {
  const before = app.status?.height;
  await refreshStatus();
  const now = app.status?.height;
  if (before === undefined || now === undefined || now <= before) return;
  for (let height = Math.max(before + 1, now - 4); height <= now; height++) {
    try {
      const b = await app.api.block(height);
      onNewBlock({ height, txs: b.txs.map((tx) => ({ ...tx, amount: tx.amount.toString() })) });
    } catch { /* bloc illisible : la prochaine relecture suffit */ }
  }
}

function listen() {
  const es = new EventSource("/api/events");
  es.addEventListener("block", async (ev) => {
    const block = JSON.parse(ev.data);
    await refreshStatus();
    onNewBlock(block);
  });
  es.onerror = () => refreshStatus();
}

async function start() {
  document.documentElement.lang = HTML_LANG[app.lang];
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
  if (modeOf(app.status) === "dashboard") {
    listen();
    setInterval(refreshStatus, 30_000);
  } else if (modeOf(app.status) === "public") {
    setInterval(poll, POLL_MS);
  }
}

start();
