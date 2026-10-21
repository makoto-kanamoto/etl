// Génère l'import map de web/dashboard/index.html : une empreinte SRI (sha384) pour CHAQUE module
// servi (dashboard, wallet, bibliothèques tierces, langues), et l'attribut integrity du point
// d'entrée. Le serveur autorise cette import map seule, par son empreinte, dans la CSP.
//
//   node web/tools/importmap.js           régénère index.html
//   node web/tools/importmap.js --check   échoue si index.html n'est plus à jour

import { createHash } from "node:crypto";
import { readFileSync, readdirSync, statSync, writeFileSync } from "node:fs";
import { join, relative, sep } from "node:path";
import { fileURLToPath } from "node:url";

const WEB = fileURLToPath(new URL("..", import.meta.url));
const PAGE = join(WEB, "dashboard", "index.html");
const SERVED = ["dashboard", "wallet", "i18n"];

function modules(dir) {
  const out = [];
  for (const name of readdirSync(dir).sort()) {
    const p = join(dir, name);
        if (name.startsWith(".")) continue;
    if (statSync(p).isDirectory()) out.push(...modules(p));
    else if (name.endsWith(".js") && !name.endsWith(".test.js")) out.push(p);
  }
  return out;
}

const sri = (p) => "sha384-" + createHash("sha384").update(readFileSync(p)).digest("base64");

// render renvoie la page attendue (import map et integrity du point d'entrée à jour).
export function render(page = readFileSync(PAGE, "utf8")) {
  const integrity = {};
  for (const dir of SERVED) {
    for (const p of modules(join(WEB, dir))) integrity["/" + relative(WEB, p).split(sep).join("/")] = sri(p);
  }
  const map = JSON.stringify({ imports: {}, integrity });
  const entry = integrity["/dashboard/app.js"];
  return page
    .replace(/<script type="importmap">[^<]*<\/script>/, `<script type="importmap">${map}</script>`)
    .replace(/<script type="module" src="\/dashboard\/app.js"[^>]*><\/script>/, `<script type="module" src="/dashboard/app.js" integrity="${entry}"></script>`);
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const current = readFileSync(PAGE, "utf8");
  const expected = render(current);
  if (process.argv.includes("--check")) {
    if (current !== expected) {
      console.error("web/dashboard/index.html n'est pas à jour : lancer « node web/tools/importmap.js »");
      process.exit(1);
    }
  } else if (current !== expected) {
    writeFileSync(PAGE, expected);
    console.log("index.html mis à jour");
  }
}
