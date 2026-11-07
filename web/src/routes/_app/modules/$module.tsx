import { useMutation, useQuery, useQueryClient, useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute, Link, notFound } from "@tanstack/react-router";
import { format } from "date-fns";
import { ArrowLeft, Bot, CircleAlert } from "lucide-react";
import { Suspense } from "react";
import { api, data, type Schemas } from "@/api/client";
import { AllowlistCard } from "@/components/allowlist-card";
import { SettingsCard } from "@/components/settings/settings-card";
import { outcomeTone, StatusDot } from "@/components/status-dot";
import { Tile } from "@/components/tile";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { listMeta, moduleMeta } from "@/lib/meta";
import { modulesQuery, recordsQuery, settingsQuery } from "@/queries/modules";
import { useChatNames } from "@/queries/names";

export const Route = createFileRoute("/_app/modules/$module")({
  loader: async ({ context, params }) => {
    const modules = await context.queryClient.ensureQueryData(modulesQuery);
    if (!modules.some((m) => m.id === params.module)) throw notFound();
    void context.queryClient.prefetchQuery(settingsQuery(params.module));
  },
  component: ModulePage,
});

function ModulePage() {
  const { module: id } = Route.useParams();
  const { data: modules } = useSuspenseQuery(modulesQuery);
  const m = modules.find((x) => x.id === id) as Schemas["Module"];
  const meta = moduleMeta(id);
  return (
    <main className="min-w-0 px-5 py-6 sm:px-10 sm:py-7">
      <Link to="/modules" className="mb-4 inline-flex items-center gap-1 text-muted-foreground text-sm md:hidden">
        <ArrowLeft className="size-4" /> Modules
      </Link>
      <div className="flex flex-wrap items-center gap-x-4 gap-y-3">
        <Tile tone={meta.tone} icon={meta.icon} size="lg" />
        <div className="min-w-0 flex-1 basis-56">
          <h1 className="font-bold text-2xl leading-tight tracking-[-0.035em] sm:text-[1.875rem]">{meta.title}</h1>
          <p className="text-muted-foreground text-sm">{meta.about}</p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <IgnoreBots module={m} />
          <Power module={m} />
        </div>
      </div>
      {m.problem && (
        <div className="mt-5 flex gap-2.5 rounded-xl border border-destructive/24 bg-destructive/4 px-3.5 py-3 text-sm">
          <CircleAlert className="mt-0.5 size-4 flex-none text-destructive-foreground" />
          <div>
            <div className="font-medium">Its settings do not build</div>
            <p className="mt-0.5 break-words text-muted-foreground">{m.problem}</p>
          </div>
        </div>
      )}
      <div className="mt-6 grid grid-cols-1 items-start gap-4 xl:grid-cols-[minmax(0,1.15fr)_minmax(0,1fr)]">
        <Suspense fallback={<Skeleton className="h-96 w-full rounded-2xl" />}>
          <SettingsCard module={id} />
        </Suspense>
        <div className="flex flex-col gap-4">
          {m.allowlists.map((l, i) => (
            <AllowlistCard
              key={l.name}
              module={id}
              list={l.name}
              meta={listMeta(id, l, i)}
              description={l.description}
            />
          ))}
          <Activity module={m} />
        </div>
      </div>
    </main>
  );
}

function Power({ module: m }: { module: Schemas["Module"] }) {
  const queryClient = useQueryClient();
  const toggle = useMutation({
    mutationFn: (enabled: boolean) =>
      data(api.PATCH("/api/modules/{module}", { params: { path: { module: m.id } }, body: { enabled } })),
    onSettled: () => queryClient.invalidateQueries({ queryKey: ["modules"] }),
  });
  const on = toggle.isPending ? Boolean(toggle.variables) : m.enabled;
  return (
    <div className="flex items-center gap-3 rounded-full border bg-background py-1.5 ps-3 pe-1.5 shadow-xs/5">
      <StatusDot tone={m.running ? "ok" : m.problem ? "error" : "off"} />
      <span className="font-medium text-sm">{m.running ? "Running" : m.enabled ? "Not running" : "Off"}</span>
      <Switch
        checked={on}
        disabled={toggle.isPending}
        onCheckedChange={(v) => toggle.mutate(v)}
        aria-label="Run this module"
      />
    </div>
  );
}

/** IgnoreBots is the module's "ignore bots" switch: on, messages from bot
 * accounts never reach it. It applies at once. */
function IgnoreBots({ module: m }: { module: Schemas["Module"] }) {
  const queryClient = useQueryClient();
  const toggle = useMutation({
    mutationFn: (ignore_bots: boolean) =>
      data(api.PATCH("/api/modules/{module}", { params: { path: { module: m.id } }, body: { ignore_bots } })),
    onSettled: () => queryClient.invalidateQueries({ queryKey: ["modules"] }),
  });
  const on = toggle.isPending ? Boolean(toggle.variables) : m.ignore_bots;
  return (
    <div
      className="flex items-center gap-2.5 rounded-full border bg-background py-1.5 ps-3 pe-1.5 shadow-xs/5"
      title="Messages from bot accounts never reach this module. Accounts a program drives for a person (userbots) are not told apart."
    >
      <Bot className="size-4 text-muted-foreground" />
      <span className="font-medium text-sm">Ignore bots</span>
      <Switch
        checked={on}
        disabled={toggle.isPending}
        onCheckedChange={(v) => toggle.mutate(v)}
        aria-label="Ignore messages from bot accounts"
      />
    </div>
  );
}

function Activity({ module: m }: { module: Schemas["Module"] }) {
  const { data: records } = useQuery(recordsQuery(m.id));
  const chatName = useChatNames();
  const stats = [
    { label: "Completed", value: m.counts.completed },
    { label: "Slow", value: m.counts.slow },
    { label: "Failed", value: m.counts.failed + m.counts.panicked },
  ];
  return (
    <section className="surface-raised rounded-2xl">
      <header className="flex items-baseline gap-2 px-4 pt-3.5 pb-2.5">
        <h2 className="font-semibold text-[15px]">Activity</h2>
        <span className="text-muted-foreground text-xs">since the bot started</span>
      </header>
      <div className="mx-3.5 grid grid-cols-3 gap-2">
        {stats.map((s) => (
          <div key={s.label} className="surface-inset rounded-xl px-3 py-2">
            <div className="text-muted-foreground text-xs">{s.label}</div>
            <div className="font-semibold text-lg tabular tracking-tight">{s.value}</div>
          </div>
        ))}
      </div>
      <ul className="surface-inset mx-3.5 mt-2 mb-3.5 divide-y divide-border/80 rounded-xl px-3">
        {(records?.records ?? []).map((r) => (
          <li key={r.id} className="flex items-center gap-2.5 py-2">
            <StatusDot tone={outcomeTone(r.outcome)} />
            <span className="min-w-0 flex-1 truncate font-mono text-xs">
              {r.kind}
              {r.conversation && (
                <span className="font-sans text-muted-foreground"> · {chatName(r.platform, r.conversation)}</span>
              )}
            </span>
            <time className="font-mono text-[11.5px] text-muted-foreground" dateTime={r.at}>
              {format(new Date(r.at), "MMM d HH:mm:ss")}
            </time>
          </li>
        ))}
        {records?.records?.length === 0 && (
          <li className="py-4 text-center text-muted-foreground text-sm">No records yet.</li>
        )}
      </ul>
    </section>
  );
}
