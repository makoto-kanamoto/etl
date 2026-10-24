// Portefeuille : création en 3 étapes (phrase, vérification de 3 mots, phrase de passe), restauration,
// déverrouillage (ou « Utiliser mes 24 mots »), solde, envoi en 3 étapes, réception (QR code),
// contacts chiffrés, activité, affichage de la phrase, retrait du navigateur.

import { h, field, clear, put, addressInput } from "../lib/dom.js";
import { parseETL, formatETL, euroCents, formatEuro } from "../lib/amount.js";
import { localeOf } from "../lib/strings.js";
import { challenge, check } from "../lib/phrase.js";
import { identiconSvg } from "../lib/identicon.js";
import { qrSvg } from "../lib/qr.js";
import { icon } from "../lib/icons.js";
import { short, group, message, actionRow as actions, steps, who } from "../lib/ui.js";
import { feedRows, renderFeed } from "../lib/feed.js";

export { short, group };

const head = (app, title) => h("div", { class: "page-head" }, h("div", {}, h("span", { class: "label" }, app.t("nav.wallet")), h("h1", {}, title)));
import * as keys from "../../wallet/keys.js";
import * as mnemonic from "../../wallet/mnemonic.js";
import * as txs from "../../wallet/tx.js";
import { MIN_PASSPHRASE, VaultError } from "../../wallet/vault.js";

const hex = (b) => Array.from(b, (x) => x.toString(16).padStart(2, "0")).join("");
const locale = localeOf;


// lockedInEscrows : ETL du vendeur retenus par ses échanges en cours (montant + rémunération de
// l'arbitre ; les frais sont payés à l'ouverture).
export function lockedInEscrows(escrows, me) {
  return escrows.filter((e) => e.seller === me && e.outcome === "pending").reduce((n, e) => n + e.amount + e.arbiterFee, 0n);
}

// timesPaid : nombre d'envois de me vers to dans l'historique disponible (blocs conservés).
export function timesPaid(hist, me, to) {
  return hist ? hist.transactions.filter((tx) => tx.from === me && tx.to === to).length : 0;
}

export function renderWallet(app) {
  const root = h("div", { class: "page wallet" });
  const s = app.session;
  if (!s.hasVault()) noWallet(app, root);
  else if (!s.isUnlocked()) locked(app, root);
  else unlocked(app, root);
  return root;
}


// who : motif de l'adresse, nom du contact (ou adresse abrégée), ligne secondaire.
// paidText : « aucun envoi », « 1 envoi » ou « n envois » vers cette adresse dans les blocs récents.
export function paidText(t, n) {
  return t(n === 0 ? "send.valid0" : n === 1 ? "send.valid1" : "send.validN", { n });
}

function noWallet(app, root) {
  const { t } = app;
  put(root, head(app, t("wallet.none.title")),
    h("section", { class: "panel narrow" },
      h("p", {}, t("wallet.none.text")),
      actions(
        h("button", { class: "primary", onClick: () => createFlow(app, root) }, t("wallet.create")),
        h("button", { onClick: () => restoreFlow(app, root) }, t("wallet.restore")),
      ),
    ),
  );
}

// --- création : phrase → vérification → phrase de passe ---

function createSteps(app, n) {
  const { t } = app;
  return steps([t("create.step.words"), t("create.step.check"), t("create.step.pass")], n);
}

function createFlow(app, root, key = keys.generateKey()) {
  const { t, lang } = app;
  const words = mnemonic.encodePhrase(key, lang === "fr" ? mnemonic.FRENCH : mnemonic.ENGLISH);
  const noted = h("input", { type: "checkbox", id: "noted" });
  const next = h("button", { class: "primary", disabled: true, onClick: () => verifyFlow(app, root, key, words) }, t("create.next"));
  noted.addEventListener("change", () => (next.disabled = !noted.checked));
  put(clear(root), head(app, t("wallet.create")),
    h("section", { class: "panel" },
      createSteps(app, 0),
      h("h2", {}, t("create.title")),
      h("p", { class: "muted" }, t("create.text")),
      h("ol", { class: "phrase" }, words.map((w) => h("li", {}, w))),
      h("p", { class: "msg warning" }, t("create.warning")),
      h("label", { class: "check", for: "noted" }, noted, t("create.noted")),
      actions(next, h("button", { class: "link", type: "button", onClick: () => (keys.wipe(key), restoreFlow(app, root)) }, t("create.restoreInstead"))),
    ),
  );
}

