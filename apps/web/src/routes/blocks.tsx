import { BlockTable } from "@/components/block-table";
import { Card, CardTitle } from "@/components/ui/card";
import { useBlocks, useBlockStream } from "@/hooks";

export function Blocks() {
  const blocks = useBlocks(25);
  useBlockStream(25);
  return (
    <Card>
      <CardTitle>Indexed blocks (reorg-aware)</CardTitle>
      <div className="mt-3">{blocks.data ? <BlockTable blocks={blocks.data} /> : <p>Loading…</p>}</div>
    </Card>
  );
}
