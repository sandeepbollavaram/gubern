import { useState } from "react";
import {
  AlertTriangle,
  BookOpen,
  Copy,
  Download,
  FileWarning,
  Lock,
  ShieldAlert,
  ShieldCheck,
  Wrench,
} from "lucide-react";
import { useNavigate } from "react-router";
import { useQueryClient } from "@tanstack/react-query";
import { client, useCan, useSkill, useSkills } from "../../data/client";
import type { Skill, SkillInstall } from "../../data/model";
import { useUIStore } from "../../store";
import { RichText } from "../../ui/RichText";
import {
  Button,
  Dialog,
  EmptyState,
  InlineError,
  Skeleton,
  Toggle,
} from "../../ui/primitives";
import { cx } from "../../ui/util";
import { PageHeader } from "./PageHeader";

// Skills (agent-platform Phase 2): the workspace's SKILL.md know-how that
// /agent offers the model. A skill is knowledge, never a grant: turning one
// off only removes it from what the model is shown.

export function Skills() {
  const { data, isLoading, error, refetch } = useSkills();
  const [selected, setSelected] = useState<string | undefined>();
  const current = selected ?? data?.skills[0]?.name;
  const canManage = useCan("system:manage");
  const [installing, setInstalling] = useState(false);
  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto max-w-6xl px-5 py-8">
        <PageHeader
          title="Skills"
          body="Procedures agents can follow, read from SKILL.md files in the workspace. Skills never grant permissions."
          action={
            canManage ? (
              <Button
                variant="primary"
                icon={<Download className="h-4 w-4" />}
                onClick={() => setInstalling(true)}
              >
                Install skill
              </Button>
            ) : undefined
          }
        />
        <InstallDialog open={installing} onClose={() => setInstalling(false)} />
        {error ? (
          <InlineError
            message="Couldn't load skills."
            onRetry={() => void refetch()}
          />
        ) : isLoading ? (
          <div className="grid gap-4 lg:grid-cols-[340px_1fr]">
            <div className="space-y-2">
              {Array.from({ length: 4 }, (_, i) => (
                <Skeleton key={i} className="h-20" />
              ))}
            </div>
            <Skeleton className="h-96" />
          </div>
        ) : !data?.skills.length ? (
          <EmptyState
            icon={<BookOpen className="h-9 w-9" />}
            title="No skills in this workspace"
            body="Add a folder with a SKILL.md under .agents/skills (or .claude/skills) and it appears here."
          />
        ) : (
          <div className="grid gap-4 lg:grid-cols-[340px_1fr]">
            <ul className="space-y-2" aria-label="Skills">
              {data.skills.map((s) => (
                <li key={s.name}>
                  <SkillRow
                    skill={s}
                    active={s.name === current}
                    onSelect={() => setSelected(s.name)}
                  />
                </li>
              ))}
              {data.problems.length > 0 && (
                <li className="rounded-xl border border-warning/40 bg-warning/5 p-3 text-[13px]">
                  <p className="mb-1 flex items-center gap-1.5 font-medium text-warning">
                    <FileWarning className="h-4 w-4" aria-hidden />
                    {data.problems.length} skill
                    {data.problems.length === 1 ? "" : "s"} couldn't load
                  </p>
                  {data.problems.map((p) => (
                    <p key={p.path} className="mt-1 text-muted">
                      <span className="font-mono text-[12px] text-foreground">
                        {p.path}
                      </span>
                      : {p.error}
                    </p>
                  ))}
                </li>
              )}
            </ul>
            {current && <SkillDetail name={current} />}
          </div>
        )}
      </div>
    </div>
  );
}

function SkillRow({
  skill,
  active,
  onSelect,
}: {
  skill: Skill;
  active: boolean;
  onSelect: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onSelect}
      aria-current={active}
      className={cx(
        "w-full cursor-pointer rounded-xl border p-3 text-left transition-colors duration-150",
        active
          ? "border-primary/60 bg-selected"
          : "border-border bg-surface hover:border-primary/40",
      )}
    >
      <span className="flex items-center gap-2">
        <BookOpen
          className={cx(
            "h-4 w-4 shrink-0",
            skill.enabled ? "text-ai-foreground" : "text-muted",
          )}
          aria-hidden
        />
        <span
          className={cx(
            "truncate font-mono text-[13px] font-medium",
            !skill.enabled && "text-muted",
          )}
        >
          {skill.name}
        </span>
        {skill.pinned && !skill.tampered && (
          <ShieldCheck
            className="ml-auto h-4 w-4 shrink-0 text-success"
            aria-label="Pinned install"
          />
        )}
        <span
          className={cx(
            "shrink-0 rounded-full px-2 py-0.5 text-[11px] font-medium",
            !(skill.pinned && !skill.tampered) && "ml-auto",
            skill.tampered
              ? "bg-danger/15 text-danger"
              : skill.enabled
                ? "bg-success/15 text-success"
                : "bg-surface-2 text-muted",
          )}
        >
          {skill.tampered ? "Changed" : skill.enabled ? "On" : "Off"}
        </span>
      </span>
      <span className="mt-1.5 line-clamp-2 block text-[13px] text-muted">
        {skill.description}
      </span>
    </button>
  );
}

