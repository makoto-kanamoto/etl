// Portefeuille : création (phrase, vérification de 3 mots, phrase de passe), restauration,
// déverrouillage, solde, envoi, historique, affichage de la phrase, retrait du navigateur.

import { h, field, clear, put } from "../lib/dom.js";
import { parseETL, formatETL, euroCents, formatEuro } from "../lib/amount.js";
import { challenge, check } from "../lib/phrase.js";
import * as keys from "../../wallet/keys.js";
import * as mnemonic from "../../wallet/mnemonic.js";
import * as txs from "../../wallet/tx.js";
import { MIN_PASSPHRASE, VaultError } from "../../wallet/vault.js";

const hex = (b) => Array.from(b, (x) => x.toString(16).padStart(2, "0")).join("");

export function renderWallet(app) {
  const root = h("div", { class: "wallet" });
  const s = app.session;
  if (!s.hasVault()) noWallet(app, root);
  else if (!s.isUnlocked()) locked(app, root);
  else unlocked(app, root);
  return root;
}

function actions(...buttons) {
  return h("div", { class: "actions" }, ...buttons);
}

function message(kind, text) {
  return h("p", { class: `msg ${kind}`, role: kind === "error" ? "alert" : "status" }, text);
}

function noWallet(app, root) {
  const { t } = app;
  put(root, 
    h("section", { class: "card" },
      h("h2", {}, t("wallet.none.title")),
      h("p", {}, t("wallet.none.text")),
      actions(
        h("button", { class: "primary", onClick: () => createFlow(app, clear(root)) }, t("wallet.create")),
        h("button", { onClick: () => restoreFlow(app, clear(root)) }, t("wallet.restore")),
      ),
    ),
  );
}

// --- création : phrase → vérification → phrase de passe ---

function createFlow(app, root, key = keys.generateKey()) {
  const { t, lang } = app;
  const words = mnemonic.encodePhrase(key, lang === "fr" ? mnemonic.FRENCH : mnemonic.ENGLISH);
  const noted = h("input", { type: "checkbox" });
  const next = h("button", { class: "primary", disabled: true, onClick: () => verifyFlow(app, clear(root), key, words) }, t("create.check.title"));
  noted.addEventListener("change", () => (next.disabled = !noted.checked));
  put(root, 
    h("section", { class: "card" },
      h("h2", {}, t("create.title")),
      h("p", {}, t("create.text")),
      h("p", { class: "msg warning" }, t("create.warning")),
      h("ol", { class: "phrase" }, words.map((w) => h("li", {}, w))),
      h("label", { class: "check" }, noted, " ", t("create.noted")),
      actions(next),
    ),
  );
}

function verifyFlow(app, root, key, words) {
  const { t } = app;
  const positions = challenge();
  const inputs = positions.map(() => h("input", { type: "text", autocomplete: "off", spellcheck: "false", autocapitalize: "off" }));
  const out = h("div");
  const submit = () => {
    if (check(words, positions, inputs.map((i) => i.value))) passphraseFlow(app, clear(root), key);
    else put(clear(out), message("error", t("create.check.failed")));
  };
  put(root, 
    h("section", { class: "card" },
      h("h2", {}, t("create.check.title")),
      h("p", {}, t("create.check.text")),
      h("form", { onSubmit: (e) => (e.preventDefault(), submit()) },
        positions.map((p, i) => field(t("create.check.word", { n: p + 1 }), inputs[i])),
        out,
        actions(
          h("button", { class: "primary", type: "submit" }, t("create.check.title")),
          h("button", { type: "button", onClick: () => createFlow(app, clear(root), key) }, t("create.back")),
        ),
      ),
    ),
  );
}

function passphraseFlow(app, root, key) {
  const { t } = app;
  const p1 = h("input", { type: "password", autocomplete: "new-password", minlength: MIN_PASSPHRASE });
  const p2 = h("input", { type: "password", autocomplete: "new-password" });
  const out = h("div");
  const save = async () => {
    if ([...p1.value.normalize("NFC")].length < MIN_PASSPHRASE) return put(clear(out), message("error", t("pass.short")));
    if (p1.value.normalize("NFC") !== p2.value.normalize("NFC")) return put(clear(out), message("error", t("pass.mismatch")));
    await app.session.create(key, p1.value);
    keys.wipe(key);
    app.render();
  };
  put(root, 
    h("section", { class: "card" },
      h("h2", {}, t("pass.title")),
      h("p", {}, t("pass.text")),
      h("form", { onSubmit: (e) => (e.preventDefault(), save()) },
        field(t("pass.label"), p1),
        field(t("pass.confirm"), p2),
        out,
        actions(h("button", { class: "primary", type: "submit" }, t("pass.save"))),
      ),
    ),
  );
}

