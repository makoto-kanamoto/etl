// Session du wallet : coffre enregistré dans le navigateur (chiffré), clé déverrouillée en mémoire,
// verrouillage automatique après 15 minutes d'inactivité (la clé est alors effacée).

import { createVault, openVault, readAddress } from "../../wallet/vault.js";
import { wipe } from "../../wallet/keys.js";

export const VAULT_KEY = "etl.vault";
export const AUTO_LOCK_MS = 15 * 60 * 1000;

export class Session {
  // storage : localStorage (ou une doublure) ; now : horloge en millisecondes.
  constructor(storage, hrp, now = () => Date.now()) {
    this.storage = storage;
    this.hrp = hrp;
    this.now = now;
    this.key = null;
    this.lastActivity = 0;
  }

  hasVault() {
    try {
      return this.storage.getItem(VAULT_KEY) !== null;
    } catch {
      return false;
    }
  }

  address() {
    return readAddress(this.storage.getItem(VAULT_KEY), this.hrp);
  }

  // create enregistre la clé dans un nouveau coffre et la garde déverrouillée.
  async create(key, passphrase) {
    if (this.hasVault()) throw new Error("un portefeuille existe déjà dans ce navigateur");
    const vault = await createVault(key, passphrase, this.hrp);
    this.storage.setItem(VAULT_KEY, vault);
    this.key = Uint8Array.from(key);
    this.touch();
  }

  async unlock(passphrase) {
    this.lock();
    this.key = await openVault(this.storage.getItem(VAULT_KEY), passphrase, this.hrp);
    this.touch();
  }

  // verify ressaisie de la phrase de passe (afficher la phrase de récupération) : renvoie une copie de la clé.
  async verify(passphrase) {
    return openVault(this.storage.getItem(VAULT_KEY), passphrase, this.hrp);
  }

  isUnlocked() {
    return this.key !== null;
  }

  touch() {
    this.lastActivity = this.now();
  }

  // check verrouille si l'inactivité dépasse AUTO_LOCK_MS ; renvoie vrai s'il vient de verrouiller.
  check() {
    if (this.key && this.now() - this.lastActivity >= AUTO_LOCK_MS) {
      this.lock();
      return true;
    }
    return false;
  }

  lock() {
    if (this.key) wipe(this.key);
    this.key = null;
  }

  // forget supprime le coffre de ce navigateur (la phrase de récupération reste le seul moyen de retour).
  forget() {
    this.lock();
    this.storage.removeItem(VAULT_KEY);
  }
}
