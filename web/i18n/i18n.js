// Détection de la langue du visiteur — partagé par le dashboard (localhost:3001) et etalon-etl.fr.
// Langues supportées : fr, en, ja, zh. Repli : en.
// Aucune dépendance : rien à charger depuis un CDN, donc rien à vérifier par SRI.

export const SUPPORTED = ["fr", "en", "ja", "zh"];
export const FALLBACK = "en";

// Extrait la langue d'une étiquette BCP 47 (« fr-FR », « zh-Hans-CN ») ; null si non supportée.
export function parseLang(tag) {
  if (typeof tag !== "string") return null;
  const base = tag.toLowerCase().split(/[-_]/)[0];
  return SUPPORTED.includes(base) ? base : null;
}

// Renvoie la première langue supportée parmi les préférences du navigateur, ou FALLBACK.
// nav est injecté pour les tests ; par défaut, le navigator du navigateur.
export function detectLang(nav = globalThis.navigator) {
  const prefs = [...(nav?.languages ?? []), nav?.language];
  for (const tag of prefs) {
    const lang = parseLang(tag);
    if (lang) return lang;
  }
  return FALLBACK;
}
