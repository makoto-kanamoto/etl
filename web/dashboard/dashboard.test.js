// Lancer : node --test web/
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync, readdirSync, statSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { parseETL, formatETL, euroCents, formatEuro, formatRate, AmountError, UNITS } from "./lib/amount.js";
import { challenge, check, CHALLENGE_WORDS } from "./lib/phrase.js";
import { Session, VAULT_KEY, AUTO_LOCK_MS } from "./lib/session.js";
import { STRINGS, LANGS, t } from "./lib/strings.js";
import { makeApi, ApiError } from "./lib/api.js";
import { render } from "../tools/importmap.js";
import * as keys from "../wallet/keys.js";
import * as mnemonic from "../wallet/mnemonic.js";

const DIR = fileURLToPath(new URL(".", import.meta.url));

test("montants : saisie « 1,5 » ou « 1.5 », 8 décimales au plus, BigInt", () => {
  for (const [input, want] of [
    ["1", UNITS],
    ["1,5", 150_000_000n],
    ["1.5", 150_000_000n],
    ["0,00000001", 1n],
    [" 1 234,5 ", 123_450_000_000n],
    ["1 234.5", 123_450_000_000n],
    ["0", 0n],
    ["184467440737,09551615", (1n << 64n) - 1n],
  ]) {
    assert.equal(parseETL(input), want, input);
  }
  for (const bad of ["", ",5", "1,", "1,123456789", "-1", "1e3", "1,5,0", "un", "184467440737,09551616", 15]) {
    assert.throws(() => parseETL(bad), AmountError, String(bad));
  }
});

test("montants : affichage selon la langue, sans zéros inutiles", () => {
  assert.equal(formatETL(123_450_000_000n, "fr"), "1 234,5");
  assert.equal(formatETL(123_450_000_000n, "en"), "1,234.5");
  assert.equal(formatETL(UNITS, "fr"), "1");
  assert.equal(formatETL(1n, "en"), "0.00000001");
  assert.equal(formatETL(0n, "fr"), "0");
  assert.equal(formatETL(1_000_000n * UNITS, "en"), "1,000,000");
  assert.equal(formatETL(5n * UNITS, "ja"), "5", "langue inconnue : séparateurs anglais");
});

test("montants : équivalent en euros au taux IPC, arrondi au centime", () => {
  // 1 ETL au genesis (base 100 000) vaut 1 € ; à 103 070, il vaut 1,0307 €.
  assert.equal(euroCents(UNITS, 103070, 100000), 103n);
  assert.equal(euroCents(50n * UNITS, 103070, 100000), 5154n); // 51,535 → 51,54
  assert.equal(euroCents(UNITS, 100000, 0), 0n, "pas de base : pas d'équivalent");
  assert.equal(formatEuro(5154n, "fr"), "51,54 €");
  assert.equal(formatEuro(123456789n, "en"), "€1,234,567.89");
  assert.equal(formatEuro(5n, "fr"), "0,05 €");
  assert.equal(formatRate(103070, 100000, "fr"), "1,0307");
  assert.equal(formatRate(103070, 100000, "en"), "1.0307");
  assert.equal(formatRate(1, 0, "en"), "—");
});

test("phrase : 3 positions distinctes, vérification tolérante", () => {
  let x = 0;
  const seq = [5, 5, 23, 0, 7];
  const pos = challenge(() => seq[x++]);
  assert.deepEqual(pos, [0, 5, 23], "doublon écarté, positions triées");
  for (let i = 0; i < 200; i++) {
    const p = challenge();
    assert.equal(new Set(p).size, CHALLENGE_WORDS);
    assert.ok(p.every((n) => n >= 0 && n < 24));
  }
  const words = mnemonic.encodePhrase(Uint8Array.from({ length: 32 }, (_, i) => i * 7), mnemonic.FRENCH);
  const accented = [0, 1, 2].find((p) => /[éè]/.test(words[p])) ?? 0;
  const answers = [0, 1, 2].map((p) => words[p]);
  assert.ok(check(words, [0, 1, 2], answers));
  assert.ok(check(words, [0, 1, 2], answers.map((w) => ` ${w.toUpperCase()} `)));
  assert.ok(check(words, [0, 1, 2], answers.map((w) => w.replace(/[éè]/g, "e"))), `accents facultatifs (${words[accented]})`);
  assert.ok(!check(words, [0, 1, 2], [answers[0], answers[2], answers[1]]), "ordre inversé");
  assert.ok(!check(words, [0, 1, 2], answers.slice(0, 2)), "réponse manquante");
});

function memoryStorage() {
  const m = new Map();
  return { getItem: (k) => (m.has(k) ? m.get(k) : null), setItem: (k, v) => m.set(k, String(v)), removeItem: (k) => m.delete(k), size: () => m.size };
}

