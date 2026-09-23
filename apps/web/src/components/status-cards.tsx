import { Badge, Card, CardTitle } from "@/components/ui/card";
import type { Status } from "@/lib/api";
import { short } from "@/lib/utils";

const tone = { closed: "ok", "half-open": "warn", open: "bad" } as const;

export function StatusCards({ status }: { status: Status }) {
  return (
    <div className="grid gap-4 md:grid-cols-3" aria-label="Gateway status">
      <Card>
        <CardTitle>Chain head</CardTitle>
        <p className="mt-2 font-mono text-3xl">{status.head.number.toLocaleString()}</p>
        <p className="font-mono text-xs text-muted">{short(status.head.hash, 10, 6)}</p>
      </Card>
      <Card>
        <CardTitle>Upstream nodes</CardTitle>
        <ul className="mt-2 space-y-1 text-sm">
          {status.upstreams.map((u) => (
            <li key={u.url} className="flex items-center justify-between gap-2">
              <span className="truncate font-mono text-xs">{u.url}</span>
              <Badge tone={tone[u.state]}>{u.state}</Badge>
            </li>
          ))}
        </ul>
      </Card>
      <Card>
        <CardTitle>Edge</CardTitle>
        <p className="mt-2 text-sm">
          <span className="font-mono text-2xl">{status.cache_entries}</span> cached responses
        </p>
        <p className="text-sm text-muted">{status.webhook_cnt} webhooks registered</p>
      </Card>
    </div>
  );
}
