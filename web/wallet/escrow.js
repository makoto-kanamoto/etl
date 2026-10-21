// Échanges pair à pair (escrow) côté navigateur — mêmes octets et mêmes règles que
// internal/blockchain/escrow.go et internal/escrow : conditions, identifiant, opérations signées par
// rôle (vendeur, acheteur, arbitre), forme texte « etl-escrow: » pour les transmettre entre parties.
//
// Avant toute signature, l'interface affiche les conditions en clair : jamais de signature à l'aveugle.
// Montants en BigInt (unités ; 1 ETL = 10^8).

import * as keys from "./keys.js";
import { requiredFee } from "./tx.js";

export const TEXT_PREFIX = "etl-escrow:";
export const MIN_DAYS = 1;
export const MAX_DAYS = 30;
export const OPEN_VALIDITY_DAYS = 7n;

export const KIND = Object.freeze({ open: 1, paid: 2, dispute: 3, release: 4, refund: 5 });
export const ROLE = Object.freeze({ seller: 1, buyer: 2, arbiter: 4 });
export const OUTCOME = Object.freeze({ pending: 0, released: 1, refunded: 2 });
const ROLES = [ROLE.seller, ROLE.buyer, ROLE.arbiter]; // ordre canonique des signatures

const TERMS_SIZE = 3 * keys.ADDRESS_HASH_SIZE + 3 * 8 + 1 + 8;
const MAX_U64 = (1n << 64n) - 1n;
const NO_ADDRESS = new Uint8Array(keys.ADDRESS_HASH_SIZE);

export class EscrowError extends Error {
  constructor(code, message) {
    super(message);
    this.code = code;
  }
}

const errTerms = () => new EscrowError("terms", "conditions non conformes aux règles du protocole");
const errRole = () => new EscrowError("role", "cette clé ne correspond pas au rôle demandé");
const errExpired = () => new EscrowError("expired", "ouverture périmée ou datée dans le futur");
const errUnsigned = () => new EscrowError("unsigned", "signature du vendeur absente ou invalide");
const errText = () => new EscrowError("text", "texte d'opération invalide");
const errKind = () => new EscrowError("kind", "type d'opération invalide");

const equal = (a, b) => a.length === b.length && a.every((x, i) => x === b[i]);
const hasArbiter = (t) => !equal(t.arbiter, NO_ADDRESS);

function u64(v) {
  if (typeof v !== "bigint" || v < 0n || v > MAX_U64) throw errTerms();
  return v;
}

function concat(...parts) {
  const out = new Uint8Array(parts.reduce((n, p) => n + p.length, 0));
  let i = 0;
  for (const p of parts) {
    out.set(p, i);
    i += p.length;
  }
  return out;
}

function be64(v) {
  const b = new Uint8Array(8);
  new DataView(b.buffer).setBigUint64(0, BigInt.asUintN(64, v));
  return b;
}

function be32(v) {
  const b = new Uint8Array(4);
  new DataView(b.buffer).setUint32(0, v);
  return b;
}

// encodeTerms : vendeur | acheteur | arbitre (20 octets chacun) | montant | rémunération | frais | délai | horodatage.
export function encodeTerms(t) {
  return concat(t.seller, t.buyer, t.arbiter, be64(t.amount), be64(t.arbiterFee), be64(t.fee), Uint8Array.of(t.days), be64(t.timestamp));
}

// termsID renvoie l'identifiant d'un échange (32 octets) ; idHex sa forme affichée.
export function termsID(t) {
  return keys.sum(keys.CONTEXT_ESCROW, concat(Uint8Array.of(0x54), encodeTerms(t))); // 'T'
}

export const idHex = (id) => Array.from(id, (b) => b.toString(16).padStart(2, "0")).join("");

// checkTerms : ce que le client peut vérifier sans l'état de la chaîne.
function checkTerms(t, params) {
  let fee;
  try {
    fee = requiredFee(u64(t.amount), params);
  } catch {
    throw errTerms();
  }
  u64(t.arbiterFee);
  u64(t.fee);
  const bad =
    t.amount === 0n ||
    t.fee !== fee ||
    equal(t.seller, t.buyer) ||
    !Number.isInteger(t.days) ||
    t.days < MIN_DAYS ||
    t.days > MAX_DAYS ||
    (hasArbiter(t) && (equal(t.arbiter, t.seller) || equal(t.arbiter, t.buyer))) ||
    (!hasArbiter(t) && t.arbiterFee !== 0n) ||
    typeof t.timestamp !== "bigint";
  if (bad) throw errTerms();
}