function verifyFlow(app, root, key, words) {
  const { t } = app;
  const positions = challenge();
  const inputs = positions.map(() => h("input", { type: "text", autocomplete: "off", spellcheck: "false", autocapitalize: "off" }));
  const out = h("div");
  const submit = () => {
    if (check(words, positions, inputs.map((i) => i.value))) passphraseFlow(app, root, key, createSteps(app, 2));
    else put(clear(out), message("error", t("create.check.failed")));
  };
  put(clear(root), head(app, t("wallet.create")),
    h("section", { class: "panel" },
      createSteps(app, 1),
      h("h2", {}, t("create.check.title")),
      h("p", { class: "muted" }, t("create.check.text")),
      h("form", { onSubmit: (e) => (e.preventDefault(), submit()) },
        positions.map((p, i) => field(t("create.check.word", { n: p + 1 }), inputs[i])),
        out,
        actions(
          h("button", { class: "primary", type: "submit" }, t("create.check.title")),
          h("button", { type: "button", onClick: () => createFlow(app, root, key) }, t("create.back")),
        ),
      ),
    ),
  );
  queueMicrotask(() => inputs[0].focus());
}

// passphraseFlow : dernière étape (création, restauration, remplacement). save(key, pass) enregistre.
function passphraseFlow(app, root, key, stepsEl, save = (k, p) => app.session.create(k, p), title = app.t("wallet.create")) {
  const { t } = app;
  const p1 = h("input", { type: "password", autocomplete: "new-password", minlength: MIN_PASSPHRASE });
  const p2 = h("input", { type: "password", autocomplete: "new-password" });
  const out = h("div");
  const submit = async () => {
    if ([...p1.value.normalize("NFC")].length < MIN_PASSPHRASE) return put(clear(out), message("error", t("pass.short")));
    if (p1.value.normalize("NFC") !== p2.value.normalize("NFC")) return put(clear(out), message("error", t("pass.mismatch")));
    await save(key, p1.value);
    keys.wipe(key);
    app.render();
  };
  put(clear(root), head(app, title),
    h("section", { class: "panel" },
      stepsEl,
      h("h2", {}, t("pass.title")),
      h("p", { class: "muted" }, t("pass.text")),
      h("form", { onSubmit: (e) => (e.preventDefault(), submit()) },
        field(t("pass.label"), p1),
        field(t("pass.confirm"), p2),
        out,
        actions(h("button", { class: "primary", type: "submit" }, t("pass.save"))),
      ),
    ),
  );
  queueMicrotask(() => p1.focus());
}

// --- restauration (aucun portefeuille dans ce navigateur) ---

function phraseInput(app) {
  return h("textarea", { rows: 4, autocomplete: "off", spellcheck: "false", autocapitalize: "off" });
}

function decode(app, phrase, out) {
  try {
    const { key } = mnemonic.decodePhrase(phrase.value);
    phrase.value = "";
    return key;
  } catch (e) {
    put(clear(out), message("error", app.t(`restore.invalid.${e.code ?? "checksum"}`)));
    return null;
  }
}

function restoreFlow(app, root) {
  const { t } = app;
  const phrase = phraseInput(app);
  const out = h("div");
  const stepsOf = (n) => steps([t("restore.step.words"), t("create.step.pass")], n);
  const next = () => {
    const key = decode(app, phrase, out);
    if (key) passphraseFlow(app, root, key, stepsOf(1), undefined, t("restore.title"));
  };
  put(clear(root), head(app, t("restore.title")),
    h("section", { class: "panel" },
      stepsOf(0),
      h("p", { class: "muted" }, t("restore.text")),
      h("form", { onSubmit: (e) => (e.preventDefault(), next()) },
        field(t("restore.label"), phrase),
        out,
        actions(h("button", { class: "primary", type: "submit" }, t("restore.next")), h("button", { type: "button", onClick: () => app.render() }, t("send.cancel"))),
      ),
    ),
  );
  queueMicrotask(() => phrase.focus());
}

