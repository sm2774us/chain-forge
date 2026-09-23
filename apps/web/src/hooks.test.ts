import { describe, expect, it } from "vitest";
import { applyEvent } from "@/hooks";
import { useUi } from "@/store";
import { blk } from "@/test/fakes";

describe("applyEvent", () => {
  it("prepends new blocks and slices to limit", () => {
    expect(applyEvent([blk(2), blk(1)], { type: "block.new", payload: blk(3) }, 2).map((b) => b.number)).toEqual([3, 2]);
  });
  it("dedupes by hash", () => {
    const l = [blk(2)];
    expect(applyEvent(l, { type: "block.new", payload: blk(2) }, 5)).toBe(l);
  });
  it("drops reorged blocks", () => {
    expect(applyEvent([blk(2), blk(1)], { type: "block.reorged", payload: blk(2) }, 5)).toEqual([blk(1)]);
  });
});

describe("useUi", () => {
  it("sets key and toggles theme both ways", () => {
    useUi.getState().setApiKey("x");
    expect(useUi.getState().apiKey).toBe("x");
    useUi.getState().toggleTheme();
    expect(useUi.getState().theme).toBe("light");
    useUi.getState().toggleTheme();
    expect(useUi.getState().theme).toBe("dark");
  });
});
