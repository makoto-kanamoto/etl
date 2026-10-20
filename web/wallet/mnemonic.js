// Phrase de récupération : les 32 octets de la clé privée + 8 bits de somme de contrôle (SHA-256),
// soit 264 bits = 24 mots de 11 bits, selon l'encodage BIP-39 et ses listes officielles.
// Mêmes règles, mot pour mot, que internal/wallet/mnemonic.go.
//
// ATTENTION : la phrase encode DIRECTEMENT la clé (une clé par phrase). Elle n'est PAS compatible
// avec les wallets Bitcoin ou Ethereum (Ledger, MetaMask…), qui dérivent leurs clés autrement à partir
// des mêmes mots : importer une phrase ETL dans un tel wallet donnerait une autre clé, un autre compte.

import { sha256 } from "./vendor/noble-hashes-2.4.0/sha2.js";
import { FRENCH_LIST, ENGLISH_LIST } from "./wordlists.js";

export const FRENCH = "fr";
export const ENGLISH = "en";

export class PhraseError extends Error {
  constructor(code, message) {
    super(message);
    this.code = code;
  }
}

const errLength = () => new PhraseError("length", "la phrase de récupération doit compter 24 mots");
const errWord = () => new PhraseError("word", "mot inconnu dans la phrase de récupération");
const errChecksum = () => new PhraseError("checksum", "phrase de récupération incorrecte (somme de contrôle)");
const errLanguage = () => new PhraseError("language", "mots de plusieurs langues mélangés");

// Espaces au sens de unicode.IsSpace (Go) — différent de \s en JavaScript (U+0085, U+FEFF).
const SPACE = /[\t\n\v\f\r \u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000]+/u;
const MARK = /^\p{Mn}$/u;
const FOLD = new Map([
  ..."éèêë".split("").map((c) => [c, "e"]),
  ..."àâä".split("").map((c) => [c, "a"]),
  ..."îï".split("").map((c) => [c, "i"]),
  ..."ôö".split("").map((c) => [c, "o"]),
  ..."ùûü".split("").map((c) => [c, "u"]),
  ["ç", "c"],
]);

// normalize met un mot en minuscules et retire ses accents, composés ou décomposés :
// « Académie », « academie » et la forme officielle décomposée deviennent identiques.
export function normalize(w) {
  let out = "";
  for (const r of w.toLowerCase()) {
    if (MARK.test(r)) continue; // accent combinant
    out += FOLD.get(r) ?? r;
  }
  return out;
}

// compose remplace les accents décomposés de la liste officielle française (lettre + accent
// combinant U+0301 ou U+0300) par les caractères usuels : seuls « é » et « è » y figurent.
function compose(w) {
  return w.replaceAll("e\u0301", "\u00e9").replaceAll("e\u0300", "\u00e8");
}

function loadList(raw) {
  const words = [];
  const index = new Map();
  raw
    .split(SPACE)
    .filter((w) => w !== "")
    .forEach((w, i) => {
      words.push(compose(w));
      index.set(normalize(w), i);
    });
  return { words, index };
}

export const LISTS = Object.freeze({ [FRENCH]: loadList(FRENCH_LIST), [ENGLISH]: loadList(ENGLISH_LIST) });

// encodePhrase renvoie les 24 mots correspondant à une clé privée de 32 octets.
export function encodePhrase(key, lang) {
  const wl = LISTS[lang];
  if (!wl || !(key instanceof Uint8Array) || key.length !== 32) throw errLength();
  const bits = new Uint8Array(33);
  bits.set(key);
  bits[32] = sha256(key)[0];
  const words = [];
  for (let i = 0; i < 24; i++) {
    let idx = 0;
    for (let j = 0; j < 11; j++) {
      const bit = i * 11 + j;
      idx = (idx << 1) | ((bits[bit >> 3] >> (7 - (bit & 7))) & 1);
    }
    words.push(wl.words[idx]);
  }
  bits.fill(0);
  return words;
}

// decodeIn décode la phrase dans une liste : ok si tous les mots y figurent, err si la somme de
// contrôle est fausse.
export function decodeIn(fields, wl) {
  const bits = new Uint8Array(33);
  for (let i = 0; i < fields.length; i++) {
    const idx = wl.index.get(normalize(fields[i]));
    if (idx === undefined) return { ok: false, err: errWord() };
    for (let j = 0; j < 11; j++) {
      if ((idx >> (10 - j)) & 1) {
        const bit = i * 11 + j;
        bits[bit >> 3] |= 1 << (7 - (bit & 7));
      }
    }
  }
  const key = bits.slice(0, 32);
  const valid = sha256(key)[0] === bits[32];
  bits.fill(0);
  if (!valid) {
    key.fill(0);
    return { ok: true, err: errChecksum() };
  }
  return { key, ok: true, err: null };
}

// decodePhrase retrouve la clé privée (32 octets) et la langue d'une phrase de 24 mots.
// Tolère majuscules, espaces multiples et absence d'accents ; détecte une faute de frappe sur un
// mot 255 fois sur 256. Certains mots existent dans les deux listes (« abandon »…) : la phrase est
// décodée dans chaque langue et la somme de contrôle tranche ; valide dans les deux, elle est refusée.
export function decodePhrase(phrase) {
  if (typeof phrase !== "string") throw errLength();
  const fields = phrase.split(SPACE).filter((w) => w !== "");
  if (fields.length !== 24) throw errLength();
  const found = [];
  let complete = 0;
  for (const lang of [FRENCH, ENGLISH]) {
    const r = decodeIn(fields, LISTS[lang]);
    if (r.ok) complete++;
    if (r.ok && r.err === null) found.push({ lang, key: r.key });
  }
  if (found.length === 1) return found[0];
  if (found.length > 1) {
    for (const f of found) f.key.fill(0);
    throw errLanguage();
  }
  if (complete > 0) throw errChecksum();
  for (const f of fields) {
    const w = normalize(f);
    if (!LISTS[FRENCH].index.has(w) && !LISTS[ENGLISH].index.has(w)) throw errWord();
  }
  throw errLanguage();
}
