import { useMutation } from "@tanstack/react-query";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Badge, Card, CardTitle } from "@/components/ui/card";
import { api, type IntentRequest, type IntentResult } from "@/lib/api";
import { micros, short, signed } from "@/lib/utils";
import { useUi } from "@/store";

const outcomeTone = { success: "ok", revert: "bad", halt: "bad" } as const;

const field = "mt-1 block w-full rounded-md border border-line bg-bg px-3 py-2 font-mono text-xs";

function Broadcast({ raw }: { raw: string }) {
  const apiKey = useUi((s) => s.apiKey);
  const send = useMutation({ mutationFn: () => api.broadcast(apiKey, raw) });
  return (
    <>
      <Button onClick={() => send.mutate()} disabled={send.isPending || send.isSuccess}>
        {send.isPending ? "Broadcasting…" : "Broadcast privately"}
      </Button>
      {send.isSuccess && (
        <p role="status" className="font-mono text-xs">
          submitted via private relay: {send.data.tx_id}
        </p>
      )}
      {send.isError && (
        <p role="alert" className="text-danger">
          {send.error.message}
        </p>
      )}
    </>
  );
}

/**
 * Intent → prediction → (optional custodial signature) → private broadcast.
 * The page never touches a chain or a key: it sends one intent to the gateway,
 * which simulates in the Rust engine before anything can be signed or sent.
 */
export function Simulate() {
  const apiKey = useUi((s) => s.apiKey);
  const [from, setFrom] = useState("0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266");
  const [to, setTo] = useState("0x70997970C51812dc3A010C7d01b50e0d17dc79C8");
  const [value, setValue] = useState("1000");
  const [data, setData] = useState("0x");
  const [keyId, setKeyId] = useState("alice");

  const body = (sign: boolean): IntentRequest => ({
    chain_id: 1337,
    from,
    to,
    value,
    data,
    ...(sign ? { sign: { key_id: keyId } } : {}),
  });
  const run = useMutation<IntentResult, Error, boolean>({
    mutationFn: (sign) => (sign ? api.intent(apiKey, body(true)) : api.simulate(apiKey, body(false))),
  });
  const res = run.data;

  return (
    <div className="grid gap-6 lg:grid-cols-2">
      <Card>
        <CardTitle>Intent (EVM)</CardTitle>
        <p className="mt-1 text-sm text-muted">Simulated in the Rust engine (revm) before anything is signed or sent.</p>
        <div className="mt-4 space-y-3">
          <label className="block text-sm">
            From
            <input className={field} value={from} onChange={(e) => setFrom(e.target.value)} />
          </label>
          <label className="block text-sm">
            To
            <input className={field} value={to} onChange={(e) => setTo(e.target.value)} />
          </label>
          <label className="block text-sm">
            Value (wei)
            <input className={field} value={value} onChange={(e) => setValue(e.target.value)} />
          </label>
          <label className="block text-sm">
            Calldata
            <input className={field} value={data} onChange={(e) => setData(e.target.value)} />
          </label>
          <label className="block text-sm">
            Custody key
            <input className={field} value={keyId} onChange={(e) => setKeyId(e.target.value)} />
          </label>
          <div className="flex flex-wrap gap-3">
            <Button onClick={() => run.mutate(false)} disabled={run.isPending}>
              {run.isPending ? "Simulating…" : "Simulate"}
            </Button>
            <Button variant="outline" onClick={() => run.mutate(true)} disabled={run.isPending}>
              Simulate &amp; sign
            </Button>
          </div>
        </div>
        {run.isError && (
          <p role="alert" className="mt-4 text-danger">
            {run.error.message}
          </p>
        )}
      </Card>

      <Card aria-label="Simulation result">
        <div className="flex items-center justify-between">
          <CardTitle>Predicted outcome</CardTitle>
          {res && <Badge tone={outcomeTone[res.simulation.outcome]}>{res.simulation.outcome}</Badge>}
        </div>
        {!res && <p className="mt-3 text-sm text-muted">Run a simulation to see gas, state changes and reverts.</p>}
        {res && (
          <div className="mt-3 space-y-4 text-sm">
            <dl className="grid grid-cols-3 gap-3">
              <div>
                <dt className="text-xs text-muted">gas used</dt>
                <dd className="font-mono">{res.simulation.gas_used.toLocaleString()}</dd>
              </div>
              <div>
                <dt className="text-xs text-muted">engine time</dt>
                <dd className="font-mono">{micros(res.simulation.elapsed_us)}</dd>
              </div>
              <div>
                <dt className="text-xs text-muted">logs</dt>
                <dd className="font-mono">{res.simulation.logs.length}</dd>
              </div>
            </dl>
            {res.simulation.reason && <p role="status">Revert reason: {res.simulation.reason}</p>}
            <table className="w-full text-left text-xs" aria-label="Balance changes">
              <thead className="uppercase text-muted">
                <tr>
                  <th className="pb-1 font-medium">Account</th>
                  <th className="pb-1 font-medium">Δ wei</th>
                </tr>
              </thead>
              <tbody>
                {res.simulation.balance_changes.map((c) => (
                  <tr key={c.address} className="border-t border-line">
                    <td className="py-1 font-mono">{short(c.address, 8, 4)}</td>
                    <td className={c.delta.startsWith("-") ? "py-1 font-mono text-danger" : "py-1 font-mono text-accent"}>
                      {signed(c.delta)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
            <p className="font-mono text-xs text-muted">
              tx: nonce {res.tx.nonce} · gas limit {res.tx.gas_limit.toLocaleString()} · gas price {res.tx.gas_price}
            </p>
            {res.signing_skipped && <p role="status">{res.signing_skipped}</p>}
            {res.signed && (
              <div className="space-y-2">
                <dl className="break-all font-mono text-xs" aria-label="Signed transaction">
                  <dt className="text-muted">signed by</dt>
                  <dd>{res.signed.address}</dd>
                  <dt className="text-muted">tx hash</dt>
                  <dd>{res.signed.tx_hash}</dd>
                </dl>
                <Broadcast raw={res.signed.raw_tx} />
              </div>
            )}
          </div>
        )}
      </Card>
    </div>
  );
}
