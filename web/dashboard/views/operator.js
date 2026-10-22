// Panneau opérateur (clé du nœud) : session à jeton (C3), quatre onglets — paramètres (règle graduée,
// votes et seuil des 2/3), parrainage, arbitrages (libérer ou rembourser), exclusion. Le nœud vérifie
// et signe lui-même ; la clé ne quitte jamais le nœud.

import { h, field, clear, put, addressInput } from "../lib/dom.js";
import { actions, roleOf, signerRoles, nextStep, NAME_OF } from "../lib/escrowui.js";
import { formatETL, parseETL } from "../lib/amount.js";
import { allowedRange, readRegistration, AMOUNT_PARAMS, PARAM_GROUPS, position } from "../lib/governance.js";
import { message, actionRow, pageHead, who, group, capitalize } from "../lib/ui.js";
import * as esc from "../../wallet/escrow.js";
import { encodeAddress } from "../../wallet/keys.js";
import { textBox, party, track } from "./escrow.js";

export const TOKEN_KEY = "etl.operatorToken";
const TABS = ["params", "sponsor", "arbiter", "exclusion"];
const locale = (lang) => (lang === "fr" ? "fr-FR" : "en-GB");

function getToken() {
  try {
    return sessionStorage.getItem(TOKEN_KEY);
  } catch {
    return null;
  }
}

function setToken(v) {
  try {
    if (v) sessionStorage.setItem(TOKEN_KEY, v);
    else sessionStorage.removeItem(TOKEN_KEY);
  } catch {
    /* session non retenue */
  }
}

export function renderOperator(app) {
  const { t } = app;
  const root = h("div", { class: "page operator" });
  if (!app.status?.node.address) {
    put(root, pageHead(t("nav.operator"), t("operator.title")), h("section", { class: "panel narrow" }, h("p", {}, t("operator.passive"))));
    return root;
  }
  const token = getToken();
  if (!token) {
    tokenForm(app, root);
    return root;
  }
  const op = app.api.operator(token);
  const onError = (out, e) => {
    if (e.status === 401) {
      setToken(null);
      app.render();
      return;
    }
    put(clear(out), message("error", e.message === "network" ? t("error.network") : e.message));
  };
  app.opTab = TABS.includes(app.opTab) ? app.opTab : "params";
  const body = h("div", { class: "tab-body" });
  const badge = h("span", { class: "count", hidden: true });
  const tabs = TABS.map((k) => h("button", { type: "button", role: "tab", id: `tab-${k}`, "aria-selected": String(app.opTab === k), onClick: () => {
    app.opTab = k;
    for (const b of tabs) b.setAttribute("aria-selected", String(b.id === `tab-${k}`));
    show();
  } }, t(`operator.tab.${k}`), k === "arbiter" ? badge : null));
  const show = () => {
    app.onBlock = null;
    clear(body);
    if (app.opTab === "params") paramsTab(app, op, onError, body);
    else if (app.opTab === "sponsor") sponsorTab(app, op, onError, body);
    else if (app.opTab === "arbiter") arbiterTab(app, op, onError, body, badge);
    else exclusionTab(app, op, onError, body);
  };
  put(root,
    pageHead(t("operator.kicker"), t("operator.heading"), h("button", { type: "button", onClick: () => (setToken(null), app.render()) }, t("operator.token.close"))),
    h("div", { class: "tabs", role: "tablist" }, tabs),
    body);
  show();
  // Pastille : arbitrages en litige, qui attendent la décision de ce nœud.
  arbitrated(app).then((items) => {
    const n = items.filter((e) => e.outcome === "pending" && e.disputed).length;
    badge.textContent = String(n);
    badge.hidden = n === 0;
  }, () => null);
  app.session.contacts?.().then((b) => (app.book = b), () => null);
  return root;
}

function tokenForm(app, root) {
  const { t } = app;
  const input = h("input", { type: "password", autocomplete: "off", spellcheck: "false" });
  put(root, pageHead(t("nav.operator"), t("operator.title")),
    h("section", { class: "panel narrow" },
      h("p", { class: "muted" }, t("operator.token.text", { path: "~/.etl/dashboard.token" })),
      h("form", { onSubmit: (e) => (e.preventDefault(), setToken(input.value.trim()), app.render()) },
        field(t("operator.token.label"), input),
        actionRow(h("button", { class: "primary", type: "submit" }, t("operator.token.open"))))));
  queueMicrotask(() => input.focus());
}

// --- paramètres (O2) ---

