// Explorateur (site public) : derniers blocs, bloc, adresse, transaction, échange, recherche.
// « Ne faites pas confiance. Vérifiez. » : tout identifiant mène à sa page, toute empreinte s'affiche
// en entier sur sa propre page.

import { h, clear, put } from "../lib/dom.js";
import { formatETL, euroCents, formatEuro } from "../lib/amount.js";
import { localeOf } from "../lib/strings.js";
import { icon } from "../lib/icons.js";
import { identiconSvg } from "../lib/identicon.js";
import { short, message, pageHead } from "../lib/ui.js";
import { feedRows, renderFeed } from "../lib/feed.js";
import { route, searchTarget, shortHex, addrLink } from "../lib/site.js";
import { reputation } from "../lib/escrowui.js";
import { party } from "./escrow.js";

export const PAGE = 20; // blocs par page de la liste
const KINDS = ["", "open", "paid", "dispute", "release", "refund"]; // blockchain.EscrowKind

// --- éléments communs ---

const date = (app, unix) => new Date(unix * 1000).toLocaleDateString(localeOf(app.lang), { day: "numeric", month: "short", year: "numeric" });
const longDate = (app, unix) => new Date(unix * 1000).toLocaleDateString(localeOf(app.lang), { day: "numeric", month: "long", year: "numeric" });
const utcTime = (unix) => `${new Date(unix * 1000).toISOString().slice(11, 16)} UTC`;
const localTime = (app, unix) => new Date(unix * 1000).toLocaleString(localeOf(app.lang), { day: "numeric", month: "long", year: "numeric", hour: "2-digit", minute: "2-digit" });
const euro = (app, units) => (app.status ? formatEuro(euroCents(units, app.status.ipc.value, app.status.base_ipc.value), app.lang) : "");

function crumbs(app, here) {
  return h("nav", { class: "crumbs", "aria-label": app.t("explorer.crumbs") }, h("a", { href: route.explorer() }, app.t("nav.explorer")), icon("next"), h("span", {}, here));
}

// searchForm : une seule recherche pour tout ; le nœud dit ce que c'est (page « search »).
export function searchForm(app, value = "") {
  const input = h("input", { id: "explorer-q", type: "text", class: "q", value, placeholder: app.t("explorer.search.placeholder"), autocomplete: "off", spellcheck: "false", autocapitalize: "off" });
  return h("form", { class: "search", role: "search", onSubmit: (e) => {
    e.preventDefault();
    const q = input.value.trim();
    if (q) location.hash = route.search(q);
  } },
    h("label", { class: "sr", for: "explorer-q" }, app.t("explorer.search.label")),
    h("span", { class: "qwrap" }, icon("search"), input),
    h("button", { class: "primary", type: "submit" }, app.t("explorer.search.go")));
}

const kv = (rows) => h("dl", { class: "kv" }, rows.filter(Boolean).flatMap(([k, v]) => [h("dt", {}, k), h("dd", {}, v)]));
const hashText = (x) => h("span", { class: "hash" }, x);

// loading : page affichée tout de suite, remplie quand le nœud répond.
function loading(root, fill) {
  put(root, h("p", { class: "muted" }, "…"));
  fill().catch((e) => put(clear(root), message("error", e?.message === "network" ? "" : e?.message ?? "")));
  return root;
}

// --- derniers blocs ---

export function counts(t, c) {
  if (!c) return [t("explorer.pruned")];
  return [t("explorer.count.txs", { n: c.txs }), t("explorer.count.ops", { n: c.escrow_ops }), t("explorer.count.parts", { n: c.participants })];
}

function blockRow(app, b) {
  const { t } = app;
  return h("li", {}, h("a", { class: "brow", href: route.block(b.height) },
    h("span", { class: "bh" }, b.height.toLocaleString(localeOf(app.lang))),
    h("span", { class: "bt" }, h("span", {}, date(app, b.time)), h("small", {}, utcTime(b.time))),
    h("span", { class: "bp" }, b.proposer ? [identiconSvg(b.proposer, "idc"), h("span", { class: "mono" }, short(b.proposer))] : h("span", { class: "muted" }, t("explorer.genesis"))),
    h("span", { class: "bc" }, counts(t, b.counts).map((c) => h("span", {}, c))),
    h("span", { class: "bi" }, b.ipc ? h("span", { class: "chip info" }, t("explorer.ipcChip", { value: ipcValue(app, b.ipc.value) })) : null),
    h("span", { class: "go" }, icon("next"))));
}

