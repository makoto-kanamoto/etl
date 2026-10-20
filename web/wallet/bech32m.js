// Encodage bech32m (BIP-350), implémenté ici plutôt qu'importé : même algorithme, mêmes refus que
// internal/crypto/bech32m.go, couvert par les vecteurs officiels du BIP.

const CHARSET = "qpzry9x8gf2tvdw0s3jn54khce6mua7l";
const BECH32M_CONST = 0x2bc830a3;
const MAX_LENGTH = 90;
const CHECKSUM_N = 6;

export class Bech32mError extends Error {
  constructor() {
    super("bech32m : chaîne invalide");
  }
}

function polymod(values) {
  const gen = [0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3];
  let chk = 1;
  for (const v of values) {
    const top = chk >>> 25;
    chk = (((chk & 0x1ffffff) << 5) ^ v) >>> 0;
    for (let i = 0; i < 5; i++) {
      if ((top >>> i) & 1) chk = (chk ^ gen[i]) >>> 0;
    }
  }
  return chk;
}

function hrpExpand(hrp) {
  const out = [];
  for (let i = 0; i < hrp.length; i++) out.push(hrp.charCodeAt(i) >> 5);
  out.push(0);
  for (let i = 0; i < hrp.length; i++) out.push(hrp.charCodeAt(i) & 31);
  return out;
}

function validHRP(hrp) {
  if (hrp.length < 1 || hrp.length > 83) return false;
  for (let i = 0; i < hrp.length; i++) {
    const c = hrp.charCodeAt(i);
    if (c < 33 || c > 126) return false;
  }
  return true;
}

// encode encode des valeurs 5 bits. hrp doit être en minuscules.
export function encode(hrp, data) {
  if (!validHRP(hrp) || hrp !== hrp.toLowerCase()) throw new Bech32mError();
  if (hrp.length + 1 + data.length + CHECKSUM_N > MAX_LENGTH) throw new Bech32mError();
  const mod = polymod([...hrpExpand(hrp), ...data, 0, 0, 0, 0, 0, 0]) ^ BECH32M_CONST;
  let s = hrp + "1";
  for (const d of data) s += CHARSET[d];
  for (let i = 0; i < CHECKSUM_N; i++) s += CHARSET[(mod >>> (5 * (5 - i))) & 31];
  return s;
}

// decode décode une chaîne bech32m : { hrp (minuscules), data (valeurs 5 bits) }.
// Refuse la casse mixte, les caractères hors alphabet et les checksums bech32 (non m).
export function decode(s) {
  if (typeof s !== "string" || s.length > MAX_LENGTH) throw new Bech32mError();
  const lower = s.toLowerCase();
  if (s !== lower && s !== s.toUpperCase()) throw new Bech32mError();
  s = lower;
  const pos = s.lastIndexOf("1");
  if (pos < 1 || pos + 1 + CHECKSUM_N > s.length) throw new Bech32mError();
  const hrp = s.slice(0, pos);
  if (!validHRP(hrp)) throw new Bech32mError();
  const data = [];
  for (let i = pos + 1; i < s.length; i++) {
    const v = CHARSET.indexOf(s[i]);
    if (v < 0) throw new Bech32mError();
    data.push(v);
  }
  if (polymod([...hrpExpand(hrp), ...data]) !== BECH32M_CONST) throw new Bech32mError();
  return { hrp, data: data.slice(0, data.length - CHECKSUM_N) };
}

// convertBits regroupe des valeurs de fromBits bits en valeurs de toBits bits.
// Sans pad (décodage), refuse un reliquat ≥ fromBits bits ou non nul.
export function convertBits(data, fromBits, toBits, pad) {
  let acc = 0;
  let bits = 0;
  const maxv = (1 << toBits) - 1;
  const out = [];
  for (const v of data) {
    if (v >>> fromBits !== 0) throw new Bech32mError();
    acc = ((acc << fromBits) | v) & 0xffffff; // seuls les derniers bits servent
    bits += fromBits;
    while (bits >= toBits) {
      bits -= toBits;
      out.push((acc >>> bits) & maxv);
    }
  }
  if (pad) {
    if (bits > 0) out.push((acc << (toBits - bits)) & maxv);
  } else if (bits >= fromBits || ((acc << (toBits - bits)) & maxv) !== 0) {
    throw new Bech32mError();
  }
  return out;
}