export function shownValue(name, v, lang) {
  return AMOUNT_PARAMS.has(name) ? `${formatETL(v, lang)} ETL` : v.toLocaleString(locale(lang));
}

// ruler : bornes immuables (rail), plage permise (zone), valeur actuelle (trait), changement en attente
// (repère) et proposition (losange, déplacée par le curseur).
function ruler(x, lo, hi, pendingValue) {
  const min = BigInt(x.min), max = BigInt(x.max), cur = BigInt(x.value);
  const pos = (v) => position(v, min, max);
  const zone = h("span", { class: "allowed" });
  zone.style.left = `${pos(lo)}%`;
  zone.style.width = `${Math.max(0, pos(hi) - pos(lo))}%`;
  const pin = h("span", { class: "pin" });
  pin.style.left = `${pos(cur)}%`;
  const prop = h("span", { class: "prop", hidden: true });
  const pend = pendingValue !== null ? h("span", { class: "pend" }) : null;
  if (pend) pend.style.left = `${pos(pendingValue)}%`;
  const el = h("div", { class: "scale", "aria-hidden": "true" }, h("span", { class: "rail" }), lo <= hi ? zone : null, pin, pend, prop);
  el.propose = (v) => {
    prop.hidden = v === null;
    if (v !== null) prop.style.left = `${pos(v)}%`;
  };
  return el;
}

function tallyBars(app, x, quorum, active) {
  const { t, lang } = app;
  if (!x.votes?.length) return null;
  return h("ul", { class: "vote-bars" }, x.votes.map((v) => {
    const fill = h("span", { class: "fill" });
    fill.style.width = `${Math.min(100, (v.votes / Math.max(1, active)) * 100)}%`;
    const mark = h("span", { class: "need" });
    mark.style.left = `${Math.min(100, (quorum / Math.max(1, active)) * 100)}%`;
    return h("li", {},
      h("span", { class: "tally-label" }, t("params.tally", { value: shownValue(x.name, BigInt(v.value), lang), votes: v.votes, quorum })),
      h("span", { class: "bar" }, fill, mark));
  }));
}

function paramCard(app, op, onError, x, p) {
  const { t, lang } = app;
  const value = BigInt(x.value);
  const [lo, hi] = allowedRange(x.name, value, BigInt(x.min), BigInt(x.max)); // plage vide : valeur hors bornes (chaîne de test)
  const pendingValue = x.pending ? BigInt(x.pending.value) : null;
  const scale = ruler(x, lo, hi, pendingValue);
  const out = h("div");
  const card = h("article", { class: "param" },
    h("div", { class: "param-top" }, h("h3", {}, t(`params.name.${x.name}`)), h("span", { class: "cur" }, shownValue(x.name, value, lang))),
    scale,
    h("div", { class: "scale-ends" }, h("span", {}, shownValue(x.name, BigInt(x.min), lang)), h("span", {}, shownValue(x.name, BigInt(x.max), lang))),
    x.pending ? message("info", t("params.pending", { value: shownValue(x.name, pendingValue, lang),
      date: new Date(x.pending.activation * 1000).toLocaleDateString(locale(lang), { timeZone: "UTC" }) })) : null,
    tallyBars(app, x, p.quorum, p.active_nodes));
  if (x.pending || lo > hi) return put(card, x.pending ? null : h("p", { class: "fine" }, t("params.outOfBounds")));
  const amountParam = AMOUNT_PARAMS.has(x.name);
  const input = h("input", { type: "text", inputmode: amountParam ? "decimal" : "numeric", autocomplete: "off", "aria-label": `${t(`params.name.${x.name}`)} — ${t("params.new")}` });
  const slider = h("input", { type: "range", min: String(lo), max: String(hi), step: "1", value: String(value), "aria-label": t("params.slider") });
  const parse = () => {
    try {
      const v = amountParam ? parseETL(input.value) : BigInt(input.value.replace(/[\s\u00a0\u202f]/g, ""));
      return v >= lo && v <= hi ? v : null;
    } catch {
      return null;
    }
  };
  slider.addEventListener("input", () => {
    const v = BigInt(slider.value);
    input.value = amountParam ? formatETL(v, lang) : String(v);
    scale.propose(v);
  });
  input.addEventListener("input", () => {
    const v = parse();
    scale.propose(v);
    if (v !== null) slider.value = String(v);
  });
  const vote = async () => {
    clear(out);
    const v = parse();
    if (v === null) return put(out, message("error", t("params.badValue", { min: shownValue(x.name, lo, lang), max: shownValue(x.name, hi, lang) })));
    try {
      await op.paramVote(x.name, v);
      put(out, message("ok", t("params.sent")));
      input.value = "";
      scale.propose(null);
    } catch (e) {
      onError(out, e);
    }
  };
  put(card,
    h("p", { class: "fine" }, t("params.allowed", { min: shownValue(x.name, lo, lang), max: shownValue(x.name, hi, lang) })),
    slider,
    h("form", { class: "inline", onSubmit: (e) => (e.preventDefault(), vote()) }, input, h("button", { type: "submit" }, t("params.vote"))),
    out);
  return card;
}

