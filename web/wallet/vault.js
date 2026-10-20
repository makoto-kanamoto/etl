// Coffre de clé du navigateur (JSON, versionné) — même structure que le coffre du nœud
// (internal/wallet/wallet.go), seule la dérivation diffère :
//
//   { "version": 1, "address": "etl1q…",
//     "kdf":    { "name": "pbkdf2-sha256", "iterations": 600000, "salt": "…" },
//     "cipher": { "name": "aes-256-gcm", "nonce": "…" },
//     "ciphertext": "…" }                                  (octets en base64 standard)
//
// La clé privée est chiffrée en AES-256-GCM (WebCrypto) avec une clé dérivée de la phrase de passe par
// PBKDF2-SHA256. L'en-tête (version, adresse, paramètres, sel) est AUTHENTIFIÉ : modifier l'adresse
// affichée ou affaiblir les paramètres rend le coffre illisible.
//
// La phrase de passe est normalisée en NFC avant usage, comme sur le nœud : « é » tapé en un caractère
// ou en deux (e + accent combinant) déverrouille le même coffre.
//
// Les coffres du navigateur et du nœud ne sont pas interchangeables : le seul passage de l'un à
// l'autre est la phrase de récupération de 24 mots.

import * as keys from "./keys.js";

export const FILE_VERSION = 1;
export const MIN_PASSPHRASE = 12; // caractères (points de code, après NFC)
export const DEFAULT_ITERATIONS = 600_000;
// Borne haute à la lecture : un coffre hostile ne doit pas bloquer le navigateur indéfiniment.
const MAX_ITERATIONS = 10_000_000;
const KDF_NAME = "pbkdf2-sha256";
const CIPHER_NAME = "aes-256-gcm";
const SALT_SIZE = 16;
const NONCE_SIZE = 12;
const CIPHERTEXT_SIZE = keys.PRIVATE_KEY_SIZE + 16; // clé + étiquette GCM

export class VaultError extends Error {
  constructor(code, message) {
    super(message);
    this.code = code;
  }
}

const errShort = () => new VaultError("short", "phrase de passe trop courte (12 caractères minimum)");
const errWrong = () => new VaultError("wrong", "phrase de passe incorrecte ou coffre altéré");
const errFormat = () => new VaultError("format", "coffre illisible ou paramètres refusés");

const utf8 = new TextEncoder();
const subtle = () => globalThis.crypto.subtle;

function toBase64(b) {
  let s = "";
  for (const x of b) s += String.fromCharCode(x);
  return btoa(s);
}

function fromBase64(s) {
  if (typeof s !== "string" || !/^[A-Za-z0-9+/]*={0,2}$/.test(s) || s.length % 4 !== 0) throw errFormat();
  return Uint8Array.from(atob(s), (c) => c.charCodeAt(0));
}

function checkPassphrase(p) {
  if (typeof p !== "string" || [...p.normalize("NFC")].length < MIN_PASSPHRASE) throw errShort();
}

// header renvoie les données authentifiées (AAD) : tout sauf le nonce et le texte chiffré.
function header(v) {
  const parts = [utf8.encode("etalon-etl wallet")];
  const u32 = (n) => {
    const b = new Uint8Array(4);
    new DataView(b.buffer).setUint32(0, n);
    return b;
  };
  const str = (s) => {
    const b = utf8.encode(s);
    return [Uint8Array.of(b.length), b];
  };
  parts.push(u32(v.version), ...str(v.address), ...str(v.kdf.name), u32(v.kdf.iterations));
  parts.push(Uint8Array.of(v.kdf.salt.length), v.kdf.salt, ...str(v.cipher.name));
  const out = new Uint8Array(parts.reduce((n, p) => n + p.length, 0));
  let i = 0;
  for (const p of parts) {
    out.set(p, i);
    i += p.length;
  }
  return out;
}

async function aesKey(passphrase, salt, iterations) {
  const base = await subtle().importKey("raw", utf8.encode(passphrase.normalize("NFC")), "PBKDF2", false, ["deriveKey"]);
  return subtle().deriveKey(
    { name: "PBKDF2", hash: "SHA-256", salt, iterations },
    base,
    { name: "AES-GCM", length: 256 },
    false,
    ["encrypt", "decrypt"],
  );
}

