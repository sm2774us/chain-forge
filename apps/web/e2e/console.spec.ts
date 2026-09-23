import { expect, test } from "@playwright/test";

const KEY = process.env.E2E_API_KEY ?? "e2e-api-key-123";
// Hardhat/Anvil account #0 key is used only by the signer under test; this is its well-known public address for `alice`.
test.beforeEach(async ({ page }) => {
  await page.goto("/");
  await page.getByLabel("API key").fill(KEY);
});

test("overview shows live head, healthy upstream and streaming state", async ({ page }) => {
  await expect(page.getByText("Chain head")).toBeVisible();
  await expect(page.getByText("stream: live")).toBeVisible();
  await expect(page.getByLabel("Gateway status").getByText("closed")).toBeVisible();
  await expect(page.getByRole("table", { name: "Recent blocks" }).getByRole("row")).not.toHaveCount(1);
});

test("blocks page keeps advancing and survives forced reorgs", async ({ page }) => {
  await page.getByRole("link", { name: "Blocks" }).click();
  const first = page.getByRole("table").getByRole("row").nth(1).getByRole("cell").first();
  const h0 = Number((await first.textContent())!.replace(/,/g, ""));
  await expect.poll(async () => Number((await first.textContent())!.replace(/,/g, "")), { timeout: 20_000 }).toBeGreaterThan(h0);
});

test("wrong API key surfaces an error, right key recovers", async ({ page }) => {
  await page.getByLabel("API key").fill("definitely-wrong");
  await expect(page.getByRole("alert")).toContainText(/unauthorized|invalid/i);
  await page.getByLabel("API key").fill(KEY);
  await expect(page.getByText("Chain head")).toBeVisible();
});

test("custody signing round-trips through Go gateway → Rust signer", async ({ page }) => {
  await page.getByRole("link", { name: "Sign" }).click();
  await page.getByLabel("Message").fill("gm chainforge");
  await page.getByRole("button", { name: "Sign message" }).click();
  const res = page.getByLabel("Signature result");
  await expect(res).toContainText("0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266");
  await expect(res).toContainText(/0x[0-9a-f]{130}/);
});

test("unknown key id is rejected by the signer policy", async ({ page }) => {
  await page.getByRole("link", { name: "Sign" }).click();
  await page.getByLabel("Key ID").fill("mallory");
  await page.getByLabel("Message").fill("x");
  await page.getByRole("button", { name: "Sign message" }).click();
  await expect(page.getByRole("alert")).toBeVisible();
});

test("theme toggle flips the document class", async ({ page }) => {
  await page.getByRole("button", { name: "Toggle theme" }).click();
  await expect(page.locator("html")).toHaveClass(/light/);
});

test("simulate → sign → private broadcast, end to end through Go, Rust and the relay", async ({ page }) => {
  await page.getByRole("link", { name: "Simulate" }).click();
  await page.getByRole("button", { name: "Simulate", exact: true }).click();
  const result = page.getByLabel("Simulation result");
  await expect(result.getByText("success")).toBeVisible();
  await expect(result.getByText("21,000")).toBeVisible(); // a plain transfer costs exactly 21000 gas
  await expect(result.getByLabel("Balance changes")).toContainText("-1000");

  await page.getByRole("button", { name: /Simulate & sign/ }).click();
  await expect(page.getByLabel("Signed transaction")).toContainText("0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266");
  await page.getByRole("button", { name: "Broadcast privately" }).click();
  await expect(page.getByText(/submitted via private relay: 0x[0-9a-f]{64}/)).toBeVisible();
});

test("a value over the signer's policy cap is simulated but never signed", async ({ page }) => {
  await page.getByRole("link", { name: "Simulate" }).click();
  await page.getByLabel("Value (wei)").fill("2000000000000000");
  await page.getByRole("button", { name: /Simulate & sign/ }).click();
  await expect(page.getByRole("alert")).toContainText(/policy|cap/i);
});
