import { Link, Outlet } from "@tanstack/react-router";
import { Label } from "@radix-ui/react-label";
import { Moon, Sun } from "lucide-react";
import { useEffect } from "react";
import { Button } from "@/components/ui/button";
import { useUi } from "@/store";

const nav = [
  { to: "/", label: "Overview" },
  { to: "/blocks", label: "Blocks" },
  { to: "/simulate", label: "Simulate" },
  { to: "/sign", label: "Sign" },
] as const;

export function Layout() {
  const { apiKey, setApiKey, theme, toggleTheme } = useUi();
  useEffect(() => {
    document.documentElement.classList.toggle("dark", theme === "dark");
    document.documentElement.classList.toggle("light", theme === "light");
  }, [theme]);
  return (
    <div className="mx-auto max-w-6xl p-6">
      <header className="mb-8 flex flex-wrap items-center justify-between gap-4">
        <div className="flex items-center gap-6">
          <span className="text-lg font-semibold">⛓ ChainForge</span>
          <nav className="flex gap-4 text-sm" aria-label="Primary">
            {nav.map((n) => (
              <Link key={n.to} to={n.to} className="text-muted hover:text-fg [&.active]:text-accent">
                {n.label}
              </Link>
            ))}
          </nav>
        </div>
        <div className="flex items-center gap-3">
          <Label htmlFor="api-key" className="text-xs text-muted">
            API key
          </Label>
          <input
            id="api-key"
            type="password"
            className="w-44 rounded-md border border-line bg-panel px-2 py-1 text-sm"
            value={apiKey}
            onChange={(e) => setApiKey(e.target.value)}
          />
          <Button variant="ghost" size="icon" aria-label="Toggle theme" onClick={toggleTheme}>
            {theme === "dark" ? <Sun className="size-4" /> : <Moon className="size-4" />}
          </Button>
        </div>
      </header>
      <main>
        <Outlet />
      </main>
    </div>
  );
}
