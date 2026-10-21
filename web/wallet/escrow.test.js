// Lancer : node --test web/
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import * as keys from "./keys.js";
import { DEFAULT_PARAMS } from "./tx.js";
import * as esc from "./escrow.js";

const V = JSON.parse(readFileSync(new URL("testdata/vectors.json", import.meta.url), "utf8"));
const hex = (s) => Uint8Array.from(Buffer.from(s, "hex"));
const toHex = (b) => Buffer.from(b).toString("hex");
const P = { ...DEFAULT_PARAMS, chainID: V.chain_id, hrp: V.hrp };
const key = (i) => hex(V.keys[i].private);
const addr = (i) => V.keys[i].address;
const code = (c) => (e) => e instanceof esc.EscrowError && e.code === c;
const roleKey = { [esc.ROLE.seller]: 0, [esc.ROLE.buyer]: 1, [esc.ROLE.arbiter]: 2 };

function termsOf(tv) {
  return esc.newTerms(
    {
      seller: addr(tv.seller),
      buyer: addr(tv.buyer),
      arbiter: tv.arbiter < 0 ? null : addr(tv.arbiter),
      amount: BigInt(tv.amount),
      arbiterFee: BigInt(tv.arbiter_fee),
      days: tv.days,
      timestamp: BigInt(tv.timestamp),
    },
    P,
  );
}

test("vecteurs croisés : conditions, identifiant et ouverture signée à l'octet près", () => {
  for (const tv of V.escrow.terms) {
    const t = termsOf(tv);
    assert.equal(t.fee, BigInt(tv.fee));
    assert.equal(toHex(esc.encodeTerms(t)), tv.encoded);
    assert.equal(esc.idHex(esc.termsID(t)), tv.id);
    let op = esc.open(key(tv.seller), t, P);
    if (tv.arbiter >= 0) op = esc.cosign(key(tv.arbiter), op, t.timestamp + 60n, P);
    assert.equal(esc.toText(op), tv.open);
    const back = esc.fromText(tv.open);
    assert.equal(esc.toText(back), tv.open);
    assert.deepEqual(esc.signerAddress(back, esc.ROLE.seller, P), keys.parseAddress(addr(tv.seller), P.hrp));
  }
});

test("vecteurs croisés : chaque opération, chaque ensemble de signataires, ordre canonique", () => {
  for (const ov of V.escrow.ops) {
    const id = hex(V.escrow.terms[ov.terms].id);
    const roles = [esc.ROLE.arbiter, esc.ROLE.buyer, esc.ROLE.seller].filter((r) => ov.signers & r); // ordre inverse
    let op = esc.act(key(roleKey[roles[0]]), ov.kind, id, ov.seq, roles[0], P);
    for (const r of roles.slice(1)) op = esc.sign(op, key(roleKey[r]), r, P);
    assert.equal(esc.toText(op), ov.text, `type ${ov.kind}, signataires ${ov.signers}`);
    assert.equal(esc.toText(esc.fromText(ov.text)), ov.text);
    for (const r of roles) {
      assert.deepEqual(esc.signerAddress(op, r, P), keys.parseAddress(addr(roleKey[r]), P.hrp));
    }
  }
});

// required est la table du Go, elle-même vérifiée contre le consensus sur toutes les combinaisons.
test("vecteurs croisés : required identique au Go (et donc au consensus)", () => {
  assert.equal(V.escrow.required.length, 288);
  for (const rv of V.escrow.required) {
    const escrow = {
      terms: { arbiter: rv.arbiter ? keys.parseAddress(addr(2), P.hrp) : new Uint8Array(20) },
      paid: rv.paid,
      disputed: rv.disputed,
      deadline: 100n + BigInt(rv.deadline_offset),
      outcome: rv.outcome,
    };
    assert.deepEqual(esc.required(escrow, rv.kind, 100n, rv.arbiter_active), rv.masks, JSON.stringify(rv));
  }
});

