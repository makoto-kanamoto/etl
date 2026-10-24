// Échanges sécurisés (vente d'ETL contre des euros), côté portefeuille : suivi fidèle au protocole
// (ouvert → euros payés → réglé, litige), prochaine action, parties avec leur réputation en chiffres,
// libellé local chiffré, ouverture en 2 étapes, complément d'une opération reçue. Les opérations à
// plusieurs signatures passent par le texte « etl-escrow: ».

import { h, field, clear, put, addressInput } from "../lib/dom.js";
import { parseETL, formatETL, euroCents, formatEuro } from "../lib/amount.js";
import { localeOf } from "../lib/strings.js";
import { actions, roleOf, completes, escrowState, reputation, signerRoles, stages, nextStep, deadlineDays, KIND_OF, NAME_OF, ROLE_NAMES } from "../lib/escrowui.js";
import { identiconSvg } from "../lib/identicon.js";
import { short, group, message, actionRow, pageHead, steps, who, capitalize } from "../lib/ui.js";
import * as esc from "../../wallet/escrow.js";
import * as keys from "../../wallet/keys.js";
import { route } from "../lib/site.js";

const locale = localeOf;

function errorText(app, e) {
  return e?.message === "network" ? app.t("error.network") : String(e?.message ?? e);
}

// textBox affiche un texte à transmettre, avec un bouton de copie.
export function textBox(app, text, helpKey, vars) {
  const area = h("textarea", { rows: 4, readonly: true, class: "mono small" });
  area.value = text;
  const copy = h("button", { class: "small", type: "button", onClick: async () => {
    try {
      await navigator.clipboard.writeText(text);
      copy.textContent = app.t("main.copied");
    } catch {
      area.select();
    }
  } }, app.t("main.copy"));
  return h("div", { class: "textbox" }, h("h4", {}, app.t("escrow.text.title")), h("p", { class: "note" }, app.t(helpKey, vars)), area, copy);
}

// repCounts : réputation on-chain d'une adresse, en chiffres (pas de note inventée).
export async function repCounts(app, address) {
  try {
    const r = reputation(await app.api.account(address));
    return app.t("escrow.repCounts", r);
  } catch {
    return "";
  }
}

// party : rôle, motif et nom, réputation (chargée ensuite).
export function party(app, roleKey, address, me) {
  const rep = h("div", { class: "rep" }, "…");
  repCounts(app, address).then((s) => (rep.textContent = s));
  // Site public : la partie ouvre sa page dans l'explorateur (réputation, historique).
  return h(app.status?.public ? "a" : "div", { class: "party", href: app.status?.public ? route.address(address) : null },
    h("span", { class: "label" }, app.t(`escrow.${roleKey}`) + (address === me ? ` · ${app.t("escrow.you")}` : "")),
    who(app, address), rep);
}

// track : les trois étapes du suivi (E1).
export function track(app, e) {
  return h("ol", { class: "track" }, stages(e).map((s) => h("li", { class: s.state }, app.t(`escrow.stage.${s.key}`))));
}

function deadlineLine(app, e) {
  const { t, lang } = app;
  if (e.outcome !== "pending") return null;
  if (e.paid) return h("p", { class: "fine" }, t("escrow.paidNote"));
  const n = deadlineDays(e, Date.now() / 1000);
  const date = new Date(e.deadline * 1000).toLocaleDateString(locale(lang), { day: "numeric", month: "long", timeZone: "UTC" });
  return h("p", { class: n > 0 ? "fine" : "fine late" }, n > 0 ? t("escrow.deadlineLeft", { date, n }) : t("escrow.deadlinePassed", { date }));
}

function whoList(app, mask) {
  return signerRoles(mask).map((r) => app.t(`escrow.who.${r}`)).join(app.t("list.and"));
}

function amountLine(app, e) {
  const { t, lang } = app;
  const st = app.status;
  const euro = st ? ` ≈ ${formatEuro(euroCents(e.amount, st.ipc.value, st.base_ipc.value), lang)}` : "";
  return h("p", { class: "deal-amount" }, h("span", { class: "big" }, `${formatETL(e.amount, lang)} ETL`),
    h("span", { class: "muted" }, euro + (e.arbiter ? ` · ${t("escrow.arbiterFeeShort", { fee: formatETL(e.arbiterFee, lang) })}` : ` · ${t("escrow.noArbiter")}`)));
}

