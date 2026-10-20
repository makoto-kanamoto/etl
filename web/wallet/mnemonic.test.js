// Lancer : node --test web/
import { test } from "node:test";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { FRENCH_LIST, ENGLISH_LIST, FRENCH_SHA256, ENGLISH_SHA256 } from "./wordlists.js";
import { encodePhrase, decodePhrase, decodeIn, normalize, LISTS, FRENCH, ENGLISH, PhraseError } from "./mnemonic.js";

const V = JSON.parse(readFileSync(new URL("testdata/vectors.json", import.meta.url), "utf8"));
const hex = (s) => Uint8Array.from(Buffer.from(s, "hex"));
const toHex = (b) => Buffer.from(b).toString("hex");

// Générateur pseudo-aléatoire à graine fixe (xorshift32) : essais reproductibles.
function rng(seed) {
  let x = seed >>> 0 || 1;
  return () => {
    x ^= x << 13;
    x >>>= 0;
    x ^= x >>> 17;
    x ^= x << 5;
    x >>>= 0;
    return x;
  };
}

function randomKey(next) {
  return Uint8Array.from({ length: 32 }, () => next() & 255);
}

const code = (c) => (e) => e instanceof PhraseError && e.code === c;

test("les listes embarquées sont exactement les fichiers officiels", () => {
  assert.equal(createHash("sha256").update(FRENCH_LIST).digest("hex"), FRENCH_SHA256);
  assert.equal(createHash("sha256").update(ENGLISH_LIST).digest("hex"), ENGLISH_SHA256);
  for (const lang of [FRENCH, ENGLISH]) {
    assert.equal(LISTS[lang].words.length, 2048, lang);
    assert.equal(LISTS[lang].index.size, 2048, `${lang} : formes normalisées distinctes`);
  }
});

// Vecteurs officiels BIP-39 (anglais, 32 octets) : github.com/trezor/python-mnemonic, vectors.json.
const OFFICIAL = [
  ["0000000000000000000000000000000000000000000000000000000000000000", "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon art"],
  ["7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f", "legal winner thank year wave sausage worth useful legal winner thank year wave sausage worth useful legal winner thank year wave sausage worth title"],
  ["8080808080808080808080808080808080808080808080808080808080808080", "letter advice cage absurd amount doctor acoustic avoid letter advice cage absurd amount doctor acoustic avoid letter advice cage absurd amount doctor acoustic bless"],
  ["ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", "zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo vote"],
  ["68a79eaca2324873eacc50cb9c6eca8cc68ea5d936f98787c60c7ebc74e6ce7c", "hamster diagram private dutch cause delay private meat slide toddler razor book happy fancy gospel tennis maple dilemma loan word shrug inflict delay length"],
  ["9f6a2878b2520799a44ef18bc7df394e7061a224d2c33cd015b157d746869863", "panda eyebrow bullet gorilla call smoke muffin taste mesh discover soft ostrich alcohol speed nation flash devote level hobby quick inner drive ghost inside"],
  ["066dca1a2bb7e8a1db2832148ce9933eea0f3ac9548d793112d9a95c9407efad", "all hour make first leader extend hole alien behind guard gospel lava path output census museum junior mass reopen famous sing advance salt reform"],
  ["f585c11aec520db57dd353c69554b21a89b20fb0650966fa0a9d6f74fd989d8f", "void come effort suffer camp survey warrior heavy shoot primary clutch crush open amazing screen patrol group space point ten exist slush involve unfold"],
];

test("vecteurs officiels BIP-39", () => {
  for (const [entropy, phrase] of OFFICIAL) {
    assert.equal(encodePhrase(hex(entropy), ENGLISH).join(" "), phrase);
    const r = decodePhrase(phrase);
    assert.equal(r.lang, ENGLISH);
    assert.equal(toHex(r.key), entropy);
  }
});

test("vecteurs croisés : mêmes phrases que le Go", () => {
  for (const k of V.keys) {
    assert.equal(encodePhrase(hex(k.private), FRENCH).join(" "), k.phrase_fr);
    assert.equal(encodePhrase(hex(k.private), ENGLISH).join(" "), k.phrase_en);
    for (const [phrase, lang] of [[k.phrase_fr, FRENCH], [k.phrase_en, ENGLISH]]) {
      const r = decodePhrase(phrase);
      assert.equal(r.lang, lang);
      assert.equal(toHex(r.key), k.private);
    }
  }
});

test("aller-retour dans les deux langues", () => {
  const next = rng(7);
  for (const lang of [FRENCH, ENGLISH]) {
    for (let i = 0; i < 200; i++) {
      const key = randomKey(next);
      const r = decodePhrase(encodePhrase(key, lang).join(" "));
      assert.equal(r.lang, lang);
      assert.equal(toHex(r.key), toHex(key));
    }
  }
});

