// Contacts du portefeuille (un nom choisi pour une adresse) et libellés d'échanges. Ils restent dans ce
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
const aad = (tag, owner) => utf8.encode(`etalon-etl ${tag} v${CONTACTS_VERSION} ${owner}`);

// normalizeName : NFC, espaces réduits ; refuse un nom vide, trop long ou avec des caractères de contrôle.
export function normalizeName(name) {
  const n = String(name ?? "").normalize("NFC").replace(/\s+/g, " ").trim();
  if (!n) throw new ContactsError("empty", "nom vide");
  if ([...n].length > MAX_NAME) throw new ContactsError("long", `${MAX_NAME} caractères au plus`);
  if (/\p{Cc}|\p{Cf}/u.test(n)) throw new ContactsError("control", "caractère invisible ou de contrôle");
  return n;
}

async function deriveKey(privateKey, owner, salt) {
  const base = await subtle().importKey("raw", privateKey, "HKDF", false, ["deriveKey"]);
  return subtle().deriveKey({ name: "HKDF", hash: "SHA-256", salt: utf8.encode(salt), info: utf8.encode(owner) },
    base, { name: "AES-GCM", length: 256 }, false, ["encrypt", "decrypt"]);
}

// Sealed : dictionnaire chiffré propre à un portefeuille (contacts, libellés). spec décrit l'emplacement
// (storageKey), la dérivation (salt), l'en-tête authentifié (tag) et la validation des clés et valeurs.
class Sealed {
  constructor(spec, storage, cryptoKey, owner, hrp, entries, unreadable) {
    Object.assign(this, { spec, storage, cryptoKey, owner, hrp, entries, unreadable });
  }

  static async openWith(spec, storage, privateKey, owner, hrp) {
    const cryptoKey = await deriveKey(privateKey, owner, spec.salt);
    const make = (entries, unreadable = false) => new spec.cls(spec, storage, cryptoKey, owner, hrp, entries, unreadable);
    let raw = null;
    try {
      raw = storage.getItem(spec.storageKey);
    } catch { /* stockage inaccessible : vide */ }
    if (raw === null) return make(new Map());
    try {
      const o = JSON.parse(raw);
      if (o.version !== CONTACTS_VERSION) throw new Error("version");
      if (o.owner !== owner) return make(new Map());
      const plain = await subtle().decrypt({ name: "AES-GCM", iv: unb64(o.nonce), additionalData: aad(spec.tag, owner) }, cryptoKey, unb64(o.ciphertext));
      const list = JSON.parse(new TextDecoder().decode(plain));
      const entries = new Map();
      const probe = make(entries);
      for (const [k, v] of list) entries.set(probe.canonical(k), normalizeName(v));
      return probe;
    } catch {
      return make(new Map(), true);
    }
  }

  get(key) {
    try {
      return this.entries.get(this.canonical(key)) ?? null;
    } catch {
      return null;
    }
  }

  async put(key, value) {
    const k = this.canonical(key);
    if (!this.entries.has(k) && this.entries.size >= MAX_CONTACTS) throw new ContactsError("full", `${MAX_CONTACTS} au plus`);
    this.entries.set(k, normalizeName(value));
    await this.save();
  }

  async delete(key) {
    this.entries.delete(this.canonical(key));
    await this.save();
  }

  async save() {
    const nonce = globalThis.crypto.getRandomValues(new Uint8Array(12));
    const plain = utf8.encode(JSON.stringify([...this.entries]));
    const ct = new Uint8Array(await subtle().encrypt({ name: "AES-GCM", iv: nonce, additionalData: aad(this.spec.tag, this.owner) }, this.cryptoKey, plain));
    this.storage.setItem(this.spec.storageKey, JSON.stringify({ version: CONTACTS_VERSION, owner: this.owner, nonce: b64(nonce), ciphertext: b64(ct) }));
    this.unreadable = false;
  }
}

export class Contacts extends Sealed {
  // open lit les contacts du portefeuille owner (clé privée déverrouillée). Ceux d'un autre portefeuille
  // sont ignorés ; un enregistrement altéré est signalé (unreadable) et sera remplacé au prochain ajout.
  static open(storage, privateKey, owner, hrp) {
    return Sealed.openWith(CONTACTS_SPEC, storage, privateKey, owner, hrp);
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
    return this.get(address);
  }

  // list : contacts triés par nom.
  list() {
    return [...this.entries].map(([address, name]) => ({ address, name })).sort((a, b) => a.name.localeCompare(b.name));
  }

  async set(address, name) {
    if (this.canonical(address) === this.owner) throw new ContactsError("self", "c'est l'adresse de ce portefeuille");
    await this.put(address, name);
  }

  async remove(address) {
    await this.delete(address);
  }
}

// Labels : libellé choisi pour un échange (« vélo », « cours de guitare »), la chaîne n'en connaissant
// aucun. Même protection que les contacts, enregistrement séparé.
export class Labels extends Sealed {
  static open(storage, privateKey, owner, hrp) {
    return Sealed.openWith(LABELS_SPEC, storage, privateKey, owner, hrp);
  }

  // canonical : identifiant d'échange (64 caractères hexadécimaux, minuscules).
  canonical(id) {
    const k = String(id).trim().toLowerCase();
    if (!/^[0-9a-f]{64}$/.test(k)) throw new ContactsError("id", "identifiant d'échange invalide");
    return k;
  }
}

const CONTACTS_SPEC = { cls: Contacts, storageKey: CONTACTS_KEY, salt: "etalon-etl contacts", tag: "contacts" };
const LABELS_SPEC = { cls: Labels, storageKey: "etl.labels", salt: "etalon-etl labels", tag: "labels" };
export const LABELS_KEY = LABELS_SPEC.storageKey;