const ipcValue = (app, v) => (v / 1000).toLocaleString(localeOf(app.lang), { minimumFractionDigits: 2, maximumFractionDigits: 3 });

function renderList(app, root) {
  const { t, api } = app;
  const list = h("ul", { class: "blocks" });
  let next = null;
  const older = h("button", { type: "button", onClick: () => load(next) }, t("explorer.older"));
  const olderRow = h("div", { class: "pad center", hidden: true }, older);
  async function load(before) {
    if (before === null) return; // fin de la chaîne
    older.disabled = true;
    try {
      const page = await api.blocks(PAGE, before ?? undefined);
      put(list, page.map((b) => blockRow(app, b)));
      const last = page[page.length - 1];
      next = last && last.height > 0 ? last.height : null;
      olderRow.hidden = next === null;
    } catch (e) {
      put(list, h("li", {}, message("error", e.message)));
    } finally {
      older.disabled = false;
    }
  }
  put(root,
    pageHead(t("nav.explorer"), t("explorer.title")),
    searchForm(app),
    h("section", { class: "panel flush" },
      h("div", { class: "panel-head pad" }, h("h2", {}, t("explorer.latest")), h("span", { class: "fine" }, t("explorer.daily", { time: utcTime(app.status.block_time) }))),
      h("div", { class: "bhead", "aria-hidden": "true" }, ["explorer.col.height", "explorer.col.date", "explorer.col.proposer", "explorer.col.content"].map((k) => h("span", {}, t(k))), h("span"), h("span")),
      list,
      olderRow));
  app.onBlock = () => (list.replaceChildren(), load());
  load();
  return root;
}

// --- un bloc ---

function renderBlock(app, root, raw) {
  const { t, lang, api } = app;
  const height = Number(raw);
  if (!/^\d+$/.test(raw ?? "")) return put(root, crumbs(app, t("explorer.block")), message("error", t("explorer.badHeight")));
  return loading(root, async () => {
    const b = await api.block(height);
    const hd = b.header;
    const tip = app.status.height;
    const nav = h("div", { class: "cta" },
      height > 0 ? h("a", { class: "button", href: route.block(height - 1), "aria-label": t("explorer.prevBlock") }, icon("prev"), String(height - 1)) : null,
      height < tip ? h("a", { class: "button", href: route.block(height + 1), "aria-label": t("explorer.nextBlock") }, String(height + 1), icon("next")) : null);
    const n = b.participants ?? 0;
    const reward = b.rewardShare === null ? null : b.rewardShare > 0n
      ? t("explorer.reward", { total: formatETL(b.rewardShare * BigInt(n), lang), n, share: formatETL(b.rewardShare, lang) })
      : t("explorer.noReward");
    const txs = h("ul", { class: "txs" }, (b.txs ?? []).map((tx) => h("li", {},
      h("a", { class: "mono txid", href: route.tx(tx.id) }, shortHex(tx.id)),
      h("span", { class: "flow" }, addrLink(tx.from), h("span", { class: "arrow", "aria-label": t("explorer.to") }, "→"), addrLink(tx.to)),
      h("span", { class: "amt" }, `${formatETL(tx.amount, lang)} ETL`, h("small", {}, t("explorer.feeShort", { fee: formatETL(tx.fee, lang) }))))));
    const ops = h("ul", { class: "txs" }, (b.escrow_ops ?? []).map((op) => h("li", {},
      h("a", { class: "mono txid", href: route.escrow(op.escrow) }, shortHex(op.escrow)),
      h("span", {}, h("span", { class: KINDS[op.kind] === "open" ? "chip warn" : "chip neutral" }, t(`explorer.op.${KINDS[op.kind] ?? "open"}`))),
      h("span"))));
    const miners = b.participant_addresses ?? [];
    put(clear(root),
      crumbs(app, t("explorer.blockN", { n: height })),
      h("div", { class: "page-head" }, h("div", {}, h("span", { class: "label" }, t("explorer.block")), h("h1", {}, height.toLocaleString(localeOf(lang))),
        h("p", {}, `${longDate(app, hd.time)} · ${utcTime(hd.time)} · ${t("explorer.final")}`)), nav),
      h("section", { class: "panel" }, kv([
        [t("explorer.hash"), hashText(hd.hash)],
        b.prev ? [t("explorer.prev"), h("a", { class: "hash", href: route.block(height - 1) }, b.prev)] : null,
        hd.proposer ? [t("explorer.proposer"), addrLink(hd.proposer)] : [t("explorer.proposer"), t("explorer.genesis")],
        reward ? [t("explorer.rewardLabel"), reward] : null,
        [t("explorer.ipc"), hd.ipc ? t("explorer.ipcAdopted", { value: ipcValue(app, hd.ipc.value), period: hd.ipc.period }) : t("explorer.ipcSame")],
        [t("explorer.round"), hd.round ? t("explorer.roundN", { n: hd.round + 1 }) : t("explorer.round1")],
      ])),
      b.pruned ? message("info", t("explorer.prunedBlock")) : [
        h("section", { class: "panel" }, h("h2", {}, t("explorer.txs"), h("span", { class: "muted" }, ` · ${b.txs.length}`)),
          b.txs.length ? txs : h("p", { class: "muted" }, t("explorer.noTx"))),
        h("div", { class: "grid-2" },
          h("section", { class: "panel" }, h("h2", {}, t("explorer.ops"), h("span", { class: "muted" }, ` · ${(b.escrow_ops ?? []).length}`)),
            (b.escrow_ops ?? []).length ? ops : h("p", { class: "muted" }, t("explorer.noOp"))),
          h("section", { class: "panel" }, h("h2", {}, t("explorer.parts"), h("span", { class: "muted" }, ` · ${n}`)),
            h("div", { class: "mosaic" }, miners.map((a) => h("a", { href: route.address(a), title: a, "aria-label": a }, identiconSvg(a, "idc")))),
            miners.length ? h("details", {}, h("summary", {}, t("explorer.showAll", { n: miners.length })),
              h("ul", { class: "plain" }, miners.map((a) => h("li", {}, h("a", { class: "hash", href: route.address(a) }, a))))) : null)),
      ]);
  });
}

