// Lancer : node --test web/
import { test } from "node:test";
import assert from "node:assert/strict";
import { parseLang, detectLang, FALLBACK } from "./i18n.js";

test("parseLang reconnaît les langues supportées", () => {
  assert.equal(parseLang("fr-FR"), "fr");
  assert.equal(parseLang("fr"), "fr");
  assert.equal(parseLang("EN-us"), "en");
  assert.equal(parseLang("ja-JP"), "ja");
  assert.equal(parseLang("zh-Hans-CN"), "zh");
  assert.equal(parseLang("zh-TW"), "zh");
  assert.equal(parseLang("fr_FR"), "fr");
});

test("parseLang renvoie null sinon", () => {
  assert.equal(parseLang("de-DE"), null);
  assert.equal(parseLang(""), null);
  assert.equal(parseLang(undefined), null);
  assert.equal(parseLang(42), null);
});

test("detectLang prend la première préférence supportée", () => {
  assert.equal(detectLang({ languages: ["de-DE", "ja-JP", "fr-FR"], language: "de-DE" }), "ja");
});

test("detectLang utilise navigator.language si languages est absent", () => {
  assert.equal(detectLang({ language: "zh-CN" }), "zh");
});

test("detectLang replie sur EN", () => {
  assert.equal(detectLang({ languages: ["de-DE", "es-ES"], language: "de-DE" }), FALLBACK);
  assert.equal(detectLang({}), FALLBACK);
  assert.equal(detectLang(undefined), FALLBACK);
});
