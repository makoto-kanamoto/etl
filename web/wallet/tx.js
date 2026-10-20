// Transferts ETL — mêmes octets que internal/blockchain/tx.go :
//   corps = version (1) | de (20) | à (20) | montant | frais | nonce | horodatage (4 × 8, gros-boutiste)
//   signature = usage « tx » sur le corps ; identifiant = Blake3(transaction, corps | signature)
//   encodage réseau = corps | signature (138 octets)
//
// Montants en BigInt, jamais en Number (1 ETL = 10^8 unités, bornes uint64 vérifiées).

import * as keys from "./keys.js";

export const TX_VERSION = 1;
export const UNITS_PER_ETL = 100_000_000n;
export const BODY_SIZE = 1 + 2 * keys.ADDRESS_HASH_SIZE + 4 * 8;
const MAX_BPS = 10_000n;
const MAX_U64 = (1n << 64n) - 1n;
const MIN_I64 = -(1n << 63n);
const MAX_I64 = (1n << 63n) - 1n;

// Paramètres par défaut du réseau (internal/blockchain/params.go). Frais et minimum sont
// gouvernables : toujours passer ceux en vigueur pour le prochain bloc, fournis par le nœud.
export const DEFAULT_PARAMS = Object.freeze({ chainID: "ETL-FR", hrp: "etl", feeBps: 10n, minFee: 10_000n });

export class TxError extends Error {}

function u64(name, v) {
  if (typeof v !== "bigint" || v < 0n || v > MAX_U64) throw new TxError(`${name} : entier BigInt sur 64 bits attendu`);
  return v;
}

function i64(name, v) {
  if (typeof v !== "bigint" || v < MIN_I64 || v > MAX_I64) throw new TxError(`${name} : entier BigInt signé sur 64 bits attendu`);
  return v;
}

// fee renvoie amount × bps / 10000, tronqué (blockchain.Fee).
export function fee(amount, bps) {
  u64("montant", amount);
  if (u64("points de base", bps) > MAX_BPS) throw new TxError("points de base > 10000");
  return (amount * bps) / MAX_BPS;
}

// requiredFee renvoie les frais exigés : max(amount × feeBps / 10000, minFee) (blockchain.RequiredFee).
export function requiredFee(amount, params) {
  const f = fee(amount, params.feeBps);
  const min = u64("frais minimum", params.minFee);
  return f > min ? f : min;
}

// body renvoie l'encodage canonique d'une transaction, signature exclue : exactement ce qui est signé.
// from et to sont les 20 octets des adresses.
export function body(tx) {
  const b = new Uint8Array(BODY_SIZE);
  const v = new DataView(b.buffer);
  b[0] = TX_VERSION;
  b.set(tx.from, 1);
  b.set(tx.to, 21);
  v.setBigUint64(41, u64("montant", tx.amount));
  v.setBigUint64(49, u64("frais", tx.fee));
  v.setBigUint64(57, u64("nonce", tx.nonce));
  v.setBigInt64(65, i64("horodatage", tx.timestamp));
  return b;
}

function withSig(tx) {
  const b = new Uint8Array(BODY_SIZE + keys.SIGNATURE_SIZE);
  b.set(body(tx));
  b.set(tx.signature, BODY_SIZE);
  return b;
}

// txID calcule l'identifiant : Blake3(transaction, corps | signature).
export function txID(tx) {
  return keys.sum(keys.CONTEXT_TX, withSig(tx));
}

// encodeTransaction renvoie l'encodage réseau (blockchain.EncodeTransaction).
export function encodeTransaction(tx) {
  return withSig(tx);
}

// transfer construit et signe un transfert de amount unités vers l'adresse to, avec les frais
// exacts selon params (ceux en vigueur pour le prochain bloc). nonce = nonce actuel du compte.
export function transfer(key, { to, amount, nonce, timestamp }, params = DEFAULT_PARAMS) {
  const fromHash = keys.addressHash(keys.publicKey(key));
  const toHash = keys.parseAddress(to, params.hrp);
  if (u64("montant", amount) === 0n) throw new TxError("montant nul");
  if (fromHash.every((b, i) => b === toHash[i])) throw new TxError("expéditeur et destinataire identiques");
  const tx = { from: fromHash, to: toHash, amount, fee: requiredFee(amount, params), nonce, timestamp };
  tx.signature = keys.sign(key, params.chainID, keys.PURPOSES.tx, body(tx));
  tx.id = txID(tx);
  if (!verifyTransaction(tx, params)) throw new TxError("transaction construite invalide");
  return tx;
}

// verifyTransaction contrôle ce qui ne dépend pas de l'état des comptes (blockchain.Transaction.Verify) :
// montant, frais exacts, identifiant et signature.
export function verifyTransaction(tx, params = DEFAULT_PARAMS) {
  try {
    if (tx.amount === 0n || tx.from.every((b, i) => b === tx.to[i])) return false;
    if (tx.fee !== requiredFee(tx.amount, params)) return false;
    const id = txID(tx);
    if (!(tx.id instanceof Uint8Array) || !id.every((b, i) => b === tx.id[i])) return false;
    return keys.verify(tx.from, params.chainID, keys.PURPOSES.tx, body(tx), tx.signature);
  } catch {
    return false;
  }
}