// --- une adresse ---

function renderAddress(app, root, address) {
  const { t, lang, api } = app;
  return loading(root, async () => {
    let acc;
    try {
      acc = await api.account(address);
    } catch (e) {
      if (e.status !== 400) throw e;
      // Adresse invalide ou d'un autre réseau : le motif vient de la recherche.
      const why = await api.search(address).then(() => null, (err) => err.data);
      return put(clear(root), crumbs(app, t("explorer.address")), notFound(app, address, why));
    }
    const rep = reputation(acc);
    const feed = h("div", { class: "feed" });
    const older = h("button", { type: "button", hidden: true, onClick: () => more() }, t("history.older"));
    const hist = await api.history(address);
    const show = () => {
      renderFeed(app, feed, feedRows(hist, address), { empty: "explorer.noHistory" });
      older.hidden = hist.nextBefore === null;
    };
    async function more() {
      if (hist.nextBefore === null) return; // aucune page suivante
      older.disabled = true;
      try {
        const p = await api.history(address, hist.nextBefore);
        for (const k of ["transactions", "escrow", "rewards"]) hist[k].push(...p[k]);
        hist.nextBefore = p.nextBefore;
        show();
      } finally {
        older.disabled = false;
      }
    }
    const node = acc.node && acc.node !== "unknown";
    const copy = h("button", { type: "button", onClick: async () => {
      try {
        await navigator.clipboard.writeText(address);
        copy.textContent = t("home.copied");
      } catch { /* l'adresse reste sélectionnable */ }
    } }, t("home.copy"));
    const stat = (label, value, note) => h("div", { class: "stat" }, h("span", { class: "label" }, label), h("span", { class: "v" }, value), note ? h("span", { class: "s" }, note) : null);
    show();
    put(clear(root),
      crumbs(app, t("explorer.address")),
      h("div", { class: "page-head" }, h("div", { class: "who-line" }, identiconSvg(address, "idc xl"),
        h("div", { class: "who-text" }, h("span", { class: "label" }, t("explorer.address")), h("h1", { class: "addr-title" }, address))), copy),
      h("section", { class: "stats" },
        stat(t("explorer.balance"), formatETL(acc.balance, lang), `ETL · ≈ ${euro(app, acc.balance)}`),
        stat(t("explorer.sent"), acc.nonce.toString(), t("explorer.sentNote")),
        stat(t("explorer.node"), h("span", { class: node && acc.node === "admitted" ? "chip good" : "chip neutral" }, node ? t(`status.${acc.node}`) : t("explorer.notNode")),
          acc.founder ? t("explorer.founder") : acc.sponsor ? t("explorer.sponsoredBy", { who: short(acc.sponsor) }) : null),
        stat(t("explorer.reputation"), String(rep.completed), t("escrow.repCounts", rep))),
      h("section", { class: "panel" }, h("div", { class: "panel-head" }, h("h2", {}, t("activity.title")), h("span", { class: "fine" }, t("explorer.fullHistory"))), feed, older));
  });
}

