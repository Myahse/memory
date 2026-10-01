import { useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";
import { supabase } from "./supabase";
import { toast } from "../components/Toast";

/**
 * Listens to status changes of the user's own memories (RLS applies to
 * Realtime too) and refreshes cached queries. Shows "Memory ready" toasts.
 */
export function useMemoryRealtime(userId: string | undefined) {
  const qc = useQueryClient();
  useEffect(() => {
    if (!userId) return;
    const channel = supabase
      .channel(`memories:${userId}`)
      .on(
        "postgres_changes",
        { event: "*", schema: "public", table: "memories", filter: `user_id=eq.${userId}` },
        (payload) => {
          const next = payload.new as { status?: string; title?: string } | undefined;
          const prev = payload.old as { status?: string } | undefined;
          if (payload.eventType === "UPDATE" && next?.status !== prev?.status) {
            if (next?.status === "ready") toast(`✓ Memory ready${next.title ? `: ${next.title}` : ""}`);
            if (next?.status === "failed") toast("Processing failed — open the memory to retry.", "error");
          }
          void qc.invalidateQueries({ queryKey: ["memories"] });
          void qc.invalidateQueries({ queryKey: ["memory"] });
          void qc.invalidateQueries({ queryKey: ["timeline"] });
        },
      )
      .subscribe();
    return () => {
      void supabase.removeChannel(channel);
    };
  }, [userId, qc]);
}
