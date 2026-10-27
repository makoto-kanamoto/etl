// Lancer : node --test web/
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync, readdirSync, statSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { parseETL, formatETL, euroCents, formatEuro, formatRate, AmountError, UNITS } from "./lib/amount.js";
import { challenge, check, CHALLENGE_WORDS } from "./lib/phrase.js";
import { Session, VAULT_KEY, AUTO_LOCK_MS } from "./lib/session.js";
import { STRINGS, LANGS, LANG_NAMES, HTML_LANG, localeOf, t } from "./lib/strings.js";
import { makeApi, ApiError } from "./lib/api.js";
import { addressHint, put, h, svg } from "./lib/dom.js";
import { capitalize } from "./lib/ui.js";
import { inseePage, missingVotes, chartScale } from "./views/ipc.js";
import { createHash } from "node:crypto";
import { identicon, HUES } from "./lib/identicon.js";
import { qrMatrix } from "./lib/qr.js";
import { Contacts, ContactsError, CONTACTS_KEY, Labels, LABELS_KEY, normalizeName, MAX_NAME } from "./lib/contacts.js";
import { lockedInEscrows, timesPaid, short, group, afterBalance, paidText } from "./views/wallet.js";
import { priceChange, simulation, countdown, countdownText, rulePosition, SIM_RATE, SIM_YEARS } from "./views/home.js";
import { render } from "../tools/importmap.js";
import { actions, roleOf, completes, escrowState, reputation, signerRoles, stages, nextStep, deadlineDays } from "./lib/escrowui.js";
import * as esc from "../wallet/escrow.js";
import { allowedRange, readRegistration, RegistrationTextError, REGISTRATION_PREFIX, PARAM_GROUPS, position } from "./lib/governance.js";
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
  assert.equal(formatETL(5n * UNITS, "ja"), "5", "japonais : séparateurs anglais");
  assert.equal(formatETL(1_234_567n * UNITS / 10n, "zh"), "123,456.7", "chinois : séparateurs anglais");
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
  const pos = challenge((n) => (assert.equal(n, 24, "tirage parmi les 24 mots, le dernier compris"), seq[x++]));
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

test("textes : aucune clé définie deux fois (la seconde écraserait la première sans erreur)", () => {
  const src = readFileSync(join(DIR, "lib", "strings.js"), "utf8");
  for (const lang of LANGS) {
    const start = src.indexOf(`  ${lang}: {`);
    const end = src.indexOf("\n  },", start);
    const seen = new Set();
    for (const [, k] of src.slice(start, end).matchAll(/^\s*"([^"]+)":/gm)) {
      assert.ok(!seen.has(k), `${lang} : « ${k} » défini deux fois`);
      seen.add(k);
    }
    assert.equal(seen.size, Object.keys(STRINGS[lang]).length);
  }
});

test("textes : mêmes clés dans les quatre langues, variables cohérentes", () => {
  const fr = Object.keys(STRINGS.fr).sort();
  const vars = (s) => [...s.matchAll(/\{(\w+)\}/g)].map((m) => m[1]).sort();
  assert.deepEqual(LANGS, ["fr", "en", "ja", "zh"]);
  for (const l of LANGS) assert.deepEqual(Object.keys(STRINGS[l]).sort(), fr, `${l} : mêmes clés que le français`);
  for (const l of LANGS) for (const k of fr) assert.deepEqual(vars(STRINGS[l][k]), vars(STRINGS.fr[k]), `${l} : ${k}`);
  for (const l of ["ja", "zh"]) for (const k of fr) {
    if (["app.title", "home.sim.watermark", "home.sim.etl", "nav.ipc", "forget.word", "replace.word", "list.and"].includes(k)) continue;
    assert.ok(/[\u3040-\u30ff\u3400-\u9fff]/.test(STRINGS[l][k]) || /^[\d{}%+ ·.()A-Z-]*$/.test(STRINGS[l][k].replace(/ETL|CPI|SHA-256|UTC|INSEE|NTP|P2P|QR|Bitcoin|Ethereum|Ledger|MetaMask|Étalon|insee\.fr|<Obs>|etl-escrow:|etl-registration:/g, "")), `${l} : « ${k} » non traduit : ${STRINGS[l][k]}`);
  }
  assert.equal(t("fr", "home.ipcPeriod", { period: "2026-Q3" }), "période 2026-Q3");
  assert.equal(t("de", "nav.home"), "Home", "langue non traduite : anglais");
  assert.equal(t("ja", "nav.home"), "ホーム");
  assert.equal(t("zh", "nav.home"), "首页");
  assert.equal(t("fr", "clé.inconnue"), "clé.inconnue");
  assert.equal(t("en", "home.ipcPeriod"), "period {period}", "variable absente laissée visible");
});

