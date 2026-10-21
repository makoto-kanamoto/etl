// Confirmation de la phrase de récupération : l'utilisateur ressaisit 3 mots tirés au hasard,
// pour prouver qu'il l'a notée avant que le coffre soit enregistré.

import { normalize } from "../../wallet/mnemonic.js";

export const CHALLENGE_WORDS = 3;

// challenge tire 3 positions distinctes (0 à 23), triées. random(n) renvoie un entier dans [0, n).
export function challenge(random = cryptoRandom) {
  const picked = new Set();
  while (picked.size < CHALLENGE_WORDS) picked.add(random(24));
  return [...picked].sort((a, b) => a - b);
}

// check compare les réponses aux mots attendus, avec la même tolérance que la restauration
// (majuscules, accents, espaces).
export function check(words, positions, answers) {
  return positions.length === answers.length && positions.every((p, i) => normalize(answers[i].trim()) === normalize(words[p]));
}

function cryptoRandom(n) {
  // Rejet des tirages biaisés : uniforme sur [0, n).
  const limit = Math.floor(0x100000000 / n) * n;
  const buf = new Uint32Array(1);
  do crypto.getRandomValues(buf);
  while (buf[0] >= limit);
  return buf[0] % n;
}
