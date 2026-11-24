import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { useVirtualizer } from "@tanstack/react-virtual";
import { format } from "date-fns";
import { Radio, X } from "lucide-react";
import { type ReactNode, useEffect, useMemo, useRef, useState } from "react";
import { z } from "zod";
import type { Schemas } from "@/api/client";
import { ChatFilter } from "@/components/chat-filter";
import { LoadError } from "@/components/load-error";
import { SearchInput } from "@/components/search-input";
import { Segmented } from "@/components/segmented";
import { outcomeTone, StatusDot } from "@/components/status-dot";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectItem, SelectPopup, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Sheet, SheetDescription, SheetHeader, SheetPanel, SheetPopup, SheetTitle } from "@/components/ui/sheet";
import { Switch } from "@/components/ui/switch";
import { took, useNow } from "@/lib/time";
import { cn } from "@/lib/utils";
import {
  type EventFilter,
  eventsQuery,
  newerQuery,
  recordQuery,
  type TaskFilter,
  tasksQuery,
  traceQuery,
} from "@/queries/events";
import { useChatNames } from "@/queries/names";

const search = z.object({
  view: z.enum(["tasks", "records"]).optional(),
  kind: z.string().optional(),
  component: z.string().optional(),
  conversation: z.string().optional(),
  outcome: z.string().optional(),
  q: z.string().optional(),
  record: z.number().optional(),
  event: z.string().optional(),
});

export const Route = createFileRoute("/_app/events")({
  validateSearch: search,
  head: () => ({ meta: [{ title: "Event log · otogi" }] }),
  component: EventLog,
});

type Entry = Schemas["Entry"];
type Task = Schemas["Task"];
type Running = Schemas["Running"];

const outcomes = {
  tasks: ["", "running", "error", "slow", "degraded", "ok"],
  records: ["", "ok", "error", "slow", "degraded", "skipped"],
};

function EventLog() {
  const { view = "tasks", record, event, ...filter } = Route.useSearch();
  const navigate = useNavigate({ from: Route.fullPath });
  const [live, setLive] = useState(false);
  const set = (patch: Partial<z.infer<typeof search>>) =>
    void navigate({ search: (s) => ({ ...s, ...patch, record: undefined, event: undefined }), replace: true });
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <h1 className="sr-only">Event log</h1>
      <div className="grid flex-none grid-cols-2 items-center gap-2 border-b px-4 py-3 sm:flex sm:flex-wrap sm:px-6">
        <Segmented
          label="View"
          className="col-span-2 sm:w-44"
          value={view}
          onChange={(v) => set({ view: v, kind: undefined, component: undefined, outcome: undefined })}
          options={[
            { value: "tasks", label: "Tasks" },
            { value: "records", label: "Records" },
          ]}
        />
        <TextFilter
          className="col-span-2 sm:min-w-48 sm:flex-1"
          label="Text in the payload"
          value={filter.q}
          onChange={(q) => set({ q })}
          search
        />
        {view === "records" && (
          <>
            <TextFilter className="sm:w-36" label="Kind" value={filter.kind} onChange={(kind) => set({ kind })} />
            <TextFilter
              className="sm:w-36"
              label="Component"
              value={filter.component}
              onChange={(component) => set({ component })}
            />
          </>
        )}
        <ChatFilter
          className="sm:w-44"
          value={filter.conversation}
          onChange={(conversation) => set({ conversation })}
        />
        <Select
          items={outcomes[view].map((v) => ({ value: v, label: v || "Any outcome" }))}
          value={filter.outcome ?? ""}
          onValueChange={(v) => set({ outcome: v || undefined })}
        >
          <SelectTrigger className="w-full sm:w-36" aria-label="Outcome">
            <SelectValue />
          </SelectTrigger>
          <SelectPopup>
            {outcomes[view].map((v) => (
              <SelectItem key={v} value={v}>
                {v || "Any outcome"}
              </SelectItem>
            ))}
          </SelectPopup>
        </Select>
        <span className="col-span-2 flex items-center justify-end gap-2 text-sm sm:ml-auto">
          <Radio className={cn("size-4", live ? "text-success" : "text-muted-foreground")} />
          Live
          <Switch checked={live} onCheckedChange={setLive} aria-label="Show new records as they arrive" />
        </span>
      </div>
      {view === "tasks" ? (
        <TaskList
          filter={{
            conversation: filter.conversation,
            q: filter.q,
            outcome: filter.outcome as TaskFilter["outcome"],
          }}
          live={live}
          selected={event}
        />
      ) : (
        <RecordList filter={filter} live={live} selected={record} />
      )}
      <TaskSheet event={event} />
    </div>
  );
}

