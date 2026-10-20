// Lancer : node --test web/
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import * as keys from "./keys.js";
import { createVault, openVault, readAddress, changePassphrase, VaultError } from "./vault.js";

const V = JSON.parse(readFileSync(new URL("testdata/vectors.json", import.meta.url), "utf8"));
const hex = (s) => Uint8Array.from(Buffer.from(s, "hex"));
const toHex = (b) => Buffer.from(b).toString("hex");
const PASS = "correct horse battery staple";
const code = (c) => (e) => e instanceof VaultError && e.code === c;

// Coffres écrits par une implémentation Go indépendante (internal/wallet/crossvectors_test.go) :
// le format est bien celui que la spécification décrit, pas seulement ce que vault.js sait relire.
test("vecteurs croisés : coffres produits par le Go", async () => {
  assert.notEqual(V.vaults[1].passphrase, V.vaults[1].passphrase.normalize("NFC"), "prémisse : vecteur en NFD");
  for (const v of V.vaults) {
    for (const form of ["NFC", "NFD"]) {
      const key = await openVault(v.vault, v.passphrase.normalize(form), V.hrp);
      assert.equal(toHex(key), V.keys[v.key].private, form);
    }
    assert.equal(readAddress(v.vault, V.hrp), V.keys[v.key].address);
    await assert.rejects(openVault(v.vault, v.passphrase + "!", V.hrp), code("wrong"));
  }
});

test("création, lecture, changement de phrase de passe", async () => {
  const key = hex(V.keys[3].private);
  const vault = await createVault(key, PASS, V.hrp);
  assert.equal(readAddress(vault, V.hrp), V.keys[3].address);
  assert.equal(toHex(await openVault(vault, PASS, V.hrp)), V.keys[3].private);
  const again = await createVault(key, PASS, V.hrp);
  assert.notEqual(JSON.parse(again).kdf.salt, JSON.parse(vault).kdf.salt, "sel neuf à chaque coffre");
  assert.notEqual(JSON.parse(again).cipher.nonce, JSON.parse(vault).cipher.nonce, "nonce neuf à chaque coffre");

  const changed = await changePassphrase(vault, PASS, "nouvelle phrase de passe", V.hrp);
  await assert.rejects(openVault(changed, PASS, V.hrp), code("wrong"));
  assert.equal(toHex(await openVault(changed, "nouvelle phrase de passe", V.hrp)), V.keys[3].private);
  await assert.rejects(changePassphrase(vault, "mauvaise phrase de passe", "nouvelle phrase de passe", V.hrp), code("wrong"));
  await assert.rejects(changePassphrase(vault, PASS, "trop court", V.hrp), code("short"));
});

test("phrase de passe : 12 caractères minimum, comptés en caractères et non en octets", async () => {
  const key = hex(V.keys[0].private);
  await assert.rejects(createVault(key, "trop court", V.hrp), code("short"));
  await assert.rejects(createVault(key, "élèveéléve!", V.hrp), code("short")); // 11 caractères, 15 octets
  await assert.rejects(createVault(key, 123456789012, V.hrp), code("short"));
  const v = await createVault(key, "élèveélève!!", V.hrp); // 12 caractères
  assert.equal(toHex(await openVault(v, "élèveélève!!", V.hrp)), V.keys[0].private);
});

