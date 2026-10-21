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