export function renderEscrow(app) {
  const { t } = app;
  const root = h("div", { class: "page escrow" });
  if (!app.session.isUnlocked()) {
    put(root, pageHead(t("nav.escrow"), t("escrow.title")),
      h("section", { class: "panel narrow" }, h("p", {}, t("escrow.locked")), h("a", { href: "#/wallet" }, t("nav.wallet"))));
    return root;
  }
  const me = app.session.address();
  const pending = h("div", { class: "stack" }, h("p", { class: "muted" }, "…"));
  const done = h("div", { class: "stack" });
  const doneBox = h("details", { class: "panel done-list", hidden: true }, h("summary", {}, t("escrow.finished")), done);
  const openBox = h("div", { hidden: !app.escrowOpen });
  const openBtn = h("button", { class: "primary", type: "button", onClick: () => {
    app.escrowOpen = !app.escrowOpen;
    openBox.hidden = !app.escrowOpen;
    if (app.escrowOpen) put(clear(openBox), openPanel(app, me, load));
  } }, t("open.title"));

  async function load() {
    try {
      const account = await app.api.account(me);
      const items = (await Promise.all(account.escrows.map((id) => app.api.escrow(id)))).reverse();
      const live = items.filter((e) => e.outcome === "pending");
      const over = items.filter((e) => e.outcome !== "pending");
      put(clear(pending), live.length ? live.map((e) => escrowCard(app, e, me, load)) : h("section", { class: "panel" }, h("p", { class: "muted" }, t("escrow.empty"))));
      clear(done);
      doneBox.hidden = !over.length;
      put(done, over.map((e) => escrowCard(app, e, me, load)));
      doneBox.querySelector("summary").textContent = t("escrow.finishedN", { n: over.length });
    } catch (e) {
      put(clear(pending), message("error", errorText(app, e)));
    }
  }

  put(root, pageHead(t("nav.escrow"), t("escrow.title"), openBtn), openBox, pending, receivePanel(app, me, load), doneBox);
  Promise.all([app.session.contacts().then((b) => (app.book = b), () => null), app.session.labels().then((l) => (app.labels = l), () => (app.labels = null))])
    .then(() => {
      if (app.escrowOpen && !openBox.firstChild) put(openBox, openPanel(app, me, load)); // jamais d'effacement d'une saisie en cours
      app.onBlock = load;
      load();
    });
  return root;
}

// --- carte d'un échange ---

function escrowCard(app, e, me, reload) {
  const { t } = app;
  const role = roleOf(e, me);
  const other = role === esc.ROLE.seller ? e.buyer : e.seller;
  const next = nextStep(e, role);
  const out = h("div");
  const titleEl = h("h3", {});
  const setTitle = () => {
    const label = app.labels?.get(e.id);
    titleEl.textContent = label ?? t("escrow.with", { who: app.book?.name(other) ?? short(other) });
  };
  setTitle();
  const rename = h("button", { class: "link small", type: "button", onClick: () => labelEditor(app, e, out, setTitle) }, t("escrow.label"));

  const acts = actions(e, role);
  const primary = next.mine && next.kind ? acts.find((a) => a.alone && a.kind === next.kind) : null;
  const buttons = acts.filter((a) => a !== primary).map((a) =>
    h("button", { type: "button", class: a.kind === "dispute" ? "danger" : "", onClick: () => confirmAction(app, e, role, a, clear(out), reload) },
      a.alone ? t(`escrow.kind.${a.kind}`) : t("escrow.prepare", { action: t(`escrow.kind.${a.kind}`), who: whoList(app, a.with) })));
  const chipClass = e.outcome !== "pending" ? "chip good" : next.mine ? "chip warn" : e.disputed ? "chip bad" : "chip neutral";

  return h("section", { class: "panel deal" },
    h("div", { class: "deal-head" }, h("div", { class: "deal-title" }, titleEl, rename), h("span", { class: chipClass }, t(`escrow.next.${next.key}`))),
    h("p", { class: "fine" }, t(`escrow.role.${ROLE_NAMES[role]}`)),
    amountLine(app, e),
    track(app, e),
    deadlineLine(app, e),
    h("div", { class: "parties" },
      party(app, "seller", e.seller, me), party(app, "buyer", e.buyer, me), e.arbiter ? party(app, "arbiter", e.arbiter, me) : null),
    acts.length ? actionRow(
      primary ? h("button", { class: "primary", type: "button", onClick: () => confirmAction(app, e, role, primary, clear(out), reload) }, t(`escrow.kind.${primary.kind}`)) : null,
      ...buttons) : null,
    out,
    h("p", { class: "fine mono id" }, e.id));
}