// L'attaquant qui a accès au stockage du navigateur modifie le coffre.
test("coffre altéré refusé", async () => {
  const base = JSON.parse(V.vaults[0].vault);
  const b64 = (b) => Buffer.from(b).toString("base64");
  const flip = (s, i) => {
    const b = Buffer.from(s, "base64");
    b[i] ^= 1;
    return b64(b);
  };
  const cases = [
    ["adresse affichée remplacée", (o) => (o.address = V.keys[0].address), "wrong"],
    ["sel modifié", (o) => (o.kdf.salt = flip(o.kdf.salt, 0)), "wrong"],
    ["texte chiffré modifié", (o) => (o.ciphertext = flip(o.ciphertext, 3)), "wrong"],
    ["étiquette modifiée", (o) => (o.ciphertext = flip(o.ciphertext, 47)), "wrong"],
    ["nonce modifié", (o) => (o.cipher.nonce = flip(o.cipher.nonce, 0)), "wrong"],
    ["itérations augmentées (en-tête authentifié)", (o) => (o.kdf.iterations = 600_001), "wrong"],
    ["itérations affaiblies", (o) => (o.kdf.iterations = 1000), "format"],
    ["itérations démesurées", (o) => (o.kdf.iterations = 1e9), "format"],
    ["itérations non entières", (o) => (o.kdf.iterations = 600000.5), "format"],
    ["version inconnue", (o) => (o.version = 2), "format"],
    ["autre dérivation", (o) => (o.kdf.name = "argon2id"), "format"],
    ["autre chiffrement", (o) => (o.cipher.name = "aes-128-gcm"), "format"],
    ["sel trop court", (o) => (o.kdf.salt = b64(new Uint8Array(8))), "format"],
    ["nonce trop court", (o) => (o.cipher.nonce = b64(new Uint8Array(8))), "format"],
    ["texte chiffré tronqué", (o) => (o.ciphertext = b64(Buffer.from(o.ciphertext, "base64").subarray(0, 40))), "format"],
    ["base64 invalide", (o) => (o.kdf.salt = "@@@@"), "format"],
    ["adresse d'un autre réseau", (o) => (o.address = "etlde" + o.address.slice(3)), "format"],
  ];
  for (const [name, edit, want] of cases) {
    const o = structuredClone(base);
    edit(o);
    await assert.rejects(openVault(JSON.stringify(o), V.vaults[0].passphrase, V.hrp), code(want), name);
  }
  for (const bad of ["", "{", "null", "[]", "42"]) {
    await assert.rejects(openVault(bad, PASS, V.hrp), code("format"), bad);
  }
  assert.throws(() => readAddress(V.vaults[0].vault, "etlde"), code("format"));
});

// Un coffre dont la clé déchiffrée n'est pas une clé secp256k1 valide (ici : zéro) est refusé.
test("clé déchiffrée invalide refusée", async () => {
  const key = hex(V.keys[0].private);
  const v = JSON.parse(await createVault(key, PASS, V.hrp));
  const { subtle } = globalThis.crypto;
  const salt = Buffer.from(v.kdf.salt, "base64");
  const nonce = Buffer.from(v.cipher.nonce, "base64");
  const base = await subtle.importKey("raw", new TextEncoder().encode(PASS), "PBKDF2", false, ["deriveKey"]);
  const aes = await subtle.deriveKey({ name: "PBKDF2", hash: "SHA-256", salt, iterations: v.kdf.iterations }, base, { name: "AES-GCM", length: 256 }, false, ["encrypt"]);
  const enc = new TextEncoder();
  const u32 = (n) => Uint8Array.of(n >>> 24, (n >>> 16) & 255, (n >>> 8) & 255, n & 255);
  const header = Buffer.concat([
    enc.encode("etalon-etl wallet"), u32(1),
    Uint8Array.of(v.address.length), enc.encode(v.address),
    Uint8Array.of(13), enc.encode("pbkdf2-sha256"), u32(v.kdf.iterations),
    Uint8Array.of(16), salt, Uint8Array.of(11), enc.encode("aes-256-gcm"),
  ]);
  const ct = await subtle.encrypt({ name: "AES-GCM", iv: nonce, additionalData: header }, aes, new Uint8Array(32));
  v.ciphertext = Buffer.from(ct).toString("base64");
  await assert.rejects(openVault(JSON.stringify(v), PASS, V.hrp), code("format"));
  assert.ok(keys.checkPrivateKey(key));
});

// Phrase de passe normalisée en NFC : forme composée et décomposée ouvrent le même coffre,
// et la longueur se compte après NFC (comme sur le nœud).
test("phrase de passe : NFC et NFD équivalentes", async () => {
  const key = hex(V.keys[1].private);
  const nfc = "phrase de passe élève été".normalize("NFC");
  const nfd = nfc.normalize("NFD");
  assert.notEqual(nfc, nfd, "prémisse : formes différentes");
  const vault = await createVault(key, nfd, V.hrp);
  assert.equal(toHex(await openVault(vault, nfc, V.hrp)), V.keys[1].private);
  const changed = await changePassphrase(vault, nfc, nfd, V.hrp);
  assert.equal(toHex(await openVault(changed, nfc, V.hrp)), V.keys[1].private);
  const short = "élèveélève!".normalize("NFC");
  assert.equal([...short].length, 11);
  assert.equal([...short.normalize("NFD")].length, 15);
  await assert.rejects(createVault(key, short.normalize("NFD"), V.hrp), code("short"));
});
