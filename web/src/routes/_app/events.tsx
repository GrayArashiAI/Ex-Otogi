import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { useVirtualizer } from "@tanstack/react-virtual";
import { format } from "date-fns";
import { Radio, Search, X } from "lucide-react";
import { type ReactNode, useEffect, useMemo, useRef, useState } from "react";
import { z } from "zod";
import type { Schemas } from "@/api/client";
import { outcomeTone, StatusDot } from "@/components/status-dot";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectItem, SelectPopup, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Sheet, SheetDescription, SheetHeader, SheetPanel, SheetPopup, SheetTitle } from "@/components/ui/sheet";
import { Switch } from "@/components/ui/switch";
import { cn } from "@/lib/utils";
import { type EventFilter, eventsQuery, newerQuery, traceQuery } from "@/queries/events";
import { useChatNames } from "@/queries/names";

const search = z.object({
  kind: z.string().optional(),
  component: z.string().optional(),
  conversation: z.string().optional(),
  outcome: z.string().optional(),
  q: z.string().optional(),
  record: z.number().optional(),
});

export const Route = createFileRoute("/_app/events")({
  validateSearch: search,
  component: EventLog,
});

type Entry = Schemas["Entry"];

function EventLog() {
  const { record, ...filter } = Route.useSearch();
  const navigate = useNavigate({ from: Route.fullPath });
  const list = useInfiniteQuery(eventsQuery(filter));
  const [live, setLive] = useState(false);
  const [fresh, setFresh] = useState<Entry[]>([]);
  // biome-ignore lint/correctness/useExhaustiveDependencies: new filters start the tail over
  useEffect(() => setFresh([]), [JSON.stringify(filter)]);
  const loaded = list.data?.pages.flatMap((p) => p.records) ?? [];
  const newest = fresh[0]?.id ?? loaded[0]?.id;
  const newer = useQuery({ ...newerQuery(filter, live ? newest : undefined), refetchInterval: live ? 3000 : false });
  useEffect(() => {
    const got = newer.data?.records ?? [];
    if (got.length) setFresh((f) => [...got.filter((r) => !f.some((x) => x.id === r.id)), ...f]);
  }, [newer.data]);
  const records = useMemo(() => [...fresh, ...loaded], [fresh, loaded]);
  const chatName = useChatNames();
  const scroller = useRef<HTMLDivElement>(null);
  const virtualizer = useVirtualizer({
    count: records.length,
    getScrollElement: () => scroller.current,
    estimateSize: () => 40,
    overscan: 12,
    getItemKey: (i) => records[i]?.id ?? i,
  });
  const set = (patch: Partial<EventFilter>) =>
    void navigate({ search: (s) => ({ ...s, ...patch, record: undefined }), replace: true });
  return (
    <main className="flex min-h-0 flex-1 flex-col md:h-[calc(100svh-3.5rem)]">
      <div className="grid grid-cols-2 items-center gap-2 border-b px-4 py-3 sm:flex sm:px-6">
        <div className="relative col-span-2 min-w-48 flex-1">
          <Search className="pointer-events-none absolute top-1/2 left-2.5 z-10 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            className="ps-7"
            placeholder="Text in the payload"
            defaultValue={filter.q}
            onKeyDown={(e) => e.key === "Enter" && set({ q: e.currentTarget.value || undefined })}
          />
        </div>
        <FilterInput label="Kind" value={filter.kind} onChange={(kind) => set({ kind })} placeholder="Kind" />
        <FilterInput
          label="Component"
          value={filter.component}
          onChange={(component) => set({ component })}
          placeholder="Component"
        />
        <FilterInput
          label="Chat"
          value={filter.conversation}
          onChange={(conversation) => set({ conversation })}
          placeholder="Chat"
        />
        <Select
          items={[
            { value: "", label: "Any outcome" },
            { value: "ok", label: "ok" },
            { value: "error", label: "error" },
            { value: "slow", label: "slow" },
            { value: "degraded", label: "degraded" },
            { value: "skipped", label: "skipped" },
          ]}
          value={filter.outcome ?? ""}
          onValueChange={(v) => set({ outcome: v || undefined })}
        >
          <SelectTrigger className="w-full sm:w-36">
            <SelectValue />
          </SelectTrigger>
          <SelectPopup>
            {["", "ok", "error", "slow", "degraded", "skipped"].map((v) => (
              <SelectItem key={v} value={v}>
                {v || "Any outcome"}
              </SelectItem>
            ))}
          </SelectPopup>
        </Select>
        <span className="col-span-2 flex items-center justify-end gap-2 text-sm sm:ml-auto">
          <Radio className={cn("size-4", live ? "text-emerald-600" : "text-muted-foreground")} />
          Live
          <Switch checked={live} onCheckedChange={setLive} aria-label="Show new records as they arrive" />
        </span>
      </div>
      <div className="hidden grid-cols-[1rem_8.5rem_minmax(0,14rem)_minmax(0,10rem)_minmax(0,1fr)_4.5rem] gap-3 border-b bg-muted/40 px-4 py-1.5 text-muted-foreground text-xs sm:grid sm:px-6">
        <span />
        <span>Time</span>
        <span>Kind</span>
        <span>Component</span>
        <span>Chat</span>
        <span className="text-right">Took</span>
      </div>
      <div
        ref={scroller}
        className="min-h-0 flex-1 overflow-y-auto"
        onScroll={(e) => {
          const el = e.currentTarget;
          if (el.scrollHeight - el.scrollTop - el.clientHeight < 400 && list.hasNextPage && !list.isFetchingNextPage) {
            void list.fetchNextPage();
          }
        }}
      >
        {list.isPending && <p className="py-16 text-center text-muted-foreground text-sm">Loading records…</p>}
        {!list.isPending && records.length === 0 && (
          <p className="py-16 text-center text-muted-foreground text-sm">No records match.</p>
        )}
        <div className="relative w-full" style={{ height: virtualizer.getTotalSize() }}>
          {virtualizer.getVirtualItems().map((row) => {
            const r = records[row.index] as Entry;
            return (
              <Link
                key={row.key}
                to="/events"
                search={(s) => ({ ...s, record: r.id })}
                data-index={row.index}
                ref={virtualizer.measureElement}
                className={cn(
                  "absolute inset-x-0 top-0 grid grid-cols-[1rem_minmax(0,1fr)_auto] items-center gap-x-3 gap-y-0.5 border-b border-border/60 px-4 py-2 text-[13px] outline-none hover:bg-accent/60 focus-visible:bg-accent sm:grid-cols-[1rem_8.5rem_minmax(0,14rem)_minmax(0,10rem)_minmax(0,1fr)_4.5rem] sm:px-6",
                  record === r.id && "bg-accent",
                  fresh.some((f) => f.id === r.id) && "animate-in fade-in",
                )}
                style={{ transform: `translateY(${row.start}px)` }}
              >
                <StatusDot tone={outcomeTone(r.outcome)} />
                <time className="order-3 font-mono text-[11.5px] text-muted-foreground sm:order-none" dateTime={r.at}>
                  {format(new Date(r.at), "MMM d HH:mm:ss")}
                </time>
                <span className="truncate font-mono text-xs">{r.kind}</span>
                <span className="col-start-2 truncate text-muted-foreground text-xs sm:col-start-auto">
                  {r.component}
                </span>
                <span className="hidden truncate text-muted-foreground text-xs sm:block">
                  {chatName(r.platform, r.conversation)}
                </span>
                <span className="hidden text-right text-muted-foreground text-xs tabular sm:block">
                  {r.duration_ms ? `${(r.duration_ms / 1000).toFixed(1)} s` : ""}
                </span>
              </Link>
            );
          })}
        </div>
        {list.isFetchingNextPage && <p className="py-4 text-center text-muted-foreground text-xs">Loading older…</p>}
      </div>
      <RecordSheet record={records.find((r) => r.id === record)} open={record !== undefined} />
    </main>
  );
}

