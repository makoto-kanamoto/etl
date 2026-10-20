// Lancer : node --test web/
import { test } from "node:test";
import assert from "node:assert/strict";
import { encode, decode, convertBits } from "./bech32m.js";

// Vecteurs officiels BIP-350 : https://github.com/bitcoin/bips/blob/master/bip-0350.mediawiki
// (mêmes listes que internal/crypto/bech32m_test.go).

test("bech32m : vecteurs valides", () => {
  for (const s of [
    "A1LQFN3A",
    "a1lqfn3a",
    "an83characterlonghumanreadablepartthatcontainsthetheexcludedcharactersbioandnumber11sg7hg6",
    "abcdef1l7aum6echk45nj3s0wdvt2fg8x9yrzpqzd3ryx",
    "11llllllllllllllllllllllllllllllllllllllllllllllllllllllllllllllllllllllllllllllllllludsr8",
    "split1checkupstagehandshakeupstreamerranterredcaperredlc445v",
    "?1v759aa",
  ]) {
    const { hrp, data } = decode(s);
    assert.equal(encode(hrp, data), s.toLowerCase(), s);
  }
});

test("bech32m : vecteurs invalides", () => {
  for (const s of [
    "\x201xj0phk",
    "\x7f1g6xzxy",
    "\x801vctc34",
    "an84characterslonghumanreadablepartthatcontainsthetheexcludedcharactersbioandnumber11d6pts4",
    "qyrz8wqd2c9m",
    "1qyrz8wqd2c9m",
    "y1b0jsk6g",
    "lt1igcx5c0",
    "in1muywd",
    "mm1crxm3i",
    "au1s5cgom",
    "M1VUXWEZ",
    "16plkw9",
    "1p2gdwpf",
    "A12UEL5L",
    "bc1p0xlxvlhemja6c4dqv22uapctqupfhlxm9h8z3k2e72q4k9hcz7vqh2y7hd",
    "tb1p0xlxvlhemja6c4dqv22uapctqupfhlxm9h8z3k2e72q4k9hcz7vq47Zagq",
    // Somme de contrôle valide, HRP court, mais 92 caractères (> 90).
    "a1qpzry9x8gf2tvdw0s3jn54khce6mua7lqpzry9x8gf2tvdw0s3jn54khce6mua7lqpzry9x8gf2tvdw0s3jnpcvnsh",
  ]) {
    assert.throws(() => decode(s), undefined, JSON.stringify(s));
  }
});

test("bech32m : rembourrage invalide refusé (5 → 8 bits)", () => {
  for (const s of [
    "bc1p0xlxvlhemja6c4dqv22uapctqupfhlxm9h8z3k2e72q4k9hcz7v07qwwzcrf",
    "tb1p0xlxvlhemja6c4dqv22uapctqupfhlxm9h8z3k2e72q4k9hcz7vpggkg4j",
  ]) {
    const { data } = decode(s);
    assert.throws(() => convertBits(data.slice(1), 5, 8, false), undefined, s);
  }
});

test("bech32m : HRP en majuscules refusé à l'encodage", () => {
  assert.throws(() => encode("ETL", []));
});