// --- verrouillé ---

function locked(app, root) {
  const { t } = app;
  const pass = h("input", { type: "password", autocomplete: "current-password" });
  const out = h("div");
  if (app.autoLocked) put(out, message("info", t("unlock.auto")));
  const unlock = async () => {
    try {
      await app.session.unlock(pass.value);
      app.autoLocked = false;
      app.render();
    } catch (e) {
      put(clear(out), message("error", e instanceof VaultError && e.code === "wrong" ? t("unlock.wrong") : e.message));
    }
  };
  put(root, head(app, t("unlock.title")),
    h("section", { class: "panel lockcard" },
      h("h2", {}, t("unlock.heading")),
      who(app, app.session.address()),
      h("form", { onSubmit: (e) => (e.preventDefault(), unlock()) },
        field(t("pass.label"), pass),
        out,
        actions(h("button", { class: "primary", type: "submit" }, t("unlock.button"))),
      ),
      h("div", { class: "alt" },
        h("button", { class: "link", type: "button", onClick: () => replaceFlow(app, root) }, t("unlock.useWords")),
        h("p", { class: "fine" }, t("unlock.useWordsNote"))),
    ),
  );
  queueMicrotask(() => pass.focus());
}

// --- « Utiliser mes 24 mots » : remplace le coffre de ce navigateur ---

function replaceFlow(app, root) {
  const { t } = app;
  const current = app.session.address();
  const phrase = phraseInput(app);
  const out = h("div");
  const firstActions = actions(h("button", { class: "primary", type: "submit" }, t("restore.next")), h("button", { type: "button", onClick: () => app.render() }, t("replace.cancel")));
  const stepsOf = (n) => steps([t("restore.step.words"), t("create.step.pass")], n);
  const toPass = (key) => passphraseFlow(app, root, key, stepsOf(1), (k, p) => app.session.replace(k, p), t("replace.title"));
  const next = () => {
    const key = decode(app, phrase, out);
    if (!key) return;
    const addr = keys.address(key, app.session.hrp);
    if (addr === current) return toPass(key);
    // Autre adresse : le portefeuille de ce navigateur sera perdu sans SES 24 mots → confirmation tapée.
    const word = t("replace.word");
    const input = h("input", { type: "text", autocomplete: "off" });
    const go = h("button", { class: "danger", type: "button", disabled: true, onClick: () => toPass(key) }, t("replace.confirm"));
    input.addEventListener("input", () => (go.disabled = input.value.trim() !== word));
    put(clear(out),
      h("div", { class: "msg warning" },
        h("p", {}, t("replace.other", { word })),
        h("dl", { class: "pair" },
          h("dt", {}, t("replace.current")), h("dd", {}, who(app, current, false, false, true)),
          h("dt", {}, t("replace.new")), h("dd", {}, who(app, addr, false, false, true)))),
      field(word, input),
      actions(go, h("button", { type: "button", onClick: () => (keys.wipe(key), app.render()) }, t("replace.cancel"))));
    firstActions.hidden = true;
  };
  put(clear(root), head(app, t("replace.title")),
    h("section", { class: "panel" },
      stepsOf(0),
      h("p", { class: "msg info" }, t("replace.text")),
      h("dl", { class: "pair" }, h("dt", {}, t("replace.current")), h("dd", {}, who(app, current, false, false, true))),
      h("form", { onSubmit: (e) => (e.preventDefault(), next()) },
        field(t("restore.label"), phrase),
        out,
        firstActions,
      ),
    ),
  );
  queueMicrotask(() => phrase.focus());
}

// --- déverrouillé ---

