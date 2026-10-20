// Lancer : node --test web/
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import * as keys from "./keys.js";
import * as bech32m from "./bech32m.js";

const V = JSON.parse(readFileSync(new URL("testdata/vectors.json", import.meta.url), "utf8"));
const hex = (s) => Uint8Array.from(Buffer.from(s, "hex"));
const toHex = (b) => Buffer.from(b).toString("hex");

// Vecteurs officiels BLAKE3, mode derive_key (32 premiers octets) — mêmes que internal/crypto.
test("Blake3 derive_key : vecteurs officiels", () => {
  const ctx = "BLAKE3 2019-12-27 16:29:52 test vectors context";
  for (const [len, want] of [
    [0, "2cc39783c223154fea8dfb7c1b1660f2ac2dcbd1c1de8277b0b0dd39b7e50d7d"],
    [1, "b3e2e340a117a499c6cf2398a19ee0d29cca2bb7404c73063382693bf66cb06c"],
    [1023, "74a16c1c3d44368a86e1ca6df64be6a2f64cce8f09220787450722d85725dea5"],
    [1024, "7356cd7720d5b66b6d0697eb3177d9f8d73a4a5c5e968896eb6a689684302706"],
    [1025, "effaa245f065fbf82ac186839a249707c3bddf6d3fdda22d1b95a3c970379bcb"],
  ]) {
    const input = Uint8Array.from({ length: len }, (_, i) => i % 251);
    assert.equal(toHex(keys.sum(ctx, input)), want, `longueur ${len}`);
  }
});

test("vecteurs croisés : clés publiques et adresses", () => {
  assert.equal(V.keys[0].address, "etl1qnwxsgqx9c4phyx8vjt6650fl5zgxzcaav8qcw0");
  for (const k of V.keys) {
    const priv = hex(k.private);
    assert.equal(toHex(keys.publicKey(priv)), k.public);
    assert.equal(keys.address(priv, V.hrp), k.address);
    assert.equal(keys.encodeAddress(keys.parseAddress(k.address, V.hrp), V.hrp), k.address);
    assert.equal(keys.encodeAddress(keys.parseAddress(k.address.toUpperCase(), V.hrp), V.hrp), k.address);
  }
});

test("vecteurs croisés : une signature par usage, à l'octet près", () => {
  assert.equal(new Set(V.signatures.map((s) => s.purpose)).size, Object.keys(keys.PURPOSES).length);
  for (const s of V.signatures) {
    const priv = hex(V.keys[s.key].private);
    const sig = keys.sign(priv, V.chain_id, s.purpose, hex(s.message));
    assert.equal(toHex(sig), s.signature, s.purpose);
    const addr = keys.parseAddress(V.keys[s.key].address, V.hrp);
    assert.ok(keys.verify(addr, V.chain_id, s.purpose, hex(s.message), hex(s.signature)), s.purpose);
  }
});

test("une signature ne vaut que pour son réseau, son usage, son message et son signataire", () => {
  const s = V.signatures[1];
  const msg = hex(s.message);
  const sig = hex(s.signature);
  const addr = keys.parseAddress(V.keys[s.key].address, V.hrp);
  const other = keys.parseAddress(V.keys[(s.key + 1) % V.keys.length].address, V.hrp);
  assert.ok(keys.verify(addr, V.chain_id, s.purpose, msg, sig));
  assert.ok(!keys.verify(addr, "ETL-DE", s.purpose, msg, sig), "autre réseau");
  assert.ok(!keys.verify(addr, V.chain_id, "tx", msg, sig), "autre usage");
  assert.ok(!keys.verify(addr, V.chain_id, s.purpose, Uint8Array.of(...msg, 0), sig), "autre message");
  assert.ok(!keys.verify(other, V.chain_id, s.purpose, msg, sig), "autre signataire");
  assert.throws(() => keys.sign(hex(V.keys[0].private), "", "tx", msg), keys.KeyError);
  assert.throws(() => keys.sign(hex(V.keys[0].private), V.chain_id, "", msg), keys.KeyError);
});

// Comme le Go : une seule signature valide par clé et par message.
test("signatures malléables refusées : high-S, clé non compressée, octet de récupération hors plage", () => {
  const s = V.signatures[0];
  const msg = hex(s.message);
  const sig = hex(s.signature);
  const addr = keys.parseAddress(V.keys[s.key].address, V.hrp);
  const N = 0xfffffffffffffffffffffffffffffffebaaedce6af48a03bbfd25e8cd0364141n;
  const sv = BigInt("0x" + toHex(sig.subarray(33)));
  const high = Uint8Array.from(sig);
  high.set(hex((N - sv).toString(16).padStart(64, "0")), 33);
  high[0] = 31 + ((sig[0] - 31) ^ 1); // (r, N-s) se vérifie avec l'autre parité
  assert.ok(!keys.verify(addr, V.chain_id, s.purpose, msg, high), "high-S");
  const uncompressed = Uint8Array.from(sig);
  uncompressed[0] -= 4;
  assert.ok(!keys.verify(addr, V.chain_id, s.purpose, msg, uncompressed), "clé non compressée");
  for (const flag of [0, 26, 35, 255]) {
    const bad = Uint8Array.from(sig);
    bad[0] = flag;
    assert.ok(!keys.verify(addr, V.chain_id, s.purpose, msg, bad), `octet ${flag}`);
  }
  assert.ok(!keys.verify(addr, V.chain_id, s.purpose, msg, sig.subarray(1)), "64 octets");
  const zeroR = Uint8Array.from(sig);
  zeroR.fill(0, 1, 33);
  assert.ok(!keys.verify(addr, V.chain_id, s.purpose, msg, zeroR), "r nul");
  assert.throws(() => keys.recoverPublicKey(V.chain_id, s.purpose, msg, zeroR), keys.KeyError);
});

test("clés privées hors plage refusées (jamais réduites en silence)", () => {
  const N = hex("fffffffffffffffffffffffffffffffebaaedce6af48a03bbfd25e8cd0364141");
  for (const bad of [new Uint8Array(32), N, new Uint8Array(32).fill(255), new Uint8Array(31), "00".repeat(32)]) {
    assert.throws(() => keys.publicKey(bad), keys.KeyError);
  }
});

test("adresses invalides refusées", () => {
  const a = V.keys[0].address;
  for (const bad of [
    a.slice(0, -1) + (a.endsWith("q") ? "p" : "q"), // somme de contrôle
    "etlde" + a.slice(3), // autre réseau
    a.slice(0, 10) + a.slice(10).toUpperCase(), // casse mixte
    bech32m.encode("etl", [1, ...bech32m.convertBits(new Uint8Array(20), 8, 5, true)]), // version 1, bien encodée
    bech32m.encode("etl", [0, ...bech32m.convertBits(new Uint8Array(19), 8, 5, true)]), // 19 octets
    "",
    42,
  ]) {
    assert.throws(() => keys.parseAddress(bad, V.hrp), keys.KeyError, String(bad));
  }
});

test("clés générées : valides, différentes, adresse « etl1q »", () => {
  const a = keys.generateKey();
  const b = keys.generateKey();
  assert.equal(a.length, 32);
  assert.notEqual(toHex(a), toHex(b));
  assert.match(keys.address(a, "etl"), /^etl1q[02-9ac-hj-np-z]{38}$/);
  keys.wipe(a);
  assert.ok(a.every((x) => x === 0));
});
