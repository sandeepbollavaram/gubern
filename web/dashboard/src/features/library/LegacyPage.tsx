import { PageHeader } from "./PageHeader";

// Wraps an existing page in the new page chrome while it moves into the new IA.
export function LegacyPage({ title, body, children }: { title: string; body: string; children: React.ReactNode }) {
  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto max-w-6xl px-5 py-8">
        <PageHeader title={title} body={body} />
        {children}
      </div>
    </div>
  );
}
