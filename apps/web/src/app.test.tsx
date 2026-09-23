import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryHistory } from "@tanstack/react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "@/app";
import { useUi } from "@/store";
import { blk, FakeEventSource, jsonResponse } from "@/test/fakes";

type Handler = () => Response | Promise<Response>;
let routes: Record<string, Handler>;

const status = { head: blk(50), upstreams: [{ url: "http://n1", state: "closed" }], cache_entries: 1, webhook_cnt: 0 };

beforeEach(() => {
  FakeEventSource.instances = [];
  vi.stubGlobal("EventSource", FakeEventSource);
  routes = {
    "/api/v1/status": () => jsonResponse(status),
    "/api/v1/blocks": () => jsonResponse({ blocks: [blk(50), blk(49)] }),
  };
  vi.stubGlobal(
    "fetch",
    vi.fn((url: string, init?: RequestInit) => {
      const p = url.split("?")[0] as string;
      const h = routes[init?.method === "POST" ? `POST ${p}` : p];
      return Promise.resolve(h ? h() : jsonResponse({ error: "missing" }, 404));
    }),
  );
  useUi.setState({ apiKey: "dev-key-change-me", theme: "dark" });
});
afterEach(() => vi.unstubAllGlobals());

const open = (path: string) => render(<App history={createMemoryHistory({ initialEntries: [path] })} />);

describe("Overview", () => {
  it("renders status, blocks, and live stream updates incl. reorg", async () => {
    open("/");
    expect(await screen.findByText("Loading status…")).toBeInTheDocument();
    expect(await screen.findByText("http://n1")).toBeInTheDocument();
    expect(await screen.findByText("stream: connecting")).toBeInTheDocument();
    const es = FakeEventSource.last();
    act(() => es.onopen?.());
    expect(screen.getByText("stream: live")).toBeInTheDocument();
    act(() => es.emit("block.new", blk(51)));
    expect(await screen.findByText("51")).toBeInTheDocument();
    act(() => es.emit("block.reorged", blk(51)));
    await waitFor(() => expect(screen.queryByText("51")).not.toBeInTheDocument());
    act(() => es.onerror?.());
    expect(screen.getByText("stream: offline")).toBeInTheDocument();
  });
  it("stream event before data loaded seeds the cache", async () => {
    routes["/api/v1/blocks"] = () => new Promise<Response>(() => {});
    open("/");
    await screen.findByText("stream: connecting");
    act(() => FakeEventSource.last().emit("block.new", blk(9)));
    expect(await screen.findByText("9")).toBeInTheDocument();
  });
  it("shows gateway error", async () => {
    routes["/api/v1/status"] = () => jsonResponse({ error: "unauthorized" }, 401);
    open("/");
    expect(await screen.findByRole("alert")).toHaveTextContent("unauthorized");
  });
});

describe("Blocks", () => {
  it("loads then renders the table", async () => {
    open("/blocks");
    expect(await screen.findByText("Loading…")).toBeInTheDocument();
    expect(await screen.findByRole("table", { name: "Recent blocks" })).toBeInTheDocument();
  });
});

describe("Layout", () => {
  it("navigates, edits api key, toggles theme", async () => {
    open("/");
    await userEvent.click(await screen.findByRole("link", { name: "Blocks" }));
    expect(await screen.findByText(/reorg-aware/)).toBeInTheDocument();
    const key = await screen.findByLabelText("API key");
    await userEvent.clear(key);
    await userEvent.type(key, "k2");
    expect(useUi.getState().apiKey).toBe("k2");
    const t = screen.getByRole("button", { name: "Toggle theme" });
    await userEvent.click(t);
    expect(document.documentElement).toHaveClass("light");
    await userEvent.click(t);
    expect(document.documentElement).toHaveClass("dark");
  });
});

describe("Sign", () => {
  it("signs a message", async () => {
    routes["POST /api/v1/sign"] = () => jsonResponse({ address: "0xADDR", digest: "0xDIG", signature: "0xSIG" });
    open("/sign");
    const btn = await screen.findByRole("button", { name: "Sign message" });
    expect(btn).toBeDisabled();
    await userEvent.clear(screen.getByLabelText("Key ID"));
    await userEvent.type(screen.getByLabelText("Key ID"), "bob");
    await userEvent.type(screen.getByLabelText("Message"), "hello");
    await userEvent.click(btn);
    const res = await screen.findByLabelText("Signature result");
    expect(res).toHaveTextContent("0xSIG");
    const body = JSON.parse(
      (vi.mocked(fetch).mock.calls.find((c) => (c[1] as RequestInit | undefined)?.method === "POST")?.[1] as RequestInit).body as string,
    );
    expect(body).toEqual({ key_id: "bob", message: "hello" });
  });
  it("shows pending then error", async () => {
    let release!: () => void;
    routes["POST /api/v1/sign"] = () => new Promise((r) => (release = () => r(jsonResponse({ error: "unknown key_id" }, 403))));
    open("/sign");
    await userEvent.type(await screen.findByLabelText("Message"), "x");
    await userEvent.click(screen.getByRole("button", { name: "Sign message" }));
    expect(await screen.findByRole("button", { name: "Signing…" })).toBeDisabled();
    act(() => release());
    expect(await screen.findByRole("alert")).toHaveTextContent("unknown key_id");
  });
});
