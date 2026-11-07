// The single data client the new screens use. Phase 1 ships the in-memory
// mock; Phase 2 replaces this export with the HTTP/SSE client that implements
// the same GubernClient interface.
import { useEffect, useSyncExternalStore } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { mockClient } from "./mock";
import type { GubernClient } from "./model";

export const client: GubernClient = mockClient;

const KEY = "gubern";

// useLiveInvalidation refetches every query when the client reports a change
// (an approval decided, a run finished), so lists never show stale state.
export function useLiveInvalidation() {
  const qc = useQueryClient();
  useEffect(() => client.subscribe(() => void qc.invalidateQueries({ queryKey: [KEY] })), [qc]);
}

export const useIdentity = () => useQuery({ queryKey: [KEY, "whoami"], queryFn: client.whoami, staleTime: Infinity });
export const useSummary = () => useQuery({ queryKey: [KEY, "summary"], queryFn: client.summary, refetchInterval: 5000 });
export const useSessions = () => useQuery({ queryKey: [KEY, "sessions"], queryFn: client.sessions });
export const useAttention = () => useQuery({ queryKey: [KEY, "attention"], queryFn: client.attention, refetchInterval: 5000 });
export const useOutputs = () => useQuery({ queryKey: [KEY, "outputs"], queryFn: client.outputs });
export const useModels = () => useQuery({ queryKey: [KEY, "models"], queryFn: client.models, staleTime: Infinity });
export const useWorkspaces = () => useQuery({ queryKey: [KEY, "workspaces"], queryFn: client.workspaces, staleTime: 60_000 });

// Live views read synchronous snapshots and re-render on every change.
function useLive<T>(get: () => T): T {
  return useSyncExternalStore(client.subscribe, get, get);
}

export const useTranscript = (sessionId: string) => useLive(() => client.transcript(sessionId));
export const useApproval = (approvalId: string) => useLive(() => client.approval(approvalId));
export const useQuestions = (sessionId: string) => useLive(() => client.questions(sessionId));
export const usePlan = (sessionId: string) => useLive(() => client.plan(sessionId));
export const useIsRunning = (sessionId: string) => useLive(() => client.isRunning(sessionId));
