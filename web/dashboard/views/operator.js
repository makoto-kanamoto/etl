// Panneau opérateur (clé du nœud) : session à jeton (C3) et arbitrages (C1). Le nœud vérifie et
// signe lui-même ; la clé ne quitte jamais le nœud.

import { h, field, clear, put } from "../lib/dom.js";
import { actions, roleOf, signerRoles, NAME_OF } from "../lib/escrowui.js";
import { formatETL, parseETL } from "../lib/amount.js";
import { allowedRange, readRegistration, AMOUNT_PARAMS } from "../lib/governance.js";
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
    put(root, h("section", { class: "card" }, h("p", {}, t("operator.passive"))));
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
    put(clear(out), msg("error", e.message === "network" ? t("error.network") : e.message));
  };
    const nodeStatus = app.status.node.status;
  put(root, 
    paramsCard(app, op, onError),
    exclusionCard(app, op, onError),
    nodeStatus === "unknown" ? prepareCard(app, op, onError) : null,
    nodeStatus === "inactive" ? reactivateCard(app, op, onError) : null,
    nodeStatus === "admitted" ? sponsorCard(app, op, onError) : null,
    arbitrations(app, op, onError),
    pasteCard(app, op, onError),
    h("section", { class: "card wide" }, h("div", { class: "actions" }, h("button", { onClick: () => (setToken(null), app.render()) }, t("operator.token.close")))),
  );
  return root;
}