// parse lit et contrôle un coffre (chaîne JSON) ; les octets sont décodés.
function parse(json, hrp) {
  let o;
  try {
    o = JSON.parse(json);
  } catch {
    throw errFormat();
  }
  const k = o?.kdf;
  const c = o?.cipher;
  if (
    o?.version !== FILE_VERSION ||
    typeof o.address !== "string" ||
    k?.name !== KDF_NAME ||
    c?.name !== CIPHER_NAME ||
    !Number.isInteger(k.iterations) ||
    k.iterations < DEFAULT_ITERATIONS ||
    k.iterations > MAX_ITERATIONS
  ) {
    throw errFormat();
  }
  const v = {
    version: o.version,
    address: o.address,
    kdf: { name: k.name, iterations: k.iterations, salt: fromBase64(k.salt) },
    cipher: { name: c.name, nonce: fromBase64(c.nonce) },
    ciphertext: fromBase64(o.ciphertext),
  };
  if (v.kdf.salt.length !== SALT_SIZE || v.cipher.nonce.length !== NONCE_SIZE || v.ciphertext.length !== CIPHERTEXT_SIZE) {
    throw errFormat();
  }
  try {
    keys.parseAddress(v.address, hrp);
  } catch {
    throw errFormat();
  }
  return v;
}

function serialize(v) {
  return JSON.stringify(
    {
      version: v.version,
      address: v.address,
      kdf: { name: v.kdf.name, iterations: v.kdf.iterations, salt: toBase64(v.kdf.salt) },
      cipher: { name: v.cipher.name, nonce: toBase64(v.cipher.nonce) },
      ciphertext: toBase64(v.ciphertext),
    },
    null,
    2,
  );
}

// createVault chiffre la clé privée et renvoie le coffre (chaîne JSON). Sel et nonce neufs à chaque appel.
export async function createVault(key, passphrase, hrp) {
  checkPassphrase(passphrase);
  const v = {
    version: FILE_VERSION,
    address: keys.address(key, hrp),
    kdf: { name: KDF_NAME, iterations: DEFAULT_ITERATIONS, salt: crypto.getRandomValues(new Uint8Array(SALT_SIZE)) },
    cipher: { name: CIPHER_NAME, nonce: crypto.getRandomValues(new Uint8Array(NONCE_SIZE)) },
  };
  const aes = await aesKey(passphrase, v.kdf.salt, v.kdf.iterations);
  const ct = await subtle().encrypt({ name: "AES-GCM", iv: v.cipher.nonce, additionalData: header(v), tagLength: 128 }, aes, key);
  v.ciphertext = new Uint8Array(ct);
  return serialize(v);
}

// openVault déchiffre le coffre et renvoie la clé privée (32 octets ; à effacer avec keys.wipe).
export async function openVault(json, passphrase, hrp) {
  const v = parse(json, hrp);
  const aes = await aesKey(typeof passphrase === "string" ? passphrase : "", v.kdf.salt, v.kdf.iterations);
  let key;
  try {
    key = new Uint8Array(
      await subtle().decrypt({ name: "AES-GCM", iv: v.cipher.nonce, additionalData: header(v), tagLength: 128 }, aes, v.ciphertext),
    );
  } catch {
    throw errWrong();
  }
  try {
    keys.checkPrivateKey(key);
  } catch {
    keys.wipe(key);
    throw errFormat();
  }
  // L'adresse stockée est authentifiée (en-tête) : elle correspond forcément à la clé déchiffrée.
  return key;
}

// readAddress renvoie l'adresse du coffre sans phrase de passe (stockée en clair, authentifiée).
export function readAddress(json, hrp) {
  return parse(json, hrp).address;
}

// changePassphrase rechiffre le coffre avec une nouvelle phrase de passe (nouveau sel, nouveau nonce).
export async function changePassphrase(json, oldPass, newPass, hrp) {
  checkPassphrase(newPass);
  const key = await openVault(json, oldPass, hrp);
  try {
    return await createVault(key, newPass, hrp);
  } finally {
    keys.wipe(key);
  }
}
