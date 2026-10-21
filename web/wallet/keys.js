// Clés, adresses et signatures ETL — mêmes octets que internal/crypto :
//   - empreintes Blake3 en mode derive_key, un contexte par usage ;
//   - adresse = bech32m(hrp, version 0 | Blake3(adresse, clé publique compressée)[:20]) ;
//   - signature ECDSA secp256k1 récupérable de 65 octets (27 + 4 + recid | r | s), nonce
//     déterministe RFC 6979, s dans la moitié basse, sur Blake3(signature, len|chain_id | len|usage | msg).
//
// Hygiène des secrets en JavaScript : on remplit de zéros les Uint8Array de clé après usage, mais le
// moteur peut garder des copies, et les BigInt internes de la bibliothèque ne s'effacent pas. La vraie
// protection est le coffre chiffré (vault.js) et une clé gardée en mémoire le moins longtemps possible.

import * as secp from "./vendor/noble-secp256k1-3.2.0/index.js";
import { blake3 } from "./vendor/noble-hashes-2.4.0/blake3.js";
import { sha256 } from "./vendor/noble-hashes-2.4.0/sha2.js";
import { hmac } from "./vendor/noble-hashes-2.4.0/hmac.js";
import * as bech32m from "./bech32m.js";

secp.hashes.sha256 = sha256;
secp.hashes.hmacSha256 = (key, msg) => hmac(sha256, key, msg);

// Contextes Blake3 : font partie du protocole (internal/crypto/hash.go).
export const CONTEXT_TX = "etalon-etl 2026-10 transaction v1";
export const CONTEXT_ADDRESS = "etalon-etl 2026-10 address v1";
export const CONTEXT_SIGNATURE = "etalon-etl 2026-10 signature v1";
export const CONTEXT_ESCROW = "etalon-etl 2026-10 escrow v1";

// Usages de signature (internal/crypto/sign.go) : une signature ne vaut que pour son usage.
export const PURPOSES = Object.freeze({
  tx: "tx",
  participation: "participation",
  blockProposal: "block-proposal",
  proposal: "proposal",
  registration: "registration",
  sponsorship: "sponsorship",
  blockVote: "block-vote",
  ipcVote: "ipc-vote",
  exclusionVote: "exclusion-vote",
    paramVote: "param-vote",
  escrow: "escrow",
});

export const PRIVATE_KEY_SIZE = 32;
export const SIGNATURE_SIZE = 65;
export const ADDRESS_HASH_SIZE = 20;
const ADDRESS_VERSION = 0;

export class KeyError extends Error {}

const utf8 = new TextEncoder();