function tokenForm(app, root) {
  const { t } = app;
  const input = h("input", { type: "password", autocomplete: "off", spellcheck: "false" });
  put(root, 
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
      if (!items.length) put(list, h("p", { class: "muted" }, t("arbiter.empty")));
      for (const e of items) {
        const out = h("div");
        const prepare = actions(e, esc.ROLE.arbiter).filter((a) => !a.alone && (a.kind === "release" || a.kind === "refund"));
        put(list, 
          h("article", { class: "escrow-item" },
            h("header", {}, h("strong", {}, t(`escrow.state.${e.outcome}`)), e.paid ? h("span", { class: "badge" }, t("escrow.badge.paid")) : null,
              e.disputed ? h("span", { class: "badge alert" }, t("escrow.badge.disputed")) : null),
            h("p", { class: "small" }, `${t("escrow.seller")} : `, h("span", { class: "mono" }, e.seller)),
            h("p", { class: "small" }, `${t("escrow.buyer")} : `, h("span", { class: "mono" }, e.buyer)),
            h("p", { class: "mono small muted" }, e.id),
            prepare.length ? h("div", { class: "actions" }, prepare.map((a) => h("button", { onClick: async () => {
              try {
                const r = await op.act(e.id, a.kind);
                put(clear(out), textBox(app, r.op, "escrow.text.help", { who: t(`escrow.who.${a.kind === "release" ? "buyer" : "seller"}`) }));
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
      return put(out, msg("error", t("receive.invalid")));
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
    put(out, 
      summary,
      h("div", { class: "actions" },
        h("button", { class: "primary", onClick: async () => {
          try {
            await (isOpen ? op.cosign(text.value.trim()) : op.sign(text.value.trim()));
            put(clear(out), msg("ok", t("escrow.submitted")));
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

// --- paramètres gouvernables (C4 : aide sur les valeurs permises, le nœud reste juge) ---

function paramsCard(app, op, onError) {
  const { t, lang } = app;
  const body = h("div", {}, h("p", { class: "muted" }, "…"));
  const card = h("section", { class: "card wide" }, h("h2", {}, t("params.title")), body);
  const shown = (name, v) => (AMOUNT_PARAMS.has(name) ? `${formatETL(v, lang)} ETL` : v.toLocaleString(lang === "fr" ? "fr-FR" : "en-GB"));
  app.api.params().then((p) => {
    put(clear(body), h("p", { class: "note" }, t("params.text", { days: p.activation_days })));
    const list = h("div", { class: "params" });
    for (const x of p.params) {
      const value = BigInt(x.value);
            const [lo, hi] = allowedRange(x.name, value, BigInt(x.min), BigInt(x.max)); // plage vide : valeur hors bornes (chaîne de test)
      const input = h("input", { type: "text", inputmode: "decimal", autocomplete: "off", "aria-label": `${t(`params.name.${x.name}`)} — ${t("params.new")}` });
      const out = h("div");
      const vote = async () => {
        clear(out);
        let v;
        try {
          v = AMOUNT_PARAMS.has(x.name) ? parseETL(input.value) : BigInt(input.value.replace(/[\s\u00a0\u202f]/g, ""));
        } catch {
          return put(out, h("p", { class: "msg error" }, t("send.badAmount")));
        }
        try {
          await op.paramVote(x.name, v);
          put(out, h("p", { class: "msg ok" }, t("params.sent")));
          input.value = "";
        } catch (e) {
          onError(out, e);
        }
      };
      put(list, 
        h("article", { class: "param" },
          h("header", {}, h("strong", {}, t(`params.name.${x.name}`)), h("span", { class: "value" }, shown(x.name, value))),
          h("p", { class: "small muted" }, t("params.bounds", { min: shown(x.name, BigInt(x.min)), max: shown(x.name, BigInt(x.max)) })),
          x.pending ? h("p", { class: "small" }, t("params.pending", { value: shown(x.name, BigInt(x.pending.value)),
            date: new Date(x.pending.activation * 1000).toLocaleDateString(lang === "fr" ? "fr-FR" : "en-GB", { timeZone: "UTC" }) })) : null,
          x.votes ? h("p", { class: "small" }, t("params.votes", { list: x.votes.map((v) => `${shown(x.name, BigInt(v.value))} (${v.votes}/${p.quorum})`).join(" · ") })) : null,
                    x.pending || lo > hi ? null : h("p", { class: "small muted" }, t("params.allowed", { min: shown(x.name, lo), max: shown(x.name, hi) })),
          x.pending ? null : h("form", { class: "inline", onSubmit: (e) => (e.preventDefault(), vote()) }, input, h("button", { type: "submit" }, t("params.vote"))),
          out,
        ),
      );
    }
    put(body, list);
  }).catch((e) => onError(body, e));
  return card;
}

// --- exclusion Sybil (C4 : statut et parrain affichés, adresse retapée) ---

function exclusionCard(app, op, onError) {
  const { t } = app;
  const target = h("input", { type: "text", autocomplete: "off", spellcheck: "false", placeholder: "etl1q…" });
  const out = h("div");
  const check = async () => {
    clear(out);
    let acc, p;
    try {
      [acc, p] = await Promise.all([app.api.account(target.value.trim()), app.api.params()]);
    } catch (e) {
      return onError(out, e);
    }
    const again = h("input", { type: "text", autocomplete: "off", spellcheck: "false" });
    const ban = p.params.find((x) => x.name === "sponsor_ban_days")?.value ?? "?";
    put(out, 
      h("div", { class: "summary" },
        h("p", { class: "mono small" }, acc.address),
        h("p", {}, t("exclusion.status", { status: t(`status.${acc.node}`) })),
        h("p", {}, acc.founder ? t("exclusion.founder") : t("exclusion.sponsor", { sponsor: acc.sponsor || "—" })),
        h("p", { class: "msg warning" }, t("exclusion.text", { days: ban })),
        field(t("exclusion.confirm"), again),
        h("div", { class: "actions" }, h("button", { class: "danger", onClick: async () => {
          if (again.value.trim() !== acc.address) return put(out, h("p", { class: "msg error" }, t("exclusion.mismatch")));
          try {
            await op.exclusionVote(acc.address);
            put(clear(out), h("p", { class: "msg ok" }, t("exclusion.sent")));
            target.value = "";
          } catch (e) {
            onError(out, e);
          }
        } }, t("exclusion.vote"))),
      ),
    );
  };
  return h("section", { class: "card wide" },
    h("h2", {}, t("exclusion.title")),
    h("form", { onSubmit: (e) => (e.preventDefault(), check()) },
      field(t("exclusion.target"), target),
      h("div", { class: "actions" }, h("button", { type: "submit" }, t("exclusion.check"))),
    ),
    out,
  );
}

// --- parrainage (C2) ---

function prepareCard(app, op, onError) {
  const { t } = app;
  const sponsor = h("input", { type: "text", autocomplete: "off", spellcheck: "false", placeholder: "etl1q…" });
  const out = h("div");
  return h("section", { class: "card wide" },
    h("h2", {}, t("sponsor.prepare.title")),
    h("p", {}, t("sponsor.prepare.text")),
    h("form", { onSubmit: async (e) => {
      e.preventDefault();
      try {
        const r = await op.registrationRequest(sponsor.value.trim());
        put(clear(out), textBox(app, r.registration, "sponsor.toSponsor", {}));
      } catch (err) {
        onError(out, err);
      }
    } }, field(t("sponsor.address"), sponsor), h("div", { class: "actions" }, h("button", { class: "primary", type: "submit" }, t("sponsor.prepare")))),
    out,
  );
}

function sponsorCard(app, op, onError) {
  const { t, lang } = app;
  const text = h("textarea", { rows: 3, autocomplete: "off", spellcheck: "false", class: "mono small" });
  const out = h("div");
  const note = h("p", {});
  app.api.params().then((p) => (note.textContent = t("sponsor.sign.text", { days: p.params.find((x) => x.name === "sponsor_cooldown_days")?.value ?? "?" })));
  const read = () => {
    clear(out);
    let r;
    try {
      r = readRegistration(text.value, app.status.hrp);
        } catch {
      return put(out, h("p", { class: "msg error" }, t("sponsor.invalid")));
    }
    const mine = r.sponsor === app.status.node.address;
    put(out, 
      h("div", { class: "summary" },
        h("p", {}, t("sponsor.read.candidate", { candidate: r.candidate })),
        h("p", {}, t("sponsor.read.sponsor", { sponsor: r.sponsor || "—" })),
        h("p", {}, t("sponsor.read.day", { date: new Date(Number(r.day) * 86400000).toLocaleDateString(lang === "fr" ? "fr-FR" : "en-GB", { timeZone: "UTC" }) })),
        mine
          ? h("div", { class: "actions" }, h("button", { class: "primary", onClick: async () => {
              try {
                await op.registrationSponsor(text.value.trim());
                put(clear(out), h("p", { class: "msg ok" }, t("sponsor.sent")));
                text.value = "";
              } catch (e) {
                onError(out, e);
              }
            } }, t("sponsor.confirm")))
          : h("p", { class: "msg error" }, t("sponsor.notMe")),
      ),
    );
  };
  return h("section", { class: "card wide" },
    h("h2", {}, t("sponsor.sign.title")),
    note,
    h("form", { onSubmit: (e) => (e.preventDefault(), read()) },
      field(t("sponsor.label"), text),
      h("div", { class: "actions" }, h("button", { class: "primary", type: "submit" }, t("receive.read"))),
    ),
    out,
  );
}

function reactivateCard(app, op, onError) {
  const { t } = app;
  const out = h("div");
  return h("section", { class: "card wide" },
    h("h2", {}, t("reactivate.title")),
    h("p", {}, t("reactivate.text")),
    h("div", { class: "actions" }, h("button", { class: "primary", onClick: async () => {
      try {
        await op.reactivate();
        put(clear(out), h("p", { class: "msg ok" }, t("reactivate.sent")));
      } catch (e) {
        onError(out, e);
      }
    } }, t("reactivate.button"))),
    out,
  );
}
