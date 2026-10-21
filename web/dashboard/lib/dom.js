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
