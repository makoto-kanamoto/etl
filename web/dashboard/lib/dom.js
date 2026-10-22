// Construction d'éléments sans innerHTML : tout texte passe par textContent (aucune injection possible).

// h(tag, attrs, ...enfants) : attrs.class, attrs.on{Event}, attributs ; enfants : nœuds ou chaînes.
export function h(tag, attrs = {}, ...children) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs ?? {})) {
    if (v === undefined || v === null || v === false) continue;
    if (k.startsWith("on") && typeof v === "function") el.addEventListener(k.slice(2).toLowerCase(), v);
    else if (k === "class") el.className = v;
    else if (k === "value") el.value = v;
    else el.setAttribute(k, v === true ? "" : String(v));
  }
  for (const c of children.flat()) {
    if (c === null || c === undefined || c === false) continue;
    el.append(c instanceof Node ? c : document.createTextNode(String(c)));
  }
  return el;
}

// svg(tag, attrs, ...enfants) : comme h, dans l'espace de noms SVG (graphiques, logo, icônes).
const SVG_NS = "http://www.w3.org/2000/svg";
export function svg(tag, attrs = {}, ...children) {
  const el = document.createElementNS(SVG_NS, tag);
  for (const [k, v] of Object.entries(attrs ?? {})) if (v !== undefined && v !== null && v !== false) el.setAttribute(k, String(v));
  for (const c of children.flat()) {
    if (c === null || c === undefined || c === false) continue;
    el.append(c instanceof Node ? c : document.createTextNode(String(c)));
  }
  return el;
}

// legible : le texte d'un graphique SVG garde la même taille à l'écran (px réels) quelle que soit la
// largeur du graphique : la taille en unités du dessin suit le rapport largeur du dessin / largeur
// affichée (variable CSS --fs, recalculée quand le graphique change de taille).
export const CHART_TEXT_PX = 11;
export function legible(el, viewWidth) {
  if (typeof ResizeObserver !== "function") return el;
  new ResizeObserver(([entry]) => {
    const w = entry.contentRect.width;
    if (w > 0) el.style.setProperty("--fs", `${(CHART_TEXT_PX * viewWidth) / w}px`);
  }).observe(el);
  return el;
}

// field : étiquette + champ (+ aide), reliés pour l'accessibilité.
let fieldSeq = 0;
export function field(label, input, help) {
  const id = `f${++fieldSeq}`;
  input.id = id;
  return h("div", { class: "field" }, h("label", { for: id }, label), input, help ? h("p", { class: "help" }, help) : null);
}

// put ajoute des enfants en ignorant null, undefined et false (sections facultatives) : append
// les écrirait en texte (« null »).
export function put(el, ...children) {
  for (const c of children.flat()) {
    if (c === null || c === undefined || c === false) continue;
    el.append(c instanceof Node ? c : document.createTextNode(String(c)));
  }
  return el;
}

export function clear(el) {
  el.replaceChildren();
  return el;
}

// addressHint : exemple d'adresse du réseau du nœud (« etl1q… » sur ETL-FR, « etlt1q… » en test).
export function addressHint(hrp) {
  return `${hrp || "etl"}1q…`;
}

// addressInput : saisie d'une adresse sur deux lignes (l'adresse entière reste visible), avec l'exemple
// du réseau du nœud ; Entrée valide le formulaire au lieu d'aller à la ligne.
export function addressInput(app) {
  const el = h("textarea", { class: "addr-input", rows: 2, autocomplete: "off", spellcheck: "false", autocapitalize: "off", placeholder: addressHint(app.status?.hrp) });
  el.addEventListener("keydown", (e) => {
    if (e.key === "Enter") {
      e.preventDefault();
      el.form?.requestSubmit();
    }
  });
  return el;
}
