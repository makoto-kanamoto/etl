// Client de l'API locale du nœud. Les montants arrivent en chaînes décimales et sont convertis en BigInt.

export class ApiError extends Error {
  constructor(status, message) {
    super(message);
    this.status = status;
  }
}

// makeApi : fetchImpl injecté pour les tests (fetch du navigateur par défaut).
export function makeApi(fetchImpl = (...a) => fetch(...a)) {
  async function call(method, path, body) {
    const init = { method, headers: {}, credentials: "same-origin", cache: "no-store" };
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
    if (!res.ok) throw new ApiError(res.status, data?.error ?? `HTTP ${res.status}`);
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
    history: async (address) => {
      const h = await call("GET", `/api/accounts/${encodeURIComponent(address)}/history`);
      return h.transactions.map((tx) => ({ ...tx, amount: BigInt(tx.amount), fee: BigInt(tx.fee) }));
    },
    submitTx: (hex) => call("POST", "/api/tx", { tx: hex }),
  };
}