test("conditions refusées", () => {
  const ok = { seller: addr(0), buyer: addr(1), arbiter: addr(2), amount: 1000n, arbiterFee: 5n, days: 3, timestamp: 1n };
  for (const [name, bad] of [
    ["montant nul", { amount: 0n }],
    ["montant en Number", { amount: 1000 }],
    ["vendeur = acheteur", { buyer: addr(0) }],
    ["délai nul", { days: 0 }],
    ["délai de 31 jours", { days: 31 }],
    ["délai non entier", { days: 2.5 }],
    ["rémunération sans arbitre", { arbiter: null }],
    ["arbitre = vendeur", { arbiter: addr(0) }],
    ["arbitre = acheteur", { arbiter: addr(1) }],
    ["adresse d'un autre réseau", { buyer: "etlde" + addr(1).slice(3) }],
    ["horodatage en Number", { timestamp: 1 }],
  ]) {
    assert.throws(() => esc.newTerms({ ...ok, ...bad }, P), code("terms"), name);
  }
  assert.ok(esc.newTerms({ ...ok, arbiter: null, arbiterFee: 0n }, P));
});

test("ouverture et cosignature refusées", () => {
  const t = termsOf(V.escrow.terms[0]);
  const ts = t.timestamp; // 1 800 000 000 : 2027-01-15 08:00 UTC
  assert.throws(() => esc.open(key(1), t, P), code("role"));
  assert.throws(() => esc.open(key(0), { ...t, fee: t.fee + 1n }, P), code("terms"));
  const op = esc.open(key(0), t, P);
  const day = ts - (ts % 86400n);
  assert.throws(() => esc.cosign(key(1), op, ts + 60n, P), code("role"), "pas l'arbitre");
  assert.throws(() => esc.cosign(key(2), op, ts - 1n, P), code("expired"), "datée dans le futur");
  // Signée le jour D : le bloc du jour D+6 est le dernier possible.
  assert.ok(esc.cosign(key(2), op, day + 6n * 86400n + 82799n, P), "jour D+6 avant 23:00");
  assert.throws(() => esc.cosign(key(2), op, day + 6n * 86400n + 82800n, P), code("expired"), "jour D+6 après 23:00");
  const forged = esc.sign({ ...op, signers: 0, signatures: [] }, key(1), esc.ROLE.seller, P);
  assert.throws(() => esc.cosign(key(2), forged, ts + 60n, P), code("unsigned"), "vendeur usurpé");
  const twice = esc.cosign(key(2), op, ts + 60n, P);
  assert.throws(() => esc.cosign(key(2), twice, ts + 60n, P), code("unsigned"), "déjà cosignée");
  const simple = esc.open(key(0), termsOf(V.escrow.terms[1]), P);
  assert.throws(() => esc.cosign(key(2), simple, ts + 60n, P), code("role"), "mode simple");
  assert.throws(() => esc.cosign(key(2), { ...op, terms: { ...op.terms, days: 0 } }, ts + 60n, P), code("terms"));
  const paid = esc.act(key(1), esc.KIND.paid, new Uint8Array(32), 0, esc.ROLE.buyer, P);
  assert.throws(() => esc.cosign(key(2), paid, ts + 60n, P), code("kind"));
  assert.throws(() => esc.sign(op, key(0), esc.ROLE.seller, P), code("role"), "rôle déjà signé");
  assert.throws(() => esc.sign(op, key(0), 8, P), code("role"), "rôle inconnu");
});