function labelEditor(app, e, out, done) {
  const { t } = app;
  if (!app.labels) return put(clear(out), message("error", t("contacts.locked")));
  const input = h("input", { type: "text", autocomplete: "off", maxlength: 60, value: app.labels.get(e.id) ?? "", "aria-label": t("escrow.label") });
  const err = h("div");
  put(clear(out), h("form", { class: "inline", onSubmit: async (ev) => {
    ev.preventDefault();
    try {
      if (input.value.trim()) await app.labels.put(e.id, input.value);
      else await app.labels.delete(e.id);
      clear(out);
      done();
    } catch (x) {
      put(clear(err), message("error", t(`contacts.err.${x.code ?? "empty"}`)));
    }
  } }, input, h("button", { class: "small primary", type: "submit" }, t("contacts.save")), h("button", { class: "small", type: "button", onClick: () => clear(out) }, t("send.cancel"))), err,
  h("p", { class: "fine" }, t("escrow.labelNote")));
  queueMicrotask(() => input.focus());
}

// confirmAction : ce que l'opération va produire, puis signature (seule) ou texte à transmettre.
function confirmAction(app, e, role, a, out, reload) {
  const { t, lang } = app;
  const st = app.status;
  const params = { chainID: st.chain_id, hrp: st.hrp };
  put(out,
    h("div", { class: "msg warning" },
      h("p", {}, h("strong", {}, t("escrow.confirm", { action: t(`escrow.kind.${a.kind}`) }))),
      h("p", {}, t(`escrow.effect.${a.kind}`, { amount: formatETL(e.amount, lang) })),
      a.alone ? null : h("p", {}, t("escrow.needs", { who: whoList(app, a.with) }))),
    actionRow(
      h("button", { class: a.kind === "dispute" ? "danger" : "primary", type: "button", onClick: async () => {
        app.session.touch();
        const id = Uint8Array.from(e.id.match(/../g).map((x) => parseInt(x, 16)));
        const op = esc.act(app.session.key, KIND_OF[a.kind], id, e.seq, role, params);
        if (!a.alone) return put(clear(out), textBox(app, esc.toText(op), "escrow.text.help", { who: whoList(app, a.with) }));
        try {
          await app.api.submitEscrow(esc.toText(op));
          put(clear(out), message("ok", t("escrow.submitted")));
        } catch (err) {
          put(clear(out), message("error", errorText(app, err)));
        }
      } }, t("escrow.sign")),
      h("button", { type: "button", onClick: () => clear(out) }, t("send.cancel"))));
}

// --- ouverture en 2 étapes : saisir → vérifier (puis texte pour l'arbitre, s'il y en a un) ---

function chips(app, input, onPick) {
  const list = (app.book?.list() ?? []).slice(0, 6);
  return list.length ? h("div", { class: "contacts" }, list.map((c) =>
    h("button", { class: "contact", type: "button", onClick: () => ((input.value = c.address), onPick()) }, identiconSvg(c.address), c.name))) : null;
}