// --- une transaction ---

function renderTx(app, root, id) {
  const { t, lang, api } = app;
  return loading(root, async () => {
    let tx;
    try {
      tx = await api.tx(id);
    } catch (e) {
      if (e.status === 404 || e.status === 400) return put(clear(root), crumbs(app, t("explorer.tx")), notFound(app, id, e.status === 400 ? { reason: "short_hash", length: id.length } : { reason: "unknown_hash" }));
      throw e;
    }
    const side = (label, a) => h("a", { class: "party", href: route.address(a) }, h("span", { class: "label" }, label),
      h("div", { class: "who-line" }, identiconSvg(a, "idc"), h("span", { class: "hash" }, a)));
    put(clear(root),
      crumbs(app, t("explorer.tx")),
      h("div", { class: "page-head" }, h("div", {}, h("span", { class: "label" }, t("explorer.tx")), h("h1", {}, `${formatETL(tx.amount, lang)} ETL`),
        h("p", {}, t("explorer.euroToday", { euro: euro(app, tx.amount) }))),
        h("span", { class: "chip good" }, t("explorer.confirmed", { n: tx.height }))),
      h("section", { class: "panel" },
        h("div", { class: "flowcards" }, side(t("explorer.from"), tx.from), h("span", { class: "arrow big", "aria-hidden": "true" }, "→"), side(t("explorer.toLabel"), tx.to)),
        kv([
          [t("explorer.id"), hashText(tx.id)],
          [t("explorer.blockLabel"), [h("a", { href: route.block(tx.height) }, String(tx.height)), ` · ${longDate(app, tx.blockTime)} · ${utcTime(tx.blockTime)}`]],
          [t("explorer.fee"), t("explorer.feeText", { fee: formatETL(tx.fee, lang) })],
          [t("explorer.nonce"), t("explorer.nonceText", { n: tx.nonce, nth: BigInt(tx.nonce) + 1n })],
          [t("explorer.signed"), localTime(app, tx.timestamp)],
        ])));
  });
}

// --- un échange ---

// timeline : étapes de l'échange dans l'ordre des blocs (open, paid, dispute, release ou refund).
export function timeline(e) {
  const steps = e.history ?? [];
  const at = (k) => steps.find((s) => s.kind === k);
  const out = [{ key: "opened", step: at("open") }];
  if (at("paid") || e.outcome === "pending") out.push({ key: "paid", step: at("paid") });
  if (at("dispute")) out.push({ key: "disputed", step: at("dispute"), alert: true });
  const end = at("release") ?? at("refund");
  out.push({ key: end ? (end.kind === "release" ? "released" : "refunded") : "settled", step: end });
  return out;
}

function renderEscrow(app, root, id) {
  const { t, lang, api } = app;
  return loading(root, async () => {
    let e;
    try {
      e = await api.escrow(id);
    } catch (err) {
      if (err.status === 404 || err.status === 400) return put(clear(root), crumbs(app, t("explorer.escrow")), notFound(app, id, err.status === 400 ? { reason: "short_hash", length: id.length } : { reason: "unknown_hash" }));
      throw err;
    }
    const steps = timeline(e);
    const settled = (e.history ?? []).find((s) => s.kind === "release" || s.kind === "refund");
    const arbiterPaid = settled && (settled.signers & 4) !== 0;
    const chip = e.outcome === "pending" ? h("span", { class: "chip warn" }, t("explorer.escrowPending"))
      : h("span", { class: "chip neutral" }, t(e.outcome === "released" ? "explorer.escrowReleased" : "explorer.escrowRefunded"));
    put(clear(root),
      crumbs(app, t("explorer.escrow")),
      h("div", { class: "page-head" }, h("div", {}, h("span", { class: "label" }, t("explorer.escrowLabel")), h("h1", {}, `${formatETL(e.amount, lang)} ETL`),
        h("p", { class: "hash" }, e.id)), chip),
      h("section", { class: "panel deal" },
        h("ol", { class: "track" }, steps.map((s) => h("li", { class: s.alert ? "alert" : s.step ? "done" : "" },
          t(`escrow.stage.${s.key}`), s.step ? [h("br"), h("a", { href: route.block(s.step.height) }, t("history.block", { n: s.step.height }))] : null))),
        h("div", { class: "parties" }, party(app, "seller", e.seller), party(app, "buyer", e.buyer), e.arbiter ? party(app, "arbiter", e.arbiter) : null),
        kv([
          [t("explorer.amount"), e.outcome === "released" ? t("explorer.amountReleased", { amount: formatETL(e.amount, lang) })
            : e.outcome === "refunded" ? t("explorer.amountRefunded", { amount: formatETL(e.amount, lang) }) : `${formatETL(e.amount, lang)} ETL`],
          e.arbiter ? [t("explorer.arbiterFee"), settled ? t(arbiterPaid ? "explorer.arbiterPaid" : "explorer.arbiterBack", { fee: formatETL(e.arbiterFee, lang) }) : `${formatETL(e.arbiterFee, lang)} ETL`] : null,
          [t("explorer.openFee"), `${formatETL(e.fee, lang)} ETL`],
          [t("explorer.days"), t("explorer.daysN", { n: e.days })],
          e.outcome === "pending" && e.deadline ? [t("explorer.deadline"), longDate(app, e.deadline)] : null,
        ]),
        e.archived ? h("p", { class: "callout info" }, icon("info"), h("span", {}, t("explorer.archived", {
          open: steps[0].step?.height ?? "?", end: settled?.height ?? "?" }))) : null));
  });
}