function SkillDetail({ name }: { name: string }) {
  const { data, isLoading, error } = useSkill(name);
  const canManage = useCan("system:manage");
  const toast = useUIStore((s) => s.toast);
  const qc = useQueryClient();
  const [busy, setBusy] = useState(false);

  if (error)
    return (
      <InlineError message="Couldn't read this skill. It may have changed on disk; reload the list." />
    );
  if (isLoading || !data) return <Skeleton className="h-96" />;
  const s = data.skill;

  const toggle = async (on: boolean) => {
    setBusy(true);
    try {
      await client.setSkillEnabled(s.name, on);
      await qc.invalidateQueries({ queryKey: ["gubern", "skills"] });
      await qc.invalidateQueries({ queryKey: ["gubern", "skill", s.name] });
      toast({
        text: on
          ? `${s.name} is on. Agents can use it.`
          : `${s.name} is off. Agents won't be shown it.`,
        tone: "success",
      });
    } catch {
      toast({ text: "Couldn't change the skill.", tone: "error" });
    } finally {
      setBusy(false);
    }
  };

  return (
    <article className="rounded-xl border border-border bg-surface">
      <header className="flex flex-wrap items-start gap-3 border-b border-border p-4">
        <div className="min-w-0 flex-1">
          <h2 className="font-mono text-[15px] font-semibold">{s.name}</h2>
          <p className="mt-1 text-[13px] text-muted">{s.description}</p>
        </div>
        {s.tampered ? (
          <span className="flex items-center gap-1 text-[12px] font-medium text-danger">
            <ShieldAlert className="h-4 w-4" aria-hidden />
            Not offered
          </span>
        ) : canManage ? (
          <Toggle
            checked={s.enabled}
            onChange={(v) => void toggle(v)}
            label={s.enabled ? "Skill on" : "Skill off"}
            disabled={busy}
          />
        ) : (
          <span className="flex items-center gap-1 text-[12px] text-muted">
            <Lock className="h-3.5 w-3.5" aria-hidden />
            Admins can switch skills
          </span>
        )}
      </header>
      {s.tampered && (
        <p className="flex items-start gap-2 border-b border-border bg-danger/10 px-4 py-2.5 text-[13px]">
          <ShieldAlert
            className="mt-0.5 h-4 w-4 shrink-0 text-danger"
            aria-hidden
          />
          <span>
            This skill&apos;s files changed after it was approved, so agents
            aren&apos;t shown it. Review the change, then install it again to
            approve the new version.
          </span>
        </p>
      )}
      <dl className="grid gap-x-6 gap-y-2 border-b border-border p-4 text-[13px] sm:grid-cols-2">
        <Field label="File">
          <span className="font-mono text-[12px]">{s.path}</span>
        </Field>
        <Field label="Scope">
          {s.scope === "project" ? "This workspace" : s.scope}
        </Field>
        <Field label="Fingerprint">
          <button
            type="button"
            className="inline-flex cursor-pointer items-center gap-1 font-mono text-[12px] hover:text-primary"
            onClick={() => {
              void navigator.clipboard?.writeText(s.hash);
              toast({ text: "SHA-256 copied", tone: "info" });
            }}
            title={s.hash}
          >
            {s.hash.slice(0, 16)}
            <Copy className="h-3 w-3" aria-hidden />
          </button>
        </Field>
        {s.license && <Field label="License">{s.license}</Field>}
        <Field label="Install">
          {s.pinned ? (
            <span className="inline-flex items-center gap-1">
              <ShieldCheck className="h-3.5 w-3.5 text-success" aria-hidden />
              Approved and pinned
            </span>
          ) : (
            <span className="text-muted">Added to the repository by hand</span>
          )}
        </Field>
        {s.source && (
          <Field label="Source" wide>
            <span className="break-all font-mono text-[12px]">{s.source}</span>
          </Field>
        )}
        <Field label="Asks for tools" wide>
          {s.allowedTools.length ? (
            <span className="flex flex-wrap gap-1.5">
              {s.allowedTools.map((t) => (
                <span
                  key={t}
                  className="inline-flex items-center gap-1 rounded-md border border-border bg-surface-2 px-1.5 py-0.5 font-mono text-[12px]"
                >
                  <Wrench className="h-3 w-3 text-muted" aria-hidden />
                  {t}
                </span>
              ))}
              <span className="text-[12px] text-muted">
                A request only; your tool policy still decides.
              </span>
            </span>
          ) : (
            <span className="text-muted">None</span>
          )}
        </Field>
      </dl>
      {!s.enabled && (
        <p className="flex items-center gap-2 border-b border-border bg-surface-2 px-4 py-2 text-[13px] text-muted">
          <AlertTriangle className="h-4 w-4" aria-hidden />
          Off: agents aren't shown this skill.
        </p>
      )}
      <div className="max-h-[60vh] overflow-y-auto p-4 text-[14px]">
        <RichText text={data.body || "_This skill has no instructions._"} />
      </div>
    </article>
  );
}