function openPanel(app, me, reload) {
  const { t, lang, api } = app;
  const st = app.status;
  const panel = h("section", { class: "panel" });
  const stepsOf = (n) => steps([t("send.step.input"), t("send.step.check"), t("open.step.done")], n);
  const buyer = addressInput(app);
  const arbiter = addressInput(app);
  const buyerWho = h("div");
  const arbiterWho = h("div");
  const amount = h("input", { type: "text", inputmode: "decimal", autocomplete: "off", placeholder: "0,00", class: "amount-input" });
  const fee = h("input", { type: "text", inputmode: "decimal", autocomplete: "off", value: "0" });
  const days = h("input", { type: "number", min: String(esc.MIN_DAYS), max: String(esc.MAX_DAYS), step: "1", value: "3" });
  const label = h("input", { type: "text", autocomplete: "off", maxlength: 60 });
  const recap = h("dl", { class: "recap" });
  const out = h("div");
  const E = (u) => `${formatETL(u, lang)} ETL`;
  let p = null;
  let seq = 0; // seul le calcul le plus récent du récapitulatif s'affiche (frappes rapprochées)

  const addr = (input) => {
    try {
      return keys.encodeAddress(keys.parseAddress(input.value.trim(), st.hrp), st.hrp);
    } catch {
      return null;
    }
  };
  const terms = async () => {
    p ??= await api.params();
    const params = { chainID: st.chain_id, hrp: st.hrp, feeBps: p.feeBps, minFee: p.minFee };
    const arb = arbiter.value.trim() ? addr(arbiter) : null;
    return { params, terms: esc.newTerms({ seller: me, buyer: addr(buyer), arbiter: arb, amount: parseETL(amount.value),
      arbiterFee: arb ? parseETL(fee.value || "0") : 0n, days: Number(days.value), timestamp: BigInt(Math.floor(Date.now() / 1000)) }, params) };
  };
  const update = async () => {
    const b = addr(buyer), a = addr(arbiter);
    put(clear(buyerWho), b ? who(app, b) : null);
    put(clear(arbiterWho), a ? who(app, a) : null);
    const mine = ++seq;
    let tm;
    try {
      ({ terms: tm } = await terms());
    } catch {
      if (mine === seq) clear(recap); // saisie incomplète
      return;
    }
    if (mine !== seq) return;
    {
      const locked = tm.amount + tm.arbiterFee + tm.fee;
      put(clear(recap),
        h("dt", {}, t("escrow.amount")), h("dd", {}, E(tm.amount)),
        tm.arbiter.some((x) => x) ? [h("dt", {}, t("escrow.arbiterFee")), h("dd", {}, E(tm.arbiterFee))] : null,
        h("dt", {}, t("send.fee")), h("dd", {}, E(tm.fee)),
        h("dt", { class: "tot" }, t("open.locked")), h("dd", { class: "tot" }, E(locked)));
    }
  };
  for (const el of [buyer, arbiter, amount, fee, days]) el.addEventListener("input", update);

  const review = async () => {
    clear(out);
    const b = addr(buyer);
    if (!b) return put(out, message("error", t("send.badAddress")));
    if (b === me) return put(out, message("error", t("send.self")));
    if (arbiter.value.trim() && !addr(arbiter)) return put(out, message("error", t("send.badAddress")));
    const a = addr(arbiter);
    if (a && (a === me || a === b)) return put(out, message("error", t("open.badArbiter")));
    if (!/^\d+$/.test(days.value) || Number(days.value) < esc.MIN_DAYS || Number(days.value) > esc.MAX_DAYS) return put(out, message("error", t("open.badDays")));
    let tm, params;
    try {
      ({ terms: tm, params } = await terms());
    } catch {
      return put(out, message("error", t("send.badAmount")));
    }
    if (tm.amount === 0n) return put(out, message("error", t("send.zero")));
    const locked = tm.amount + tm.arbiterFee + tm.fee;
    const account = await api.account(me);
    if (account.balance < locked) return put(out, message("error", t("send.insufficient")));
    verify(tm, params, locked, b, a);
  };

  const verify = (tm, params, locked, b, a) => {
    const simple = !a;
    const err = h("div");
    const sign = async () => {
      app.session.touch();
      const op = esc.open(app.session.key, tm, params);
      const id = esc.idHex(esc.termsID(tm));
      if (label.value.trim() && app.labels) await app.labels.put(id, label.value).catch(() => null);
      if (!simple) return put(clear(panel), stepsOf(2), h("h2", {}, t("open.done.title")), textBox(app, esc.toText(op), "open.toArbiter", {}));
      try {
        await api.submitEscrow(esc.toText(op));
        put(clear(panel), stepsOf(2), h("h2", {}, t("open.done.title")), message("ok", t("escrow.submitted")));
        reload();
      } catch (x) {
        put(clear(err), message("error", errorText(app, x)));
      }
    };
    put(clear(panel), stepsOf(1),
      h("h2", {}, t("open.check.title")),
      h("div", { class: "parties" },
        h("div", { class: "party" }, h("span", { class: "label" }, t("escrow.buyer")), who(app, b), h("p", { class: "addr grouped" }, group(b)), h("div", { class: "rep" }, "…")),
        a ? h("div", { class: "party" }, h("span", { class: "label" }, t("escrow.arbiter")), who(app, a), h("p", { class: "addr grouped" }, group(a)), h("div", { class: "rep" }, "…")) : null),
      h("dl", { class: "recap" },
        h("dt", {}, t("escrow.amount")), h("dd", {}, E(tm.amount)),
        a ? [h("dt", {}, t("escrow.arbiterFee")), h("dd", {}, E(tm.arbiterFee))] : null,
        h("dt", {}, t("send.fee")), h("dd", {}, E(tm.fee)),
        h("dt", { class: "tot" }, t("open.locked")), h("dd", { class: "tot" }, E(locked)),
        h("dt", {}, t("open.payBefore")), h("dd", {}, t("escrow.days", { n: tm.days }))),
      simple ? message("warning", t("open.simple")) : message("info", t("open.withArbiter")),
      err,
      actionRow(h("button", { class: "primary", type: "button", onClick: sign }, t("escrow.sign")),
        h("button", { type: "button", onClick: () => put(clear(panel), ...form()) }, t("send.edit"))));
    const reps = panel.querySelectorAll(".party .rep");
    repCounts(app, b).then((s) => (reps[0].textContent = s));
    if (a) repCounts(app, a).then((s) => (reps[1].textContent = s));
  };

  const form = () => [stepsOf(0),
    h("h2", {}, t("open.title")),
    h("p", { class: "muted" }, t("open.text")),
    h("form", { onSubmit: (ev) => (ev.preventDefault(), review()) },
      h("div", { class: "field" }, h("span", { class: "label-strong" }, t("open.buyer")), chips(app, buyer, update), buyer, buyerWho),
      h("div", { class: "field" }, h("span", { class: "label-strong" }, t("open.arbiter")), chips(app, arbiter, update), arbiter, arbiterWho),
      h("div", { class: "form-cols" }, field(t("send.amount"), amount), field(t("open.arbiterFee"), fee), field(t("open.days"), days)),
      field(t("open.label"), label, t("escrow.labelNote")),
      recap, out,
      actionRow(h("button", { class: "primary", type: "submit" }, t("send.check")), h("button", { type: "button", onClick: () => ((app.escrowOpen = false), app.render()) }, t("send.cancel"))))];
  put(panel, ...form());
  return panel;
}

