import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

/** Merge conditional class names, resolving Tailwind conflicts (shadcn convention). */
export function cn(...inputs: ClassValue[]): string {
  return twMerge(clsx(inputs));
}

/** 0xabcdef…1234 style abbreviation for hashes and addresses. */
export function short(hex: string, head = 6, tail = 4): string {
  return hex.length <= head + tail + 1 ? hex : `${hex.slice(0, head)}…${hex.slice(-tail)}`;
}

/** Adds an explicit sign to a decimal delta so gains and losses read at a glance. */
export function signed(delta: string): string {
  return delta.startsWith("-") || delta === "0" ? delta : `+${delta}`;
}

/** Microseconds → "812 µs" / "1.25 ms". */
export function micros(us: number): string {
  return us < 1000 ? `${us} µs` : `${(us / 1000).toFixed(2)} ms`;
}