/** Rows is a virtual, endlessly scrolling list under a column header. */
function Rows<T>({
  items,
  itemKey,
  header,
  pending,
  error,
  retry,
  what,
  empty,
  more,
  render,
}: {
  items: T[];
  itemKey: (item: T) => string | number;
  header: ReactNode;
  pending: boolean;
  /** error is why the list, or its next page, failed to load. */
  error: Error | null;
  retry: () => void;
  what: string;
  empty: string;
  more: { has: boolean; fetching: boolean; fetch: () => void };
  render: (item: T) => ReactNode;
}) {
  const scroller = useRef<HTMLDivElement>(null);
  const virtualizer = useVirtualizer({
    count: items.length,
    getScrollElement: () => scroller.current,
    estimateSize: () => 40,
    overscan: 12,
    getItemKey: (i) => (items[i] === undefined ? i : itemKey(items[i] as T)),
  });
  return (
    <>
      {header}
      <div
        ref={scroller}
        className="min-h-0 flex-1 overflow-y-auto"
        onScroll={(e) => {
          const el = e.currentTarget;
          const near = el.scrollHeight - el.scrollTop - el.clientHeight < 400;
          if (near && more.has && !more.fetching && !error) more.fetch();
        }}
      >
        {pending && <p className="py-16 text-center text-muted-foreground text-sm">Loading…</p>}
        {error && items.length === 0 && (
          <LoadError className="mx-auto max-w-lg px-4 py-12" what={what} error={error} retry={retry} />
        )}
        {!pending && !error && items.length === 0 && (
          <p className="py-16 text-center text-muted-foreground text-sm">{empty}</p>
        )}
        <div className="relative w-full" style={{ height: virtualizer.getTotalSize() }}>
          {virtualizer.getVirtualItems().map((row) => (
            <div
              key={row.key}
              data-index={row.index}
              ref={virtualizer.measureElement}
              className="absolute inset-x-0 top-0"
              style={{ transform: `translateY(${row.start}px)` }}
            >
              {render(items[row.index] as T)}
            </div>
          ))}
        </div>
        {more.fetching && <p className="py-4 text-center text-muted-foreground text-xs">Loading older…</p>}
        {error && items.length > 0 && (
          <LoadError className="mx-auto max-w-lg px-4" what={`older ${what}`} error={error} retry={more.fetch} />
        )}
      </div>
    </>
  );
}

const rowClass =
  "grid grid-cols-[1rem_minmax(0,1fr)_auto] items-center gap-x-3 gap-y-0.5 border-b border-border/60 px-4 py-2 text-[13px] outline-none hover:bg-accent/60 focus-visible:bg-accent sm:px-6";
const taskColumns = "sm:grid-cols-[1rem_8.5rem_minmax(0,1fr)_minmax(0,10rem)_4.5rem]";
const recordColumns = "sm:grid-cols-[1rem_8.5rem_minmax(0,14rem)_minmax(0,10rem)_minmax(0,1fr)_4.5rem]";

function ColumnHeader({ columns, children }: { columns: string; children: ReactNode }) {
  return (
    <div
      className={cn(
        "hidden gap-3 border-b bg-muted/40 px-4 py-1.5 text-muted-foreground text-xs sm:grid sm:px-6",
        columns,
      )}
    >
      {children}
    </div>
  );
}

