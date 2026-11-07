import { useQuery, useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { format, formatDistanceToNowStrict } from "date-fns";
import { Activity, Database } from "lucide-react";
import type { ReactNode } from "react";
import type { Schemas } from "@/api/client";
import { outcomeTone, StatusDot } from "@/components/status-dot";
import { Tile } from "@/components/tile";
import { moduleMeta, platformMeta } from "@/lib/meta";
import { ago, took, useNow } from "@/lib/time";
import { cn } from "@/lib/utils";
import { failuresQuery } from "@/queries/events";
import { modulesQuery } from "@/queries/modules";
import { useChatNames } from "@/queries/names";
import { statusQuery } from "@/queries/status";

export const Route = createFileRoute("/_app/status")({
  loader: ({ context }) => context.queryClient.ensureQueryData(statusQuery),
  component: StatusPage,
});

function StatusPage() {
  const { data: status } = useSuspenseQuery(statusQuery);
  const down = status.drivers.filter((d) => !d.ready);
  const healthy = status.ready && status.event_log.ok;
  return (
    <main className="mx-auto w-full max-w-6xl px-5 py-7 sm:px-8">
      <div className="flex flex-wrap items-end gap-x-6 gap-y-2">
        <div>
          <h1 className="font-bold text-[1.875rem] leading-tight tracking-[-0.035em]">
            {healthy
              ? "Everything is running"
              : down.length
                ? "A platform is not connected"
                : "The event log is failing"}
          </h1>
          <p className="text-muted-foreground text-sm">
            Up {formatDistanceToNowStrict(new Date(status.started_at))}, since{" "}
            {format(new Date(status.started_at), "MMM d, HH:mm")} ·{" "}
            <span className="font-mono" title={status.version}>
              {status.version.split(" ").at(-1)?.slice(0, 12)}
            </span>
          </p>
        </div>
      </div>
      <div className="mt-6 grid grid-cols-1 items-start gap-4 lg:grid-cols-2">
        <div className="flex flex-col gap-4">
          <Card title="Platforms" note={`${status.drivers.length} account${status.drivers.length === 1 ? "" : "s"}`}>
            {status.drivers.map((d) => (
              <Driver key={`${d.platform}/${d.account}`} d={d} />
            ))}
            {status.drivers.length === 0 && <Empty>No platform accounts are configured.</Empty>}
          </Card>
          <Modules />
        </div>
        <div className="flex flex-col gap-4">
          <Working running={status.running} />
          <Failures />
          <Card title="Runtime">
            <Fact icon={<Tile tone="slate" icon={Activity} size="sm" />} label="Kernel" value={status.kernel.state}>
              {status.kernel.in_flight} of {status.kernel.max_in_flight} events in flight, {status.kernel.workers}{" "}
              workers
            </Fact>
            <Fact
              icon={<Tile tone="slate" icon={Database} size="sm" />}
              label="Event log"
              value={status.event_log.ok ? "Writing" : "Failing"}
              tone={status.event_log.ok ? "ok" : "error"}
            >
              {status.event_log.ok
                ? `Keeps ${Math.round(status.event_log.retention_ms / 86_400_000)} days of records`
                : status.event_log.last_error}
            </Fact>
          </Card>
        </div>
      </div>
    </main>
  );
}

function Card({ title, note, children }: { title: string; note?: string; children: ReactNode }) {
  return (
    <section className="surface-raised rounded-2xl">
      <header className="flex items-baseline gap-2 px-4 pt-3.5 pb-2.5">
        <h2 className="font-semibold text-[15px]">{title}</h2>
        {note && <span className="text-muted-foreground text-xs">{note}</span>}
      </header>
      <ul className="surface-inset mx-3.5 mb-3.5 divide-y divide-border/80 rounded-xl px-3">{children}</ul>
    </section>
  );
}

function Empty({ children }: { children: ReactNode }) {
  return <li className="py-5 text-center text-muted-foreground text-sm">{children}</li>;
}

function Fact({
  icon,
  label,
  value,
  tone = "ok",
  children,
}: {
  icon: ReactNode;
  label: string;
  value: string;
  tone?: "ok" | "error";
  children: ReactNode;
}) {
  return (
    <li className="flex items-center gap-3 py-2.5">
      {icon}
      <div className="min-w-0 flex-1">
        <div className="font-medium text-sm">{label}</div>
        <div className="truncate text-muted-foreground text-xs">{children}</div>
      </div>
      <span className="flex items-center gap-2 text-sm">
        <StatusDot tone={tone} /> {value}
      </span>
    </li>
  );
}

function Driver({ d }: { d: Schemas["DriverStatus"] }) {
  const p = platformMeta(d.platform);
  const backlog = d.pending_inbox > 0 || d.failed_inbox > 0 || d.gaps > 0;
  return (
    <li className="py-2.5">
      <div className="flex items-center gap-3">
        <Tile tone={p.tone} icon={p.icon} size="sm" />
        <div className="min-w-0 flex-1">
          <div className="font-medium text-sm">
            {p.title} <span className="font-mono text-muted-foreground text-xs">{d.account}</span>
          </div>
          <div className="text-muted-foreground text-xs">
            {d.last_event ? `Last event ${ago(d.last_event)} ago` : "No events yet"}
            {backlog && ` · ${d.pending_inbox} waiting, ${d.failed_inbox} failed, ${d.gaps} gaps`}
          </div>
        </div>
        <span className="flex items-center gap-2 text-sm">
          <StatusDot tone={d.ready ? "ok" : "error"} /> {d.ready ? "Ready" : "Not ready"}
        </span>
      </div>
      {d.error && (
        <p className="mt-2 rounded-lg bg-destructive/6 px-2.5 py-1.5 text-destructive-foreground text-xs">{d.error}</p>
      )}
    </li>
  );
}

function Modules() {
  const { data: modules = [] } = useQuery(modulesQuery);
  const running = modules.filter((m) => m.running);
  return (
    <Card title="Modules" note={`${running.length} running, runs since start`}>
      {running.map((m) => {
        const meta = moduleMeta(m.id);
        const failed = m.counts.failed + m.counts.panicked;
        return (
          <li key={m.id}>
            <Link
              to="/modules/$module"
              params={{ module: m.id }}
              className="flex items-center gap-3 py-2.5 outline-none focus-visible:ring-2 focus-visible:ring-ring"
            >
              <Tile tone={meta.tone} icon={meta.icon} size="sm" />
              <span className="min-w-0 flex-1 truncate font-medium text-sm">{meta.title}</span>
              <span className="text-muted-foreground text-xs tabular">{m.counts.completed} done</span>
              <span
                className={cn("text-xs tabular", m.counts.slow ? "text-warning-foreground" : "text-muted-foreground")}
              >
                {m.counts.slow} slow
              </span>
              <span
                className={cn(
                  "text-xs tabular",
                  failed ? "font-medium text-destructive-foreground" : "text-muted-foreground",
                )}
              >
                {failed} failed
              </span>
            </Link>
          </li>
        );
      })}
      {running.length === 0 && <Empty>No module is running. Turn modules on under Modules.</Empty>}
    </Card>
  );
}

/** Working lists what modules are handling now, each running for a time
 * that ticks, and opens its task. */
function Working({ running }: { running: Schemas["Running"][] }) {
  const now = useNow(running.length > 0);
  const chatName = useChatNames();
  return (
    <Card title="Working now" note={running.length ? `${running.length} running` : undefined}>
      {running.map((r) => (
        <li key={`${r.module}/${r.origin}`}>
          <Link
            to="/events"
            search={r.event ? { view: "tasks", event: r.event } : { view: "records", q: r.origin }}
            className="flex items-center gap-2.5 py-2.5 outline-none focus-visible:ring-2 focus-visible:ring-ring"
          >
            <StatusDot tone="running" />
            <span className="min-w-0 flex-1">
              <span className="block truncate font-mono text-xs">{r.module}</span>
              <span className="block truncate text-muted-foreground text-xs">
                {r.event ? chatName(r.platform, r.conversation) : "A job or timer"}
              </span>
            </span>
            <span className="font-medium text-sky-700 text-xs tabular">
              {took(now - new Date(r.started).getTime())}
            </span>
          </Link>
        </li>
      ))}
      {running.length === 0 && <Empty>No module is handling anything right now.</Empty>}
    </Card>
  );
}

function Failures() {
  const { data } = useQuery(failuresQuery);
  const records = data?.records ?? [];
  return (
    <Card title="Recent failures">
      {records.map((r) => (
        <li key={r.id}>
          <Link
            to="/events"
            search={{ view: "records", record: r.id }}
            className="flex items-center gap-2.5 py-2.5 outline-none focus-visible:ring-2 focus-visible:ring-ring"
          >
            <StatusDot tone={outcomeTone(r.outcome)} />
            <span className="min-w-0 flex-1">
              <span className="block truncate font-mono text-xs">
                {r.kind} <span className="text-muted-foreground">· {r.component}</span>
              </span>
              <span className="block truncate text-muted-foreground text-xs">{errorText(r.payload)}</span>
            </span>
            <time className="font-mono text-[11.5px] text-muted-foreground" dateTime={r.at}>
              {ago(r.at)}
            </time>
          </Link>
        </li>
      ))}
      {records.length === 0 && <Empty>Nothing has failed within the retention.</Empty>}
    </Card>
  );
}

function errorText(payload: unknown): string {
  const p = payload as { error?: string } | null;
  return p?.error ?? "";
}