// newTerms prépare les conditions avec les frais exacts en vigueur (params : prochain bloc).
// arbiter : adresse texte, ou null pour le mode simple (arbiterFee doit alors être 0n).
export function newTerms({ seller, buyer, arbiter = null, amount, arbiterFee = 0n, days, timestamp }, params) {
  let t;
  try {
    t = {
      seller: keys.parseAddress(seller, params.hrp),
      buyer: keys.parseAddress(buyer, params.hrp),
      arbiter: arbiter === null ? new Uint8Array(NO_ADDRESS) : keys.parseAddress(arbiter, params.hrp),
      amount,
      arbiterFee,
      fee: requiredFee(u64(amount), params),
      days,
      timestamp,
    };
  } catch {
    throw errTerms();
  }
  checkTerms(t, params);
  return t;
}

// message : ce que signent tous les signataires d'une opération.
function message(op) {
  if (op.kind === KIND.open) return concat(Uint8Array.of(op.kind), encodeTerms(op.terms));
  return concat(Uint8Array.of(op.kind), op.escrow, be32(op.seq));
}

// encodeOp : type | (conditions si ouverture, sinon échange + séquence) | masque | signatures.
export function encodeOp(op) {
  return concat(message(op), Uint8Array.of(op.signers), ...op.signatures);
}

// opID : identifiant d'une opération (signatures comprises).
export function opID(op) {
  return keys.sum(keys.CONTEXT_ESCROW, concat(Uint8Array.of(0x4f), encodeOp(op))); // 'O'
}

// decodeOp : refuse toute entrée non canonique, tronquée ou trop longue.
export function decodeOp(b) {
  const fail = () => {
    throw errText();
  };
  if (!(b instanceof Uint8Array) || b.length < 1) fail();
  const kind = b[0];
  let i = 1;
  const op = { kind, signers: 0, signatures: [] };
  if (kind === KIND.open) {
    if (b.length < i + TERMS_SIZE) fail();
    const v = new DataView(b.buffer, b.byteOffset + i, TERMS_SIZE);
    const s = (o, n) => b.slice(i + o, i + o + n);
    op.terms = {
      seller: s(0, 20),
      buyer: s(20, 20),
      arbiter: s(40, 20),
      amount: v.getBigUint64(60),
      arbiterFee: v.getBigUint64(68),
      fee: v.getBigUint64(76),
      days: v.getUint8(84),
      timestamp: v.getBigInt64(85),
    };
    i += TERMS_SIZE;
  } else if (kind >= KIND.paid && kind <= KIND.refund) {
    if (b.length < i + 36) fail();
    op.escrow = b.slice(i, i + 32);
    op.seq = new DataView(b.buffer, b.byteOffset + i + 32, 4).getUint32(0);
    i += 36;
  } else fail();
  if (b.length < i + 1) fail();
  op.signers = b[i++];
  if (op.signers === 0 || op.signers > 7) fail();
  for (const r of ROLES) {
    if (!(op.signers & r)) continue;
    if (b.length < i + keys.SIGNATURE_SIZE) fail();
    op.signatures.push(b.slice(i, i + keys.SIGNATURE_SIZE));
    i += keys.SIGNATURE_SIZE;
  }
  if (i !== b.length) fail();
  return op;
}

// sign renvoie une copie de op avec la signature de key pour role, rangée dans l'ordre canonique.
export function sign(op, key, role, params) {
  if (!ROLES.includes(role) || op.signers & role) throw errRole();
  const sig = keys.sign(key, params.chainID, keys.PURPOSES.escrow, message(op));
  const sigs = [];
  let j = 0;
  for (const r of ROLES) {
    if (r === role) sigs.push(sig);
    else if (op.signers & r) sigs.push(op.signatures[j++]);
  }
  return { ...op, signers: op.signers | role, signatures: sigs };
}

// signerAddress renvoie les 20 octets d'adresse de celui qui a signé pour role.
export function signerAddress(op, role, params) {
  if (!(op.signers & role)) throw errRole();
  let j = 0;
  for (const r of ROLES) {
    if (r === role) break;
    if (op.signers & r) j++;
  }
  try {
    return keys.addressHash(keys.recoverPublicKey(params.chainID, keys.PURPOSES.escrow, message(op), op.signatures[j]));
  } catch {
    throw errUnsigned();
  }
}

const addressOf = (key) => keys.addressHash(keys.publicKey(key));

// open construit l'ouverture et la signe comme vendeur (avec arbitre : à faire cosigner ensuite).
export function open(key, terms, params) {
  checkTerms(terms, params);
  if (!equal(addressOf(key), terms.seller)) throw errRole();
  return sign({ kind: KIND.open, terms, signers: 0, signatures: [] }, key, ROLE.seller, params);
}

// nextBlockDay : jour du prochain bloc à l'heure nowSeconds (après 23:00 UTC, celui du lendemain).
function nextBlockDay(nowSeconds, params) {
  const blockSecond = BigInt(params.blockTimeOfDaySeconds ?? 82800);
  let day = nowSeconds / 86400n;
  if (nowSeconds % 86400n >= blockSecond) day++;
  return day;
}

