// Échanges sous escrow, côté portefeuille (vendeur, acheteur) : liste et actions, ouverture,
// complément d'une opération reçue. Avant toute signature : résumé lisible et réputation de l'autre
// partie (et de l'arbitre). Les opérations à plusieurs signatures passent par le texte « etl-escrow: ».

import { h, field, clear, put, addressInput } from "../lib/dom.js";
import { parseETL, formatETL, euroCents, formatEuro } from "../lib/amount.js";
import { actions, roleOf, completes, escrowState, reputation, signerRoles, KIND_OF, NAME_OF } from "../lib/escrowui.js";
import * as esc from "../../wallet/escrow.js";
import * as keys from "../../wallet/keys.js";

const ROLE_KEY = { [esc.ROLE.seller]: "seller", [esc.ROLE.buyer]: "buyer", [esc.ROLE.arbiter]: "arbiter" };

function msg(kind, text) {
  return h("p", { class: `msg ${kind}`, role: kind === "error" ? "alert" : "status" }, text);
}

function errorText(app, e) {
  return e?.message === "network" ? app.t("error.network") : String(e?.message ?? e);
}

// textBox affiche un texte à transmettre, avec un bouton de copie.
export function textBox(app, text, helpKey, vars) {
  const area = h("textarea", { rows: 4, readonly: true, class: "mono small" });
  area.value = text;
  const copy = h("button", { class: "small", onClick: async () => {
    try {
      await navigator.clipboard.writeText(text);
      copy.textContent = app.t("main.copied");
    } catch {
      area.select();
    }
  } }, app.t("main.copy"));
  return h("div", { class: "textbox" }, h("h4", {}, app.t("escrow.text.title")), h("p", { class: "note" }, app.t(helpKey, vars)), area, copy);
}

// repLine : réputation on-chain d'une adresse (C5).
export async function repLine(app, who, address) {
  try {
    const r = reputation(await app.api.account(address));
    return h("p", { class: "rep small" }, app.t("escrow.rep", { who, ...r }));
  } catch {
    return null;
  }
}

function details(app, e) {
  const { t, lang } = app;
  const st = app.status;
  const euro = (u) => (st ? ` (≈ ${formatEuro(euroCents(u, st.ipc.value, st.base_ipc.value), lang)})` : "");
  const date = new Date(e.deadline * 1000).toLocaleDateString(lang === "fr" ? "fr-FR" : "en-GB", { day: "numeric", month: "long", year: "numeric", timeZone: "UTC" });
  return h("dl", { class: "terms" },
    h("dt", {}, t("escrow.amount")), h("dd", { class: "strong" }, `${formatETL(e.amount, lang)} ETL${euro(e.amount)}`),
    h("dt", {}, t("escrow.seller")), h("dd", { class: "mono small" }, e.seller),
    h("dt", {}, t("escrow.buyer")), h("dd", { class: "mono small" }, e.buyer),
    h("dt", {}, t("escrow.arbiter")), h("dd", { class: e.arbiter ? "mono small" : "" }, e.arbiter || t("escrow.noArbiter")),
    e.arbiter ? [h("dt", {}, t("escrow.arbiterFee")), h("dd", {}, `${formatETL(e.arbiterFee, lang)} ETL`)] : null,
    h("dt", {}, t("escrow.deadline")), h("dd", {}, date),
  );
}

function badges(app, e) {
  const { t } = app;
  return h("p", { class: "badges" },
    h("span", { class: `badge ${e.outcome}` }, t(`escrow.state.${e.outcome}`)),
    e.paid ? h("span", { class: "badge" }, t("escrow.badge.paid")) : null,
    e.disputed ? h("span", { class: "badge alert" }, t("escrow.badge.disputed")) : null,
  );
}

// whoList : « l'arbitre », « l'acheteur et l'arbitre »…
function whoList(app, mask) {
  return signerRoles(mask).map((r) => app.t(`escrow.who.${r}`)).join(app.lang === "fr" ? " et " : " and ");
}

