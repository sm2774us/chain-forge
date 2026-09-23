import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { Button } from "./button";
import { Badge, Card, CardTitle } from "./card";

describe("ui primitives", () => {
  it("Button renders variants and asChild", () => {
    render(
      <>
        <Button>plain</Button>
        <Button variant="outline" size="sm" className="x">
          o
        </Button>
        <Button asChild>
          <a href="/z">link</a>
        </Button>
      </>,
    );
    expect(screen.getByRole("button", { name: "plain" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "link" })).toHaveAttribute("href", "/z");
  });
  it("Card, CardTitle, Badge tones (default ok)", () => {
    render(
      <Card data-testid="c">
        <CardTitle>t</CardTitle>
        <Badge>default</Badge>
        <Badge tone="warn">w</Badge>
        <Badge tone="bad">b</Badge>
      </Card>,
    );
    expect(screen.getByTestId("c")).toBeInTheDocument();
    expect(screen.getByText("default").className).toContain("text-accent");
    expect(screen.getByText("b").className).toContain("text-danger");
  });
});
