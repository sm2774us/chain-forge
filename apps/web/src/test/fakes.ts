import type { Block } from "@/lib/api";

export const blk = (n: number, h = `0x${n.toString(16).padStart(64, "0")}`): Block => ({
  number: n,
  hash: h,
  parentHash: `0x${(n - 1).toString(16).padStart(64, "0")}`,
  timestamp: 1_700_000_000 + n,
  txCount: n % 5,
});

export class FakeEventSource {
  static instances: FakeEventSource[] = [];
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  closed = false;
  listeners = new Map<string, (e: MessageEvent<string>) => void>();
  constructor(public url: string) {
    FakeEventSource.instances.push(this);
  }
  addEventListener(t: string, fn: (e: MessageEvent<string>) => void) {
    this.listeners.set(t, fn);
  }
  close() {
    this.closed = true;
  }
  emit(type: string, payload: Block) {
    this.listeners.get(type)?.({ data: JSON.stringify({ type, payload }) } as MessageEvent<string>);
  }
  static last() {
    return FakeEventSource.instances[FakeEventSource.instances.length - 1] as FakeEventSource;
  }
}

export function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}