// --- restauration ---

function restoreFlow(app, root) {
  const { t } = app;
  const phrase = h("textarea", { rows: 4, autocomplete: "off", spellcheck: "false", autocapitalize: "off" });
  const out = h("div");
  const next = () => {
    try {
      const { key } = mnemonic.decodePhrase(phrase.value);
      phrase.value = "";
      passphraseFlow(app, clear(root), key);
    } catch (e) {
      put(clear(out), message("error", t(`restore.invalid.${e.code ?? "checksum"}`)));
    }
  };
  put(root, 
    h("section", { class: "card" },
      h("h2", {}, t("restore.title")),
      h("p", {}, t("restore.text")),
      h("form", { onSubmit: (e) => (e.preventDefault(), next()) },
        field(t("restore.label"), phrase),
        out,
        actions(h("button", { class: "primary", type: "submit" }, t("restore.next"))),
      ),
    ),
  );
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
  put(root, 
    h("section", { class: "card narrow" },
      h("h2", {}, t("unlock.title")),
      h("p", { class: "mono small" }, app.session.address()),
      h("form", { onSubmit: (e) => (e.preventDefault(), unlock()) },
        field(t("pass.label"), pass),
        out,
        actions(h("button", { class: "primary", type: "submit" }, t("unlock.button"))),
      ),
    ),
  );
  queueMicrotask(() => pass.focus());
}

// --- déverrouillé ---

function unlocked(app, root) {
  const { t, lang, api } = app;
  const address = app.session.address();
  const balanceEl = h("p", { class: "value" }, "…");
  const euroEl = h("p", { class: "note" });
  const historyEl = h("div", { class: "history" }, h("p", { class: "muted" }, "…"));
  const copy = h("button", { class: "small", onClick: async () => {
    try {
      await navigator.clipboard.writeText(address);
      copy.textContent = t("main.copied");
      setTimeout(() => (copy.textContent = t("main.copy")), 2000);
    } catch { /* presse-papiers refusé : l'adresse reste sélectionnable */ }
  } }, t("main.copy"));

  let account = null;
  const refresh = async () => {
    try {
      account = await api.account(address);
      balanceEl.textContent = `${formatETL(account.balance, lang)} ETL`;
      const st = app.status;
      euroEl.textContent = st ? `≈ ${formatEuro(euroCents(account.balance, st.ipc.value, st.base_ipc.value), lang)}` : "";
      renderHistory(app, historyEl, await api.history(address), address);
    } catch {
      balanceEl.textContent = "—";
    }
  };
  app.onBlock = refresh;
  refresh();

  put(root, 
    h("div", { class: "grid" },
      h("section", { class: "card wide" },
        h("h3", {}, t("main.address")),
        h("p", { class: "address" }, h("span", { class: "mono" }, address), " ", copy),
      ),
      h("section", { class: "card" }, h("h3", {}, t("main.balance")), balanceEl, euroEl),
      sendCard(app, address, () => account, refresh),
      h("section", { class: "card wide" }, h("h3", {}, t("history.title")), historyEl, h("p", { class: "note" }, t("history.note"))),
      h("section", { class: "card wide" },
        actions(
          h("button", { onClick: () => (app.session.lock(), app.render()) }, t("main.lock")),
          h("button", { onClick: () => phraseDialog(app, clear(root)) }, t("main.showPhrase")),
          h("button", { class: "danger", onClick: () => forgetDialog(app, clear(root)) }, t("main.forget")),
        ),
      ),
    ),
  );
}

// renderHistory fusionne transferts et mouvements d'escrow, du plus récent au plus ancien.
function renderHistory(app, el, hist, address) {
  const { t, lang } = app;
  clear(el);
  const rows = [
    ...hist.transactions.map((tx) => {
      const out = tx.from === address;
      return { height: tx.height, out, label: out ? t("history.out") : t("history.in"), amount: tx.amount, peer: out ? tx.to : tx.from };
    }),
    ...hist.escrow.map((m) => ({ height: m.height, out: m.out, label: t(`history.escrow.${m.kind}`), amount: m.amount, peer: m.escrow.slice(0, 16) + "…" })),
  ].sort((a, b) => b.height - a.height);
  if (!rows.length) return put(el, h("p", { class: "muted" }, t("history.empty")));
  put(el, 
    h("ul", {},
      rows.map((r) => h("li", { class: r.out ? "out" : "in" },
        h("span", { class: "dir" }, r.label),
        h("span", { class: "amount" }, `${r.out ? "−" : "+"}${formatETL(r.amount, lang)} ETL`),
        h("span", { class: "mono small peer" }, r.peer),
        h("span", { class: "muted small" }, t("history.block", { n: r.height })),
      )),
    ),
  );
}