function paramsTab(app, op, onError, body) {
  const { t } = app;
  put(body, h("p", { class: "muted" }, "…"));
  app.api.params().then((p) => {
    const byName = new Map(p.params.map((x) => [x.name, x]));
    put(clear(body),
      h("p", { class: "muted" }, t("params.text", { days: p.activation_days })),
      h("div", { class: "key" },
        h("span", {}, h("i", { class: "key-rail" }), t("params.key.bounds")),
        h("span", {}, h("i", { class: "key-allowed" }), t("params.key.allowed")),
        h("span", {}, h("i", { class: "key-pin" }), t("params.key.current")),
        h("span", {}, h("i", { class: "key-pend" }), t("params.key.pending")),
        h("span", {}, h("i", { class: "key-prop" }), t("params.key.proposal"))),
      PARAM_GROUPS.map(([g, names], i) => h("details", { class: "panel group", open: i === 0 },
        h("summary", {}, h("span", {}, t(`params.group.${g}`)), h("span", { class: "fine" }, t(names.length === 1 ? "params.groupCount1" : "params.groupCountN", { n: names.length }))),
        h("div", { class: "params" }, names.filter((n) => byName.has(n)).map((n) => paramCard(app, op, onError, byName.get(n), p))))));
  }).catch((e) => onError(body, e));
}

// --- arbitrages (O3) ---

async function arbitrated(app) {
  const node = app.status.node.address;
  const account = await app.api.account(node);
  return (await Promise.all(account.escrows.map((id) => app.api.escrow(id)))).filter((e) => roleOf(e, node) === esc.ROLE.arbiter);
}

function arbiterTab(app, op, onError, body, badge) {
  const { t } = app;
  const list = h("div", { class: "stack" }, h("p", { class: "muted" }, "…"));
  const load = async () => {
    try {
      const items = await arbitrated(app);
      const live = items.filter((e) => e.outcome === "pending");
      const n = live.filter((e) => e.disputed).length;
      badge.textContent = String(n);
      badge.hidden = n === 0;
      put(clear(list), live.length ? live.map((e) => arbitrationCard(app, op, onError, e)) : h("section", { class: "panel" }, h("p", { class: "muted" }, t("arbiter.empty"))));
    } catch (e) {
      onError(list, e);
    }
  };
  app.onBlock = load;
  load();
  put(body, list, pastePanel(app, op, onError));
}

function arbitrationCard(app, op, onError, e) {
  const { t, lang } = app;
  const node = app.status.node.address;
  const next = nextStep(e, esc.ROLE.arbiter);
  const out = h("div");
  const prepare = actions(e, esc.ROLE.arbiter).filter((a) => !a.alone && (a.kind === "release" || a.kind === "refund"));
  const decide = (a) => put(clear(out),
    h("div", { class: "msg warning" },
      h("p", {}, h("strong", {}, t(`arbiter.decide.${a.kind}`))),
      h("p", {}, t(`escrow.effect.${a.kind}`, { amount: formatETL(e.amount, lang) })),
      h("p", {}, t("arbiter.fee", { fee: formatETL(e.arbiterFee, lang) })),
      h("p", {}, t("escrow.needs", { who: t(`escrow.who.${a.kind === "release" ? "buyer" : "seller"}`) }))),
    actionRow(
      h("button", { class: "primary", type: "button", onClick: async () => {
        try {
          const r = await op.act(e.id, a.kind);
          put(clear(out), textBox(app, r.op, "escrow.text.help", { who: t(`escrow.who.${a.kind === "release" ? "buyer" : "seller"}`) }));
        } catch (err) {
          onError(out, err);
        }
      } }, t("arbiter.signDecision")),
      h("button", { type: "button", onClick: () => clear(out) }, t("send.cancel"))));
  return h("section", { class: "panel deal" },
    h("div", { class: "deal-head" }, h("h3", {}, t("arbiter.dealTitle", { amount: formatETL(e.amount, lang) })),
      h("span", { class: e.disputed ? "chip warn" : "chip neutral" }, t(`escrow.next.${next.key}`))),
    track(app, e),
    h("div", { class: "parties" }, party(app, "seller", e.seller, node), party(app, "buyer", e.buyer, node)),
    prepare.length ? actionRow(...prepare.map((a) => h("button", { type: "button", class: e.disputed ? "primary" : "", onClick: () => decide(a) }, t(`arbiter.choose.${a.kind}`)))) : null,
    out,
    h("p", { class: "fine mono id" }, e.id));
}