// Saisie tolérante : majuscules, espaces multiples, sans accents, accents composés ou décomposés.
test("saisie française tolérante", () => {
  const key = Uint8Array.from({ length: 32 }, (_, i) => (i * 37) & 255);
  const words = encodePhrase(key, FRENCH);
  const joined = words.join(" ");
  assert.match(joined, /[\u00e9\u00e8]/, "prémisse : phrase avec accents");
  const strip = (s) => s.replace(/[\u00e9\u00e8]/g, "e");
  for (const [name, input] of [
    ["telle quelle", joined],
    ["sans accents", strip(joined)],
    ["majuscules", joined.toUpperCase()],
    ["accents décomposés", joined.normalize("NFD")],
    ["espaces multiples", "  " + words.join(" \t ") + "\n"],
    ["espace insécable", words.join("\u00a0")],
  ]) {
    const r = decodePhrase(input);
    assert.equal(r.lang, FRENCH, name);
    assert.equal(toHex(r.key), toHex(key), name);
  }
  // L'affichage utilise les caractères accentués usuels, jamais la forme décomposée de la liste officielle.
  assert.ok(!/[\u0300\u0301]/.test(joined), "accents décomposés dans la phrase affichée");
  assert.equal(joined, joined.normalize("NFC"));
});

// Même tolérance que le Go, pas plus : un caractère que le Go ne ramène pas à une lettre de base reste tel quel.
test("normalisation identique au Go", () => {
  assert.equal(normalize("ÉLÈVE"), "eleve");
  assert.equal(normalize("e\u0301le\u0300ve"), "eleve");
  assert.equal(normalize("Ça"), "ca");
  assert.equal(normalize("pañda"), "pañda");
  assert.equal(normalize("øre"), "øre");
});

// Une faute de frappe sur un mot est détectée (somme de contrôle de 8 bits : 255 fois sur 256).
test("faute de frappe détectée", () => {
  const next = rng(11);
  const wl = LISTS[ENGLISH];
  let detected = 0;
  const trials = 4000;
  for (let i = 0; i < trials; i++) {
    const key = randomKey(next);
    const words = encodePhrase(key, ENGLISH);
    const pos = i % 24;
    words[pos] = wl.words[(wl.index.get(words[pos]) + 1 + (i % 2000)) % 2048];
    try {
      const r = decodePhrase(words.join(" "));
      if (toHex(r.key) !== toHex(key)) detected++;
    } catch {
      detected++;
    }
  }
  assert.ok(detected / trials >= 0.98, `taux de détection ${detected / trials}`);
});

test("phrases refusées", () => {
  const fr = encodePhrase(new Uint8Array(32), FRENCH);
  const en = encodePhrase(new Uint8Array(32), ENGLISH);
  assert.throws(() => decodePhrase(en.slice(0, 23).join(" ")), code("length"));
  assert.throws(() => decodePhrase([...en.slice(0, 23), "bitcoin"].join(" ")), code("word"));
  assert.throws(() => decodePhrase([...fr.slice(0, 12), ...en.slice(12)].join(" ")), code("language"));
  assert.throws(() => decodePhrase([...en.slice(0, 23), "zoo"].join(" ")), code("checksum"));
  assert.throws(() => decodePhrase(42), code("length"));
  assert.throws(() => encodePhrase(new Uint8Array(31), ENGLISH), code("length"));
  assert.throws(() => encodePhrase(new Uint8Array(32), "de"), code("length"));
});

// Une phrase formée de mots communs aux deux listes peut être valide dans les deux langues
// (environ 1 fois sur 65 536) : elle désigne alors deux clés différentes et doit être refusée.
test("phrase valide dans les deux langues refusée", () => {
  const common = [...LISTS[FRENCH].index.keys()].filter((w) => LISTS[ENGLISH].index.has(w)).sort();
  const next = rng(1);
  for (let t = 0; t < 1 << 22; t++) {
    const fields = Array.from({ length: 24 }, () => common[next() % common.length]);
    const fr = decodeIn(fields, LISTS[FRENCH]);
    const en = decodeIn(fields, LISTS[ENGLISH]);
    if (fr.err || en.err) continue;
    assert.throws(() => decodePhrase(fields.join(" ")), code("language"));
    return;
  }
  assert.fail("prémisse : aucune phrase ambiguë trouvée");
});

// Saisie arbitraire : jamais d'autre exception que PhraseError, et toute phrase acceptée
// désigne une clé de 32 octets qui se ré-encode en une phrase équivalente.
test("entrées aléatoires (fuzzing à graine fixe)", () => {
  const next = rng(2026);
  const all = [...LISTS[FRENCH].words, ...LISTS[ENGLISH].words];
  const noise = ["", " ", "\u00e9", "e\u0301", "\u0000", "\ud800", "\u{1f600}", "\t", "\u0085", "\ufeff", "zz", "ABANDON"];
  const valid = encodePhrase(Uint8Array.from({ length: 32 }, (_, i) => i), ENGLISH);
  for (let i = 0; i < 20000; i++) {
    let words;
    if (i % 4 === 0) {
      words = [...valid];
      words[next() % 24] = all[next() % all.length];
    } else {
      words = Array.from({ length: 20 + (next() % 8) }, () =>
        next() % 5 === 0 ? noise[next() % noise.length] : all[next() % all.length],
      );
    }
    const input = words.join(next() % 3 === 0 ? "  " : " ");
    let r;
    try {
      r = decodePhrase(input);
    } catch (e) {
      assert.ok(e instanceof PhraseError, `exception inattendue : ${e}`);
      continue;
    }
    assert.equal(r.key.length, 32);
    const again = decodePhrase(encodePhrase(r.key, r.lang).join(" "));
    assert.equal(toHex(again.key), toHex(r.key));
  }
});