function Field({
  label,
  children,
  wide,
}: {
  label: string;
  children: React.ReactNode;
  wide?: boolean;
}) {
  return (
    <div className={cx("min-w-0", wide && "sm:col-span-2")}>
      <dt className="text-[12px] text-muted">{label}</dt>
      <dd className="mt-0.5 break-words">{children}</dd>
    </div>
  );
}

function InstallDialog({
  open,
  onClose,
}: {
  open: boolean;
  onClose: () => void;
}) {
  const [source, setSource] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | undefined>();
  const [result, setResult] = useState<SkillInstall | undefined>();
  const navigate = useNavigate();
  const close = () => {
    setSource("");
    setError(undefined);
    setResult(undefined);
    onClose();
  };
  const submit = async () => {
    setBusy(true);
    setError(undefined);
    try {
      setResult(await client.installSkill(source.trim()));
    } catch (e) {
      setError(
        e instanceof Error ? e.message : "The skill couldn't be fetched.",
      );
    } finally {
      setBusy(false);
    }
  };
  return (
    <Dialog
      open={open}
      onClose={close}
      title="Install a skill"
      className="max-w-xl"
    >
      {!result ? (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            void submit();
          }}
          className="space-y-3 p-5"
        >
          <p className="text-[13px] text-muted">
            A folder on the server, or a git repository at a full commit.
            Nothing is installed until someone approves it; the approval shows
            every file and its fingerprint.
          </p>
          <label
            className="block text-[13px] font-medium"
            htmlFor="skill-source"
          >
            Source
          </label>
          <input
            id="skill-source"
            value={source}
            onChange={(e) => setSource(e.target.value)}
            placeholder="https://github.com/org/skills.git@<40-character commit>"
            className="w-full rounded-lg border border-border bg-background px-3 py-2 font-mono text-[13px] outline-none focus:border-primary"
            autoFocus
            spellCheck={false}
          />
          {error && <p className="text-[13px] text-danger">{error}</p>}
          <div className="flex justify-end gap-2 pt-1">
            <Button onClick={close}>Cancel</Button>
            <Button
              variant="primary"
              type="submit"
              disabled={busy || !source.trim()}
            >
              {busy ? "Fetching…" : "Fetch and review"}
            </Button>
          </div>
        </form>
      ) : (
        <div className="space-y-3 p-5 text-[13px]">
          <p className="flex items-center gap-2 rounded-lg bg-warning/10 px-3 py-2 text-warning">
            <ShieldAlert className="h-4 w-4 shrink-0" aria-hidden />
            Waiting for approval.{" "}
            <span className="font-mono">{result.skill.name}</span> is in
            quarantine.
          </p>
          <dl className="space-y-1">
            <div className="flex gap-2">
              <dt className="w-24 shrink-0 text-muted">Tree hash</dt>
              <dd className="min-w-0 flex-1 break-all font-mono text-[12px]">
                {result.treeHash}
              </dd>
            </div>
            <div className="flex gap-2">
              <dt className="w-24 shrink-0 text-muted">Files</dt>
              <dd className="min-w-0 flex-1">
                <ul className="space-y-0.5">
                  {result.files.map((f) => (
                    <li
                      key={f.path}
                      className="flex justify-between gap-3 font-mono text-[12px]"
                    >
                      <span className="truncate">{f.path}</span>
                      <span className="shrink-0 text-muted">
                        {f.size} B · {f.sha256.slice(0, 12)}
                      </span>
                    </li>
                  ))}
                </ul>
              </dd>
            </div>
          </dl>
          <div className="flex justify-end gap-2 pt-1">
            <Button onClick={close}>Close</Button>
            <Button
              variant="primary"
              onClick={() => {
                close();
                navigate(
                  `/chat/${encodeURIComponent(result.sessionId)}?approval=${encodeURIComponent(result.approvalId)}`,
                );
              }}
            >
              Review the approval
            </Button>
          </div>
        </div>
      )}
    </Dialog>
  );
}
