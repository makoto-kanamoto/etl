// QR code d'une adresse (affichage seulement : aucune lecture par la caméra).
//
// L'adresse est codée en MAJUSCULES : le mode alphanumérique du QR donne un code plus petit, donc
// plus facile à lire. Les adresses bech32m sont acceptées tout en majuscules (Go et JS), jamais en
// casse mixte.

import { encode } from "../../wallet/vendor/uqr-0.1.3/index.js";
import { svg } from "./dom.js";

export const QUIET_ZONE = 4; // marge blanche exigée autour du code (modules)

// qrMatrix renvoie la matrice des modules (true = noir), sans marge.
export function qrMatrix(address) {
  return encode(address.toUpperCase(), { ecc: "M", border: 0 }).data;
}

// qrSvg dessine le code : un seul chemin pour les modules noirs, sur fond blanc dans les deux thèmes
// (un lecteur de QR attend du sombre sur clair).
export function qrSvg(address, label) {
  const m = qrMatrix(address);
  const n = m.length + 2 * QUIET_ZONE;
  let d = "";
  m.forEach((row, y) => row.forEach((dark, x) => {
    if (dark) d += `M${x + QUIET_ZONE} ${y + QUIET_ZONE}h1v1h-1z`;
  }));
  return svg("svg", { class: "qr", viewBox: `0 0 ${n} ${n}`, role: "img", "aria-label": label, "shape-rendering": "crispEdges" },
    svg("rect", { width: n, height: n, fill: "#FFFFFF" }),
    svg("path", { d, fill: "#000000" }));
}