function FilterInput({
  label,
  value,
  onChange,
  placeholder,
}: {
  label: string;
  value?: string;
  onChange: (v: string | undefined) => void;
  placeholder: string;
}) {
  if (value) {
    return (
      <button
        type="button"
        onClick={() => onChange(undefined)}
        className="inline-flex h-8 items-center gap-1.5 rounded-lg border bg-accent px-2.5 text-xs"
        aria-label={`Clear ${label.toLowerCase()} filter`}
      >
        <span className="text-muted-foreground">{label}</span>
        <span className="font-mono">{value}</span>
        <X className="size-3.5 text-muted-foreground" />
      </button>
    );
  }
  return (
    <Input
      className="w-full text-xs sm:w-36"
      placeholder={placeholder}
      aria-label={label}
      onKeyDown={(e) => e.key === "Enter" && e.currentTarget.value && onChange(e.currentTarget.value.trim())}
    />
  );
}

function RecordSheet({ record, open }: { record?: Entry; open: boolean }) {
  const navigate = useNavigate({ from: Route.fullPath });
  const close = () => void navigate({ search: (s) => ({ ...s, record: undefined }), replace: true });
  return (
    <Sheet open={open} onOpenChange={(o) => !o && close()}>
      <SheetPopup className="sm:max-w-2xl">
        {record ? (
          <>
            <SheetHeader>
              <SheetTitle className="flex items-center gap-2 font-mono text-base">
                <StatusDot tone={outcomeTone(record.outcome)} />
                {record.kind}
              </SheetTitle>
              <SheetDescription>
                {record.component} · {format(new Date(record.at), "MMM d, yyyy HH:mm:ss")}
              </SheetDescription>
            </SheetHeader>
            <SheetPanel className="flex flex-col gap-5">
              <dl className="surface-inset grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1.5 rounded-xl px-3.5 py-3 text-[13px]">
                <Fact label="Record">{record.id}</Fact>
                {record.outcome && <Fact label="Outcome">{record.outcome}</Fact>}
                {record.duration_ms !== undefined && (
                  <Fact label="Took">{(record.duration_ms / 1000).toFixed(1)} s</Fact>
                )}
                {record.event && <Fact label="Event">{record.event}</Fact>}
                {record.platform && (
                  <Fact label="Chat">
                    {record.conversation ? (
                      <Link
                        to="/chats/$chat"
                        params={{ chat: `${record.platform}:${record.conversation}` }}
                        className="underline-offset-3 hover:underline"
                      >
                        {record.platform}:{record.conversation}
                      </Link>
                    ) : (
                      record.platform
                    )}{" "}
                    <span className="text-muted-foreground">via {record.account}</span>
                  </Fact>
                )}
              </dl>
              {record.event && <Trace event={record.event} current={record.id} />}
              <section>
                <h3 className="mb-2 font-semibold text-[13px]">Payload</h3>
                <pre className="surface-inset overflow-x-auto whitespace-pre-wrap break-all rounded-xl px-3.5 py-3 font-mono text-xs leading-relaxed">
                  <Json value={record.payload} />
                </pre>
              </section>
            </SheetPanel>
          </>
        ) : (
          <SheetPanel>
            <p className="text-muted-foreground text-sm">
              This record is not among those loaded; it may be gone with the retention.
            </p>
          </SheetPanel>
        )}
      </SheetPopup>
    </Sheet>
  );
}