test("api : JSON, montants en BigInt, erreurs du nœud et réseau injoignable", async () => {
  const calls = [];
  const replies = {
    "/api/accounts/etl1qabc": { balance: "150000000", nonce: "2", next_nonce: "4" },
    "/api/params": { params: [{ name: "fee_bps", value: "10" }, { name: "min_fee", value: "10000" }] },
        "/api/accounts/etl1qabc/history": { transactions: [{ amount: "5", fee: "10000" }], escrow_movements: [{ amount: "50051000", kind: "lock", out: true }], rewards: [{ height: 7, amount: "100000000" }] },
  };
  const fake = async (path, init) => {
    calls.push({ path, init });
        if (path === "/api/tx") return { ok: false, status: 409, json: async () => ({ error: "mempool: nonce incorrect" }) };
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
    const hist = await api.history("etl1qabc");
  assert.equal(hist.transactions[0].fee, 10_000n);
  assert.equal(hist.escrow[0].amount, 50_051_000n);
  assert.equal(hist.rewards[0].amount, 100_000_000n);
  await assert.rejects(api.submitTx("00"), (e) => e instanceof ApiError && e.status === 409 && e.message === "nonce incorrect");
  const post = calls.find((c) => c.path === "/api/tx").init;
  assert.equal(post.method, "POST");
  assert.equal(post.headers["Content-Type"], "application/json");
  assert.equal(post.body, '{"tx":"00"}');
  await assert.rejects(api.status(), (e) => e instanceof ApiError && e.status === 0);
  await api.operator("jeton-op").paramVote("fee_bps", 12);
  const vote = calls.find((c) => c.path === "/api/operator/param-vote").init;
  assert.equal(vote.headers.Authorization, "Bearer jeton-op", "jeton opérateur transmis");
  assert.equal(vote.body, '{"param":"fee_bps","value":"12"}');
  await assert.rejects(api.submitTx("01"));
  assert.equal(calls.filter((c) => c.path === "/api/tx").at(-1).init.headers.Authorization, undefined, "pas de jeton hors des actions opérateur");
});

test("h, svg et put : null, undefined et false ignorés (jamais écrits en texte)", () => {
  const saved = { Node: globalThis.Node, document: globalThis.document };
  class FakeNode {
    constructor(tag, ns) { Object.assign(this, { tag, ns, attrs: {}, kids: [], on: {} }); }
    setAttribute(k, v) { this.attrs[k] = v; }
    addEventListener(k, f) { this.on[k] = f; }
    append(c) { this.kids.push(c); }
  }
  globalThis.Node = FakeNode;
  globalThis.document = {
    createTextNode: (s) => ({ text: s }),
    createElement: (tag) => new FakeNode(tag),
    createElementNS: (ns, tag) => new FakeNode(tag, ns),
  };
  const texts = (el) => el.kids.map((c) => (c instanceof FakeNode ? c.tag : c.text));
  try {
    const onClick = () => {};
    const b = h("button", { class: "primary", onClick, disabled: false, title: null, "aria-current": "page" }, null, "OK", [undefined, h("span")], false, 0);
    assert.deepEqual(texts(b), ["OK", "span", "0"]);
    assert.equal(b.className, "primary");
    assert.equal(b.on.click, onClick);
    assert.deepEqual(b.attrs, { "aria-current": "page" }, "attributs faux ou nuls omis");
    const s = svg("svg", { viewBox: "0 0 1 1", fill: null }, null, svg("rect"), false, "t");
    assert.equal(s.ns, "http://www.w3.org/2000/svg");
    assert.deepEqual(s.attrs, { viewBox: "0 0 1 1" });
    assert.deepEqual(texts(s), ["rect", "t"]);
    const el = new FakeNode("div");
    put(el, null, undefined, false, "texte", [new FakeNode("p"), null], 0);
    assert.deepEqual(texts(el), ["texte", "p", "0"]);
  } finally {
    Object.assign(globalThis, saved);
  }
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
    if (f.includes("/views/")) assert.ok(!/\.append\(/.test(src), `${f} : utiliser put() (append écrirait « null » pour une section absente)`);
    assert.ok(!/\bstyle:\s|setAttribute\("style"/.test(src), `${f} : attribut style refusé par la CSP (classe CSS ou propriété .style)`);
  }
});

test("échanges : rôle, actions possibles, complément d'une opération reçue", () => {
  const e = { seller: "etl1qs", buyer: "etl1qb", arbiter: "etl1qa", outcome: "pending", seq: 2,
    required: { paid: [2], dispute: [1, 2], release: [1, 6], refund: [2, 5] } };
  assert.equal(roleOf(e, "etl1qs"), esc.ROLE.seller);
  assert.equal(roleOf(e, "etl1qb"), esc.ROLE.buyer);
  assert.equal(roleOf(e, "etl1qa"), esc.ROLE.arbiter);
  assert.equal(roleOf(e, "etl1qx"), 0);
  assert.equal(roleOf({ ...e, arbiter: "" }, ""), 0, "pas d'arbitre : adresse vide jamais reconnue");
  assert.deepEqual(actions(e, esc.ROLE.seller), [
    { kind: "dispute", alone: true }, { kind: "release", alone: true }, { kind: "refund", alone: false, with: esc.ROLE.arbiter }]);
  assert.deepEqual(actions(e, esc.ROLE.buyer), [
    { kind: "paid", alone: true }, { kind: "dispute", alone: true }, { kind: "release", alone: false, with: esc.ROLE.arbiter }, { kind: "refund", alone: true }]);
  assert.deepEqual(actions(e, esc.ROLE.arbiter), [
    { kind: "release", alone: false, with: esc.ROLE.buyer }, { kind: "refund", alone: false, with: esc.ROLE.seller }]);
  assert.deepEqual(actions({ ...e, outcome: "released" }, esc.ROLE.seller), [], "échange réglé : plus rien");
  assert.deepEqual(actions(e, 0), []);
  // Une libération signée par l'acheteur : l'arbitre la complète, le vendeur non (2 sur 3 = acheteur + arbitre).
  const rel = { kind: esc.KIND.release, signers: esc.ROLE.buyer };
  assert.ok(completes(e, rel, esc.ROLE.arbiter));
  assert.ok(!completes(e, rel, esc.ROLE.seller));
  assert.ok(!completes(e, rel, esc.ROLE.buyer), "déjà signée par ce rôle");
  assert.ok(!completes(e, { kind: esc.KIND.refund, signers: esc.ROLE.buyer }, esc.ROLE.buyer), "un rôle ne signe pas deux fois, même pour une opération qu'il peut faire seul");
  assert.ok(completes(e, { kind: esc.KIND.refund, signers: esc.ROLE.arbiter }, esc.ROLE.seller));
  assert.deepEqual(signerRoles(6), ["buyer", "arbiter"]);
  assert.deepEqual(reputation({ reputation: { completed: "3", refunded: "1", disputes_opened: "0", disputes_lost: "2" } }), { completed: 3, refunded: 1, opened: 0, lost: 2 });
  assert.deepEqual(reputation(null), { completed: 0, refunded: 0, opened: 0, lost: 0 });
});

test("échanges : état converti pour la signature, sans arbitre = adresse nulle", () => {
  const a = keys.address(keys.generateKey(), "etl");
  const b = keys.address(keys.generateKey(), "etl");
  const st = escrowState({ seller: a, buyer: b, arbiter: "", seq: 4 }, "etl");
  assert.deepEqual(st.terms.seller, keys.parseAddress(a, "etl"));
  assert.ok(st.terms.arbiter.every((x) => x === 0) && st.terms.arbiter.length === 20);
  assert.equal(st.seq, 4);
});

// Aide aux votes : mêmes règles que blockchain.CheckParamChange (le nœud reste juge).
test("paramètres : valeurs permises pour un vote", () => {
  assert.deepEqual(allowedRange("fee_bps", 10n, 0n, 100n), [5n, 20n], "×2 / ÷2");
  assert.deepEqual(allowedRange("fee_bps", 80n, 0n, 100n), [40n, 100n], "borne haute");
  assert.deepEqual(allowedRange("fee_bps", 0n, 0n, 100n), [0n, 1n], "depuis zéro : jusqu'à 1");
  assert.deepEqual(allowedRange("min_fee", 3n, 0n, 1_000_000n), [1n, 6n], "÷2 arrondi vers le bas");
  assert.deepEqual(allowedRange("max_block_txs", 100_000n, 10_000n, 1_000_000n), [50_000n, 200_000n]);
  assert.deepEqual(allowedRange("target_bits", 21n, 16n, 28n), [19n, 23n], "±2 bits");
  assert.deepEqual(allowedRange("target_bits", 17n, 16n, 28n), [16n, 19n], "borne basse");
  assert.deepEqual(allowedRange("target_bits", 27n, 16n, 28n), [25n, 28n]);
  assert.deepEqual(allowedRange("inactivity_days", 30n, 7n, 365n), [7n, 365n], "sans règle de variation : les bornes");
});

test("parrainage : lecture d'une candidature transmise", () => {
  const cand = keys.generateKey();
  const sponsor = keys.address(keys.generateKey(), "etl");
  const b = new Uint8Array(178);
  b.set(keys.addressHash(keys.publicKey(cand)), 0);
  b.set(keys.parseAddress(sponsor, "etl"), 20);
  new DataView(b.buffer).setBigInt64(40, 20383n);
  b.fill(7, 48, 113); // signature du candidat
  const text = REGISTRATION_PREFIX + Buffer.from(b).toString("base64url");
  const r = readRegistration(" " + text + " ", "etl");
  assert.equal(r.candidate, keys.address(cand, "etl"));
  assert.equal(r.sponsor, sponsor);
  assert.equal(r.day, 20383n);
  assert.equal(r.sponsored, false);
  b.fill(9, 113);
  assert.equal(readRegistration(REGISTRATION_PREFIX + Buffer.from(b).toString("base64url"), "etl").sponsored, true);
  const react = Uint8Array.from(b);
  react.fill(0, 20, 40);
  assert.equal(readRegistration(REGISTRATION_PREFIX + Buffer.from(react).toString("base64url"), "etl").sponsor, "", "réactivation : sans parrain");
  for (const bad of [Buffer.from(b).toString("base64url"), "etl-escrow:" + Buffer.from(b).toString("base64url"),
    REGISTRATION_PREFIX + Buffer.from(b.slice(0, 177)).toString("base64url"), REGISTRATION_PREFIX + "@@@", 42]) {
    assert.throws(() => readRegistration(bad, "etl"), RegistrationTextError, String(bad).slice(0, 30));
  }
});

test("exemple d'adresse : préfixe du réseau du nœud", () => {
  assert.equal(addressHint("etl"), "etl1q…");
  assert.equal(addressHint("etlt"), "etlt1q…");
  assert.equal(addressHint(undefined), "etl1q…");
});

test("accueil : variation des prix depuis le lancement", () => {
  assert.deepEqual(priceChange(103070, 103070, "fr"), { dir: "same", pct: "0" });
  assert.deepEqual(priceChange(105660, 103070, "fr"), { dir: "up", pct: "2,51" });
  assert.deepEqual(priceChange(105660, 103070, "en"), { dir: "up", pct: "2.51" });
  assert.deepEqual(priceChange(102040, 103070, "fr"), { dir: "down", pct: "1,00" });
  assert.deepEqual(priceChange(103071, 103070, "fr"), { dir: "up", pct: "0,00" }, "variation minime : arrondie, mais le sens reste juste");
});

test("accueil : simulation à 2 %/an sur 10 ans, clairement annoncée", () => {
  const s = simulation();
  assert.equal(SIM_RATE, 0.02);
  assert.equal(SIM_YEARS, 10);
  assert.equal(s.length, 11);
  assert.equal(s[0], 100);
  assert.ok(Math.abs(s[10] - 82.03) < 0.01, String(s[10]));
  for (let i = 1; i < s.length; i++) assert.ok(s[i] < s[i - 1]);
  assert.match(t("fr", "home.sim.badge"), /^Simulation — inflation à 2 %\/an$/);
  assert.match(t("en", "home.sim.badge"), /^Simulation/);
  for (const l of LANGS) assert.match(t(l, "home.sim.alt"), /^(Simulation|シミュレーション|模拟)/, `${l} : description accessible : simulation dite d'emblée`);
  for (const l of LANGS) assert.match(t(l, "home.sim.badge"), /(Simulation|シミュレーション|模拟).*2/, `${l} : bandeau de simulation`);
});

test("accueil : compte à rebours et position sur la règle du jour", () => {
  assert.deepEqual(countdown(3 * 3600 + 7 * 60 + 5.9), { h: 3, m: 7, s: 5 });
  assert.deepEqual(countdown(-10), { h: 0, m: 0, s: 0 });
  assert.equal(countdownText((k, v) => t("fr", k, v), 3 * 3600 + 7 * 60 + 5), "3 h 07 min 05 s");
  assert.equal(countdownText((k, v) => t("fr", k, v), 65), "1 min 05 s");
  assert.equal(countdownText((k, v) => t("fr", k, v), 42), "42 s");
  assert.equal(countdownText((k, v) => t("en", k, v), 3 * 3600 + 7 * 60 + 5), "3h 07m 05s");
  const day = Date.UTC(2026, 9, 22) / 1000;
  assert.equal(rulePosition(day), 0);
  assert.equal(rulePosition(day + 23 * 3600), 23 / 24);
  assert.equal(rulePosition(day + 86400 + 6 * 3600), 0.25);
});

test("accueil : chaque critère de santé du réseau a son explication", () => {
  for (const l of LANGS) for (const k of ["peers", "halted", "stale"]) assert.ok(STRINGS[l][`home.issue.${k}`], `${l} : home.issue.${k}`);
  assert.ok(STRINGS.fr["home.clock"].includes("{s}"));
});

test("polices embarquées : empreintes vérifiées, licences présentes, déclarées dans la feuille de style", () => {
  const fonts = join(DIR, "fonts");
  const manifest = JSON.parse(readFileSync(join(fonts, "integrity.json"), "utf8"));
  const css = readFileSync(join(DIR, "style.css"), "utf8");
  const files = readdirSync(fonts).filter((f) => f.endsWith(".woff2")).sort();
  assert.deepEqual(Object.keys(manifest).sort(), files, "chaque police est listée, aucune en trop");
  for (const f of files) {
    const data = readFileSync(join(fonts, f));
    assert.equal(manifest[f].sha384, "sha384-" + createHash("sha384").update(data).digest("base64"), f);
    assert.equal(manifest[f].bytes, data.length);
    assert.ok(css.includes(`url("fonts/${f}")`), `${f} non déclarée dans style.css`);
  }
  for (const l of ["gloock", "public-sans", "ibm-plex-mono"]) assert.match(readFileSync(join(fonts, `LICENSE-${l}.txt`), "utf8"), /SIL Open Font License, Version 1\.1/);
  assert.ok(!/url\(["']?(https?:)?\/\//.test(css), "aucune ressource externe dans la feuille de style");
});

test("logo : icône SVG et PNG 180 px référencées par la page", () => {
  const page = readFileSync(join(DIR, "index.html"), "utf8");
  assert.ok(page.includes('href="/dashboard/logo.svg"') && page.includes('href="/dashboard/icon-180.png"'));
  assert.match(readFileSync(join(DIR, "logo.svg"), "utf8"), /^<svg xmlns="http:\/\/www\.w3\.org\/2000\/svg"/);
  const png = readFileSync(join(DIR, "icon-180.png"));
  assert.equal(png.subarray(1, 4).toString(), "PNG");
  assert.equal(png.readUInt32BE(16), 180);
  assert.equal(png.readUInt32BE(20), 180);
});

const addrOf = (seed, hrp = "etl") => keys.address(new Uint8Array(32).fill(seed), hrp);

test("motif visuel : déterministe, insensible à la casse, varié, jamais vide", () => {
  const a = addrOf(7);
  const m = identicon(a);
  assert.deepEqual(identicon(a.toUpperCase()), m);
  assert.equal(m.cells.length, 15);
  assert.ok(m.hue >= 0 && m.hue < HUES);
  const hues = new Set();
  const shapes = new Set();
  let on = 0;
  for (let i = 1; i <= 200; i++) {
    const x = identicon(addrOf(i));
    hues.add(x.hue);
    shapes.add(x.cells.map(Number).join(""));
    on += x.cells.filter(Boolean).length;
    assert.ok(x.cells.some(Boolean), "jamais de motif vide");
  }
  assert.equal(hues.size, HUES, "toutes les teintes apparaissent");
  assert.ok(shapes.size > 190, `${shapes.size} motifs distincts sur 200`);
  assert.ok(on / 200 > 6 && on / 200 < 9, "environ la moitié des cases remplies");
  // Adresse trouvée par recherche (environ 1 sur 32 768) dont les 15 bits du motif sont nuls.
  const blank = identicon("etl1qqqq8vsgqqqqqqqqqqqqqqqqqqqqqqqqq2ynwfu");
  assert.deepEqual(blank.cells.map(Number).join(""), "000000000000100", "motif nul : case centrale allumée");
});

test("QR code : adresse en majuscules (mode alphanumérique), matrice stable", () => {
  const a = addrOf(7);
  const m = qrMatrix(a);
  assert.deepEqual(qrMatrix(a.toUpperCase()), m);
  assert.equal(m.length, 29, "version 3 (niveau de correction M) pour une adresse etl1q…");
  // Repères de position aux trois coins : carré noir 7 × 7, anneau blanc, centre 3 × 3 noir.
  for (const [ox, oy] of [[0, 0], [22, 0], [0, 22]]) {
    assert.ok(m[oy][ox] && m[oy + 6][ox + 6] && !m[oy + 1][ox + 1] && m[oy + 3][ox + 3]);
  }
  const fp = createHash("sha256").update(m.map((r) => r.map((x) => (x ? 1 : 0)).join("")).join("\n")).digest("hex");
  assert.equal(fp, "f2ef64474f5507084a03aa1adad7e6dc8d23718c5394de114d78137c7b48ad33", "matrice de référence (régression)");
});

test("contacts : chiffrés, liés au portefeuille, altération détectée", async () => {
  const st = memoryStorage();
  const key = new Uint8Array(32).fill(3);
  const owner = keys.address(key, "etl");
  const leo = addrOf(4);
  const c = await Contacts.open(st, key, owner, "etl");
  assert.deepEqual(c.list(), []);
  await c.set(leo.toUpperCase(), "  Léo   (vélo)  ");
  assert.equal(c.name(leo), "Léo (vélo)", "adresse en minuscules, espaces réduits");
  await c.set(addrOf(5), "Boulangerie");
  assert.deepEqual(c.list().map((x) => x.name), ["Boulangerie", "Léo (vélo)"]);
  const raw = st.getItem(CONTACTS_KEY);
  assert.ok(!raw.includes("Boulangerie") && !raw.includes(leo), "ni nom ni adresse en clair");
  assert.equal(JSON.parse(raw).owner, owner);

  const again = await Contacts.open(st, key, owner, "etl");
  assert.equal(again.name(leo), "Léo (vélo)");
  await again.remove(leo);
  assert.equal((await Contacts.open(st, key, owner, "etl")).name(leo), null);

  // Autre portefeuille : ne voit rien. Mauvaise clé ou texte altéré : illisible, remplacé au prochain ajout.
  const otherKey = new Uint8Array(32).fill(9);
  const stranger = await Contacts.open(st, otherKey, keys.address(otherKey, "etl"), "etl");
  assert.deepEqual(stranger.list(), []);
  assert.equal(stranger.unreadable, false, "contacts d'un autre portefeuille : ignorés, pas signalés comme altérés");
  const forged = await Contacts.open(st, otherKey, owner, "etl");
  assert.ok(forged.unreadable && forged.list().length === 0, "clé d'un autre portefeuille");
  const o = JSON.parse(st.getItem(CONTACTS_KEY));
  o.ciphertext = btoa(String.fromCharCode(...Uint8Array.from(atob(o.ciphertext), (ch) => ch.charCodeAt(0)).map((b, i) => (i === 0 ? b ^ 1 : b))));
  st.setItem(CONTACTS_KEY, JSON.stringify(o));
  const tampered = await Contacts.open(st, key, owner, "etl");
  assert.ok(tampered.unreadable);
  await tampered.set(leo, "Léo");
  assert.ok(!tampered.unreadable && (await Contacts.open(st, key, owner, "etl")).name(leo) === "Léo");

  for (const [addr, name, code] of [[owner, "Moi", "self"], ["etl1qfaux", "X", "address"], [addrOf(4, "etlt"), "X", "address"], [leo, " ", "empty"], [leo, "a".repeat(MAX_NAME + 1), "long"], [leo, "a\u200bb", "control"]]) {
    await assert.rejects(tampered.set(addr, name), (e) => e instanceof ContactsError && e.code === code, code);
  }
  assert.equal(normalizeName("e\u0301té"), "été", "NFC");
});

test("session : contacts lisibles déverrouillé seulement, effacés avec le portefeuille", async () => {
  const st = memoryStorage();
  const s = new Session(st, "etl");
  await s.create(new Uint8Array(32).fill(3), "phrase de passe assez longue");
  const book = await s.contacts();
  await book.set(addrOf(4), "Léo");
  assert.equal(await s.contacts(), book, "déchiffrés une fois par session");
  s.lock();
  assert.equal(s.book, null);
  await assert.rejects(s.contacts(), /verrouillé/);
  await s.unlock("phrase de passe assez longue");
  assert.equal((await s.contacts()).name(addrOf(4)), "Léo");
  // « Utiliser mes 24 mots » avec une autre clé : coffre et contacts remplacés.
  const other = new Uint8Array(32).fill(8);
  await s.replace(other, "autre phrase de passe longue");
  assert.equal(s.address(), keys.address(other, "etl"));
  assert.equal(st.getItem(CONTACTS_KEY), null);
  assert.deepEqual((await s.contacts()).list(), []);
  s.forget();
  assert.equal(st.size(), 0, "rien ne reste dans le navigateur");
});

test("portefeuille : montant bloqué, envois déjà faits, adresses abrégées et groupées", () => {
  const me = addrOf(1);
  const e = (seller, outcome, amount, arbiterFee) => ({ seller, outcome, amount, arbiterFee, fee: 999n });
  assert.equal(lockedInEscrows([e(me, "pending", 50n, 5n), e(me, "released", 70n, 0n), e(addrOf(2), "pending", 80n, 0n), e(me, "pending", 10n, 0n)], me), 65n,
    "échanges en cours où je vends ; frais déjà payés non comptés");
  const hist = { transactions: [{ from: me, to: addrOf(2) }, { from: me, to: addrOf(2) }, { from: addrOf(2), to: me }, { from: me, to: addrOf(3) }, { from: addrOf(5), to: addrOf(2) }] };
  assert.equal(timesPaid(hist, me, addrOf(2)), 2);
  assert.equal(timesPaid(hist, me, addrOf(4)), 0);
  assert.equal(timesPaid(null, me, addrOf(2)), 0);
  assert.equal(short("etl1qn3wvn2vj4vrxz7r9qndymx99h3rswprgl0p84g"), "etl1qn3w…p84g");
  assert.equal(group("etl1qn3wvn2v"), "etl1 qn3w vn2v");
});

test("portefeuille : envois déjà faits, au singulier et au pluriel", () => {
  const fr = (k, v) => t("fr", k, v);
  assert.match(paidText(fr, 0), /aucun envoi/);
  assert.match(paidText(fr, 1), /1 envoi vers/);
  assert.match(paidText(fr, 3), /3 envois vers/);
});

test("portefeuille : solde après l'envoi jamais négatif", () => {
  const saved = { Node: globalThis.Node, document: globalThis.document };
  globalThis.Node = class {};
  globalThis.document = { createElement: () => ({ setAttribute() {}, append(c) { this.kids = [...(this.kids ?? []), c]; } }), createTextNode: (s) => ({ text: s }) };
  try {
    const app = { lang: "fr", t: (k, v) => t("fr", k, v) };
    assert.equal(afterBalance(app, 300_000_000n, 150_150_000n).kids[0].text, "1,4985 ETL");
    assert.equal(afterBalance(app, 100n, 100n).kids[0].text, "0 ETL");
    const short = afterBalance(app, 149_850_000n, 150_150_000n);
    assert.equal(short.kids[0].text, t("fr", "send.insufficient"));
    assert.equal(short.className, "short");
  } finally {
    Object.assign(globalThis, saved);
  }
});

// Échange de test : règles « required » telles que les calcule le nœud (escrow.Required).
function deal({ paid = false, disputed = false, outcome = "pending", arbiter = true, late = false } = {}) {
  const S = esc.ROLE.seller, B = esc.ROLE.buyer, A = esc.ROLE.arbiter;
  const required = outcome !== "pending" ? {} : {
    paid: !paid && !late ? [B] : [],
    dispute: disputed ? [] : [S, B],
    release: arbiter ? [S, B | A] : [S],
    refund: [B, ...(arbiter ? [S | A] : []), ...(!paid && late ? [S] : [])],
  };
  return { seller: "etl1qs", buyer: "etl1qb", arbiter: arbiter ? "etl1qa" : "", outcome, paid, disputed, required, deadline: 1_800_000_000 };
}

test("échanges : suivi fidèle au protocole (ouvert → euros payés → réglé, litige)", () => {
  const st = (e) => stages(e).map((x) => `${x.key}:${x.state}`).join(" ");
  assert.equal(st(deal()), "opened:done paid:current settled:todo");
  assert.equal(st(deal({ paid: true })), "opened:done paid:done settled:current");
  assert.equal(st(deal({ disputed: true })), "opened:done paid:todo disputed:alert");
  assert.equal(st(deal({ paid: true, disputed: true })), "opened:done paid:done disputed:alert");
  assert.equal(st(deal({ paid: true, outcome: "released" })), "opened:done paid:done released:done");
  assert.equal(st(deal({ outcome: "refunded" })), "opened:done paid:skipped refunded:done", "remboursé sans paiement déclaré");
});

test("échanges : prochaine action selon le rôle", () => {
  const S = esc.ROLE.seller, B = esc.ROLE.buyer, A = esc.ROLE.arbiter;
  const n = (e, r) => { const x = nextStep(e, r); return `${x.key}${x.mine ? "!" : ""}${x.kind ? ":" + x.kind : ""}`; };
  assert.equal(n(deal(), B), "buyer.pay!:paid");
  assert.equal(n(deal(), S), "wait.payment");
  assert.equal(n(deal(), A), "wait.payment");
  assert.equal(n(deal({ paid: true }), S), "seller.release!:release");
  assert.equal(n(deal({ paid: true }), B), "wait.release");
  assert.equal(n(deal({ late: true }), S), "seller.late!:refund", "échéance passée : le vendeur récupère seul");
  assert.equal(n(deal({ late: true }), B), "buyer.late");
  assert.equal(n(deal({ disputed: true }), A), "arbiter.decide!");
  assert.equal(n(deal({ disputed: true }), B), "wait.arbiter");
  assert.equal(n(deal({ outcome: "released", paid: true }), S), "done.released");
  assert.equal(deadlineDays({ deadline: 1_800_000_000 }, 1_800_000_000 - 86400 * 4.5), 5);
  assert.equal(deadlineDays({ deadline: 1_800_000_000 }, 1_800_000_000 + 10), 0);
  for (const l of LANGS) for (const k of ["done.released", "done.refunded", "arbiter.decide", "wait.arbiter", "buyer.pay", "seller.late", "buyer.late", "wait.payment", "seller.release", "wait.release"]) {
    assert.ok(STRINGS[l][`escrow.next.${k}`], `${l} : escrow.next.${k}`);
  }
});

test("paramètres : chaque paramètre gouvernable dans exactement un groupe ; position sur la règle", () => {
  const all = PARAM_GROUPS.flatMap(([, names]) => names);
  assert.equal(new Set(all).size, all.length, "aucun doublon");
  const named = Object.keys(STRINGS.fr).filter((k) => k.startsWith("params.name.")).map((k) => k.slice(12)).sort();
  assert.deepEqual([...all].sort(), named, "les 12 paramètres, ni plus ni moins");
  for (const l of LANGS) for (const [g] of PARAM_GROUPS) assert.ok(STRINGS[l][`params.group.${g}`]);
  assert.equal(position(50n, 0n, 100n), 50);
  assert.equal(position(16n, 16n, 28n), 0);
  assert.equal(position(28n, 16n, 28n), 100);
  assert.equal(position(8n, 16n, 28n), 0, "hors bornes : ramené au bord");
  assert.equal(position(5n, 5n, 5n), 0);
});

test("phrases : majuscule initiale", () => {
  assert.equal(capitalize("l'arbitre demande"), "L'arbitre demande");
  assert.equal(capitalize("école"), "École");
  assert.equal(capitalize(""), "");
});

test("libellés d'échanges : chiffrés, liés au portefeuille, identifiant vérifié", async () => {
  const st = memoryStorage();
  const key = new Uint8Array(32).fill(3);
  const owner = keys.address(key, "etl");
  const id = "ab".repeat(32);
  const l = await Labels.open(st, key, owner, "etl");
  await l.put(id.toUpperCase(), "Vélo de ville");
  assert.equal(l.get(id), "Vélo de ville");
  assert.ok(!st.getItem(LABELS_KEY).includes("Vélo"));
  assert.equal(st.getItem(CONTACTS_KEY), null, "enregistrement séparé des contacts");
  assert.equal((await Labels.open(st, key, owner, "etl")).get(id), "Vélo de ville");
  await assert.rejects(l.put("xyz", "a"), (e) => e.code === "id");
  await l.delete(id);
  assert.equal((await Labels.open(st, key, owner, "etl")).get(id), null);
  // Clés de chiffrement distinctes : un enregistrement de libellés (même vide) recopié à la place des
  // contacts est illisible ; avec la même clé, il se lirait comme une liste de contacts vide.
  st.setItem(CONTACTS_KEY, st.getItem(LABELS_KEY));
  assert.ok((await Contacts.open(st, key, owner, "etl")).unreadable);
  const s = new Session(memoryStorage(), "etl");
  await s.create(key, "phrase de passe assez longue");
  await (await s.labels()).put(id, "Cours");
  s.lock();
  assert.equal(s.tags, null, "libellés effacés de la mémoire au verrouillage");
  await assert.rejects(s.labels(), /verrouillé/);
  s.forget();
  assert.equal(s.storage.size(), 0, "libellés retirés avec le portefeuille");
});

test("IPC : page INSEE de la série lue, voix manquantes, échelle de la courbe", () => {
  assert.equal(inseePage("https://api.insee.fr/series/BDM/V1/data/SERIES_BDM/011814630"), "https://www.insee.fr/fr/statistiques/serie/011814630");
  assert.equal(inseePage("https://exemple.org/ipc"), null, "autre source : pas de lien inventé");
  assert.equal(inseePage("https://api.insee.fr/series/BDM/V1/data/SERIES_BDM/011814630?x=1"), null);
  assert.equal(inseePage(undefined), null);
  assert.equal(missingVotes(26, 29), 3);
  assert.equal(missingVotes(30, 29), 0);
  assert.deepEqual(chartScale([103070]), [102000, 104000], "une seule valeur : axe d'au moins un point de part et d'autre");
  const [lo, hi] = chartScale([100300, 105660]);
  assert.ok(lo <= 100300 - 500 && hi >= 105660 + 500 && lo % 1000 === 0 && hi % 1000 === 0);
});

test("langues : nom dans la langue, conventions de dates, attribut lang de la page", () => {
  assert.deepEqual(Object.keys(LANG_NAMES), LANGS);
  assert.deepEqual(Object.values(LANG_NAMES), ["Français", "English", "日本語", "中文"]);
  assert.equal(localeOf("fr"), "fr-FR");
  assert.equal(localeOf("ja"), "ja-JP");
  assert.equal(localeOf("zh"), "zh-CN");
  assert.equal(localeOf("de"), "en-GB", "autre langue : conventions anglaises");
  assert.equal(HTML_LANG.zh, "zh-Hans", "chinois simplifié : formes de caractères correctes");
  assert.equal(HTML_LANG.ja, "ja");
  assert.equal(t("fr", "list.and"), " et ");
  assert.equal(t("ja", "list.and"), "と");
  assert.equal(t("zh", "list.and"), "和");
});

// --- site public (etalon-etl.com) ---

import { parseRoute, route, searchTarget, ago, docURL, docFor, DOC_KEYS, DOC_LANGS, REPO, NAVS, modeOf, POLL_MS, shortHex } from "./lib/site.js";
import { feedRows, weave, MARK } from "./lib/feed.js";
import { timeline, counts, PAGE } from "./views/explorer.js";
import { existsSync } from "node:fs";

test("site public : mode et navigation (opérateur absent ; avant le lancement, accueil et rejoindre)", () => {
  assert.equal(modeOf(null), "dashboard");
  assert.equal(modeOf({ public: false }), "dashboard");
  assert.equal(modeOf({ public: true, launched: true }), "public");
  assert.equal(modeOf({ public: true }), "public");
  assert.equal(modeOf({ public: true, launched: false }), "prelaunch");
  for (const v of [1, "true", {}]) assert.equal(modeOf({ public: v }), "dashboard", "statut mal formé : jamais le site public");
  const pages = (m) => NAVS[m].map(([r]) => r);
  assert.deepEqual(pages("dashboard"), ["", "wallet", "escrow", "ipc", "operator"]);
  assert.deepEqual(pages("public"), ["", "wallet", "escrow", "explorer", "ipc", "join"]);
  assert.deepEqual(pages("prelaunch"), ["", "join"]);
  for (const m of Object.keys(NAVS)) for (const [, , key] of NAVS[m]) for (const l of LANGS) assert.ok(STRINGS[l][key], `${l} : ${key}`);
  assert.equal(POLL_MS, 30_000, "D4 : relecture toutes les 30 secondes");
});

test("site public : routes de l'explorateur, aller et retour", () => {
  assert.deepEqual(parseRoute(""), { page: "", params: [] });
  assert.deepEqual(parseRoute("#/"), { page: "", params: [] });
  assert.deepEqual(parseRoute("#/join"), { page: "join", params: [] });
  assert.deepEqual(parseRoute("#/explorer/block/42"), { page: "explorer", params: ["block", "42"] });
  assert.deepEqual(parseRoute("#/explorer/"), { page: "explorer", params: [] });
  assert.deepEqual(parseRoute("#/explorer/search/%E2%80%A6%25zz"), { page: "explorer", params: ["search", "…%zz"] });
  assert.deepEqual(parseRoute("#/explorer/search/%E0%A4%A"), { page: "explorer", params: ["search", "%E0%A4%A"] }, "encodage invalide : tel quel");
  const q = "a b/c?d#e";
  assert.deepEqual(parseRoute(route.search(q)).params, ["search", q]);
  assert.deepEqual(parseRoute(route.address("etl1qabc")).params, ["address", "etl1qabc"]);
  assert.equal(route.block(0), "#/explorer/block/0");
  assert.equal(searchTarget({ type: "block", height: 7 }), "#/explorer/block/7");
  assert.equal(searchTarget({ type: "address", address: "etl1qx" }), "#/explorer/address/etl1qx");
  assert.equal(searchTarget({ type: "tx", id: "ab", height: 3 }), "#/explorer/tx/ab");
  assert.equal(searchTarget({ type: "escrow", id: "cd" }), "#/explorer/escrow/cd");
  assert.equal(searchTarget({ type: "autre" }), null);
  assert.equal(searchTarget(null), null);
  assert.equal(shortHex("0123456789abcdef".repeat(4)), "0123456789…abcdef");
  assert.equal(PAGE, 20);
});

test("site public : « il y a … » en minutes, heures, puis jours", () => {
  const tt = (key, vars) => t("fr", key, vars);
  const now = 1_800_000_000;
  assert.equal(ago(tt, now, now), "il y a 1 min");
  assert.equal(ago(tt, now + 60, now), "il y a 1 min", "heure future (horloge en retard) : jamais négatif");
  assert.equal(ago(tt, now + 7200, now), "il y a 1 min");
  assert.equal(ago(tt, now - 59 * 60, now), "il y a 59 min");
  assert.equal(ago(tt, now - 3600, now), "il y a 1 h");
  assert.equal(ago(tt, now - 17 * 3600 - 59, now), "il y a 17 h");
  assert.equal(ago(tt, now - (2 * 86400 - 1), now), "il y a 47 h");
  assert.equal(ago(tt, now - 2 * 86400, now), "il y a 2 jours");
});

test("site public : chaque lien de documentation pointe vers un fichier du dépôt (P2)", () => {
  const root = join(DIR, "..", "..");
  let n = 0;
  for (const d of DOC_KEYS) {
    for (const l of DOC_LANGS) {
      const url = docURL(d, l);
      if (!url) continue;
      n++;
      assert.ok(url.startsWith(`${REPO}/blob/main/`), url);
      assert.ok(existsSync(join(root, url.slice(`${REPO}/blob/main/`.length))), `fichier absent : ${url}`);
    }
  }
  assert.equal(n, 11, "4 + 4 manifestes, 2 guides, 1 architecture");
  assert.equal(docURL("guide", "ja"), null);
  assert.match(docFor("guide", "ja"), /OPERATOR_GUIDE\.md$/, "repli : anglais");
  assert.match(docFor("architecture", "en"), /ARCHITECTURE\.md$/, "repli : français");
  assert.match(docFor("short", "zh"), /zh\/SHORT_MANIFESTO\.md$/);
  assert.equal(REPO, "https://github.com/makoto-kanamoto/etl");
});

test("historique : lignes triées du bloc le plus récent au plus ancien", () => {
  const me = "etl1qme", other = "etl1qother";
  const rows = feedRows({
    transactions: [{ id: "t1", from: me, to: other, amount: 5n, fee: 1n, height: 3 }, { id: "t2", from: other, to: me, amount: 7n, fee: 2n, height: 9 }],
    escrow: [{ height: 5, escrow: "e1", kind: "lock", amount: 4n, out: true }],
    rewards: [{ height: 7, amount: 1n }],
  }, me);
  assert.deepEqual(rows.map((r) => [r.height, r.kind]), [[9, "in"], [7, "reward"], [5, "lock"], [3, "out"]]);
  assert.deepEqual(rows.map((r) => r.peer), [other, undefined, undefined, other]);
  assert.equal(rows[0].fee, 0n, "frais d'un transfert reçu : payés par l'expéditeur");
  assert.equal(rows[3].fee, 1n);
  assert.equal(rows[0].tx, "t2");
  assert.equal(rows[2].escrow, "e1");
  assert.equal(rows[2].escrowKind, "lock");
});

test("historique : le lien vers l'adresse respecte l'ordre des mots de chaque langue", () => {
  const node = { node: true };
  for (const l of LANGS) {
    const parts = weave(t(l, "activity.from", { who: MARK }), node);
    assert.equal(parts.length, 3, l);
    assert.equal(parts[1], node);
    assert.equal(parts[0] + "X" + parts[2], t(l, "activity.from", { who: "X" }), l);
  }
  assert.deepEqual(weave("sans marque", node), ["sans marque"]);
});

test("explorateur : étapes d'un échange (frise)", () => {
  const step = (kind, height) => ({ kind, height, time: 0, signers: 1 });
  const keys = (e) => timeline(e).map((s) => `${s.key}${s.step ? "@" + s.step.height : ""}${s.alert ? "!" : ""}`).join(" ");
  assert.equal(keys({ outcome: "pending", history: [step("open", 2)] }), "opened@2 paid settled");
  assert.equal(keys({ outcome: "pending", history: [step("open", 2), step("paid", 3)] }), "opened@2 paid@3 settled");
  assert.equal(keys({ outcome: "released", history: [step("open", 2), step("paid", 3), step("release", 4)] }), "opened@2 paid@3 released@4");
  assert.equal(keys({ outcome: "released", history: [step("open", 2), step("release", 4)] }), "opened@2 released@4", "libéré sans « payé »");
  assert.equal(keys({ outcome: "refunded", history: [step("open", 2), step("paid", 3), step("dispute", 4), step("refund", 6)] }), "opened@2 paid@3 disputed@4! refunded@6");
  assert.equal(keys({ outcome: "pending" }), "opened paid settled", "nœud sans index : étapes sans bloc");
  for (const l of LANGS) for (const k of ["opened", "paid", "settled", "released", "refunded", "disputed"]) assert.ok(STRINGS[l][`escrow.stage.${k}`], `${l} : ${k}`);
});

test("explorateur : contenu résumé d'un bloc, motifs des recherches sans résultat traduits", () => {
  const tt = (key, vars) => t("fr", key, vars);
  assert.deepEqual(counts(tt, { txs: 3, escrow_ops: 1, participants: 42 }), ["3 tx", "1 éch.", "42 part."]);
  assert.deepEqual(counts(tt, undefined), ["contenu élagué"]);
  for (const l of LANGS) {
    for (const k of ["short", "unknown", "pending", "test", "other", "future", "nextBlock", "unrecognized", "title", "can"]) assert.ok(STRINGS[l][`explorer.nf.${k}`], `${l} : explorer.nf.${k}`);
    for (const k of ["open", "paid", "dispute", "release", "refund"]) assert.ok(STRINGS[l][`explorer.op.${k}`], `${l} : explorer.op.${k}`);
    for (const k of ["ipc", "premine", "member"]) assert.ok(STRINGS[l][`site.pillar.${k}.title`] && STRINGS[l][`site.pillar.${k}.text`], `${l} : ${k}`);
    for (const d of DOC_KEYS) assert.ok(STRINGS[l][`join.doc.${d}`], `${l} : join.doc.${d}`);
  }
  assert.match(t("fr", "explorer.nf.short", { n: 10 }), /64 .* 10/);
  assert.match(t("fr", "wallet.webWarning"), /propre nœud/, "P3");
});
