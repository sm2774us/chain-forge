import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { BlockTable } from "./block-table";
import { StatusCards } from "./status-cards";
import { blk } from "@/test/fakes";

describe("BlockTable", () => {
  it("renders rows sorted desc and toggles sort", async () => {
    render(<BlockTable blocks={[blk(1), blk(3), blk(2)]} />);
    const heights = () =>
      screen
        .getAllByRole("row")
        .slice(1)
        .map((r) => within(r).getAllByRole("cell")[0]?.textContent);
    expect(heights()).toEqual(["3", "2", "1"]);
    await userEvent.click(screen.getByRole("button", { name: /height/i }));
    expect(heights()).toEqual(["1", "2", "3"]);
    expect(screen.getAllByText(/UTC$/).length).toBe(3);
  });
});

describe("StatusCards", () => {
  it("shows head, upstream badges, edge counts", () => {
    render(
      <StatusCards
        status={{
          head: blk(1234567),
          upstreams: [
            { url: "http://a", state: "closed" },
            { url: "http://b", state: "half-open" },
            { url: "http://c", state: "open" },
          ],
          cache_entries: 7,
          webhook_cnt: 2,
        }}
      />,
    );
    expect(screen.getByText("1,234,567")).toBeInTheDocument();
    expect(screen.getByText("half-open")).toBeInTheDocument();
    expect(screen.getByText("open").className).toContain("danger");
    expect(screen.getByText(/2 webhooks/)).toBeInTheDocument();
  });
});