function Fact({ label, children }: { label: string; children: ReactNode }) {
  return (
    <>
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="truncate font-mono text-xs leading-5">{children}</dd>
    </>
  );
}

/** Trace lays one event's records on a timeline: each bar spans its
 * duration, ending when it was recorded. */
function Trace({ event, current }: { event: string; current: number }) {
  const { data } = useQuery(traceQuery(event));
  const records = data?.records ?? [];
  if (records.length === 0) return null;
  const span = (r: Entry) => {
    const end = new Date(r.at).getTime();
    return [end - (r.duration_ms ?? 0), end] as const;
  };
  const t0 = Math.min(...records.map((r) => span(r)[0]));
  const t1 = Math.max(...records.map((r) => span(r)[1]), t0 + 1);
  return (
    <section>
      <h3 className="mb-2 font-semibold text-[13px]">
        Trace <span className="font-normal text-muted-foreground">· {((t1 - t0) / 1000).toFixed(1)} s</span>
      </h3>
      <ol className="surface-inset flex flex-col gap-1 rounded-xl px-3.5 py-3">
        {records.map((r) => {
          const [a, b] = span(r);
          return (
            <li key={r.id}>
              <Link
                to="/events"
                search={(s) => ({ ...s, record: r.id })}
                className={cn(
                  "grid grid-cols-[minmax(0,11rem)_minmax(0,1fr)_3rem] items-center gap-3 rounded-md px-1 py-0.5 hover:bg-background",
                  r.id === current && "bg-background shadow-[0_0_0_1px_--theme(--color-black/6%)]",
                )}
              >
                <span className="truncate font-mono text-[11.5px]">{r.kind}</span>
                <span className="relative h-4">
                  <span
                    className={cn(
                      "absolute top-1/2 h-2 -translate-y-1/2 rounded-full",
                      r.outcome === "error"
                        ? "bg-red-500"
                        : r.duration_ms
                          ? "bg-linear-to-r from-neutral-600 to-neutral-800"
                          : "bg-neutral-400",
                    )}
                    style={{
                      left: `${((a - t0) / (t1 - t0)) * 100}%`,
                      width: `max(0.375rem, ${((b - a) / (t1 - t0)) * 100}%)`,
                    }}
                  />
                </span>
                <span className="text-right text-[11.5px] text-muted-foreground tabular">
                  {r.duration_ms ? `${(r.duration_ms / 1000).toFixed(1)} s` : ""}
                </span>
              </Link>
            </li>
          );
        })}
      </ol>
    </section>
  );
}

/** Json shows a payload in full; very long strings, such as inline images,
 * fold until opened. */
function Json({ value }: { value: unknown }) {
  const text = JSON.stringify(value, null, 2) ?? "null";
  const [open, setOpen] = useState(false);
  if (open || text.length < 20_000) return <>{text}</>;
  return (
    <>
      {text.replace(/"([A-Za-z0-9+/=]{400,})"/g, (_, s: string) => `"${s.slice(0, 80)}… ${s.length} characters"`)}
      {"\n"}
      <Button variant="link" size="xs" className="px-0" onClick={() => setOpen(true)}>
        Show the long values in full
      </Button>
    </>
  );
}
