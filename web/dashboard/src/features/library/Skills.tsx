import { useState } from "react";
import {
  AlertTriangle,
  BookOpen,
  Copy,
  FileWarning,
  Lock,
  Wrench,
} from "lucide-react";
import { useQueryClient } from "@tanstack/react-query";
import { client, useCan, useSkill, useSkills } from "../../data/client";
import type { Skill } from "../../data/model";
import { useUIStore } from "../../store";
import { RichText } from "../../ui/RichText";
import { EmptyState, InlineError, Skeleton, Toggle } from "../../ui/primitives";
import { cx } from "../../ui/util";
import { PageHeader } from "./PageHeader";

// Skills (agent-platform Phase 2): the workspace's SKILL.md know-how that
// /agent offers the model. A skill is knowledge, never a grant: turning one
// off only removes it from what the model is shown.

export function Skills() {
  const { data, isLoading, error, refetch } = useSkills();
  const [selected, setSelected] = useState<string | undefined>();
  const current = selected ?? data?.skills[0]?.name;
  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto max-w-6xl px-5 py-8">
        <PageHeader
          title="Skills"
          body="Procedures agents can follow, read from SKILL.md files in the workspace. Skills never grant permissions."
        />
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
        <span
          className={cx(
            "ml-auto shrink-0 rounded-full px-2 py-0.5 text-[11px] font-medium",
            skill.enabled
              ? "bg-success/15 text-success"
              : "bg-surface-2 text-muted",
          )}
        >
          {skill.enabled ? "On" : "Off"}
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
        {canManage ? (
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