function sendCard(app, address, getAccount, refresh) {
  const { t, lang, api } = app;
  const to = h("input", { type: "text", autocomplete: "off", spellcheck: "false", autocapitalize: "off", placeholder: "etl1q…" });
  const amount = h("input", { type: "text", inputmode: "decimal", autocomplete: "off", placeholder: "0,00" });
  const out = h("div");
    const card = h("section", { class: "card span2" });

  const review = async () => {
    clear(out);
    const st = app.status;
    let units;
    try {
      keys.parseAddress(to.value.trim(), st.hrp);
    } catch {
      return put(out, message("error", t("send.badAddress")));
    }
    if (to.value.trim().toLowerCase() === address) return put(out, message("error", t("send.self")));
        try {
      units = parseETL(amount.value);
    } catch {
      return put(out, message("error", t("send.badAmount")));
    }
    if (units === 0n) return put(out, message("error", t("send.zero")));
    const p = await api.params();
    const params = { chainID: st.chain_id, hrp: st.hrp, feeBps: p.feeBps, minFee: p.minFee };
    const fee = txs.requiredFee(units, params);
    const account = getAccount() ?? (await api.account(address));
    if (account.balance < units + fee) return put(out, message("error", t("send.insufficient")));
    const euro = (u) => formatEuro(euroCents(u, st.ipc.value, st.base_ipc.value), lang);
    const confirm = async () => {
      app.session.touch();
      const tx = txs.transfer(app.session.key, { to: to.value.trim(), amount: units, nonce: account.nextNonce,
        timestamp: BigInt(Math.floor(Date.now() / 1000)) }, params);
      try {
        await api.submitTx(hex(txs.encodeTransaction(tx)));
        const when = new Date(st.next_block * 1000).toLocaleString(lang === "fr" ? "fr-FR" : "en-GB", { hour: "2-digit", minute: "2-digit", weekday: "short" });
        put(clear(out), message("ok", t("send.sent", { time: when })), h("p", { class: "mono small" }, hex(tx.id)));
        to.value = amount.value = "";
        refresh();
      } catch (e) {
        put(clear(out), message("error", e.message));
      }
    };
    put(out, 
      h("div", { class: "summary" },
        h("h4", {}, t("send.summary")),
        h("dl", {},
          h("dt", {}, t("send.to")), h("dd", { class: "mono small" }, to.value.trim()),
          h("dt", {}, t("send.amount")), h("dd", {}, `${formatETL(units, lang)} ETL (≈ ${euro(units)})`),
          h("dt", {}, t("send.fee")), h("dd", {}, `${formatETL(fee, lang)} ETL`),
          h("dt", {}, t("send.total")), h("dd", { class: "strong" }, `${formatETL(units + fee, lang)} ETL`),
        ),
        actions(
          h("button", { class: "primary", onClick: confirm }, t("send.confirm")),
          h("button", { onClick: () => clear(out) }, t("send.cancel")),
        ),
      ),
    );
  };

  put(card, 
    h("h3", {}, t("send.title")),
    h("form", { onSubmit: (e) => (e.preventDefault(), review()) },
      field(t("send.to"), to),
      field(t("send.amount"), amount),
      actions(h("button", { class: "primary", type: "submit" }, t("send.review"))),
    ),
    out,
  );
  return card;
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
  put(root, 
    h("section", { class: "card" },
      h("h2", {}, t("phrase.title")),
      h("p", {}, t("phrase.text")),
      h("form", { onSubmit: (e) => (e.preventDefault(), show()) }, field(t("pass.label"), pass), actions(h("button", { class: "primary", type: "submit" }, t("phrase.show")))),
      out,
      actions(h("button", { onClick: () => app.render() }, t("phrase.hide"))),
    ),
  );
}

// --- retrait du navigateur (confirmation par un mot tapé) ---

function forgetDialog(app, root) {
  const { t } = app;
  const word = t("forget.word");
  const input = h("input", { type: "text", autocomplete: "off" });
  const button = h("button", { class: "danger", disabled: true, onClick: () => (app.session.forget(), app.render()) }, t("forget.button"));
  input.addEventListener("input", () => (button.disabled = input.value.trim() !== word));
  put(root, 
    h("section", { class: "card" },
      h("h2", {}, t("forget.title")),
      h("p", { class: "msg warning" }, t("forget.text", { word })),
      field(word, input),
      actions(button, h("button", { onClick: () => app.render() }, t("send.cancel"))),
    ),
  );
}