export function renderEscrow(app) {
  const root = h("div", { class: "escrow" });
  if (!app.session.isUnlocked()) {
    put(root, h("section", { class: "card narrow" }, h("p", {}, app.t("escrow.locked")), h("a", { href: "#/wallet" }, app.t("nav.wallet"))));
    return root;
  }
  const me = app.session.address();
  const list = h("div", { class: "escrow-list" }, h("p", { class: "muted" }, "…"));
  const load = async () => {
    try {
      const account = await app.api.account(me);
      const items = await Promise.all(account.escrows.map((id) => app.api.escrow(id)));
      clear(list);
      if (!items.length) put(list, h("p", { class: "muted" }, app.t("escrow.empty")));
      for (const e of items.reverse()) put(list, escrowCard(app, e, me, load));
    } catch (e) {
      put(clear(list), msg("error", errorText(app, e)));
    }
  };
  app.onBlock = load;
  load();
  put(root, 
    h("section", { class: "card wide" }, h("h2", {}, app.t("escrow.title")), list),
    openCard(app, me),
    receiveCard(app, me, load),
  );
  return root;
}

function escrowCard(app, e, me, reload) {
  const { t } = app;
  const role = roleOf(e, me);
  const out = h("div");
  const other = role === esc.ROLE.seller ? e.buyer : e.seller;
  const buttons = actions(e, role).map((a) =>
    h("button", { class: a.alone && (a.kind === "paid" || a.kind === "release") ? "primary" : a.kind === "dispute" ? "danger" : "",
      onClick: () => confirmAction(app, e, role, a, clear(out), reload) },
    a.alone ? t(`escrow.kind.${a.kind}`) : t("escrow.prepare", { action: t(`escrow.kind.${a.kind}`), who: whoList(app, a.with) })),
  );
  const card = h("article", { class: "escrow-item" },
    h("header", {}, h("strong", {}, t(`escrow.role.${ROLE_KEY[role]}`)), badges(app, e)),
    details(app, e),
    h("p", { class: "mono small muted" }, e.id),
    buttons.length ? h("div", { class: "actions" }, buttons) : null,
    out,
  );
  repLine(app, t(`escrow.${role === esc.ROLE.seller ? "buyer" : "seller"}`), other).then((r) => r && card.insertBefore(r, card.children[2]));
  return card;
}

async function confirmAction(app, e, role, a, out, reload) {
  const { t } = app;
  const st = app.status;
  const params = { chainID: st.chain_id, hrp: st.hrp };
  put(out, 
    msg("warning", t("escrow.confirm", { action: t(`escrow.kind.${a.kind}`) })),
    e.arbiter ? await repLine(app, t("escrow.arbiter"), e.arbiter) : null,
    h("div", { class: "actions" },
      h("button", { class: "primary", onClick: async () => {
        app.session.touch();
        const id = Uint8Array.from(e.id.match(/../g).map((x) => parseInt(x, 16)));
        const op = esc.act(app.session.key, KIND_OF[a.kind], id, e.seq, role, params);
        if (!a.alone) {
          put(clear(out), textBox(app, esc.toText(op), "escrow.text.help", { who: whoList(app, a.with) }));
          return;
        }
        try {
          await app.api.submitEscrow(esc.toText(op));
          put(clear(out), msg("ok", t("escrow.submitted")));
        } catch (err) {
          put(clear(out), msg("error", errorText(app, err)));
        }
      } }, t("escrow.sign")),
      h("button", { onClick: () => clear(out) }, t("send.cancel")),
    ),
  );
}

