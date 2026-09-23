import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { api, streamUrl, type Block, type StreamEvent } from "@/lib/api";
import { useUi } from "@/store";

export function useBlocks(limit = 25) {
  const apiKey = useUi((s) => s.apiKey);
  return useQuery({ queryKey: ["blocks", apiKey, limit], queryFn: () => api.blocks(apiKey, limit), refetchInterval: 15_000 });
}

export function useStatus() {
  const apiKey = useUi((s) => s.apiKey);
  return useQuery({ queryKey: ["status", apiKey], queryFn: () => api.status(apiKey), refetchInterval: 5_000 });
}

/** Merge a stream event into a newest-first block list, dropping reorged blocks. */
export function applyEvent(blocks: Block[], ev: StreamEvent, limit: number): Block[] {
  if (ev.type === "block.reorged") return blocks.filter((b) => b.hash !== ev.payload.hash);
  if (blocks.some((b) => b.hash === ev.payload.hash)) return blocks;
  return [ev.payload, ...blocks].slice(0, limit);
}

/**
 * Subscribes to the gateway SSE stream and patches the TanStack Query cache in
 * place, so every consumer of `useBlocks` updates without refetching.
 */
export function useBlockStream(limit = 25): "connecting" | "live" | "offline" {
  const apiKey = useUi((s) => s.apiKey);
  const qc = useQueryClient();
  const [state, setState] = useState<"connecting" | "live" | "offline">("connecting");
  useEffect(() => {
    const es = new EventSource(streamUrl(apiKey));
    es.onopen = () => setState("live");
    es.onerror = () => setState("offline");
    const handle = (type: StreamEvent["type"]) => (e: MessageEvent<string>) => {
      const { payload } = JSON.parse(e.data) as { payload: Block };
      qc.setQueryData<Block[]>(["blocks", apiKey, limit], (old = []) => applyEvent(old, { type, payload }, limit));
    };
    es.addEventListener("block.new", handle("block.new") as EventListener);
    es.addEventListener("block.reorged", handle("block.reorged") as EventListener);
    return () => es.close();
  }, [apiKey, limit, qc]);
  return state;
}