/** workers names who did something for a task: the modules running it and
 * the components that recorded work. */
function workers(components: string[], running: Running[]): string[] {
  const modules = components.filter((c) => c.startsWith("module:")).map((c) => c.slice("module:".length));
  const others = components.filter((c) => !c.startsWith("module:"));
  return [...new Set([...running.map((r) => r.module), ...modules, ...others])];
}

function TaskList({ filter, live, selected }: { filter: TaskFilter; live: boolean; selected?: string }) {
  const list = useInfiniteQuery(tasksQuery(filter, live));
  const tasks = list.data?.pages.flatMap((p) => p.tasks) ?? [];
  const now = useNow(tasks.some((t) => t.outcome === "running"));
  const chatName = useChatNames();
  return (
    <Rows
      items={tasks}
      itemKey={(t: Task) => t.event}
      pending={list.isPending}
      error={list.error}
      retry={() => void list.refetch()}
      what="tasks"
      empty="No tasks match. A task is an event a module did something about."
      more={{ has: list.hasNextPage, fetching: list.isFetchingNextPage, fetch: () => void list.fetchNextPage() }}
      header={
        <ColumnHeader columns={taskColumns}>
          <span />
          <span>Started</span>
          <span>Task</span>
          <span>Chat</span>
          <span className="text-right">Took</span>
        </ColumnHeader>
      }
      render={(t) => {
        const running = t.outcome === "running";
        const elapsed = took((running ? now : new Date(t.ended).getTime()) - new Date(t.started).getTime());
        return (
          <Link
            to="/events"
            search={(s) => ({ ...s, event: t.event })}
            className={cn(rowClass, taskColumns, selected === t.event && "bg-accent")}
          >
            <StatusDot tone={outcomeTone(t.outcome)} label={t.outcome} />
            <time className="order-3 font-mono text-muted-foreground text-xs sm:order-none" dateTime={t.started}>
              {running && <span className="font-medium text-info-foreground sm:hidden">{elapsed}</span>}
              <span className={cn(running && "hidden sm:inline")}>{format(new Date(t.started), "MMM d HH:mm:ss")}</span>
            </time>
            <span className="truncate">
              <span className="font-mono text-xs">{workers(t.components, t.running).join(" · ")}</span>
              <span className="ms-2 text-muted-foreground">
                {t.actor && `${t.actor}: `}
                {t.text || t.action}
              </span>
            </span>
            <span className="col-start-2 truncate text-muted-foreground text-xs sm:col-start-auto">
              {chatName(t.platform, t.conversation)}
            </span>
            <span
              className={cn(
                "hidden text-right text-xs tabular sm:block",
                running ? "font-medium text-info-foreground" : "text-muted-foreground",
              )}
            >
              {elapsed}
            </span>
          </Link>
        );
      }}
    />
  );
}

function RecordList({ filter, live, selected }: { filter: EventFilter; live: boolean; selected?: number }) {
  const list = useInfiniteQuery(eventsQuery(filter));
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
  return (
    <>
      <Rows
        items={records}
        itemKey={(r: Entry) => r.id}
        pending={list.isPending}
        error={list.error}
        retry={() => void list.refetch()}
        what="records"
        empty="No records match."
        more={{ has: list.hasNextPage, fetching: list.isFetchingNextPage, fetch: () => void list.fetchNextPage() }}
        header={
          <ColumnHeader columns={recordColumns}>
            <span />
            <span>Time</span>
            <span>Kind</span>
            <span>Component</span>
            <span>Chat</span>
            <span className="text-right">Took</span>
          </ColumnHeader>
        }
        render={(r) => (
          <Link
            to="/events"
            search={(s) => ({ ...s, record: r.id })}
            className={cn(
              rowClass,
              recordColumns,
              selected === r.id && "bg-accent",
              fresh.some((f) => f.id === r.id) && "animate-in fade-in",
            )}
          >
            <StatusDot tone={outcomeTone(r.outcome)} label={r.outcome ?? "no outcome"} />
            <time className="order-3 font-mono text-xs text-muted-foreground sm:order-none" dateTime={r.at}>
              {format(new Date(r.at), "MMM d HH:mm:ss")}
            </time>
            <span className="truncate font-mono text-xs">{r.kind}</span>
            <span className="col-start-2 truncate text-muted-foreground text-xs sm:col-start-auto">{r.component}</span>
            <span className="hidden truncate text-muted-foreground text-xs sm:block">
              {chatName(r.platform, r.conversation)}
            </span>
            <span className="hidden text-right text-muted-foreground text-xs tabular sm:block">
              {r.duration_ms ? took(r.duration_ms) : ""}
            </span>
          </Link>
        )}
      />
      <RecordSheet id={selected} loaded={records.find((r) => r.id === selected)} />
    </>
  );
}

