import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryHistory } from "@tanstack/react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "@/app";
import type { IntentResult } from "@/lib/api";
import { useUi } from "@/store";
import { FakeEventSource, jsonResponse } from "@/test/fakes";

const result = (over: Partial<IntentResult> = {}): IntentResult => ({
  chain: "evm",
  tx: { chain_id: 1337, nonce: 4, gas_price: "9", gas_limit: 25200, to: "0x22", value: "1000", data: "0x" },
  simulation: {
    outcome: "success",
    gas_used: 21000,
    output: "0x",
    logs: [],
    elapsed_us: 812,
    balance_changes: [
      { address: "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266", before: "5000", after: "4000", delta: "-1000" },
      { address: "0x70997970C51812dc3A010C7d01b50e0d17dc79C8", before: "0", after: "1000", delta: "1000" },
    ],
  },
  ...over,
});

let handlers: Record<string, () => Response | Promise<Response>>;
const calls: { url: string; body: unknown }[] = [];

beforeEach(() => {
  calls.length = 0;
  FakeEventSource.instances = [];
  vi.stubGlobal("EventSource", FakeEventSource);
  handlers = {};
  vi.stubGlobal(
    "fetch",
    vi.fn((url: string, init?: RequestInit) => {
      calls.push({ url, body: init?.body ? JSON.parse(init.body as string) : undefined });
      const h = handlers[url];
      return Promise.resolve(h ? h() : jsonResponse({ error: "missing" }, 404));
    }),
  );
  useUi.setState({ apiKey: "dev-key-change-me", theme: "dark" });
});
afterEach(() => vi.unstubAllGlobals());

const open = () => render(<App history={createMemoryHistory({ initialEntries: ["/simulate"] })} />);

describe("Simulate", () => {
  it("simulates without signing and shows gas, latency and signed deltas", async () => {
    handlers["/api/v1/simulate"] = () => jsonResponse(result());
    open();
    await userEvent.click(await screen.findByRole("button", { name: "Simulate" }));
    const panel = await screen.findByLabelText("Simulation result");
    expect(within(panel).getByText("success")).toBeInTheDocument();
    expect(within(panel).getByText("21,000")).toBeInTheDocument();
    expect(within(panel).getByText("812 µs")).toBeInTheDocument();
    expect(within(panel).getByText("-1000")).toHaveClass("text-danger");
    expect(within(panel).getByText("+1000")).toHaveClass("text-accent");
    expect(calls[0]?.body).toMatchObject({ chain_id: 1337, value: "1000", data: "0x" });
    expect(calls[0]?.body).not.toHaveProperty("sign");
    expect(screen.queryByRole("button", { name: "Broadcast privately" })).not.toBeInTheDocument();
  });

  it("edits every field and sends the custody key when signing", async () => {
    handlers["/api/v1/intent"] = () =>
      jsonResponse(result({ signed: { address: "0xA", signing_hash: "0xh", raw_tx: "0xraw", tx_hash: "0xTXHASH" } }));
    open();
    for (const [label, text] of [
      ["From", "0x1"],
      ["To", "0x2"],
      ["Value (wei)", "7"],
      ["Calldata", "0xab"],
      ["Custody key", "bob"],
    ] as const) {
      const el = await screen.findByLabelText(label);
      await userEvent.clear(el);
      await userEvent.type(el, text);
    }
    await userEvent.click(screen.getByRole("button", { name: /Simulate & sign/ }));
    expect(await screen.findByLabelText("Signed transaction")).toHaveTextContent("0xTXHASH");
    expect(calls[0]?.body).toEqual({ chain_id: 1337, from: "0x1", to: "0x2", value: "7", data: "0xab", sign: { key_id: "bob" } });
  });

  it("broadcasts the signed transaction through the private relay", async () => {
    handlers["/api/v1/intent"] = () =>
      jsonResponse(result({ signed: { address: "0xA", signing_hash: "0xh", raw_tx: "0xraw", tx_hash: "0xt" } }));
    handlers["/api/v1/broadcast"] = () => jsonResponse({ tx_id: "0xRELAYED" });
    open();
    await userEvent.click(await screen.findByRole("button", { name: /Simulate & sign/ }));
    await userEvent.click(await screen.findByRole("button", { name: "Broadcast privately" }));
    expect(await screen.findByText(/0xRELAYED/)).toBeInTheDocument();
    expect(calls.at(-1)?.body).toEqual({ raw_tx: "0xraw" });
    expect(screen.getByRole("button", { name: "Broadcast privately" })).toBeDisabled();
  });

  it("shows relay errors and a pending state while broadcasting", async () => {
    handlers["/api/v1/intent"] = () =>
      jsonResponse(result({ signed: { address: "0xA", signing_hash: "0xh", raw_tx: "0xraw", tx_hash: "0xt" } }));
    let release!: () => void;
    handlers["/api/v1/broadcast"] = () =>
      new Promise((r) => (release = () => r(jsonResponse({ error: "node error: nonce too low" }, 502))));
    open();
    await userEvent.click(await screen.findByRole("button", { name: /Simulate & sign/ }));
    await userEvent.click(await screen.findByRole("button", { name: "Broadcast privately" }));
    expect(await screen.findByRole("button", { name: "Broadcasting…" })).toBeDisabled();
    release();
    expect(await screen.findByRole("alert")).toHaveTextContent("nonce too low");
  });

  it("explains reverts and why nothing was signed", async () => {
    handlers["/api/v1/intent"] = () =>
      jsonResponse(
        result({
          simulation: {
            outcome: "revert",
            gas_used: 30000,
            output: "0x",
            reason: "insufficient allowance",
            logs: [{ address: "0x1", topics: [], data: "0x" }],
            elapsed_us: 2500,
            balance_changes: [],
          },
          signing_skipped: "simulation revert; nothing was signed",
        }),
      );
    open();
    await userEvent.click(await screen.findByRole("button", { name: /Simulate & sign/ }));
    expect(await screen.findByText("revert")).toBeInTheDocument();
    expect(screen.getByText(/insufficient allowance/)).toBeInTheDocument();
    expect(screen.getByText(/nothing was signed/)).toBeInTheDocument();
    expect(screen.getByText("2.50 ms")).toBeInTheDocument();
  });

  it("shows gateway errors and a pending state while simulating", async () => {
    let release!: () => void;
    handlers["/api/v1/simulate"] = () =>
      new Promise((r) => (release = () => r(jsonResponse({ error: "simulation engine unavailable" }, 503))));
    open();
    await userEvent.click(await screen.findByRole("button", { name: "Simulate" }));
    expect(await screen.findByRole("button", { name: "Simulating…" })).toBeDisabled();
    release();
    expect(await screen.findByRole("alert")).toHaveTextContent("engine unavailable");
  });
});
