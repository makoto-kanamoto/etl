// Logo et icônes, dessinés en SVG (aucune image externe, aucun style en ligne : la CSP les refuse).

import { svg } from "./dom.js";

// Logo « E gradué » : les branches du E sont les traits majeurs d'une règle, deux traits mineurs
// entre elles. Couleur : currentColor.
export function logo(cls = "logo") {
  const r = (x, y, w, hgt, rx) => svg("rect", { x, y, width: w, height: hgt, rx });
  return svg("svg", { class: cls, viewBox: "0 0 32 32", "aria-hidden": "true", fill: "currentColor" },
    r(6, 4, 3.4, 24, 0.6), r(6, 4, 20, 3.4, 0.6), r(6, 14.3, 14, 3.4, 0.6), r(6, 24.6, 20, 3.4, 0.6),
    r(9, 9.6, 6, 1.6, 0.4), r(9, 20.8, 6, 1.6, 0.4));
}

// Icônes de navigation (trait de 1,8 sur une grille de 24).
const PATHS = {
  home: "M3 11l9-7 9 7v9a1 1 0 0 1-1 1h-5v-6h-6v6H4a1 1 0 0 1-1-1z",
  wallet: "M3 7a1 1 0 0 1 1-1h16a1 1 0 0 1 1 1v12a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1zM3 10h18M16 15h2",
  escrow: "M7 7h11l-3-3M17 17H6l3 3",
  ipc: "M4 19V5M4 19h16M8 15l4-4 3 3 5-6",
  operator: "M12 9a3 3 0 1 0 0 6 3 3 0 0 0 0-6zM12 2v3M12 19v3M4.2 4.2l2.1 2.1M17.7 17.7l2.1 2.1M2 12h3M19 12h3M4.2 19.8l2.1-2.1M17.7 6.3l2.1-2.1",
};

export function icon(name) {
  return svg("svg", { class: "icon", viewBox: "0 0 24 24", fill: "none", stroke: "currentColor", "stroke-width": "1.8",
    "stroke-linecap": "round", "stroke-linejoin": "round", "aria-hidden": "true" }, svg("path", { d: PATHS[name] }));
}