// --- recherche ---

// notFound : ce qui a été compris, pourquoi rien n'est trouvé, ce qu'on peut faire (5 motifs).
export function notFound(app, q, data) {
  const { t, lang } = app;
  const st = app.status;
  const nextAt = new Date(st.next_block * 1000);
  const when = t("home.nextAt", { day: nextAt.toLocaleDateString(localeOf(lang), { weekday: "long" }), time: nextAt.toLocaleTimeString(localeOf(lang), { hour: "2-digit", minute: "2-digit" }) });
  const reason = data?.reason ?? "unrecognized";
  const body = [];
  if (reason === "short_hash") body.push(h("p", { class: "muted" }, t("explorer.nf.short", { n: data.length })));
  else if (reason === "unknown_hash") body.push(h("p", { class: "muted" }, t("explorer.nf.unknown")), h("p", { class: "callout info" }, icon("info"), h("span", {}, t("explorer.nf.pending", { when }))));
  else if (reason === "other_network") body.push(h("p", { class: "muted" }, data.hrp === "etlt" ? t("explorer.nf.test", { prefix: `${st.hrp}1` }) : t("explorer.nf.other", { hrp: data.hrp, prefix: `${st.hrp}1` })));
  else if (reason === "future_block") body.push(h("p", { class: "muted" }, t("explorer.nf.future", { tip: data.tip }), " ", h("a", { href: route.block(data.tip) }, t("explorer.blockN", { n: data.tip }))), h("p", { class: "fine" }, t("explorer.nf.nextBlock", { when })));
  else body.push(h("p", { class: "muted" }, t("explorer.nf.unrecognized")));
  return h("section", { class: "panel" },
    h("h2", {}, t("explorer.nf.title", { q: q.length > 80 ? `${q.slice(0, 80)}…` : q })),
    ...body,
    h("span", { class: "label" }, t("explorer.nf.can")),
    h("ul", { class: "plain hints" },
      h("li", {}, h("strong", {}, t("explorer.nf.height")), " · 412"),
      h("li", {}, h("strong", {}, t("explorer.nf.address")), ` · ${st.hrp}1q…`),
      h("li", {}, h("strong", {}, t("explorer.nf.hash")), ` · ${t("explorer.nf.hex")}`)));
}

function renderSearch(app, root, q) {
  const { t, api } = app;
  return loading(root, async () => {
    try {
      const target = searchTarget(await api.search(q));
      if (target) return location.replace(target);
    } catch (e) {
      if (e.status !== 404 && e.status !== 400) throw e;
      return put(clear(root), crumbs(app, t("explorer.searchCrumb")), searchForm(app, q), notFound(app, q, e.status === 404 ? e.data : null));
    }
    put(clear(root), crumbs(app, t("explorer.searchCrumb")), searchForm(app, q), notFound(app, q, null));
  });
}

export function renderExplorer(app) {
  const root = h("div", { class: "page explorer" });
  const [sub, arg] = app.route?.params ?? [];
  switch (sub) {
    case "block": return renderBlock(app, root, arg);
    case "address": return renderAddress(app, root, arg ?? "");
    case "tx": return renderTx(app, root, arg ?? "");
    case "escrow": return renderEscrow(app, root, arg ?? "");
    case "search": return renderSearch(app, root, arg ?? "");
    default: return renderList(app, root);
  }
}
