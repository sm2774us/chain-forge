import { create } from "zustand";

interface UiState {
  apiKey: string;
  theme: "dark" | "light";
  setApiKey: (k: string) => void;
  toggleTheme: () => void;
}

/** Client-only UI state. Server state lives in TanStack Query, never here. */
export const useUi = create<UiState>((set) => ({
  apiKey: "dev-key-change-me",
  theme: "dark",
  setApiKey: (apiKey) => set({ apiKey }),
  toggleTheme: () => set((s) => ({ theme: s.theme === "dark" ? "light" : "dark" })),
}));
