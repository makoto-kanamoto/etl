// Lancer : node --test web/
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import * as keys from "./keys.js";
import * as txs from "./tx.js";

const V = JSON.parse(readFileSync(new URL("testdata/vectors.json", import.meta.url), "utf8"));
const hex = (s) => Uint8Array.from(Buffer.from(s, "hex"));
const toHex = (b) => Buffer.from(b).toString("hex");
const paramsOf = (t) => ({ chainID: V.chain_id, hrp: V.hrp, feeBps: BigInt(t.fee_bps), minFee: BigInt(t.min_fee) });
const fieldsOf = (t) => ({ to: t.to, amount: BigInt(t.amount), nonce: BigInt(t.nonce), timestamp: BigInt(t.timestamp) });

test("vecteurs croisés : frais, corps, signature, identifiant et encodage réseau à l'octet près", () => {
  for (const t of V.transactions) {
    const tx = txs.transfer(hex(V.keys[t.key].private), fieldsOf(t), paramsOf(t));
    assert.equal(tx.fee, BigInt(t.fee), `frais ${t.amount}`);
    assert.equal(toHex(txs.body(tx)), t.body);
    assert.equal(toHex(tx.signature), t.signature);
    assert.equal(toHex(tx.id), t.id);
    assert.equal(toHex(txs.encodeTransaction(tx)), t.encoded);
    assert.equal(txs.encodeTransaction(tx).length, 138);
  }
});

test("frais : max(montant × bps / 10000 tronqué, minimum)", () => {
  const p = txs.DEFAULT_PARAMS;
  assert.equal(txs.requiredFee(1n, p), 10_000n); // minimum
  assert.equal(txs.requiredFee(10_000_000n, p), 10_000n); // 0,1 ETL : seuil exact
  assert.equal(txs.requiredFee(10_009_999n, p), 10_009n); // troncature
  assert.equal(txs.requiredFee(txs.UNITS_PER_ETL, p), 100_000n);
  assert.equal(txs.requiredFee(txs.UNITS_PER_ETL, { ...p, feeBps: 20n }), 200_000n);
  assert.equal(txs.fee((1n << 64n) - 1n, 10_000n), (1n << 64n) - 1n); // aucun débordement
  assert.throws(() => txs.fee(1n, 10_001n), txs.TxError);
  assert.throws(() => txs.fee(1, 10n), txs.TxError, "Number refusé : BigInt obligatoire");
  assert.throws(() => txs.fee(-1n, 10n), txs.TxError);
  assert.throws(() => txs.fee(1n << 64n, 10n), txs.TxError);
});

test("transferts refusés", () => {
  const key = hex(V.keys[0].private);
  const ok = { to: V.keys[1].address, amount: 1n, nonce: 0n, timestamp: 1_800_000_000n };
  // Chaque refus porte un message précis (affiché tel quel par l'interface).
  for (const [name, f, msg] of [
    ["montant nul", { ...ok, amount: 0n }, /montant nul/],
    ["montant en Number", { ...ok, amount: 1 }, /BigInt/],
    ["à soi-même", { ...ok, to: V.keys[0].address }, /identiques/],
    ["adresse d'un autre réseau", { ...ok, to: "etlde" + V.keys[1].address.slice(3) }, /adresse invalide/],
    ["nonce hors uint64", { ...ok, nonce: 1n << 64n }, /nonce/],
    ["horodatage hors int64", { ...ok, timestamp: 1n << 63n }, /horodatage/],
  ]) {
    assert.throws(() => txs.transfer(key, f), msg, name);
  }
});

test("vérification : toute altération invalide la transaction", () => {
  const t = V.transactions[2];
  const p = paramsOf(t);
  const tx = txs.transfer(hex(V.keys[t.key].private), fieldsOf(t), p);
  assert.ok(txs.verifyTransaction(tx, p));
  const resign = (edit) => {
    const x = { ...tx };
    edit(x);
    x.signature = keys.sign(hex(V.keys[t.key].private), p.chainID, keys.PURPOSES.tx, txs.body(x));
    x.id = txs.txID(x);
    return x;
  };
  for (const [name, bad, params] of [
    ["montant modifié", { ...tx, amount: tx.amount + 1n }, p],
    ["frais modifiés", { ...tx, fee: tx.fee + 1n }, p],
    ["identifiant faux", { ...tx, id: new Uint8Array(32) }, p],
    ["autre réseau", tx, { ...p, chainID: "ETL-DE" }],
    ["frais votés ensuite", tx, { ...p, feeBps: 20n }],
    ["frais inexacts même re-signés", resign((x) => (x.fee += 1n)), p],
    ["montant nul même re-signé", resign((x) => ((x.amount = 0n), (x.fee = p.minFee))), p],
    ["signé par un autre", { ...tx, signature: keys.sign(hex(V.keys[0].private), p.chainID, "tx", txs.body(tx)) }, p],
  ]) {
    assert.ok(!txs.verifyTransaction(bad, params), name);
  }
  for (const bad of [{}, { ...tx, to: undefined }, { ...tx, amount: 1 }, { ...tx, signature: new Uint8Array(3) }, null]) {
    assert.equal(txs.verifyTransaction(bad, p), false);
  }
});
