// Contacts du portefeuille : un nom choisi par l'utilisateur pour une adresse. Ils restent dans ce
// navigateur, CHIFFRÉS (AES-256-GCM, WebCrypto) avec une clé dérivée de la clé privée du portefeuille
// (HKDF-SHA256) : lisibles seulement portefeuille déverrouillé, et liés à son adresse. Ils ne font pas
// partie de la phrase de 24 mots : effacer les données du navigateur les perd.
//
//   { "version": 1, "owner": "etl1q…", "nonce": "…", "ciphertext": "…" }     (octets en base64)
//
// Le texte clair est la liste [[adresse, nom], …] en JSON ; l'en-tête (version, propriétaire) est
// authentifié.

import * as keys from "../../wallet/keys.js";

export const CONTACTS_KEY = "etl.contacts";
export const CONTACTS_VERSION = 1;
export const MAX_NAME = 40;
export const MAX_CONTACTS = 500;

export class ContactsError extends Error {
  constructor(code, message) {
    super(message);
    this.code = code;
  }
}

const utf8 = new TextEncoder();
const subtle = () => globalThis.crypto.subtle;
const b64 = (b) => btoa(String.fromCharCode(...b));
const unb64 = (s) => Uint8Array.from(atob(s), (c) => c.charCodeAt(0));
const aad = (owner) => utf8.encode(`etalon-etl contacts v${CONTACTS_VERSION} ${owner}`);

// normalizeName : NFC, espaces réduits ; refuse un nom vide, trop long ou avec des caractères de contrôle.
export function normalizeName(name) {
  const n = String(name ?? "").normalize("NFC").replace(/\s+/g, " ").trim();
  if (!n) throw new ContactsError("empty", "nom vide");
  if ([...n].length > MAX_NAME) throw new ContactsError("long", `${MAX_NAME} caractères au plus`);
  if (/\p{Cc}|\p{Cf}/u.test(n)) throw new ContactsError("control", "caractère invisible ou de contrôle");
  return n;
}

async function deriveKey(privateKey, owner) {
  const base = await subtle().importKey("raw", privateKey, "HKDF", false, ["deriveKey"]);
  return subtle().deriveKey({ name: "HKDF", hash: "SHA-256", salt: utf8.encode("etalon-etl contacts"), info: utf8.encode(owner) },
    base, { name: "AES-GCM", length: 256 }, false, ["encrypt", "decrypt"]);
}

export class Contacts {
  constructor(storage, cryptoKey, owner, hrp, entries, unreadable = false) {
    Object.assign(this, { storage, cryptoKey, owner, hrp, entries, unreadable });
  }

  // open lit les contacts du portefeuille owner (clé privée déverrouillée). Ceux d'un autre portefeuille
  // sont ignorés ; un enregistrement altéré est signalé (unreadable) et sera remplacé au prochain ajout.
  static async open(storage, privateKey, owner, hrp) {
    const cryptoKey = await deriveKey(privateKey, owner);
    let raw = null;
    try {
      raw = storage.getItem(CONTACTS_KEY);
    } catch { /* stockage inaccessible : aucun contact */ }
    if (raw === null) return new Contacts(storage, cryptoKey, owner, hrp, new Map());
    try {
      const o = JSON.parse(raw);
      if (o.version !== CONTACTS_VERSION) throw new Error("version");
      if (o.owner !== owner) return new Contacts(storage, cryptoKey, owner, hrp, new Map());
      const plain = await subtle().decrypt({ name: "AES-GCM", iv: unb64(o.nonce), additionalData: aad(owner) }, cryptoKey, unb64(o.ciphertext));
      const list = JSON.parse(new TextDecoder().decode(plain));
      const entries = new Map();
      for (const [a, n] of list) entries.set(keys.encodeAddress(keys.parseAddress(a, hrp), hrp), normalizeName(n));
      return new Contacts(storage, cryptoKey, owner, hrp, entries);
    } catch {
      return new Contacts(storage, cryptoKey, owner, hrp, new Map(), true);
    }
  }

  // canonical : adresse en minuscules après vérification (casse et préfixe du réseau).
  canonical(address) {
    try {
      return keys.encodeAddress(keys.parseAddress(String(address).trim(), this.hrp), this.hrp);
    } catch {
      throw new ContactsError("address", "adresse invalide");
    }
  }

  name(address) {
    try {
      return this.entries.get(this.canonical(address)) ?? null;
    } catch {
      return null;
    }
  }

  // list : contacts triés par nom.
  list() {
    return [...this.entries].map(([address, name]) => ({ address, name })).sort((a, b) => a.name.localeCompare(b.name));
  }

  async set(address, name) {
    const a = this.canonical(address);
    if (a === this.owner) throw new ContactsError("self", "c'est l'adresse de ce portefeuille");
    if (!this.entries.has(a) && this.entries.size >= MAX_CONTACTS) throw new ContactsError("full", `${MAX_CONTACTS} contacts au plus`);
    this.entries.set(a, normalizeName(name));
    await this.save();
  }

  async remove(address) {
    this.entries.delete(this.canonical(address));
    await this.save();
  }

  async save() {
    const nonce = globalThis.crypto.getRandomValues(new Uint8Array(12));
    const plain = utf8.encode(JSON.stringify([...this.entries]));
    const ct = new Uint8Array(await subtle().encrypt({ name: "AES-GCM", iv: nonce, additionalData: aad(this.owner) }, this.cryptoKey, plain));
    this.storage.setItem(CONTACTS_KEY, JSON.stringify({ version: CONTACTS_VERSION, owner: this.owner, nonce: b64(nonce), ciphertext: b64(ct) }));
    this.unreadable = false;
  }
}
