// Montants : saisie, affichage et équivalent en euros. Tout en BigInt (unités ; 1 ETL = 10^8).

export const UNITS = 100_000_000n;
const MAX_U64 = (1n << 64n) - 1n;

export class AmountError extends Error {}

// parseETL lit un montant saisi en ETL : « 1,5 » comme « 1.5 », espaces (y compris insécables)
// ignorés, 8 décimales au plus. Renvoie des unités (BigInt).
export function parseETL(input) {
  if (typeof input !== "string") throw new AmountError("montant invalide");
  const s = input.replace(/[\s\u00a0\u202f]/g, "");
  const m = /^(\d+)(?:[.,](\d{1,8}))?$/.exec(s);
  if (!m) throw new AmountError("montant invalide");
  const units = BigInt(m[1]) * UNITS + BigInt((m[2] ?? "").padEnd(8, "0"));
  if (units > MAX_U64) throw new AmountError("montant trop grand");
  return units;
}

const SEP = { fr: { group: "\u202f", decimal: "," }, en: { group: ",", decimal: "." } };

function group(digits, sep) {
  return digits.replace(/\B(?=(\d{3})+(?!\d))/g, sep);
}

// formatETL écrit un montant selon la langue : « 1 234,5 » (fr), « 1,234.5 » (en), sans zéros inutiles.
export function formatETL(units, lang) {
  const sep = SEP[lang] ?? SEP.en;
  const whole = group((units / UNITS).toString(), sep.group);
  const frac = (units % UNITS).toString().padStart(8, "0").replace(/0+$/, "");
  return frac ? `${whole}${sep.decimal}${frac}` : whole;
}

// euros renvoie l'équivalent en centimes d'un montant au taux IPC : 1 ETL = ipc / base euro
// (arrondi au centime le plus proche).
export function euroCents(units, ipc, base) {
  if (!base) return 0n;
  const num = units * BigInt(ipc) * 100n;
  const den = UNITS * BigInt(base);
  return (num + den / 2n) / den;
}

// formatEuro écrit des centimes selon la langue : « 1 234,56 € » (fr), « €1,234.56 » (en).
export function formatEuro(cents, lang) {
  const sep = SEP[lang] ?? SEP.en;
  const s = `${group((cents / 100n).toString(), sep.group)}${sep.decimal}${(cents % 100n).toString().padStart(2, "0")}`;
  return lang === "fr" ? `${s}\u00a0€` : `€${s}`;
}

// rate : valeur de 1 ETL en euros, 4 décimales (« 1,0307 »).
export function formatRate(ipc, base, lang) {
  if (!base) return "—";
  const tenThousandths = (BigInt(ipc) * 10000n + BigInt(base) / 2n) / BigInt(base);
  const sep = SEP[lang] ?? SEP.en;
  return `${tenThousandths / 10000n}${sep.decimal}${(tenThousandths % 10000n).toString().padStart(4, "0")}`;
}
