import { afterEach, describe, expect, it, vi } from "vitest";
import { cn, micros, short, signed } from "@/lib/utils";
import { jsonResponse } from "@/test/fakes";

afterEach(() => {
  vi.unstubAllGlobals();
  vi.unstubAllEnvs();
  vi.resetModules();
});

describe("utils", () => {
  it("cn merges and resolves tailwind conflicts", () => {
    expect(cn("p-2", { x: false }, "p-4")).toBe("p-4");
  });
  it("short abbreviates long hex only", () => {
    expect(short("0x1234")).toBe("0x1234");
    expect(short("0xabcdef0123456789")).toBe("0xabcd…6789");
    expect(short("0xabcdef0123456789", 10, 6)).toBe("0xabcdef01…456789");
  });
});

describe("formatters", () => {
  it("signed adds + only to gains", () => {
    expect([signed("5"), signed("-5"), signed("0")]).toEqual(["+5", "-5", "0"]);
  });
  it("micros switches units at 1 ms", () => {
    expect([micros(999), micros(1000), micros(2500)]).toEqual(["999 µs", "1.00 ms", "2.50 ms"]);
  });
});

describe("api", () => {
  it("blocks uses default limit and unwraps", async () => {
    const f = vi.fn().mockResolvedValue(jsonResponse({ blocks: [{ number: 1 }] }));
    vi.stubGlobal("fetch", f);
    const { api } = await import("@/lib/api");
    expect(await api.blocks("k")).toEqual([{ number: 1 }]);
    expect(f.mock.calls[0]?.[0]).toBe("/api/v1/blocks?limit=25");
    await api.blocks("k", 3);
    expect(f.mock.calls[1]?.[0]).toBe("/api/v1/blocks?limit=3");
  });
  it("status + sign hit the right routes", async () => {
    const f = vi.fn().mockImplementation(() => Promise.resolve(jsonResponse({ ok: 1 })));
    vi.stubGlobal("fetch", f);
    const { api } = await import("@/lib/api");
    await api.status("k");
    await api.sign("k", "alice", "hi");
    expect(f.mock.calls[1]?.[1]).toMatchObject({ method: "POST", body: JSON.stringify({ key_id: "alice", message: "hi" }) });
    expect(f.mock.calls[1]?.[1].headers["x-api-key"]).toBe("k");
  });
  it("simulate/intent/broadcast hit the right routes", async () => {
    const f = vi.fn().mockImplementation(() => Promise.resolve(jsonResponse({ ok: 1 })));
    vi.stubGlobal("fetch", f);
    const { api } = await import("@/lib/api");
    const req = { chain_id: 1, from: "0x1", to: "0x2", value: "1", data: "0x" };
    await api.simulate("k", req);
    await api.intent("k", { ...req, sign: { key_id: "alice" } });
    await api.broadcast("k", "0xraw");
    expect(f.mock.calls.map((c) => c[0])).toEqual(["/api/v1/simulate", "/api/v1/intent", "/api/v1/broadcast"]);
    expect(JSON.parse(f.mock.calls[2]?.[1].body)).toEqual({ raw_tx: "0xraw" });
  });
  it("throws ApiError with server message", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ error: "nope" }, 403)));
    const { api, ApiError } = await import("@/lib/api");
    const err = await api.status("k").catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err).toMatchObject({ status: 403, message: "nope", name: "ApiError" });
  });
  it("falls back to statusText when body is not JSON", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("<html>", { status: 502, statusText: "Bad Gateway" })));
    const { api } = await import("@/lib/api");
    await expect(api.status("k")).rejects.toMatchObject({ status: 502, message: "Bad Gateway" });
  });
  it("streamUrl encodes the key; VITE_API_URL overrides base", async () => {
    vi.stubEnv("VITE_API_URL", "https://gw.example");
    const { streamUrl, API_BASE } = await import("@/lib/api");
    expect(API_BASE).toBe("https://gw.example");
    expect(streamUrl("a b")).toBe("https://gw.example/v1/stream?api_key=a%20b");
  });
});