function pastePanel(app, op, onError) {
  const { t, lang } = app;
  const node = app.status.node.address;
  const text = h("textarea", { rows: 3, autocomplete: "off", spellcheck: "false", class: "mono small" });
  const out = h("div");
  const read = async () => {
    clear(out);
    let o;
    try {
      o = esc.fromText(text.value);
    } catch {
      return put(out, message("error", t("receive.invalid")));
    }
    const isOpen = o.kind === esc.KIND.open;
    let summary;
    if (isOpen) {
      const tm = o.terms;
      const hrp = app.status.hrp;
      summary = [
        h("p", {}, h("strong", {}, t("arbiter.open"))),
        h("p", {}, t("arbiter.openTerms", { amount: formatETL(tm.amount, lang), fee: formatETL(tm.arbiterFee, lang), days: tm.days })),
        h("div", { class: "parties" }, party(app, "seller", encodeAddress(tm.seller, hrp), node), party(app, "buyer", encodeAddress(tm.buyer, hrp), node))];
    } else {
      let e;
      try {
        e = await app.api.escrow(esc.idHex(o.escrow));
      } catch (err) {
        return onError(out, err);
      }
      summary = [
        h("p", {}, h("strong", {}, capitalize(t("receive.asks", { who: signerRoles(o.signers).map((r) => t(`escrow.who.${r}`)).join(lang === "fr" ? " et " : " and "), action: t(`escrow.kind.${NAME_OF[o.kind]}`) })))),
        h("p", {}, t(`escrow.effect.${NAME_OF[o.kind]}`, { amount: formatETL(e.amount, lang) })),
        track(app, e),
        h("div", { class: "parties" }, party(app, "seller", e.seller, node), party(app, "buyer", e.buyer, node))];
    }
    put(out, h("div", { class: "stack" }, ...summary),
      actionRow(h("button", { class: "primary", type: "button", onClick: async () => {
        try {
          await (isOpen ? op.cosign(text.value.trim()) : op.sign(text.value.trim()));
          put(clear(out), message("ok", t("escrow.submitted")));
          text.value = "";
        } catch (err) {
          onError(out, err);
        }
      } }, isOpen ? t("arbiter.cosign") : t("arbiter.sign"))));
  };
  return h("details", { class: "panel receive" },
    h("summary", {}, t("arbiter.paste.title")),
    h("p", { class: "muted" }, t("arbiter.paste.text")),
    h("form", { onSubmit: (e) => (e.preventDefault(), read()) },
      field(t("receive.label"), text),
      actionRow(h("button", { class: "primary", type: "submit" }, t("receive.read")))),
    out);
}

// --- parrainage (O4) ---

function sponsorTab(app, op, onError, body) {
  const status = app.status.node.status;
  if (status === "unknown") return put(body, preparePanel(app, op, onError));
  if (status === "inactive") return put(body, reactivatePanel(app, op, onError));
  if (status === "admitted") return put(body, sponsorPanel(app, op, onError));
  put(body, h("section", { class: "panel" }, h("p", { class: "muted" }, app.t("sponsor.none", { status: app.t(`status.${status}`) }))));
}

function preparePanel(app, op, onError) {
  const { t } = app;
  const sponsor = addressInput(app);
  const out = h("div");
  return h("section", { class: "panel" },
    h("h2", {}, t("sponsor.prepare.title")),
    h("p", { class: "muted" }, t("sponsor.prepare.text")),
    h("form", { onSubmit: async (e) => {
      e.preventDefault();
      try {
        const r = await op.registrationRequest(sponsor.value.trim());
        put(clear(out), textBox(app, r.registration, "sponsor.toSponsor", {}));
      } catch (err) {
        onError(out, err);
      }
    } }, field(t("sponsor.address"), sponsor), actionRow(h("button", { class: "primary", type: "submit" }, t("sponsor.prepare")))),
    out);
}