// cosign ajoute la signature de l'arbitre à une ouverture, après vérification : il est l'arbitre
// désigné, les conditions sont conformes, le vendeur a signé, l'ouverture n'est pas périmée pour le
// prochain bloc (nowSeconds : BigInt, heure Unix).
export function cosign(key, op, nowSeconds, params) {
  if (op.kind !== KIND.open) throw errKind();
  const t = op.terms;
  checkTerms(t, params);
  if (!hasArbiter(t) || !equal(addressOf(key), t.arbiter)) throw errRole();
  const day = nextBlockDay(nowSeconds, params);
  if (t.timestamp > nowSeconds || t.timestamp / 86400n + OPEN_VALIDITY_DAYS <= day) throw errExpired();
  if (op.signers !== ROLE.seller || !equal(signerAddress(op, ROLE.seller, params), t.seller)) throw errUnsigned();
  return sign(op, key, ROLE.arbiter, params);
}

// act construit et signe une opération sur un échange existant (seq : séquence actuelle).
export function act(key, kind, escrowID, seq, role, params) {
  if (!(kind > KIND.open && kind <= KIND.refund)) throw errKind();
  return sign({ kind, escrow: Uint8Array.from(escrowID), seq, signers: 0, signatures: [] }, key, role, params);
}

// addSignature complète une opération reçue d'une autre partie, contre l'état de l'échange.
export function addSignature(key, op, role, escrowID, escrow, params) {
  if (op.kind === KIND.open || !equal(op.escrow, escrowID) || op.seq !== escrow.seq) throw errTerms();
  const want = { [ROLE.seller]: escrow.terms.seller, [ROLE.buyer]: escrow.terms.buyer, [ROLE.arbiter]: escrow.terms.arbiter }[role];
    // Sans arbitre, l'adresse attendue est nulle : aucune clé ne correspond.
  if (!want || !equal(addressOf(key), want)) throw errRole();
  return sign(op, key, role, params);
}

// required : ensembles de signataires (masques) qui rendraient l'opération applicable le jour day
// (BigInt) ; vide si impossible. Mêmes règles que le consensus (voir internal/escrow.Required).
export function required(escrow, kind, day, arbiterActive) {
  if (escrow.outcome !== OUTCOME.pending) return [];
  const arb = hasArbiter(escrow.terms) && arbiterActive;
  const out = [];
  const add = (ok, m) => ok && out.push(m);
  switch (kind) {
    case KIND.paid:
      add(!escrow.paid && day < escrow.deadline, ROLE.buyer);
      break;
    case KIND.dispute:
      add(!escrow.disputed, ROLE.seller);
      add(!escrow.disputed, ROLE.buyer);
      break;
    case KIND.release:
      add(true, ROLE.seller);
      add(arb, ROLE.buyer | ROLE.arbiter);
      break;
    case KIND.refund:
      add(true, ROLE.buyer);
      add(arb, ROLE.seller | ROLE.arbiter);
      add(!escrow.paid && day >= escrow.deadline, ROLE.seller);
      break;
  }
  return out;
}

// --- forme texte : « etl-escrow: » + base64url sans remplissage (canonique) ---

const B64 = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_";

function toBase64url(b) {
  let s = "";
  for (let i = 0; i < b.length; i += 3) {
    const n = (b[i] << 16) | ((b[i + 1] ?? 0) << 8) | (b[i + 2] ?? 0);
    const chars = [(n >> 18) & 63, (n >> 12) & 63, (n >> 6) & 63, n & 63].map((c) => B64[c]);
    s += chars.slice(0, Math.min(4, b.length - i + 1)).join("");
  }
  return s;
}

export function fromBase64url(s) {
    if (s.length % 4 === 1) throw errText(); // caractère hors alphabet : refusé par le contrôle canonique ci-dessous
  const out = [];
  for (let i = 0; i < s.length; i += 4) {
    const chunk = s.slice(i, i + 4);
    let n = 0;
    for (let j = 0; j < 4; j++) n = (n << 6) | (j < chunk.length ? B64.indexOf(chunk[j]) : 0);
    const bytes = [(n >> 16) & 255, (n >> 8) & 255, n & 255].slice(0, chunk.length - 1);
    out.push(...bytes);
  }
  const b = Uint8Array.from(out);
  if (toBase64url(b) !== s) throw errText(); // bits de remplissage non nuls : non canonique
  return b;
}

// toText renvoie la forme texte d'une opération, à transmettre à une autre partie.
export const toText = (op) => TEXT_PREFIX + toBase64url(encodeOp(op));

// fromText relit une opération transmise (espaces autour tolérés).
export function fromText(s) {
  if (typeof s !== "string") throw errText();
  s = s.trim();
  if (!s.startsWith(TEXT_PREFIX)) throw errText();
  return decodeOp(fromBase64url(s.slice(TEXT_PREFIX.length)));
}