function unlocked(app, root) {
  const { t, lang, api } = app;
  const address = app.session.address();
  const ctx = { account: null, hist: null, params: null };
  const balanceEl = h("span", { class: "big" }, "…");
  const euroEl = h("span", { class: "muted" });
  const lockedEl = h("span", { class: "muted" });
  const feed = h("div", { class: "feed" }, h("p", { class: "muted" }, "…"));
  // Historique complet (nœud avec index, site public) : pages suivantes à la demande.
  const older = h("button", { class: "older", type: "button", hidden: true, onClick: () => more() }, t("history.older"));
  const side = h("section", { class: "panel" });
  const euro = (u) => (app.status ? formatEuro(euroCents(u, app.status.ipc.value, app.status.base_ipc.value), lang) : "");
  app.walletAct ??= "send";

  const showSide = () => {
    clear(side);
    if (app.walletAct === "receive") receivePanel(app, side, address);
    else if (app.walletAct === "contacts") contactsPanel(app, side, rerender);
    else sendPanel(app, side, address, ctx, refresh);
  };
  const actButtons = [["send", "act.send"], ["receive", "act.receive"], ["contacts", "act.contacts"]].map(([k, key]) =>
    h("button", { class: "act", type: "button", "aria-pressed": String(app.walletAct === k), onClick: () => {
      app.walletAct = k;
      for (const b of actButtons) b.setAttribute("aria-pressed", String(b.dataset.act === k));
      showSide();
    }, "data-act": k }, t(key)));

  const addContact = (peer) => ((app.walletAct = "contacts"), (app.contactDraft = peer), app.render());
  const showFeed = () => {
    if (!ctx.hist) return;
    renderFeed(app, feed, feedRows(ctx.hist, address), { addContact: app.book ? addContact : null });
    older.hidden = ctx.hist.nextBefore === null;
  };
  const rerender = () => {
    showFeed();
    showSide();
  };
  async function more() {
    if (ctx.hist?.nextBefore === null || ctx.hist?.nextBefore === undefined) return; // aucune page suivante
    older.disabled = true;
    try {
      const page = await api.history(address, ctx.hist.nextBefore);
      for (const k of ["transactions", "escrow", "rewards"]) ctx.hist[k].push(...page[k]);
      ctx.hist.nextBefore = page.nextBefore;
      showFeed();
    } catch {
      /* réessai au prochain clic */
    } finally {
      older.disabled = false;
    }
  }

  async function refresh() {
    try {
      ctx.account = await api.account(address);
      balanceEl.textContent = formatETL(ctx.account.balance, lang);
      euroEl.textContent = `≈ ${euro(ctx.account.balance)}`;
      ctx.hist = await api.history(address); // première page : un nouveau bloc la remplace
      showFeed();
      const escrows = await Promise.all(ctx.account.escrows.map((id) => api.escrow(id)));
      const held = lockedInEscrows(escrows, address);
      lockedEl.textContent = held ? t("main.held", { amount: formatETL(held, lang) }) : "";
    } catch {
      balanceEl.textContent = "—";
    }
  }
  app.onBlock = refresh;

  put(root, head(app, t("main.title")),
    h("div", { class: "split" },
      h("div", { class: "stack" },
        h("section", { class: "panel" },
          h("div", { class: "balance" }, h("span", { class: "label" }, t("main.balance")), h("p", { class: "amount" }, balanceEl, h("span", { class: "unit" }, "ETL")), euroEl, lockedEl),
          h("div", { class: "acts" }, actButtons, h("a", { class: "act", href: "#/escrow" }, t("act.escrow"))),
          app.status?.public ? h("p", { class: "callout info" }, icon("info"), h("span", {}, t("wallet.webWarning"))) : null),
        h("section", { class: "panel" }, h("h2", {}, t("activity.title")), feed, older,
          app.status?.public ? null : h("p", { class: "fine" }, t("history.note")))),
      side),
    h("section", { class: "panel" },
      actions(
        h("button", { onClick: () => (app.session.lock(), app.render()) }, t("main.lock")),
        h("button", { onClick: () => phraseDialog(app, root) }, t("main.showPhrase")),
        h("button", { class: "danger", onClick: () => forgetDialog(app, root) }, t("main.forget")),
      ),
    ),
  );
  // Contacts déchiffrés une fois par session, puis données du nœud.
  app.session.contacts().then((book) => (app.book = book), () => (app.book = null)).then(() => {
    showSide();
    refresh();
  });
}

