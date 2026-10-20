// Lancer : node --test web/
import { test } from "node:test";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { readFileSync, readdirSync } from "node:fs";

const here = (p) => new URL(p, import.meta.url);
const manifest = JSON.parse(readFileSync(here("integrity.json"), "utf8"));

// Toute modification d'un fichier tiers fait échouer ce test : l'empreinte SRI ne correspond plus.
test("les fichiers tiers correspondent à leurs empreintes SRI", () => {
  for (const [path, want] of Object.entries(manifest.files)) {
    const got = "sha384-" + createHash("sha384").update(readFileSync(here(path))).digest("base64");
    assert.equal(got, want, path);
  }
});

// Aucun fichier JavaScript tiers sans empreinte (ajout discret).
test("aucun fichier tiers hors manifeste", () => {
  for (const { dir } of Object.values(manifest.packages)) {
    for (const f of readdirSync(here(dir))) {
      if (f.endsWith(".js")) assert.ok(manifest.files[`${dir}/${f}`], `${dir}/${f} absent de integrity.json`);
    }
  }
});

// Le wallet n'importe que ses propres modules et les fichiers tiers listés (jamais un CDN).
test("imports limités au wallet et aux fichiers tiers vérifiés", () => {
  for (const f of readdirSync(here("."))) {
    if (!f.endsWith(".js") || f.endsWith(".test.js")) continue;
    const src = readFileSync(here(f), "utf8");
    for (const [, spec] of src.matchAll(/^import .* from "([^"]+)";$/gm)) {
      assert.ok(spec.startsWith("./"), `${f} : import externe ${spec}`);
      if (spec.startsWith("./vendor/")) assert.ok(manifest.files[spec.slice(2)], `${f} : ${spec} non vérifié`);
    }
  }
});