test("act et addSignature refusés", () => {
  const t = termsOf(V.escrow.terms[0]);
  const id = esc.termsID(t);
  assert.throws(() => esc.act(key(1), esc.KIND.open, id, 0, esc.ROLE.buyer, P), code("kind"));
  assert.throws(() => esc.act(key(1), 9, id, 0, esc.ROLE.buyer, P), code("kind"));
  const escrow = { terms: t, seq: 3 };
  const rel = esc.act(key(1), esc.KIND.release, id, 3, esc.ROLE.buyer, P);
  assert.ok(esc.addSignature(key(2), rel, esc.ROLE.arbiter, id, escrow, P));
  assert.throws(() => esc.addSignature(key(0), rel, esc.ROLE.arbiter, id, escrow, P), code("role"));
  assert.throws(() => esc.addSignature(key(2), rel, esc.ROLE.arbiter, new Uint8Array(32), escrow, P), code("terms"));
  assert.throws(() => esc.addSignature(key(2), rel, esc.ROLE.arbiter, id, { ...escrow, seq: 4 }, P), code("terms"));
  const simple = termsOf(V.escrow.terms[1]);
  assert.throws(() => esc.addSignature(key(2), rel, esc.ROLE.arbiter, id, { terms: simple, seq: 3 }, P), code("role"));
  assert.throws(() => esc.addSignature(key(2), rel, 8, id, escrow, P), code("role"));
});

test("forme texte : refus", () => {
  const text = V.escrow.ops[0].text;
  assert.equal(esc.toText(esc.fromText("  " + text + "\n")), text);
    const body = text.slice(esc.TEXT_PREFIX.length);
  for (const [name, s] of [
    ["sans préfixe", body],
    ["autre préfixe", "etl-tx:" + body],
    ["remplissage", text + "="],
    ["caractère base64 standard", text.slice(0, 20) + "+" + text.slice(21)],
    ["tronqué", text.slice(0, -4)],
    ["longueur impossible", text + "A"],
    ["octet de trop", esc.TEXT_PREFIX + Buffer.from([...esc.encodeOp(esc.fromText(text)), 0]).toString("base64url")],
    ["vide", esc.TEXT_PREFIX],
    ["pas une chaîne", 42],
  ]) {
    assert.throws(() => esc.fromText(s), code("text"), name);
  }
    // Bits de remplissage non nuls (bit de poids faible du dernier caractère) : mêmes octets décodés,
  // encodage non canonique, refusé.
  assert.notEqual(esc.encodeOp(esc.fromText(text)).length % 3, 0, "prémisse : dernier groupe incomplet");
  const B64 = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_";
  const variant = text.slice(0, -1) + B64[B64.indexOf(text.at(-1)) + 1];
  assert.throws(() => esc.fromText(variant), code("text"), "remplissage non nul");
});

// Masque de signataires hors des trois rôles : refusé même si le nombre de signatures concorde.
test("décodage : masque hors plage refusé", () => {
  const b = esc.encodeOp(esc.fromText(V.escrow.ops[0].text)); // « payé » : un seul signataire
  const at = 1 + 32 + 4;
  for (const extra of [8, 16, 128]) {
    const bad = Uint8Array.from(b);
    bad[at] |= extra;
    assert.throws(() => esc.decodeOp(bad), code("text"), String(extra));
  }
});

// Entrées aléatoires (graine fixe) : jamais d'autre exception qu'EscrowError, et toute opération
// acceptée se ré-encode à l'identique.
test("décodage : entrées aléatoires et mutations d'opérations valides", () => {
  let x = 2026;
  const next = () => {
    x ^= x << 13;
    x >>>= 0;
    x ^= x >>> 17;
    x ^= x << 5;
    x >>>= 0;
    return x;
  };
  const valid = [...V.escrow.ops.map((o) => o.text), ...V.escrow.terms.map((t) => t.open)].map((s) => esc.encodeOp(esc.fromText(s)));
  for (let i = 0; i < 20000; i++) {
    let b;
    if (i % 2) {
      b = Uint8Array.from(valid[next() % valid.length]);
      const n = 1 + (next() % 3);
      for (let k = 0; k < n; k++) b[next() % b.length] = next() & 255;
      if (next() % 4 === 0) b = b.slice(0, next() % b.length);
    } else {
      b = Uint8Array.from({ length: next() % 300 }, () => next() & 255);
    }
    let op;
    try {
      op = esc.decodeOp(b);
    } catch (e) {
      assert.ok(e instanceof esc.EscrowError, String(e));
      continue;
    }
    assert.deepEqual(esc.encodeOp(op), b);
  }
});
