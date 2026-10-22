// Logique de l'écran des échanges (sans affichage) : rôle, actions possibles, complément d'une
// opération reçue. Les règles viennent de l'API (champ « required », calculé par escrow.Required,
// lui-même vérifié contre le consensus).

import { ROLE, KIND } from "../../wallet/escrow.js";
import { parseAddress } from "../../wallet/keys.js";

export const KINDS = ["paid", "dispute", "release", "refund"];
export const KIND_OF = { paid: KIND.paid, dispute: KIND.dispute, release: KIND.release, refund: KIND.refund };
export const NAME_OF = Object.fromEntries(Object.entries(KIND).map(([k, v]) => [v, k]));
export const ROLE_NAMES = { [ROLE.seller]: "seller", [ROLE.buyer]: "buyer", [ROLE.arbiter]: "arbiter" };

// roleOf renvoie le rôle (ROLE.*) de address dans l'échange, ou 0.
export function roleOf(e, address) {
  if (address === e.seller) return ROLE.seller;
  if (address === e.buyer) return ROLE.buyer;
  if (e.arbiter && address === e.arbiter) return ROLE.arbiter;
  return 0;
}

// actions renvoie, pour le rôle role, les opérations possibles : seul (« alone ») ou à faire
// compléter par une autre partie (« with », masque des autres signataires).
export function actions(e, role) {
  if (!role || e.outcome !== "pending") return [];
  const out = [];
  for (const kind of KINDS) {
    for (const mask of e.required[kind] ?? []) {
      if (mask === role) out.push({ kind, alone: true });
      else if (mask & role) out.push({ kind, alone: false, with: mask & ~role });
    }
  }
  return out;
}

// signerRoles décompose un masque en noms de rôles, dans l'ordre canonique.
export function signerRoles(mask) {
  return [ROLE.seller, ROLE.buyer, ROLE.arbiter].filter((r) => mask & r).map((r) => ROLE_NAMES[r]);
}

// completes indique si ajouter la signature de role à op aboutit à un ensemble accepté par l'échange e.
export function completes(e, op, role) {
  if (op.signers & role) return false;
  const kind = NAME_OF[op.kind];
  return (e.required[kind] ?? []).includes(op.signers | role);
}

// escrowState convertit l'échange de l'API au format attendu par wallet/escrow.js (addSignature).
export function escrowState(e, hrp) {
  const addr = (a) => (a ? parseAddress(a, hrp) : new Uint8Array(20));
  return { terms: { seller: addr(e.seller), buyer: addr(e.buyer), arbiter: addr(e.arbiter) }, seq: e.seq };
}

// reputationLine résume les compteurs de réputation d'une adresse.
export function reputation(account) {
  const r = account?.reputation ?? {};
  return {
    completed: Number(r.completed ?? 0),
    refunded: Number(r.refunded ?? 0),
    opened: Number(r.disputes_opened ?? 0),
    lost: Number(r.disputes_lost ?? 0),
  };
}

// --- suivi d'un échange (E1, E2) ---

// stages : les trois étapes du suivi. state : done, current, todo, alert (litige) ou skipped (réglé
// sans paiement déclaré). L'étape « réglé » précise l'issue (released / refunded).
export function stages(e) {
  const settled = e.outcome !== "pending";
  const paid = e.paid ? "done" : settled ? "skipped" : e.disputed ? "todo" : "current";
  let end = "todo";
  if (settled) end = "done";
  else if (e.disputed) end = "alert";
  else if (e.paid) end = "current";
  return [
    { key: "opened", state: "done" },
    { key: "paid", state: paid },
    { key: settled ? e.outcome : e.disputed ? "disputed" : "settled", state: end },
  ];
}

// nextStep : qui doit agir, vu par role. mine : l'action revient à role ; kind : l'opération
// principale à proposer (seule, sans autre signature), s'il y en a une.
export function nextStep(e, role) {
  if (e.outcome !== "pending") return { key: `done.${e.outcome}`, mine: false };
  const alone = new Set(actions(e, role).filter((a) => a.alone).map((a) => a.kind));
  const R = ROLE_NAMES[role];
  if (e.disputed) return role === ROLE.arbiter ? { key: "arbiter.decide", mine: true } : { key: "wait.arbiter", mine: false };
  if (!e.paid) {
    if (R === "buyer" && alone.has("paid")) return { key: "buyer.pay", mine: true, kind: "paid" };
    if (R === "seller" && e.required.refund?.includes(ROLE.seller)) return { key: "seller.late", mine: true, kind: "refund" };
    if (R === "buyer") return { key: "buyer.late", mine: false };
    return { key: "wait.payment", mine: false };
  }
  if (R === "seller") return { key: "seller.release", mine: true, kind: "release" };
  return { key: "wait.release", mine: false };
}

// deadline : jours restants avant l'échéance de déclaration du paiement (négatif : dépassée).
export function deadlineDays(e, nowSeconds) {
  return Math.ceil((e.deadline - nowSeconds) / 86400) + 0; // + 0 : jamais « -0 »
}
