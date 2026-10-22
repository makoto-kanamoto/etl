// Aide à la saisie des votes de l'opérateur (C4) : valeurs permises pour un paramètre, lecture d'une
// candidature transmise. Le nœud reste seul juge : ces règles ne servent qu'à guider la saisie.

import { fromBase64url } from "../../wallet/escrow.js";
import { encodeAddress } from "../../wallet/keys.js";

// Règles de variation (internal/blockchain/governance_params.go) : ×2 / ÷2, ou ±2 bits.
const HALVING = new Set(["fee_bps", "min_fee", "max_block_txs", "daily_reward"]);
const STEP = { target_bits: 2n };
// Paramètres exprimés en unités d'ETL (affichés et saisis en ETL).
export const AMOUNT_PARAMS = new Set(["min_fee", "daily_reward"]);

// allowedRange renvoie [min, max] des valeurs qu'un vote peut proposer depuis value (BigInt),
// dans les bornes immuables [lo, hi].
export function allowedRange(name, value, lo, hi) {
  let min = lo;
  let max = hi;
  if (HALVING.has(name)) {
    const twice = value * 2n > 1n ? value * 2n : 1n;
    min = value / 2n > min ? value / 2n : min;
    max = twice < max ? twice : max;
  }
  if (STEP[name] !== undefined) {
    const step = STEP[name];
    min = value > step && value - step > min ? value - step : min;
    max = value + step < max ? value + step : max;
  }
  return [min, max];
}

export const REGISTRATION_PREFIX = "etl-registration:";

export class RegistrationTextError extends Error {}

// readRegistration lit une candidature transmise : candidat, parrain, jour de signature.
export function readRegistration(text, hrp) {
  if (typeof text !== "string" || !text.trim().startsWith(REGISTRATION_PREFIX)) throw new RegistrationTextError();
  let b;
  try {
    b = fromBase64url(text.trim().slice(REGISTRATION_PREFIX.length));
  } catch {
    throw new RegistrationTextError();
  }
  if (b.length !== 20 + 20 + 8 + 65 + 65) throw new RegistrationTextError();
  const day = new DataView(b.buffer, b.byteOffset + 40, 8).getBigInt64(0);
  return {
    candidate: encodeAddress(b.slice(0, 20), hrp),
    sponsor: b.slice(20, 40).every((x) => x === 0) ? "" : encodeAddress(b.slice(20, 40), hrp),
    day,
    sponsored: !b.slice(113).every((x) => x === 0),
  };
}

// Paramètres regroupés par thème pour le panneau opérateur (O2).
export const PARAM_GROUPS = [
  ["economy", ["fee_bps", "min_fee", "daily_reward", "max_block_txs"]],
  ["blocks", ["target_bits"]],
  ["admission", ["observation_days", "observation_min_days", "inactivity_days", "min_active_nodes"]],
  ["sponsorship", ["sponsor_seniority_days", "sponsor_cooldown_days", "sponsor_ban_days"]],
];

// position place v (BigInt) sur la règle [lo, hi], en pourcentage borné à [0, 100].
export function position(v, lo, hi) {
  if (hi <= lo) return 0;
  const p = Number(((v - lo) * 10000n) / (hi - lo)) / 100;
  return Math.min(100, Math.max(0, p));
}
