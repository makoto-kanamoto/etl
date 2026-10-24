// Client de l'API locale du nœud. Les montants arrivent en chaînes décimales et sont convertis en BigInt.

export class ApiError extends Error {
  // data : corps de la réponse d'erreur (motif d'une recherche sans résultat, par exemple).
  constructor(status, message, data = null) {
    super(message);
    this.status = status;
    this.data = data;
  }
}

// txOf convertit les montants d'une transaction en BigInt.
const txOf = (tx) => ({ ...tx, amount: BigInt(tx.amount), fee: BigInt(tx.fee) });

// makeApi : fetchImpl injecté pour les tests (fetch du navigateur par défaut).
export function makeApi(fetchImpl = (...a) => fetch(...a)) {
    async function call(method, path, body, token) {
    const init = { method, headers: {}, credentials: "same-origin", cache: "no-store" };
    if (token) init.headers.Authorization = `Bearer ${token}`;
    if (body !== undefined) {
      init.headers["Content-Type"] = "application/json";
      init.body = JSON.stringify(body);
    }
    let res;
    try {
      res = await fetchImpl(path, init);
    } catch {
      throw new ApiError(0, "network");
    }
    let data = null;
    try {
      data = await res.json();
    } catch {
      // corps absent ou illisible
    }
        // Erreur du nœud : le préfixe technique du paquet Go (« blockchain: … ») n'est pas affiché.
    if (!res.ok) throw new ApiError(res.status, (data?.error ?? `HTTP ${res.status}`).replace(/^[a-z]+: /, ""), data);
    return data;
  }

  return {
    status: () => call("GET", "/api/status"),
    params: async () => {
      const p = await call("GET", "/api/params");
      const value = (name) => BigInt(p.params.find((x) => x.name === name)?.value ?? "0");
      return { ...p, feeBps: value("fee_bps"), minFee: value("min_fee") };
    },
    account: async (address) => {
      const a = await call("GET", `/api/accounts/${encodeURIComponent(address)}`);
      return { ...a, balance: BigInt(a.balance), nonce: BigInt(a.nonce), nextNonce: BigInt(a.next_nonce) };
    },
    // history : historique d'une adresse. Nœud avec index (site public) : complet, page par page
    // (before = nextBefore de la page précédente) ; sinon, les blocs conservés.
    history: async (address, before) => {
      const q = before !== undefined && before !== null ? `?before=${before}` : "";
      const h = await call("GET", `/api/accounts/${encodeURIComponent(address)}/history${q}`);
      return {
        transactions: h.transactions.map(txOf),
        escrow: (h.escrow_movements ?? []).map((m) => ({ ...m, amount: BigInt(m.amount) })),
        rewards: (h.rewards ?? []).map((r) => ({ ...r, amount: BigInt(r.amount) })),
        indexed: h.indexed === true,
        nextBefore: h.next_before ?? null,
      };
    },
    // Explorateur.
    blocks: (limit = 20, before) => call("GET", `/api/blocks?limit=${limit}${before !== undefined && before !== null ? `&before=${before}` : ""}`),
    block: async (height) => {
      const b = await call("GET", `/api/blocks/${encodeURIComponent(height)}`);
      return { ...b, txs: (b.txs ?? []).map(txOf), rewardShare: b.reward_share !== undefined ? BigInt(b.reward_share) : null };
    },
    tx: async (id) => {
      const r = await call("GET", `/api/tx/${encodeURIComponent(id)}`);
      return { ...txOf(r.tx), blockTime: r.block_time };
    },
    search: (q) => call("GET", `/api/search?q=${encodeURIComponent(q)}`),
    ipc: () => call("GET", "/api/ipc"),
        submitTx: (hex) => call("POST", "/api/tx", { tx: hex }),
    escrow: async (id) => {
      const e = await call("GET", `/api/escrows/${encodeURIComponent(id)}`);
      return { ...e, amount: BigInt(e.amount), arbiterFee: BigInt(e.arbiter_fee), fee: BigInt(e.fee) };
    },
    submitEscrow: (text) => call("POST", "/api/escrow", { op: text }),
    // Actions de l'opérateur : jeton du fichier ~/.etl/dashboard.token.
    operator: (token) => ({
      cosign: (text) => call("POST", "/api/operator/escrow/cosign", { op: text }, token),
      sign: (text) => call("POST", "/api/operator/escrow/sign", { op: text }, token),
            act: (escrow, kind) => call("POST", "/api/operator/escrow/act", { escrow, kind }, token),
      paramVote: (param, value) => call("POST", "/api/operator/param-vote", { param, value: String(value) }, token),
      exclusionVote: (target) => call("POST", "/api/operator/exclusion-vote", { target }, token),
      ipcRead: () => call("POST", "/api/operator/ipc-read", {}, token),
      registrationRequest: (sponsor) => call("POST", "/api/operator/registration/request", { sponsor }, token),
      registrationSponsor: (text) => call("POST", "/api/operator/registration/sponsor", { registration: text }, token),
      reactivate: () => call("POST", "/api/operator/registration/reactivate", {}, token),
    }),
  };
}