/** TextFilter is a filter one types: it applies a moment after the typing
 * stops, and its button clears it. */
function TextFilter({
  label,
  value,
  onChange,
  search,
  className,
}: {
  label: string;
  value?: string;
  onChange: (v: string | undefined) => void;
  search?: boolean;
  className?: string;
}) {
  const [text, setText] = useState(value ?? "");
  // A filter set from elsewhere, such as a link, shows here.
  // biome-ignore lint/correctness/useExhaustiveDependencies: only when the applied value changes
  useEffect(() => {
    if ((value ?? "") !== text.trim()) setText(value ?? "");
  }, [value]);
  // biome-ignore lint/correctness/useExhaustiveDependencies: only typing starts the wait
  useEffect(() => {
    const next = text.trim() || undefined;
    if (next === value) return;
    const timer = setTimeout(() => onChange(next), 300);
    return () => clearTimeout(timer);
  }, [text]);
  const input = { "aria-label": label, placeholder: label, value: text };
  const typed = (e: { target: { value: string } }) => setText(e.target.value);
  return (
    <div className={cn("relative", className)}>
      {search ? <SearchInput {...input} onChange={typed} /> : <Input className="text-xs" {...input} onChange={typed} />}
      {text && (
        <button
          type="button"
          onClick={() => setText("")}
          aria-label={`Clear ${label.toLowerCase()}`}
          className="hit-target absolute top-1/2 right-1 z-10 grid size-6 -translate-y-1/2 place-items-center rounded-md bg-background text-muted-foreground outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring"
        >
          <X className="size-3.5" />
        </button>
      )}
    </div>
  );
}

/** RecordSheet shows one record. One that is not among the rows loaded,
 * as when a link names it, is fetched by its ID. */
function RecordSheet({ id, loaded }: { id?: number; loaded?: Entry }) {
  const navigate = useNavigate({ from: Route.fullPath });
  const close = () => void navigate({ search: (s) => ({ ...s, record: undefined }), replace: true });
  const fetched = useQuery({ ...recordQuery(id ?? 0), enabled: id !== undefined && !loaded });
  const record = loaded ?? fetched.data ?? undefined;
  const trace = useQuery({ ...traceQuery(record?.event ?? ""), enabled: Boolean(record?.event) });
  return (
    <Sheet open={id !== undefined} onOpenChange={(o) => !o && close()}>
      <SheetPopup className="sm:max-w-2xl">
        {record ? (
          <>
            <SheetHeader>
              <SheetTitle className="flex items-center gap-2.5 font-mono text-base">
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
                {record.duration_ms !== undefined && <Fact label="Took">{took(record.duration_ms)}</Fact>}
                {record.event && (
                  <Fact label="Event">
                    <Link
                      to="/events"
                      search={{ view: "tasks", event: record.event }}
                      className="underline-offset-3 hover:underline"
                    >
                      {record.event}
                    </Link>
                  </Fact>
                )}
                {record.platform && <ChatFact entry={record} />}
              </dl>
              {trace.error && (
                <LoadError what="this record's trace" error={trace.error} retry={() => void trace.refetch()} />
              )}
              {trace.data && trace.data.records.length > 0 && (
                <Trace
                  records={trace.data.records}
                  running={trace.data.running}
                  selected={record.id}
                  onSelect={(next) => void navigate({ search: (s) => ({ ...s, record: next }) })}
                />
              )}
              <section>
                <h3 className="mb-2 font-semibold text-[13px]">Payload</h3>
                <Payload value={record.payload} />
              </section>
            </SheetPanel>
          </>
        ) : (
          <>
            <SheetHeader>
              <SheetTitle className="text-base">Record {id}</SheetTitle>
            </SheetHeader>
            <SheetPanel>
              {fetched.error ? (
                <LoadError what={`record ${id}`} error={fetched.error} retry={() => void fetched.refetch()} />
              ) : (
                <p className="text-muted-foreground text-sm">
                  {fetched.data === null
                    ? "The event log has no record with this ID. Records older than the retention are deleted."
                    : "Loading…"}
                </p>
              )}
            </SheetPanel>
          </>
        )}
      </SheetPopup>
    </Sheet>
  );
}

