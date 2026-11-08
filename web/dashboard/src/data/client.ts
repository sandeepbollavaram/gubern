// The single data client the new screens use: the real HTTP/SSE client
// (milestone E), or the in-memory mock with ?mock=1 (kept for UI work and
// tests without a server). Both implement GubernClient.
import { useEffect, useSyncExternalStore } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { mockClient } from "./mock";
import { httpClient } from "./http";
import { mockMode } from "./auth";
import type { GubernClient } from "./model";

export const usingMock = mockMode();
export const client: GubernClient = usingMock ? mockClient : httpClient;

const KEY = "gubern";

// useLiveInvalidation refetches every query when the client reports a change
// (an approval decided, a run finished), so lists never show stale state.
export function useLiveInvalidation() {
  const qc = useQueryClient();
  useEffect(() => client.subscribe(() => void qc.invalidateQueries({ queryKey: [KEY] })), [qc]);
}

export const useIdentity = () => useQuery({ queryKey: [KEY, "whoami"], queryFn: client.whoami, staleTime: Infinity });

// useCan reports whether the signed-in principal holds scope, so screens can
// hide actions it would be refused. The server enforces every scope anyway;
// this only keeps the UI honest. The mock can do everything.
export function useCan(scope: string): boolean {
  const { data } = useIdentity();
  if (usingMock) return true;
  return !!data?.scopes?.includes(scope);
}
export const useSummary = () => useQuery({ queryKey: [KEY, "summary"], queryFn: client.summary, refetchInterval: 5000 });
export const useSessions = () => useQuery({ queryKey: [KEY, "sessions"], queryFn: client.sessions });
export const useAttention = () => useQuery({ queryKey: [KEY, "attention"], queryFn: client.attention, refetchInterval: 5000 });
export const useOutputs = () => useQuery({ queryKey: [KEY, "outputs"], queryFn: client.outputs });
export const useModels = () => useQuery({ queryKey: [KEY, "models"], queryFn: client.models, staleTime: Infinity });
export const useWorkspaces = () => useQuery({ queryKey: [KEY, "workspaces"], queryFn: client.workspaces, staleTime: 60_000 });
export const useWorkspaceList = () => useQuery({ queryKey: [KEY, "workspaces", "list"], queryFn: client.workspaceList });
export const useConnectors = () => useQuery({ queryKey: [KEY, "connectors"], queryFn: client.connectors, refetchInterval: 10_000 });
export const useSkills = () => useQuery({ queryKey: [KEY, "skills"], queryFn: client.skills });
export const useSkill = (name: string | undefined) =>
  useQuery({ queryKey: [KEY, "skill", name], queryFn: () => client.skill(name!), enabled: !!name });

// Live views read synchronous snapshots and re-render on every change.
function useLive<T>(get: () => T): T {
  return useSyncExternalStore(client.subscribe, get, get);
}

export const useTranscript = (sessionId: string) => useLive(() => client.transcript(sessionId));
export const useApproval = (approvalId: string) => useLive(() => client.approval(approvalId));
export const useQuestions = (sessionId: string) => useLive(() => client.questions(sessionId));
export const usePlan = (sessionId: string) => useLive(() => client.plan(sessionId));
export const useIsRunning = (sessionId: string) => useLive(() => client.isRunning(sessionId));
