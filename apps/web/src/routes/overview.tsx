import { BlockTable } from "@/components/block-table";
import { StatusCards } from "@/components/status-cards";
import { Badge, Card, CardTitle } from "@/components/ui/card";
import { useBlocks, useBlockStream, useStatus } from "@/hooks";

const streamTone = { live: "ok", connecting: "warn", offline: "bad" } as const;

export function Overview() {
  const status = useStatus();
  const blocks = useBlocks(10);
  const stream = useBlockStream(10);
  return (
    <div className="space-y-6">
      {status.data ? (
        <StatusCards status={status.data} />
      ) : (
        <Card>{status.isError ? <p role="alert">Gateway unreachable: {status.error.message}</p> : <p>Loading status…</p>}</Card>
      )}
      <Card>
        <div className="mb-3 flex items-center justify-between">
          <CardTitle>Latest blocks</CardTitle>
          <Badge tone={streamTone[stream]}>stream: {stream}</Badge>
        </div>
        {blocks.data ? <BlockTable blocks={blocks.data} /> : <p>Loading blocks…</p>}
      </Card>
    </div>
  );
}
