// API key handling for the dashboard. The key is kept in sessionStorage (gone
// when the tab closes) unless the person chooses "remember on this device",
// which uses localStorage. It is sent only to this origin, as a Bearer token.

const STORE_KEY = "gubern.apiKey";

function read(store: Storage | undefined): string | null {
  try {
    return store?.getItem(STORE_KEY) ?? null;
  } catch {
    return null;
  }
}

export function getApiKey(): string | null {
  return read(typeof sessionStorage === "undefined" ? undefined : sessionStorage) ?? read(typeof localStorage === "undefined" ? undefined : localStorage);
}

export function setApiKey(key: string, remember: boolean): void {
  clearApiKey();
  try {
    (remember ? localStorage : sessionStorage).setItem(STORE_KEY, key.trim());
  } catch {
    /* storage unavailable: the key lives only for this page */
    memoryKey = key.trim();
  }
  notify();
}

export function clearApiKey(): void {
  memoryKey = null;
  try {
    sessionStorage.removeItem(STORE_KEY);
    localStorage.removeItem(STORE_KEY);
  } catch {
    /* ignore */
  }
  notify();
}

let memoryKey: string | null = null;
const listeners = new Set<() => void>();
function notify() {
  listeners.forEach((l) => l());
}

/** Subscribe to sign-in / sign-out. */
export function onAuthChange(fn: () => void): () => void {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

/** The key currently in use, including one kept only in memory. */
export function currentKey(): string | null {
  return getApiKey() ?? memoryKey;
}

/** True when the dashboard runs on the in-memory mock (?mock=1). */
export function mockMode(): boolean {
  if (typeof window === "undefined") return false;
  const q = new URLSearchParams(window.location.search);
  if (q.get("mock") === "1") {
    try {
      sessionStorage.setItem("gubern.mock", "1");
    } catch {
      /* ignore */
    }
    return true;
  }
  if (q.get("mock") === "0") {
    try {
      sessionStorage.removeItem("gubern.mock");
    } catch {
      /* ignore */
    }
    return false;
  }
  try {
    return sessionStorage.getItem("gubern.mock") === "1";
  } catch {
    return false;
  }
}

export class HttpError extends Error {
  readonly status: number;
  readonly reason?: string;
  constructor(path: string, status: number, reason?: string, message?: string) {
    super(message || `${path} returned ${status}`);
    this.name = "HttpError";
    this.status = status;
    this.reason = reason;
  }
}

/** fetch with the API key attached. A 401 signs the person out. */
export async function authFetch(path: string, init: RequestInit = {}): Promise<Response> {
  const headers = new Headers(init.headers);
  const key = currentKey();
  if (key) headers.set("Authorization", `Bearer ${key}`);
  if (!headers.has("Accept")) headers.set("Accept", "application/json");
  const res = await fetch(path, { ...init, headers });
  if (res.status === 401 && key) clearApiKey();
  return res;
}

/** JSON request; non-2xx becomes an HttpError carrying the server's reason. */
export async function apiJSON<T>(path: string, init: RequestInit = {}): Promise<T> {
  const res = await authFetch(path, init);
  if (!res.ok) {
    let reason: string | undefined;
    let message: string | undefined;
    try {
      const body = (await res.json()) as { reason?: string; error?: string };
      reason = body.reason;
      message = body.error;
    } catch {
      /* not JSON */
    }
    throw new HttpError(path, res.status, reason, message);
  }
  return (await res.json()) as T;
}