/** TaskSheet follows one event from its receipt to its last record, live
 * while a module still handles it; each record opens in place. */
function TaskSheet({ event }: { event?: string }) {
  const navigate = useNavigate({ from: Route.fullPath });
  const close = () => void navigate({ search: (s) => ({ ...s, event: undefined }), replace: true });
  const { data, error, refetch } = useQuery({ ...traceQuery(event ?? ""), enabled: Boolean(event) });
  // What the reader opened in this event's trace; until then, the record
  // that failed is open.
  const [opened, setOpened] = useState<{ event?: string; id?: number }>();
  const records = data?.records ?? [];
  const failure = records.find((r) => r.outcome === "error");
  const shown = opened?.event === event ? opened?.id : failure?.id;
  const running = data?.running ?? [];
  const now = useNow(running.length > 0);
  const chatName = useChatNames();
  const received = records.find((r) => r.kind === "event.received");
  const message = received?.payload as ReceivedEvent | undefined;
  const first = records[0];
  const started = Math.min(
    ...records.map((r) => new Date(r.at).getTime() - (r.duration_ms ?? 0)),
    ...running.map((r) => new Date(r.started).getTime()),
  );
  const ended = Math.max(...records.map((r) => new Date(r.at).getTime()));
  const outcome = running.length ? "running" : worstOutcome(records);
  const names = workers(
    records.filter((r) => isWork(r)).map((r) => r.component),
    running,
  );
  return (
    <Sheet open={event !== undefined} onOpenChange={(o) => !o && close()}>
      <SheetPopup className="sm:max-w-2xl">
        {first ? (
          <>
            <SheetHeader>
              <SheetTitle className="flex items-center gap-2.5 text-base">
                <StatusDot tone={outcomeTone(outcome)} />
                <span className="font-mono">{names.join(" · ") || "Nothing done"}</span>
              </SheetTitle>
              <SheetDescription>
                {chatName(first.platform, first.conversation) || first.platform} ·{" "}
                {format(started, "MMM d, yyyy HH:mm:ss")}
              </SheetDescription>
            </SheetHeader>
            <SheetPanel className="flex flex-col gap-5">
              <dl className="surface-inset grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1.5 rounded-xl px-3.5 py-3 text-[13px]">
                <Fact label="State">
                  {running.length ? (
                    <span className="text-info-foreground">
                      Running for {took(now - started)}, last record {took(now - ended)} ago
                    </span>
                  ) : (
                    `${outcome}, took ${took(ended - started)}`
                  )}
                </Fact>
                {failure && (
                  <Fact label="Failed" wrap>
                    <span className="text-destructive-foreground">
                      {failure.kind}
                      {errorText(failure) && `: ${errorText(failure)}`}
                    </span>
                  </Fact>
                )}
                <Fact label="Event">{first.event}</Fact>
                <Fact label="Records">{records.length}</Fact>
                {first.platform && <ChatFact entry={first} />}
              </dl>
              {message?.message && (
                <section>
                  <h3 className="mb-2 font-semibold text-[13px]">
                    {message.actor?.name || "Message"}{" "}
                    <span className="font-normal text-muted-foreground">· {message.action}</span>
                  </h3>
                  <p className="surface-inset whitespace-pre-wrap rounded-xl px-3.5 py-3 text-[13px] leading-relaxed wrap-anywhere">
                    {plainText(message.message) || "No text"}
                  </p>
                </section>
              )}
              <Trace
                records={records}
                running={running}
                selected={shown}
                onSelect={(id) => setOpened({ event, id: shown === id ? undefined : id })}
                expand
              />
            </SheetPanel>
          </>
        ) : (
          <>
            <SheetHeader>
              <SheetTitle className="text-base">Task</SheetTitle>
            </SheetHeader>
            <SheetPanel>
              {error ? (
                <LoadError what="this task" error={error} retry={() => void refetch()} />
              ) : (
                <p className="text-muted-foreground text-sm">
                  {data
                    ? "The event log has no records of this event. Records older than the retention are deleted."
                    : "Loading…"}
                </p>
              )}
            </SheetPanel>
          </>
        )}
      </SheetPopup>
    </Sheet>
  );
}

