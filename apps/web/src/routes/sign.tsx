import { useMutation } from "@tanstack/react-query";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Card, CardTitle } from "@/components/ui/card";
import { api } from "@/lib/api";
import { useUi } from "@/store";

export function Sign() {
  const apiKey = useUi((s) => s.apiKey);
  const [keyId, setKeyId] = useState("alice");
  const [message, setMessage] = useState("");
  const sign = useMutation({ mutationFn: () => api.sign(apiKey, keyId, message) });
  return (
    <Card className="max-w-2xl">
      <CardTitle>Custody signing (EIP-191 personal_sign)</CardTitle>
      <p className="mt-1 text-sm text-muted">The gateway forwards to the Rust signer. Private keys never leave that process.</p>
      <div className="mt-4 space-y-3">
        <label className="block text-sm">
          Key ID
          <input
            className="mt-1 block w-full rounded-md border border-line bg-bg px-3 py-2"
            value={keyId}
            onChange={(e) => setKeyId(e.target.value)}
          />
        </label>
        <label className="block text-sm">
          Message
          <textarea
            className="mt-1 block w-full rounded-md border border-line bg-bg px-3 py-2 font-mono"
            rows={3}
            value={message}
            onChange={(e) => setMessage(e.target.value)}
          />
        </label>
        <Button onClick={() => sign.mutate()} disabled={sign.isPending || message === ""}>
          {sign.isPending ? "Signing…" : "Sign message"}
        </Button>
      </div>
      {sign.isError && (
        <p role="alert" className="mt-4 text-danger">
          {sign.error.message}
        </p>
      )}
      {sign.data && (
        <dl className="mt-4 space-y-2 break-all font-mono text-xs" aria-label="Signature result">
          <div>
            <dt className="text-muted">address</dt>
            <dd>{sign.data.address}</dd>
          </div>
          <div>
            <dt className="text-muted">digest</dt>
            <dd>{sign.data.digest}</dd>
          </div>
          <div>
            <dt className="text-muted">signature</dt>
            <dd>{sign.data.signature}</dd>
          </div>
        </dl>
      )}
    </Card>
  );
}
