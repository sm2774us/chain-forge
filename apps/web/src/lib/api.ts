/** Typed client for the ChainForge gateway. */
export interface Block {
  number: number;
  hash: string;
  parentHash: string;
  timestamp: number;
  txCount: number;
}
export interface Upstream {
  url: string;
  state: "closed" | "open" | "half-open";
}
export interface Status {
  head: Block;
  upstreams: Upstream[];
  cache_entries: number;
  webhook_cnt: number;
}
export interface SignResult {
  address: string;
  digest: string;
  signature: string;
}
export interface IntentRequest {
  chain_id: number;
  from: string;
  to: string;
  value: string;
  data: string;
  sign?: { key_id: string };
}
export interface BalanceChange {
  address: string;
  before: string;
  after: string;
  delta: string;
}
export interface SimulationLog {
  address: string;
  topics: string[];
  data: string;
}
export interface Simulation {
  outcome: "success" | "revert" | "halt";
  gas_used: number;
  output: string;
  reason?: string;
  logs: SimulationLog[];
  balance_changes: BalanceChange[];
  elapsed_us: number;
  created_address?: string;
}
export interface UnsignedTx {
  chain_id: number;
  nonce: number;
  gas_price: string;
  gas_limit: number;
  to: string;
  value: string;
  data: string;
}
export interface SignedTx {
  address: string;
  signing_hash: string;
  raw_tx: string;
  tx_hash: string;
}
export interface IntentResult {
  chain: string;
  tx: UnsignedTx;
  simulation: Simulation;
  signed?: SignedTx;
  signing_skipped?: string;
}
export interface StreamEvent {
  type: "block.new" | "block.reorged";
  payload: Block;
}

export const API_BASE: string = import.meta.env.VITE_API_URL ?? "/api";

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

async function request<T>(path: string, apiKey: string, init: RequestInit = {}): Promise<T> {
  const res = await fetch(`${API_BASE}${path}`, {
    ...init,
    headers: { "content-type": "application/json", "x-api-key": apiKey },
  });
  const body = (await res.json().catch(() => ({}))) as { error?: string } & T;
  if (!res.ok) throw new ApiError(res.status, body.error ?? res.statusText);
  return body;
}

export const api = {
  blocks: (key: string, limit = 25) => request<{ blocks: Block[] }>(`/v1/blocks?limit=${limit}`, key).then((r) => r.blocks),
  status: (key: string) => request<Status>("/v1/status", key),
  sign: (key: string, keyId: string, message: string) =>
    request<SignResult>("/v1/sign", key, { method: "POST", body: JSON.stringify({ key_id: keyId, message }) }),
  intent: (key: string, body: IntentRequest) => request<IntentResult>("/v1/intent", key, { method: "POST", body: JSON.stringify(body) }),
  simulate: (key: string, body: IntentRequest) =>
    request<IntentResult>("/v1/simulate", key, { method: "POST", body: JSON.stringify(body) }),
  broadcast: (key: string, rawTx: string) =>
    request<{ tx_id: string }>("/v1/broadcast", key, { method: "POST", body: JSON.stringify({ raw_tx: rawTx }) }),
};

/** SSE URL (EventSource cannot set headers, so the key travels as a query param). */
export const streamUrl = (apiKey: string): string => `${API_BASE}/v1/stream?api_key=${encodeURIComponent(apiKey)}`;