// afterBalance : solde après l'envoi, ou « solde insuffisant » (jamais un montant négatif).
export function afterBalance(app, balance, debit) {
  return balance >= debit ? h("dd", {}, `${formatETL(balance - debit, app.lang)} ETL`) : h("dd", { class: "short" }, app.t("send.insufficient"));
}

// --- envoi en 3 étapes : saisir → vérifier → envoyé ---

function sendPanel(app, panel, address, ctx, refresh) {
  const { t, lang, api } = app;
  const st = app.status;
  const stepsOf = (n) => steps([t("send.step.input"), t("send.step.check"), t("send.step.sent")], n);
  const to = addressInput(app);
  const amount = h("input", { type: "text", inputmode: "decimal", autocomplete: "off", placeholder: "0,00", class: "amount-input" });
  const whoEl = h("div");
  const euroEl = h("p", { class: "help" });
  const recap = h("dl", { class: "recap" });
  const out = h("div");
  const euro = (u) => formatEuro(euroCents(u, st.ipc.value, st.base_ipc.value), lang);
  const E = (u) => `${formatETL(u, lang)} ETL`;

  const params = async () => {
    if (!ctx.params) {
      const p = await api.params();
      ctx.params = { chainID: st.chain_id, hrp: st.hrp, feeBps: p.feeBps, minFee: p.minFee };
    }
    return ctx.params;
  };
  const target = () => {
    try {
      return keys.encodeAddress(keys.parseAddress(to.value.trim(), st.hrp), st.hrp);
    } catch {
      return null;
    }
  };
  const units = () => {
    try {
      return parseETL(amount.value);
    } catch {
      return null;
    }
  };
  let seq = 0; // seul le calcul le plus récent du récapitulatif s'affiche (frappes rapprochées)
  const update = async () => {
    const mine = ++seq;
    const a = target();
    clear(whoEl);
    if (a && a !== address) put(whoEl, who(app, a, paidText(t, timesPaid(ctx.hist, address, a))));
    else if (to.value.trim() && !a && to.value.trim().length >= 40) put(whoEl, message("error", t("send.badAddress")));
    const u = units();
    euroEl.textContent = u ? t("send.euro", { euro: euro(u) }) : "";
    if (!u) return clear(recap);
    const p = await params();
    if (mine !== seq) return;
    const fee = txs.requiredFee(u, p);
    put(clear(recap),
      h("dt", {}, t("send.amount.label")), h("dd", {}, E(u)),
      h("dt", {}, t("send.feeRule", { pct: (Number(p.feeBps) / 100).toLocaleString(locale(lang)), min: formatETL(p.minFee, lang) })), h("dd", {}, E(fee)),
      h("dt", { class: "tot" }, t("send.total")), h("dd", { class: "tot" }, E(u + fee)),
      ctx.account ? [h("dt", {}, t("send.after")), afterBalance(app, ctx.account.balance, u + fee)] : null);
  };
  to.addEventListener("input", update);
  amount.addEventListener("input", update);

  const chips = (app.book?.list() ?? []).slice(0, 8).map((c) =>
    h("button", { class: "contact", type: "button", onClick: () => ((to.value = c.address), update()) }, identiconSvg(c.address), c.name));

  const review = async () => {
    clear(out);
    const a = target();
    if (!a) return put(out, message("error", t("send.badAddress")));
    if (a === address) return put(out, message("error", t("send.self")));
    const u = units();
    if (u === null) return put(out, message("error", t("send.badAmount")));
    if (u === 0n) return put(out, message("error", t("send.zero")));
    const p = await params();
    const fee = txs.requiredFee(u, p);
    const account = ctx.account ?? (await api.account(address));
    if (account.balance < u + fee) return put(out, message("error", t("send.insufficient")));
    verifyStep(a, u, fee, p, account);
  };

  const blockTime = () => new Date(st.next_block * 1000).toLocaleString(locale(lang), { weekday: "long", hour: "2-digit", minute: "2-digit" });

  const verifyStep = (a, u, fee, p, account) => {
    const first = timesPaid(ctx.hist, address, a) === 0;
    const err = h("div");
    const sign = async () => {
      app.session.touch();
      const tx = txs.transfer(app.session.key, { to: a, amount: u, nonce: account.nextNonce, timestamp: BigInt(Math.floor(Date.now() / 1000)) }, p);
      try {
        await api.submitTx(hex(txs.encodeTransaction(tx)));
        sentStep(a, tx);
        refresh();
      } catch (e) {
        put(clear(err), message("error", e.message));
      }
    };
    put(clear(panel), stepsOf(1),
      h("h2", {}, t("send.check.title")),
      who(app, a, false, true),
      h("p", { class: "label" }, t("send.fullAddress")),
      h("p", { class: "addr grouped" }, group(a)),
      first ? message("warning", t("send.newAddress")) : null,
      h("dl", { class: "recap" },
        h("dt", {}, t("send.amount.label")), h("dd", {}, `${E(u)} (≈ ${euro(u)})`),
        h("dt", {}, t("send.fee")), h("dd", {}, E(fee)),
        h("dt", { class: "tot" }, t("send.total")), h("dd", { class: "tot" }, E(u + fee)),
        h("dt", {}, t("send.after")), h("dd", {}, E(account.balance - u - fee))),
      h("p", { class: "msg info" }, t("send.signNote", { time: blockTime() })),
      err,
      actions(h("button", { class: "primary", onClick: sign }, t("send.confirm")), h("button", { onClick: () => sendPanel(app, clear(panel), address, ctx, refresh) }, t("send.edit"))));
  };

  const sentStep = (a, tx) => {
    const known = app.book?.name(a);
    put(clear(panel), stepsOf(2),
      h("h2", {}, t("send.done.title")),
      message("ok", t("send.sent", { time: blockTime() })),
      h("p", { class: "fine mono" }, hex(tx.id)),
      actions(
        h("button", { class: "primary", onClick: () => sendPanel(app, clear(panel), address, ctx, refresh) }, t("send.new")),
        !known && app.book ? h("button", { onClick: () => ((app.walletAct = "contacts"), (app.contactDraft = a), app.render()) }, t("contacts.add")) : null));
  };

  put(panel, stepsOf(0),
    h("h2", {}, t("send.title")),
    chips.length ? h("div", { class: "contacts" }, chips) : null,
    h("form", { onSubmit: (e) => (e.preventDefault(), review()) },
      field(t("send.to"), to), whoEl,
      field(t("send.amount"), amount), euroEl,
      recap,
      out,
      actions(h("button", { class: "primary", type: "submit" }, t("send.check"))),
    ));
}

