import { useState } from "react";
import { KeyRound } from "lucide-react";
import { setApiKey } from "../data/auth";
import { GubernMark } from "./GubernMark";

// SignIn asks for a Gubern API key. It is checked against GET /api/me before
// it is kept, and stays in this tab only unless "remember" is ticked. The key
// is never shown again and is sent only to this server.
export function SignIn() {
  const [key, setKey] = useState("");
  const [remember, setRemember] = useState(false);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError("");
    setBusy(true);
    try {
      const res = await fetch("/api/me", { headers: { Authorization: `Bearer ${key.trim()}`, Accept: "application/json" } });
      if (res.status === 401) {
        setError("That key was not accepted. Check it, or create a new one with gubern setup.");
        return;
      }
      if (!res.ok) {
        setError(`The server answered ${res.status}. Try again in a moment.`);
        return;
      }
      setApiKey(key, remember);
    } catch {
      setError("The server could not be reached.");
    } finally {
      setBusy(false);
    }
  };

  return (
    <main className="flex min-h-screen items-center justify-center bg-background px-4 text-foreground">
      <form onSubmit={(e) => void submit(e)} className="w-full max-w-sm rounded-xl border border-border bg-surface p-6 shadow-sm">
        <div className="mb-5 flex items-center gap-2">
          <GubernMark className="h-6 w-6" />
          <h1 className="text-[17px] font-semibold">Sign in to Gubern</h1>
        </div>
        <label htmlFor="apikey" className="mb-1.5 block text-[13px] font-medium">
          API key
        </label>
        <div className="flex items-center gap-2 rounded-lg border border-border bg-background px-3 focus-within:ring-2 focus-within:ring-ring">
          <KeyRound className="h-4 w-4 text-muted" aria-hidden />
          <input
            id="apikey"
            type="password"
            autoComplete="off"
            spellCheck={false}
            value={key}
            onChange={(e) => setKey(e.target.value)}
            className="h-10 w-full bg-transparent font-mono text-[13px] outline-none"
            placeholder="0123abcd….…"
            required
          />
        </div>
        <p className="mt-1.5 text-[12px] text-muted">
          On the server machine, your key is in the <span className="font-mono">tui-key</span> file. Run <span className="font-mono">gubern identity add-person</span> to give someone their own.
        </p>
        <label className="mt-4 flex cursor-pointer items-center gap-2 text-[13px]">
          <input type="checkbox" checked={remember} onChange={(e) => setRemember(e.target.checked)} className="h-4 w-4" />
          Remember on this device
        </label>
        {error && (
          <p role="alert" className="mt-3 rounded border border-danger/40 bg-danger/10 px-3 py-2 text-[13px] text-danger">
            {error}
          </p>
        )}
        <button
          type="submit"
          disabled={busy || key.trim() === ""}
          className="mt-5 h-10 w-full cursor-pointer rounded-lg bg-primary text-[14px] font-medium text-primary-foreground disabled:cursor-not-allowed disabled:opacity-60"
        >
          {busy ? "Checking…" : "Sign in"}
        </button>
        <p className="mt-4 text-center text-[12px] text-muted">
          Exploring without a server? <a className="underline" href="?mock=1">Open the demo</a>
        </p>
      </form>
    </main>
  );
}