function sponsorPanel(app, op, onError) {
  const { t, lang } = app;
  const text = h("textarea", { rows: 3, autocomplete: "off", spellcheck: "false", class: "mono small" });
  const out = h("div");
  const note = h("p", { class: "muted" });
  app.api.params().then((p) => (note.textContent = t("sponsor.sign.text", { days: p.params.find((x) => x.name === "sponsor_cooldown_days")?.value ?? "?" })));
  const read = () => {
    clear(out);
    let r;
    try {
      r = readRegistration(text.value, app.status.hrp);
    } catch {
      return put(out, message("error", t("sponsor.invalid")));
    }
    const mine = r.sponsor === app.status.node.address;
    put(out,
      h("dl", { class: "pair" },
        h("dt", {}, t("sponsor.candidate")), h("dd", {}, who(app, r.candidate, false)),
        h("dt", {}, t("sponsor.designated")), h("dd", {}, r.sponsor ? who(app, r.sponsor, false, false, true) : "—"),
        h("dt", {}, t("sponsor.signedOn")), h("dd", {}, new Date(Number(r.day) * 86400000).toLocaleDateString(locale(lang), { timeZone: "UTC" }))),
      h("p", { class: "addr grouped" }, group(r.candidate)),
      mine
        ? actionRow(h("button", { class: "primary", type: "button", onClick: async () => {
            try {
              await op.registrationSponsor(text.value.trim());
              put(clear(out), message("ok", t("sponsor.sent")));
              text.value = "";
            } catch (e) {
              onError(out, e);
            }
          } }, t("sponsor.confirm")))
        : message("error", t("sponsor.notMe")));
  };
  return h("section", { class: "panel" },
    h("h2", {}, t("sponsor.sign.title")),
    note,
    h("form", { onSubmit: (e) => (e.preventDefault(), read()) },
      field(t("sponsor.label"), text),
      actionRow(h("button", { class: "primary", type: "submit" }, t("receive.read")))),
    out);
}

function reactivatePanel(app, op, onError) {
  const { t } = app;
  const out = h("div");
  return h("section", { class: "panel" },
    h("h2", {}, t("reactivate.title")),
    h("p", { class: "muted" }, t("reactivate.text")),
    actionRow(h("button", { class: "primary", type: "button", onClick: async () => {
      try {
        await op.reactivate();
        put(clear(out), message("ok", t("reactivate.sent")));
      } catch (e) {
        onError(out, e);
      }
    } }, t("reactivate.button"))),
    out);
}

// --- exclusion (O5 : statut et parrain affichés, adresse retapée) ---

function exclusionTab(app, op, onError, body) {
  const { t } = app;
  const target = addressInput(app);
  const out = h("div");
  const check = async () => {
    clear(out);
    let acc, p;
    try {
      [acc, p] = await Promise.all([app.api.account(target.value.trim()), app.api.params()]);
    } catch (e) {
      return onError(out, e);
    }
    const again = addressInput(app);
    const ban = p.params.find((x) => x.name === "sponsor_ban_days")?.value ?? "?";
    put(out,
      h("dl", { class: "pair" },
        h("dt", {}, t("exclusion.node")), h("dd", {}, who(app, acc.address, false, false, true)),
        h("dt", {}, t("exclusion.statusLabel")), h("dd", {}, t(`status.${acc.node}`)),
        h("dt", {}, t("exclusion.sponsorLabel")), h("dd", {}, acc.founder ? t("exclusion.founder") : acc.sponsor ? who(app, acc.sponsor, false, false, true) : "—")),
      h("p", { class: "addr grouped" }, group(acc.address)),
      message("warning", t("exclusion.text", { days: ban })),
      field(t("exclusion.confirm"), again),
      actionRow(h("button", { class: "danger", type: "button", onClick: async () => {
        if (again.value.trim().toLowerCase() !== acc.address) return put(out, message("error", t("exclusion.mismatch")));
        try {
          await op.exclusionVote(acc.address);
          put(clear(out), message("ok", t("exclusion.sent")));
          target.value = "";
        } catch (e) {
          onError(out, e);
        }
      } }, t("exclusion.vote"))));
  };
  put(body, h("section", { class: "panel" },
    h("h2", {}, t("exclusion.title")),
    h("form", { onSubmit: (e) => (e.preventDefault(), check()) },
      field(t("exclusion.target"), target),
      actionRow(h("button", { type: "submit" }, t("exclusion.check")))),
    out));
}