type ReceivedEvent = {
  action?: string;
  actor?: { name?: string };
  message?: { parts?: { text?: string }[] };
};

function plainText(message: NonNullable<ReceivedEvent["message"]>): string {
  return (message.parts ?? [])
    .map((p) => p.text ?? "")
    .filter(Boolean)
    .join("\n");
}

/** isWork tells the records that say a module did something from an
 * event's receipt and the plain finish of a delivery. */
function isWork(r: Entry): boolean {
  return !(r.kind === "event.received" || (r.kind === "module.completed" && r.outcome === "ok"));
}

function errorText(r: Entry): string {
  return (r.payload as { error?: string } | null)?.error ?? "";
}

function worstOutcome(records: Entry[]): string {
  const ranks = ["ok", "degraded", "slow", "error"];
  return ranks[Math.max(0, ...records.map((r) => ranks.indexOf(r.outcome ?? "")))] ?? "ok";
}

function ChatFact({ entry }: { entry: Entry }) {
  return (
    <Fact label="Chat">
      {entry.conversation ? (
        <Link
          to="/chats/$chat"
          params={{ chat: `${entry.platform}:${entry.conversation}` }}
          className="underline-offset-3 hover:underline"
        >
          {entry.platform}:{entry.conversation}
        </Link>
      ) : (
        entry.platform
      )}{" "}
      <span className="text-muted-foreground">via {entry.account}</span>
    </Fact>
  );
}

function Fact({ label, wrap, children }: { label: string; wrap?: boolean; children: ReactNode }) {
  return (
    <>
      <dt className="text-muted-foreground">{label}</dt>
      <dd className={cn("font-mono text-xs leading-5", wrap ? "wrap-anywhere" : "truncate")}>{children}</dd>
    </>
  );
}

/** detail says what a record was about, in a word or two. */
function detail(r: Entry): string {
  const p = (r.payload ?? {}) as Record<string, unknown>;
  switch (r.kind) {
    case "chat.tool":
      return String(p.function ?? "");
    case "chat.reply":
      return String(p.profile ?? "");
    case "llm.completed":
    case "llm.failed":
      return String(p.model ?? "");
    case "effect.applied":
    case "effect.failed":
      return [(p.effect as { kind?: string } | undefined)?.kind, p.slot].filter(Boolean).join(" ");
    case "event.received":
      return String(p.action ?? "");
  }
  return r.component.replace(/^module:/, "");
}

/** Trace lays one event's records on a timeline: each bar spans its
 * duration, ending when it was recorded. Deliveries still running end at
 * the moment, and move with it. With expand, the selected record's payload
 * opens under it. */