// sum calcule l'empreinte Blake3-256 de data dans le contexte donné (crypto.Sum).
export function sum(context, data) {
  return blake3(data, { context: utf8.encode(context) });
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

function u32be(n) {
  const b = new Uint8Array(4);
  new DataView(b.buffer).setUint32(0, n);
  return b;
}

// checkPrivateKey refuse tout ce qui n'est pas 32 octets dans [1, N-1] (jamais de réduction silencieuse).
export function checkPrivateKey(key) {
  if (!(key instanceof Uint8Array) || key.length !== PRIVATE_KEY_SIZE || !secp.utils.isValidSecretKey(key)) {
    throw new KeyError("clé privée invalide");
  }
  return key;
}

// generateKey tire une clé privée avec l'aléa cryptographique du navigateur.
export function generateKey() {
  return checkPrivateKey(secp.utils.randomSecretKey());
}

// publicKey renvoie la clé publique compressée (33 octets).
export function publicKey(key) {
  return secp.getPublicKey(checkPrivateKey(key), true);
}

// addressHash renvoie les 20 octets d'adresse d'une clé publique compressée.
export function addressHash(pub) {
  return sum(CONTEXT_ADDRESS, pub).slice(0, ADDRESS_HASH_SIZE);
}

// encodeAddress écrit une adresse en bech32m minuscules (« etl1q… »).
export function encodeAddress(hash, hrp) {
  if (!(hash instanceof Uint8Array) || hash.length !== ADDRESS_HASH_SIZE) throw new KeyError("adresse invalide");
  return bech32m.encode(hrp, [ADDRESS_VERSION, ...bech32m.convertBits(hash, 8, 5, true)]);
}

// parseAddress décode une adresse du réseau hrp et renvoie ses 20 octets.
// Accepte tout-minuscules ou tout-majuscules (QR codes), jamais la casse mixte.
export function parseAddress(s, hrp) {
  try {
    const { hrp: got, data } = bech32m.decode(s);
    if (got !== hrp || data.length < 1 || data[0] !== ADDRESS_VERSION) throw new KeyError();
    const hash = bech32m.convertBits(data.slice(1), 5, 8, false);
    if (hash.length !== ADDRESS_HASH_SIZE) throw new KeyError();
    return Uint8Array.from(hash);
  } catch {
    throw new KeyError("adresse invalide");
  }
}

// address renvoie l'adresse texte d'une clé privée.
export function address(key, hrp) {
  return encodeAddress(addressHash(publicKey(key)), hrp);
}

// signingHash construit l'empreinte réellement signée (crypto.signingHash).
export function signingHash(chainID, purpose, msg) {
  if (!chainID) throw new KeyError("chain_id vide");
  if (!purpose) throw new KeyError("usage de signature vide");
  const c = utf8.encode(chainID);
  const p = utf8.encode(purpose);
  return sum(CONTEXT_SIGNATURE, concat(u32be(c.length), c, u32be(p.length), p, msg));
}

// sign signe msg pour le réseau chainID et l'usage purpose : 65 octets, déterministe, low-S.
export function sign(key, chainID, purpose, msg) {
  const h = signingHash(chainID, purpose, msg);
  const rec = secp.sign(h, checkPrivateKey(key), { prehash: false, lowS: true, format: "recovered" });
  const out = new Uint8Array(SIGNATURE_SIZE);
  out[0] = 27 + 4 + rec[0]; // format compact « clé compressée », comme le Go
  out.set(rec.subarray(1), 1);
  return out;
}

const HALF_N = 0x7fffffffffffffffffffffffffffffff5d576e7357a4501ddfe92f46681b20a0n;

function bigFrom(bytes) {
  let n = 0n;
  for (const b of bytes) n = (n << 8n) | BigInt(b);
  return n;
}

// recoverPublicKey retrouve la clé publique compressée du signataire. Refuse, comme le Go, les
// signatures high-S et celles produites pour une clé non compressée : une seule signature valide par clé.
export function recoverPublicKey(chainID, purpose, msg, sig) {
  if (!(sig instanceof Uint8Array) || sig.length !== SIGNATURE_SIZE) throw new KeyError("signature invalide");
  const h = signingHash(chainID, purpose, msg);
  const flag = sig[0];
  const s = bigFrom(sig.subarray(33));
  if (flag < 31 || flag > 34 || s > HALF_N) throw new KeyError("signature invalide");
  const rec = new Uint8Array(SIGNATURE_SIZE);
  rec[0] = flag - 31;
  rec.set(sig.subarray(1), 1);
  try {
    return secp.recoverPublicKey(rec, h, { prehash: false });
  } catch {
    throw new KeyError("signature invalide");
  }
}

// verify vérifie que sig est une signature valide de msg par le détenteur de l'adresse (20 octets).
export function verify(addrHash, chainID, purpose, msg, sig) {
  let pub;
  try {
    pub = recoverPublicKey(chainID, purpose, msg, sig);
  } catch {
    return false;
  }
  const got = addressHash(pub);
  return got.length === addrHash.length && got.every((b, i) => b === addrHash[i]);
}

// wipe remplit un tampon de zéros (voir l'avertissement en tête de fichier).
export function wipe(b) {
  if (b instanceof Uint8Array) b.fill(0);
}