test("session : création, verrouillage manuel et automatique, clé effacée, retrait", async () => {
  let now = 1_000_000;
  const storage = memoryStorage();
  const s = new Session(storage, "etl", () => now);
  assert.ok(!s.hasVault() && !s.isUnlocked());
  const key = keys.generateKey();
  const address = keys.address(key, "etl");
  await s.create(key, "phrase de passe solide");
  assert.ok(s.hasVault() && s.isUnlocked());
  assert.equal(s.address(), address);
  assert.ok(storage.getItem(VAULT_KEY).includes('"pbkdf2-sha256"'), "coffre chiffré dans le stockage");
  assert.ok(!storage.getItem(VAULT_KEY).includes(Buffer.from(key).toString("hex")), "aucune clé en clair");
  await assert.rejects(s.create(key, "phrase de passe solide"), /existe déjà/);

  const held = s.key;
  now += AUTO_LOCK_MS - 1;
  assert.equal(s.check(), false, "juste avant 15 minutes");
  s.touch();
  now += AUTO_LOCK_MS - 1;
  assert.equal(s.check(), false, "l'activité repousse le verrouillage");
  now += 1;
  assert.equal(s.check(), true, "15 minutes d'inactivité");
  assert.ok(!s.isUnlocked() && held.every((b) => b === 0), "clé effacée de la mémoire");

  await assert.rejects(s.unlock("mauvaise phrase de passe"));
  await s.unlock("phrase de passe solide");
  assert.equal(keys.address(s.key, "etl"), address);
  const copy = await s.verify("phrase de passe solide");
  assert.equal(keys.address(copy, "etl"), address);
  const k2 = s.key;
  s.lock();
  assert.ok(!s.isUnlocked() && k2.every((b) => b === 0));
  s.forget();
  assert.ok(!s.hasVault() && storage.size() === 0);
  const broken = new Session({ getItem: () => { throw new Error("stockage bloqué"); } }, "etl");
  assert.equal(broken.hasVault(), false, "stockage inaccessible : pas de portefeuille");
});

test("textes : mêmes clés en français et en anglais, variables cohérentes", () => {
  assert.deepEqual(LANGS, ["fr", "en"]);
  const fr = Object.keys(STRINGS.fr).sort();
  assert.deepEqual(Object.keys(STRINGS.en).sort(), fr);
  const vars = (s) => [...s.matchAll(/\{(\w+)\}/g)].map((m) => m[1]).sort();
  for (const k of fr) assert.deepEqual(vars(STRINGS.en[k]), vars(STRINGS.fr[k]), k);
  assert.equal(t("fr", "home.in", { time: "2 h" }), "dans 2 h");
  assert.equal(t("ja", "nav.home"), "Home", "langue non traduite : anglais");
  assert.equal(t("fr", "clé.inconnue"), "clé.inconnue");
  assert.equal(t("en", "home.in"), "in {time}", "variable absente laissée visible");
});

test("api : JSON, montants en BigInt, erreurs du nœud et réseau injoignable", async () => {
  const calls = [];
  const replies = {
    "/api/accounts/etl1qabc": { balance: "150000000", nonce: "2", next_nonce: "4" },
    "/api/params": { params: [{ name: "fee_bps", value: "10" }, { name: "min_fee", value: "10000" }] },
    "/api/accounts/etl1qabc/history": { transactions: [{ amount: "5", fee: "10000" }] },
  };
  const fake = async (path, init) => {
    calls.push({ path, init });
    if (path === "/api/tx") return { ok: false, status: 409, json: async () => ({ error: "nonce incorrect" }) };
    if (path === "/api/status") throw new TypeError("connexion refusée");
    return { ok: true, status: 200, json: async () => replies[path] };
  };
  const api = makeApi(fake);
  const a = await api.account("etl1qabc");
  assert.equal(a.balance, 150_000_000n);
  assert.equal(a.nextNonce, 4n);
  const p = await api.params();
  assert.equal(p.feeBps, 10n);
  assert.equal(p.minFee, 10_000n);
  assert.equal((await api.history("etl1qabc"))[0].fee, 10_000n);
  await assert.rejects(api.submitTx("00"), (e) => e instanceof ApiError && e.status === 409 && e.message === "nonce incorrect");
  const post = calls.find((c) => c.path === "/api/tx").init;
  assert.equal(post.method, "POST");
  assert.equal(post.headers["Content-Type"], "application/json");
  assert.equal(post.body, '{"tx":"00"}');
  await assert.rejects(api.status(), (e) => e instanceof ApiError && e.status === 0);
});

test("import map à jour : chaque module servi a son empreinte, le point d'entrée aussi", () => {
  const page = readFileSync(join(DIR, "index.html"), "utf8");
  assert.equal(page, render(page), "lancer : node web/tools/importmap.js");
  const map = JSON.parse(/<script type="importmap">([^<]*)<\/script>/.exec(page)[1]);
  for (const m of ["/dashboard/app.js", "/dashboard/lib/session.js", "/wallet/vault.js", "/wallet/vendor/noble-secp256k1-3.2.0/index.js", "/i18n/i18n.js"]) {
    assert.match(map.integrity[m] ?? "", /^sha384-/, m);
  }
  assert.ok(!Object.keys(map.integrity).some((m) => m.endsWith(".test.js") || m.includes("/testdata/")));
  assert.match(page, /<script type="module" src="\/dashboard\/app.js" integrity="sha384-[^"]+"><\/script>/);
  assert.equal((page.match(/<script/g) ?? []).length, 2, "aucun autre script dans la page");
});

// Garde-fous de sécurité sur le code de l'interface.
test("interface : imports relatifs seulement, jamais innerHTML ni eval", () => {
  const files = [];
  const walk = (d) => {
    for (const n of readdirSync(d)) {
      const p = join(d, n);
      if (statSync(p).isDirectory()) walk(p);
      else if (n.endsWith(".js") && !n.endsWith(".test.js")) files.push(p);
    }
  };
  walk(DIR);
  assert.ok(files.length >= 8);
  for (const f of files) {
    const src = readFileSync(f, "utf8");
    for (const [, spec] of src.matchAll(/^import .* from "([^"]+)";$/gm)) assert.ok(spec.startsWith("."), `${f} : ${spec}`);
    assert.ok(!/\.innerHTML|outerHTML|insertAdjacentHTML|document\.write|\beval\(|new Function/.test(src), `${f} : construction de HTML interdite`);
  }
});
