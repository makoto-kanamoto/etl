// Panneau opérateur (clé du nœud) : session à jeton (C3) et arbitrages (C1). Le nœud vérifie et
// signe lui-même ; la clé ne quitte jamais le nœud.

import { h, field, clear } from "../lib/dom.js";
import { actions, roleOf, signerRoles, NAME_OF } from "../lib/escrowui.js";
import { formatETL } from "../lib/amount.js";
import * as esc from "../../wallet/escrow.js";
import { encodeAddress } from "../../wallet/keys.js";
import { textBox, repLine } from "./escrow.js";

export const TOKEN_KEY = "etl.operatorToken";

function msg(kind, text) {
  return h("p", { class: `msg ${kind}`, role: kind === "error" ? "alert" : "status" }, text);
}

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
  const root = h("div", { class: "operator" });
  if (!app.status?.node.address) {
    root.append(h("section", { class: "card" }, h("p", {}, t("operator.passive"))));
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
    clear(out).append(msg("error", e.message === "network" ? t("error.network") : e.message));
  };
  root.append(
    arbitrations(app, op, onError),
    pasteCard(app, op, onError),
    h("section", { class: "card wide" }, h("div", { class: "actions" }, h("button", { onClick: () => (setToken(null), app.render()) }, t("operator.token.close")))),
  );
  return root;
}

function tokenForm(app, root) {
  const { t } = app;
  const input = h("input", { type: "password", autocomplete: "off", spellcheck: "false" });
  root.append(
    h("section", { class: "card narrow" },
      h("h2", {}, t("operator.title")),
      h("p", {}, t("operator.token.text", { path: "~/.etl/dashboard.token" })),
      h("form", { onSubmit: (e) => (e.preventDefault(), setToken(input.value.trim()), app.render()) },
        field(t("operator.token.label"), input),
        h("div", { class: "actions" }, h("button", { class: "primary", type: "submit" }, t("operator.token.open"))),
      ),
    ),
  );
}

function arbitrations(app, op, onError) {
  const { t } = app;
  const node = app.status.node.address;
  const list = h("div", {}, h("p", { class: "muted" }, "…"));
  const load = async () => {
    try {
      const account = await app.api.account(node);
      const items = (await Promise.all(account.escrows.map((id) => app.api.escrow(id)))).filter((e) => roleOf(e, node) === esc.ROLE.arbiter);
      clear(list);
      if (!items.length) list.append(h("p", { class: "muted" }, t("arbiter.empty")));
      for (const e of items) {
        const out = h("div");
        const prepare = actions(e, esc.ROLE.arbiter).filter((a) => !a.alone && (a.kind === "release" || a.kind === "refund"));
        list.append(
          h("article", { class: "escrow-item" },
            h("header", {}, h("strong", {}, t(`escrow.state.${e.outcome}`)), e.paid ? h("span", { class: "badge" }, t("escrow.badge.paid")) : null,
              e.disputed ? h("span", { class: "badge alert" }, t("escrow.badge.disputed")) : null),
            h("p", { class: "small" }, `${t("escrow.seller")} : `, h("span", { class: "mono" }, e.seller)),
            h("p", { class: "small" }, `${t("escrow.buyer")} : `, h("span", { class: "mono" }, e.buyer)),
            h("p", { class: "mono small muted" }, e.id),
            prepare.length ? h("div", { class: "actions" }, prepare.map((a) => h("button", { onClick: async () => {
              try {
                const r = await op.act(e.id, a.kind);
                clear(out).append(textBox(app, r.op, "escrow.text.help", { who: t(`escrow.who.${a.kind === "release" ? "buyer" : "seller"}`) }));
              } catch (err) {
                onError(out, err);
              }
            } }, t(`arbiter.prepare.${a.kind}`)))) : null,
            out,
          ),
        );
      }
    } catch (e) {
      onError(list, e);
    }
  };
  app.onBlock = load;
  load();
  return h("section", { class: "card wide" }, h("h2", {}, t("arbiter.title")), list);
}

function pasteCard(app, op, onError) {
  const { t, lang } = app;
  const text = h("textarea", { rows: 3, autocomplete: "off", spellcheck: "false", class: "mono small" });
  const out = h("div");
  const read = async () => {
    clear(out);
    let o;
    try {
      o = esc.fromText(text.value);
    } catch {
      return out.append(msg("error", t("receive.invalid")));
    }
    const isOpen = o.kind === esc.KIND.open;
    let summary;
    if (isOpen) {
            const tm = o.terms;
      const hrp = app.status.hrp;
      const seller = encodeAddress(tm.seller, hrp);
      const buyer = encodeAddress(tm.buyer, hrp);
      summary = h("div", {},
        h("h4", {}, t("arbiter.open")),
        h("p", { class: "small" }, `${t("escrow.seller")} : `, h("span", { class: "mono" }, seller)),
        h("p", { class: "small" }, `${t("escrow.buyer")} : `, h("span", { class: "mono" }, buyer)),
                h("p", {}, `${t("escrow.amount")} : ${formatETL(tm.amount, lang)} ETL · ${t("escrow.arbiterFee")} : ${formatETL(tm.arbiterFee, lang)} ETL · ${t("escrow.days", { n: tm.days })}`),
        await repLine(app, t("escrow.seller"), seller),
        await repLine(app, t("escrow.buyer"), buyer),
      );
    } else {
      let e;
      try {
        e = await app.api.escrow(esc.idHex(o.escrow));
      } catch (err) {
        return onError(out, err);
      }
      summary = h("div", {},
                h("h4", {}, t(`escrow.kind.${NAME_OF[o.kind]}`)),
        h("p", {}, t("receive.signedBy", { who: signerRoles(o.signers).map((r) => t(`escrow.who.${r}`)).join(", ") })),
        h("p", { class: "small" }, `${t("escrow.seller")} : `, h("span", { class: "mono" }, e.seller)),
        h("p", { class: "small" }, `${t("escrow.buyer")} : `, h("span", { class: "mono" }, e.buyer)),
        await repLine(app, t("escrow.seller"), e.seller),
        await repLine(app, t("escrow.buyer"), e.buyer),
      );
    }
    out.append(
      summary,
      h("div", { class: "actions" },
        h("button", { class: "primary", onClick: async () => {
          try {
            await (isOpen ? op.cosign(text.value.trim()) : op.sign(text.value.trim()));
            clear(out).append(msg("ok", t("escrow.submitted")));
            text.value = "";
          } catch (err) {
            onError(out, err);
          }
        } }, isOpen ? t("arbiter.cosign") : t("arbiter.sign")),
      ),
    );
  };
  return h("section", { class: "card wide" },
    h("h2", {}, t("arbiter.paste.title")),
    h("form", { onSubmit: (e) => (e.preventDefault(), read()) },
      field(t("receive.label"), text),
      h("div", { class: "actions" }, h("button", { class: "primary", type: "submit" }, t("receive.read"))),
    ),
    out,
  );
}