// --- réception ---

function receivePanel(app, panel, address) {
  const { t } = app;
  const copy = h("button", { class: "primary", type: "button", onClick: async () => {
    try {
      await navigator.clipboard.writeText(address);
      copy.textContent = t("main.copied");
      setTimeout(() => (copy.textContent = t("recv.copy")), 2000);
    } catch { /* presse-papiers refusé : l'adresse reste sélectionnable */ }
  } }, t("recv.copy"));
  put(panel,
    h("h2", {}, t("recv.title")),
    h("p", { class: "muted" }, t("recv.text")),
    h("div", { class: "qr-box" }, qrSvg(address, t("recv.qr"))),
    who(app, address, false),
    h("p", { class: "addr grouped" }, group(address)),
    actions(copy));
}

// --- contacts ---

function contactsPanel(app, panel, rerender) {
  const { t } = app;
  const book = app.book;
  const out = h("div");
  const fail = (e) => put(clear(out), message("error", t(`contacts.err.${e.code ?? "address"}`)));
  if (!book) return put(panel, h("h2", {}, t("contacts.title")), message("error", t("contacts.locked")));
  const addr = addressInput(app);
  addr.value = app.contactDraft ?? "";
  app.contactDraft = null;
  const name = h("input", { type: "text", autocomplete: "off", maxlength: 60 });
  const add = async () => {
    try {
      await book.set(addr.value, name.value);
      addr.value = name.value = "";
      clear(out);
      rerender();
    } catch (e) {
      fail(e);
    }
  };
  const row = (c) => {
    const li = h("li", {});
    const view = () => put(clear(li),
      who(app, c.address),
      h("div", { class: "row-actions" },
        h("button", { class: "small", type: "button", onClick: edit }, t("contacts.rename")),
        h("button", { class: "small danger", type: "button", onClick: confirmDelete }, t("contacts.delete"))));
    const edit = () => {
      const input = h("input", { type: "text", value: c.name, autocomplete: "off", maxlength: 60, "aria-label": t("contacts.name") });
      put(clear(li), h("form", { class: "inline", onSubmit: async (e) => {
        e.preventDefault();
        try {
          await book.set(c.address, input.value);
          rerender();
        } catch (err) {
          fail(err);
        }
      } }, identiconSvg(c.address), input, h("button", { class: "small primary", type: "submit" }, t("contacts.save")), h("button", { class: "small", type: "button", onClick: view }, t("send.cancel"))));
      queueMicrotask(() => input.focus());
    };
    const confirmDelete = () => put(clear(li), who(app, c.address),
      h("div", { class: "row-actions" },
        h("button", { class: "small danger", type: "button", onClick: async () => (await book.remove(c.address), rerender()) }, t("contacts.confirmDelete")),
        h("button", { class: "small", type: "button", onClick: view }, t("send.cancel"))));
    view();
    return li;
  };
  const list = book.list();
  put(panel,
    h("h2", {}, t("contacts.title")),
    h("p", { class: "muted" }, t("contacts.text")),
    book.unreadable ? message("warning", t("contacts.unreadable")) : null,
    list.length ? h("ul", { class: "contact-list" }, list.map(row)) : h("p", { class: "muted" }, t("contacts.empty")),
    h("form", { class: "add-contact", onSubmit: (e) => (e.preventDefault(), add()) },
      h("h3", { class: "label" }, t("contacts.add")),
      field(t("contacts.address"), addr),
      field(t("contacts.name"), name),
      out,
      actions(h("button", { class: "primary", type: "submit" }, t("contacts.save")))));
  if (addr.value) queueMicrotask(() => name.focus());
}