function Trace({
  records,
  running,
  selected,
  onSelect,
  expand,
}: {
  records: Entry[];
  running: Running[];
  selected?: number;
  onSelect: (id: number) => void;
  expand?: boolean;
}) {
  const live = running.length > 0;
  const now = useNow(live);
  const span = (r: Entry) => {
    const end = new Date(r.at).getTime();
    return [end - (r.duration_ms ?? 0), end] as const;
  };
  const t0 = Math.min(...records.map((r) => span(r)[0]), ...running.map((r) => new Date(r.started).getTime()));
  const t1 = Math.max(...records.map((r) => span(r)[1]), live ? now : 0, t0 + 1);
  const place = (a: number, b: number) => ({
    left: `${((a - t0) / (t1 - t0)) * 100}%`,
    width: `max(0.375rem, ${((b - a) / (t1 - t0)) * 100}%)`,
  });
  // On a phone the label sits over its bar, which then has the full width.
  const row =
    "grid w-full grid-cols-[minmax(0,1fr)_3.5rem] items-center gap-x-3 rounded-md px-1 py-0.5 text-left outline-none focus-visible:ring-2 focus-visible:ring-ring pointer-coarse:min-h-11 sm:grid-cols-[minmax(0,13rem)_minmax(0,1fr)_3.5rem]";
  const bar = "relative col-span-2 row-start-2 h-4 sm:col-span-1 sm:row-start-auto";
  const time = "col-start-2 row-start-1 text-right text-xs tabular sm:col-start-auto sm:row-start-auto";
  return (
    <section>
      <h3 className="mb-2 font-semibold text-[13px]">
        Trace <span className="font-normal text-muted-foreground">· {took(t1 - t0)}</span>
        {live && <span className="font-normal text-info-foreground"> · running</span>}
      </h3>
      <ol className="surface-inset flex flex-col gap-1 rounded-xl px-3.5 py-3">
        {records.map((r) => {
          const [a, b] = span(r);
          const open = expand && selected === r.id;
          return (
            <li key={r.id}>
              <button
                type="button"
                onClick={() => onSelect(r.id)}
                aria-expanded={expand ? open : undefined}
                className={cn(
                  row,
                  "hover:bg-background",
                  selected === r.id && "bg-background shadow-[0_0_0_1px_--theme(--color-black/6%)]",
                )}
              >
                <span className="truncate font-mono text-xs">
                  {r.outcome && r.outcome !== "ok" && (
                    <StatusDot tone={outcomeTone(r.outcome)} label={r.outcome} className="mr-1.5 ml-0" />
                  )}
                  {r.kind} <span className="text-muted-foreground">{detail(r)}</span>
                </span>
                <span className={bar}>
                  <span
                    className={cn(
                      "absolute top-1/2 h-2 -translate-y-1/2 rounded-full",
                      r.outcome === "error"
                        ? "bg-destructive"
                        : r.duration_ms
                          ? "bg-linear-to-r from-neutral-600 to-neutral-800"
                          : "bg-neutral-400",
                    )}
                    style={place(a, b)}
                  />
                </span>
                <span className={cn(time, "text-muted-foreground")}>{r.duration_ms ? took(r.duration_ms) : ""}</span>
              </button>
              {open && <Payload value={r.payload} className="mt-1 mb-2" />}
            </li>
          );
        })}
        {running.map((r) => {
          const a = new Date(r.started).getTime();
          return (
            <li key={`${r.module}/${r.origin}`} className={row}>
              <span className="truncate font-mono text-xs">
                {r.module} <span className="text-info-foreground">running</span>
              </span>
              <span className={bar}>
                <span
                  className="absolute top-1/2 h-2 -translate-y-1/2 animate-pulse rounded-full bg-info"
                  style={place(a, now)}
                />
              </span>
              <span className={cn(time, "font-medium text-info-foreground")}>{took(now - a)}</span>
            </li>
          );
        })}
      </ol>
    </section>
  );
}

function Payload({ value, className }: { value: unknown; className?: string }) {
  return (
    <pre
      className={cn(
        "surface-inset overflow-x-auto whitespace-pre-wrap break-all rounded-xl px-3.5 py-3 font-mono text-xs leading-relaxed",
        className,
      )}
    >
      <Json value={value} />
    </pre>
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
