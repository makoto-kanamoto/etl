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
import { addressHint, put, h, svg } from "./lib/dom.js";
import { createHash } from "node:crypto";
import { priceChange, simulation, countdown, countdownText, rulePosition, SIM_RATE, SIM_YEARS } from "./views/home.js";
import { render } from "../tools/importmap.js";
import { actions, roleOf, completes, escrowState, reputation, signerRoles } from "./lib/escrowui.js";
import * as esc from "../wallet/escrow.js";
import { allowedRange, readRegistration, RegistrationTextError, REGISTRATION_PREFIX } from "./lib/governance.js";
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

test("textes : mêmes clés en français et en anglais, variables cohérentes", () => {
  assert.deepEqual(LANGS, ["fr", "en"]);
  const fr = Object.keys(STRINGS.fr).sort();
  assert.deepEqual(Object.keys(STRINGS.en).sort(), fr);
  const vars = (s) => [...s.matchAll(/\{(\w+)\}/g)].map((m) => m[1]).sort();
  for (const k of fr) assert.deepEqual(vars(STRINGS.en[k]), vars(STRINGS.fr[k]), k);
  assert.equal(t("fr", "home.ipcPeriod", { period: "2026-Q3" }), "période 2026-Q3");
  assert.equal(t("ja", "nav.home"), "Home", "langue non traduite : anglais");
  assert.equal(t("fr", "clé.inconnue"), "clé.inconnue");
  assert.equal(t("en", "home.ipcPeriod"), "period {period}", "variable absente laissée visible");
});

test("api : JSON, montants en BigInt, erreurs du nœud et réseau injoignable", async () => {
  const calls = [];
  const replies = {
    "/api/accounts/etl1qabc": { balance: "150000000", nonce: "2", next_nonce: "4" },
    "/api/params": { params: [{ name: "fee_bps", value: "10" }, { name: "min_fee", value: "10000" }] },
        "/api/accounts/etl1qabc/history": { transactions: [{ amount: "5", fee: "10000" }], escrow_movements: [{ amount: "50051000", kind: "lock", out: true }] },
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
  for (const l of LANGS) assert.match(t(l, "home.sim.alt"), /^Simulation/, "description accessible : simulation dite d'emblée");
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