// --- phrase de récupération (après ressaisie de la phrase de passe) ---

function phraseDialog(app, root) {
  const { t, lang } = app;
  const pass = h("input", { type: "password", autocomplete: "current-password" });
  const out = h("div");
  const show = async () => {
    try {
      const key = await app.session.verify(pass.value);
      pass.value = "";
      const words = mnemonic.encodePhrase(key, lang === "fr" ? mnemonic.FRENCH : mnemonic.ENGLISH);
      keys.wipe(key);
      put(clear(out), h("p", { class: "msg warning" }, t("create.warning")), h("ol", { class: "phrase" }, words.map((w) => h("li", {}, w))));
    } catch {
      put(clear(out), message("error", t("unlock.wrong")));
    }
  };
  put(clear(root), head(app, t("phrase.title")),
    h("section", { class: "panel" },
      h("p", { class: "muted" }, t("phrase.text")),
      h("form", { onSubmit: (e) => (e.preventDefault(), show()) }, field(t("pass.label"), pass), actions(h("button", { class: "primary", type: "submit" }, t("phrase.show")))),
      out,
      actions(h("button", { onClick: () => app.render() }, t("phrase.hide"))),
    ),
  );
  queueMicrotask(() => pass.focus());
}

// --- retrait du navigateur (confirmation par un mot tapé) ---

function forgetDialog(app, root) {
  const { t } = app;
  const word = t("forget.word");
  const input = h("input", { type: "text", autocomplete: "off" });
  const button = h("button", { class: "danger", disabled: true, onClick: () => (app.session.forget(), (app.book = null), app.render()) }, t("forget.button"));
  input.addEventListener("input", () => (button.disabled = input.value.trim() !== word));
  put(clear(root), head(app, t("forget.title")),
    h("section", { class: "panel" },
      h("p", { class: "msg warning" }, t("forget.text", { word })),
      field(word, input),
      actions(button, h("button", { onClick: () => app.render() }, t("send.cancel"))),
    ),
  );
}
