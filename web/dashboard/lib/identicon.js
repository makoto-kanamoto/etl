// Motif visuel d'une adresse : une grille 5 × 5 symétrique et une teinte parmi 12, tirées de
// l'empreinte BLAKE3 de l'adresse. Il aide à reconnaître un correspondant d'un coup d'œil ; il ne
// prouve RIEN : une adresse au motif voisin se fabrique sans grand effort. L'adresse complète reste
// affichée avant toute signature.

import { sum } from "../../wallet/keys.js";
import { svg } from "./dom.js";

export const IDENTICON_CONTEXT = "etalon-etl 2026-10 identicon v1";
export const HUES = 12;

const utf8 = new TextEncoder();

// identicon renvoie la teinte (0 à 11) et les 15 cases de la moitié gauche (3 colonnes × 5 lignes,
// colonne par colonne) ; la grille est symétrique. Même motif en majuscules et en minuscules.
export function identicon(address) {
  const h = sum(IDENTICON_CONTEXT, utf8.encode(address.toLowerCase()));
  const bits = (h[0] << 8) | h[1];
  const cells = Array.from({ length: 15 }, (_, i) => ((bits >> i) & 1) === 1);
  if (!cells.some(Boolean)) cells[12] = true; // jamais de motif vide (case centrale)
  return { hue: h[2] % HUES, cells };
}

export function identiconSvg(address, cls = "idc") {
  const { hue, cells } = identicon(address);
  const rects = [];
  cells.forEach((on, i) => {
    if (!on) return;
    const x = Math.floor(i / 5), y = i % 5;
    rects.push(svg("rect", { x: x + 1, y: y + 1, width: 1, height: 1 }));
    if (x < 2) rects.push(svg("rect", { x: 5 - x, y: y + 1, width: 1, height: 1 }));
  });
  return svg("svg", { class: `${cls} idc-h${hue}`, viewBox: "0 0 7 7", "aria-hidden": "true", "shape-rendering": "crispEdges" },
    svg("rect", { class: "idc-bg", width: 7, height: 7 }), svg("g", { class: "idc-fg" }, rects));
}