// --- compléter une opération reçue (E6) : qui demande quoi, avant de signer ---

function receivePanel(app, me, reload) {
  const { t, lang } = app;
  const text = h("textarea", { rows: 3, autocomplete: "off", spellcheck: "false", class: "mono small" });
  const out = h("div");
  const read = async () => {
    clear(out);
    let op, e;
    try {
      op = esc.fromText(text.value);
      if (op.kind === esc.KIND.open) throw new Error();
      e = await app.api.escrow(esc.idHex(op.escrow));
    } catch {
      return put(out, message("error", t("receive.invalid")));
    }
    const role = roleOf(e, me);
    if (!role || !completes(e, op, role) || op.seq !== e.seq) return put(out, message("error", t("receive.cannot")));
    const st = app.status;
    const params = { chainID: st.chain_id, hrp: st.hrp };
    const kind = NAME_OF[op.kind];
    put(out,
      h("div", { class: "msg warning" },
        h("p", {}, h("strong", {}, capitalize(t("receive.asks", { who: whoList(app, op.signers), action: t(`escrow.kind.${kind}`) })))),
        h("p", {}, t(`escrow.effect.${kind}`, { amount: formatETL(e.amount, lang) }))),
      amountLine(app, e),
      track(app, e),
      h("div", { class: "parties" }, party(app, "seller", e.seller, me), party(app, "buyer", e.buyer, me), e.arbiter ? party(app, "arbiter", e.arbiter, me) : null),
      actionRow(h("button", { class: "primary", type: "button", onClick: async () => {
        app.session.touch();
        try {
          const full = esc.addSignature(app.session.key, op, role, op.escrow, escrowState(e, st.hrp), params);
          await app.api.submitEscrow(esc.toText(full));
          put(clear(out), message("ok", t("escrow.submitted")));
          text.value = "";
          reload();
        } catch (err) {
          put(clear(out), message("error", errorText(app, err)));
        }
      } }, t("escrow.sign"))));
  };
  return h("details", { class: "panel receive" },
    h("summary", {}, t("receive.title")),
    h("p", { class: "muted" }, t("receive.text")),
    h("form", { onSubmit: (ev) => (ev.preventDefault(), read()) },
      field(t("receive.label"), text),
      actionRow(h("button", { class: "primary", type: "submit" }, t("receive.read")))),
    out);
}