function openCard(app, me) {
  const { t, lang } = app;
  const buyer = addressInput(app);
  const arbiter = addressInput(app);
  const amount = h("input", { type: "text", inputmode: "decimal", autocomplete: "off", placeholder: "0,00" });
  const fee = h("input", { type: "text", inputmode: "decimal", autocomplete: "off", value: "0" });
  const days = h("input", { type: "number", min: "1", max: "30", step: "1", value: "3" });
  const out = h("div");

  const review = async () => {
    clear(out);
    const st = app.status;
    const p = await app.api.params();
    const params = { chainID: st.chain_id, hrp: st.hrp, feeBps: p.feeBps, minFee: p.minFee };
        let terms;
    for (const a of [buyer.value.trim(), arbiter.value.trim()].filter(Boolean)) {
      try {
        keys.parseAddress(a, st.hrp);
      } catch {
        return put(out, msg("error", t("send.badAddress")));
      }
    }
    try {
      const arb = arbiter.value.trim() || null;
      if (arb && (arb === me || arb === buyer.value.trim())) return put(out, msg("error", t("open.badArbiter")));
      if (!/^\d+$/.test(days.value) || Number(days.value) < 1 || Number(days.value) > 30) return put(out, msg("error", t("open.badDays")));
      terms = esc.newTerms({ seller: me, buyer: buyer.value.trim(), arbiter: arb, amount: parseETL(amount.value),
        arbiterFee: arb ? parseETL(fee.value || "0") : 0n, days: Number(days.value), timestamp: BigInt(Math.floor(Date.now() / 1000)) }, params);
        } catch {
      return put(out, msg("error", t("send.badAmount")));
    }
    const locked = terms.amount + terms.arbiterFee + terms.fee;
    const account = await app.api.account(me);
    if (account.balance < locked) return put(out, msg("error", t("send.insufficient")));
    const reps = await Promise.all([repLine(app, t("escrow.buyer"), buyer.value.trim()), terms.arbiter.some((b) => b) ? repLine(app, t("escrow.arbiter"), arbiter.value.trim()) : null]);
    const simple = !terms.arbiter.some((b) => b);
    put(out, 
      h("div", { class: "summary" },
        h("h4", {}, t("send.summary")),
        h("dl", {},
          h("dt", {}, t("escrow.amount")), h("dd", {}, `${formatETL(terms.amount, lang)} ETL`),
          h("dt", {}, t("send.fee")), h("dd", {}, `${formatETL(terms.fee, lang)} ETL`),
          simple ? null : [h("dt", {}, t("escrow.arbiterFee")), h("dd", {}, `${formatETL(terms.arbiterFee, lang)} ETL`)],
          h("dt", {}, t("open.locked")), h("dd", { class: "strong" }, `${formatETL(locked, lang)} ETL`),
                    h("dt", {}, t("escrow.deadline")), h("dd", {}, t("escrow.days", { n: terms.days })),
        ),
        reps,
        simple ? msg("warning", t("open.simple")) : null,
        h("div", { class: "actions" },
          h("button", { class: "primary", onClick: async () => {
            app.session.touch();
            const op = esc.open(app.session.key, terms, params);
            if (!simple) {
              put(clear(out), textBox(app, esc.toText(op), "open.toArbiter", {}));
              return;
            }
            try {
              await app.api.submitEscrow(esc.toText(op));
              put(clear(out), msg("ok", t("escrow.submitted")));
            } catch (err) {
              put(clear(out), msg("error", errorText(app, err)));
            }
          } }, t("escrow.sign")),
          h("button", { onClick: () => clear(out) }, t("send.cancel")),
        ),
      ),
    );
  };

  return h("section", { class: "card wide" },
    h("h2", {}, t("open.title")),
    h("form", { class: "cols", onSubmit: (e) => (e.preventDefault(), review()) },
      field(t("open.buyer"), buyer),
      field(t("open.arbiter"), arbiter),
      field(t("send.amount"), amount),
      field(t("open.arbiterFee"), fee),
      field(t("open.days"), days),
      h("div", { class: "actions" }, h("button", { class: "primary", type: "submit" }, t("send.review"))),
    ),
    out,
  );
}

function receiveCard(app, me, reload) {
  const { t } = app;
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
      return put(out, msg("error", t("receive.invalid")));
    }
    const role = roleOf(e, me);
    if (!role || !completes(e, op, role) || op.seq !== e.seq) return put(out, msg("error", t("receive.cannot")));
    const st = app.status;
    const params = { chainID: st.chain_id, hrp: st.hrp };
    put(out, 
      badges(app, e),
      details(app, e),
      h("p", {}, t("receive.signedBy", { who: whoList(app, op.signers) })),
      msg("warning", t("escrow.confirm", { action: t(`escrow.kind.${NAME_OF[op.kind]}`) })),
      e.arbiter ? await repLine(app, t("escrow.arbiter"), e.arbiter) : null,
      h("div", { class: "actions" },
        h("button", { class: "primary", onClick: async () => {
          app.session.touch();
          try {
            const full = esc.addSignature(app.session.key, op, role, op.escrow, escrowState(e, st.hrp), params);
            await app.api.submitEscrow(esc.toText(full));
            put(clear(out), msg("ok", t("escrow.submitted")));
            text.value = "";
          } catch (err) {
            put(clear(out), msg("error", errorText(app, err)));
          }
        } }, t("escrow.sign")),
      ),
    );
  };
  return h("section", { class: "card wide" },
    h("h2", {}, t("receive.title")),
    h("form", { onSubmit: (e) => (e.preventDefault(), read()) },
      field(t("receive.label"), text),
      h("div", { class: "actions" }, h("button", { class: "primary", type: "submit" }, t("receive.read"))),
    ),
    out,
  );
}



