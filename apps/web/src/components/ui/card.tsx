import type * as React from "react";
import { cn } from "@/lib/utils";

export function Card({ className, ...p }: React.HTMLAttributes<HTMLDivElement>) {
  return <div className={cn("rounded-xl border border-line bg-panel p-5", className)} {...p} />;
}
export function CardTitle({ className, ...p }: React.HTMLAttributes<HTMLHeadingElement>) {
  return <h3 className={cn("text-xs font-medium uppercase tracking-wider text-muted", className)} {...p} />;
}
export function Badge({ tone = "ok", className, ...p }: React.HTMLAttributes<HTMLSpanElement> & { tone?: "ok" | "warn" | "bad" }) {
  const tones = { ok: "bg-accent/15 text-accent", warn: "bg-warn/15 text-warn", bad: "bg-danger/15 text-danger" };
  return <span className={cn("rounded-full px-2 py-0.5 text-xs font-medium", tones[tone], className)} {...p} />;
}
