import { useInfiniteQuery, useMutation, useQuery, useQueryClient, useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute, Link, notFound } from "@tanstack/react-router";
import { useVirtualizer } from "@tanstack/react-virtual";
import { format, isToday, isYesterday } from "date-fns";
import { ArrowLeft, Pencil, ScrollText } from "lucide-react";
import { useLayoutEffect, useRef, useState } from "react";
import { ApiError, api, data, type Schemas } from "@/api/client";
import { outcomeTone, StatusDot } from "@/components/status-dot";
import { Tile } from "@/components/tile";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Sheet, SheetHeader, SheetPanel, SheetPopup, SheetTitle, SheetTrigger } from "@/components/ui/sheet";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { chatKind, chatName } from "@/lib/chats";
import { listMeta, moduleMeta, platformMeta } from "@/lib/meta";
import { cn } from "@/lib/utils";
import { chatQuery, messagesQuery } from "@/queries/chats";
import { modulesQuery } from "@/queries/modules";

export const Route = createFileRoute("/_app/chats/$chat")({
  loader: async ({ context, params }) => {
    try {
      await context.queryClient.ensureQueryData(chatQuery(params.chat));
    } catch (e) {
      if (e instanceof ApiError && e.status === 404) throw notFound();
      throw e;
    }
  },
  component: ChatPage,
});

function ChatPage() {
  const { chat: id } = Route.useParams();
  const { data: chat } = useSuspenseQuery(chatQuery(id));
  const p = platformMeta(chat.platform);
  return (
    <div className="grid min-h-0 flex-1 grid-cols-1 lg:grid-cols-[minmax(0,1fr)_19rem]">
      <section className="flex min-h-0 flex-col md:h-[calc(100svh-3.5rem)]">
        <header className="flex items-center gap-3 border-b px-4 py-3.5 sm:px-6">
          <Link
            to="/chats"
            className="-ml-1 rounded-md p-1 text-muted-foreground hover:text-foreground md:hidden"
            aria-label="All chats"
          >
            <ArrowLeft className="size-5" />
          </Link>
          <Tile tone={p.tone} icon={p.icon} />
          <div className="min-w-0 flex-1">
            <h1 className="truncate font-semibold text-[17px] leading-tight tracking-[-0.01em]">{chatName(chat)}</h1>
            <span className="block truncate font-mono text-[11.5px] text-muted-foreground">{chat.id}</span>
          </div>
          <Button
            variant="outline"
            size="sm"
            render={<Link to="/events" search={{ conversation: chat.conversation }} />}
          >
            <ScrollText /> <span className="max-sm:sr-only">Open in event log</span>
          </Button>
          <Sheet>
            <SheetTrigger render={<Button variant="outline" size="sm" className="lg:hidden" />}>About</SheetTrigger>
            <SheetPopup>
              <SheetHeader>
                <SheetTitle>{chatName(chat)}</SheetTitle>
              </SheetHeader>
              <SheetPanel>
                <About chat={chat} />
              </SheetPanel>
            </SheetPopup>
          </Sheet>
        </header>
        <div className="border-b px-4 py-2 lg:hidden">
          <ModuleSwitches chat={chat} compact />
        </div>
        <Conversation chat={id} />
      </section>
      <aside className="hidden flex-col gap-5 border-l px-4 py-5 lg:flex lg:h-[calc(100svh-3.5rem)] lg:overflow-y-auto">
        <ModuleSwitches chat={chat} />
        <About chat={chat} />
      </aside>
    </div>
  );
}

type Message = Schemas["ChatMessage"];

/** Conversation shows a chat's messages, oldest at the top, and loads
 * older ones as one scrolls up. */
function Conversation({ chat }: { chat: string }) {
  const messages = useInfiniteQuery(messagesQuery(chat));
  const { data: modules } = useQuery(modulesQuery);
  const items = (messages.data?.pages.flatMap((p) => p.messages ?? []) ?? []).toReversed();
  const scroller = useRef<HTMLDivElement>(null);
  const virtualizer = useVirtualizer({
    count: items.length,
    getScrollElement: () => scroller.current,
    estimateSize: () => 76,
    overscan: 8,
    getItemKey: (i) => items[i]?.id ?? i,
  });
  // Keep the reader's place: start at the newest, and when older messages
  // arrive above, keep the ones in view where they were.
  const newest = items.at(-1)?.id;
  const oldest = items[0]?.id;
  const placed = useRef<{ oldest?: number; newest?: number; height: number }>({ height: 0 });
  useLayoutEffect(() => {
    const el = scroller.current;
    if (!el || items.length === 0) return;
    const before = placed.current;
    const height = virtualizer.getTotalSize();
    if (before.newest === undefined) {
      virtualizer.scrollToIndex(items.length - 1, { align: "end" });
    } else if (oldest !== before.oldest) {
      el.scrollTop += height - before.height;
    } else if (newest !== before.newest && el.scrollHeight - el.scrollTop - el.clientHeight < 160) {
      virtualizer.scrollToIndex(items.length - 1, { align: "end" });
    }
    placed.current = { oldest, newest, height };
  });
  const labels = Object.fromEntries(
    (modules ?? []).flatMap((m) => m.allowlists.map((l, i) => [`${m.id}/${l.name}`, listMeta(m.id, l.title, i)])),
  );
  if (messages.isPending) {
    return (
      <div className="flex flex-1 flex-col gap-4 p-6">
        {[0, 1, 2].map((i) => (
          <Skeleton key={i} className="h-14 w-2/3 rounded-xl" />
        ))}
      </div>
    );
  }
  return (
    <div
      ref={scroller}
      className="min-h-0 flex-1 overflow-y-auto px-4 sm:px-6"
      onScroll={(e) => {
        if (e.currentTarget.scrollTop < 240 && messages.hasNextPage && !messages.isFetchingNextPage) {
          void messages.fetchNextPage();
        }
      }}
    >
      {items.length === 0 && (
        <p className="py-16 text-center text-muted-foreground text-sm">
          Nothing said here within the event log's retention.
        </p>
      )}
      <div className="relative w-full" style={{ height: virtualizer.getTotalSize() }}>
        {virtualizer.getVirtualItems().map((row) => {
          const m = items[row.index] as Message;
          const prev = items[row.index - 1];
          const newDay = !prev || new Date(prev.at).toDateString() !== new Date(m.at).toDateString();
          return (
            <div
              key={row.key}
              data-index={row.index}
              ref={virtualizer.measureElement}
              className="absolute inset-x-0 top-0 pt-3.5"
              style={{ transform: `translateY(${row.start}px)` }}
            >
              {newDay && <DayLabel at={m.at} />}
              <MessageRow chat={chat} m={m} labels={labels} />
            </div>
          );
        })}
      </div>
      <div className="h-5" />
    </div>
  );
}

function DayLabel({ at }: { at: string }) {
  const d = new Date(at);
  const text = isToday(d) ? "Today" : isYesterday(d) ? "Yesterday" : format(d, "EEEE, MMMM d");
  return <div className="mb-3.5 text-center text-muted-foreground text-xs">{text}</div>;
}

type Reply = { profile?: string; model?: string };

function MessageRow({
  chat,
  m,
  labels,
}: {
  chat: string;
  m: Message;
  labels: Record<string, ReturnType<typeof listMeta>>;
}) {
  const records = m.records ?? [];
  const own = records.find((r) => !r.kind.startsWith("module."));
  const done = records.find((r) => r.kind.startsWith("module."));
  const reply = own?.payload as Reply | undefined;
  const meta = m.bot
    ? (reply?.profile && labels[`${m.module}/${reply.profile}`]) || moduleMeta(m.module ?? "")
    : undefined;
  const who = m.bot ? (meta?.title ?? "Bot") : m.actor?.name || m.actor?.id?.split(":").pop() || "Someone";
  const parts = m.message.parts ?? [];
  const text = parts
    .filter((part) => part.kind === "text" && part.text)
    .map((part) => part.text)
    .join("\n");
  const media = parts.filter((part) => part.media);
  return (
    <div className="flex gap-2.5">
      {meta ? (
        <Tile tone={meta.tone} icon={meta.icon} size="sm" className="mt-0.5 rounded-full" />
      ) : (
        <span className="mt-0.5 grid size-7 flex-none place-items-center rounded-full bg-linear-to-b from-neutral-100 to-neutral-200 font-semibold text-neutral-600 text-xs shadow-[inset_0_0_0_1px_--theme(--color-black/5%)]">
          {who.slice(0, 1).toUpperCase()}
        </span>
      )}
      <div className="min-w-0 max-w-[36rem]">
        <div className="text-[12.5px] text-muted-foreground">
          <span className="mr-1.5 font-semibold text-foreground">{who}</span>
          <time dateTime={m.at}>{format(new Date(m.at), "HH:mm")}</time>
        </div>
        {text && (
          <div
            className={cn(
              "mt-1 w-fit whitespace-pre-wrap wrap-anywhere rounded-[4px_14px_14px_14px] px-3 py-2 text-sm leading-relaxed",
              m.bot ? "border bg-background shadow-xs/5" : "bg-neutral-100",
            )}
          >
            {text}
          </div>
        )}
        {media.map((part) => (
          <MediaPart key={part.id} chat={chat} message={m.id} part={part} />
        ))}
        {m.bot && (
          <div className="mt-1.5 flex items-center gap-1.5 font-mono text-[11.5px] text-muted-foreground">
            <StatusDot tone={outcomeTone(own?.outcome ?? done?.outcome)} className="mr-0.5" />
            {[
              meta?.title,
              own?.kind,
              done?.duration_ms !== undefined && `${(done.duration_ms / 1000).toFixed(1)} s`,
              reply?.model,
            ]
              .filter(Boolean)
              .join(" · ")}
          </div>
        )}
      </div>
    </div>
  );
}

function MediaPart({ chat, message, part }: { chat: string; message: number; part: Schemas["Part"] }) {
  const [failed, setFailed] = useState(false);
  const src = `/api/chats/${encodeURIComponent(chat)}/media/${message}/${encodeURIComponent(part.id)}`;
  const image =
    part.media?.role === "image" || part.media?.role === "sticker" || part.media?.media_type?.startsWith("image/");
  if (!image || failed) {
    return (
      <div className="mt-1 w-fit rounded-xl border bg-background px-3 py-2 text-muted-foreground text-xs">
        {failed ? "The platform no longer has this image" : (part.media?.filename ?? part.media?.role ?? "File")}
      </div>
    );
  }
  return (
    <a href={src} target="_blank" rel="noreferrer" className="mt-1 block w-fit">
      <img
        src={src}
        alt=""
        loading="lazy"
        onError={() => setFailed(true)}
        className="h-auto max-h-80 w-auto max-w-[min(20rem,100%)] rounded-xl border shadow-xs/5"
        width={part.media?.width}
        height={part.media?.height}
      />
    </a>
  );
}

function ModuleSwitches({ chat, compact }: { chat: Schemas["ChatOutputBody"]; compact?: boolean }) {
  const { data: modules } = useQuery(modulesQuery);
  const queryClient = useQueryClient();
  const toggle = useMutation({
    mutationFn: ({ module, list, on }: { module: string; list: string; on: boolean }) => {
      const params = { params: { path: { module, list, chat: chat.id } } };
      return data(
        on
          ? api.PUT("/api/modules/{module}/allowlists/{list}/chats/{chat}", params)
          : api.DELETE("/api/modules/{module}/allowlists/{list}/chats/{chat}", params),
      );
    },
    onSettled: async () => {
      await queryClient.invalidateQueries({ queryKey: ["chats"] });
      await queryClient.invalidateQueries({ queryKey: ["modules"] });
    },
  });
  const lists = (modules ?? []).flatMap((m) =>
    m.allowlists.map((l, i) => ({ m, l, meta: listMeta(m.id, l.title, i) })),
  );
  const on = (module: string, list: string) => chat.lists.some((x) => x.module === module && x.list === list);
  if (compact) {
    return (
      <ul className="-mx-1 flex gap-2 overflow-x-auto px-1 py-0.5">
        {lists.map(({ m, l, meta }) => (
          <li
            key={`${m.id}/${l.name}`}
            className="flex flex-none items-center gap-2 rounded-full border bg-background py-1 ps-1 pe-1.5 shadow-xs/5"
          >
            <Tile tone={meta.tone} icon={meta.icon} size="sm" className="size-6 rounded-full" />
            <span className="font-medium text-[13px]">{meta.title}</span>
            <Switch
              checked={on(m.id, l.name)}
              disabled={toggle.isPending}
              onCheckedChange={(v) => toggle.mutate({ module: m.id, list: l.name, on: v })}
              aria-label={`${meta.title} answers in this chat`}
            />
          </li>
        ))}
      </ul>
    );
  }
  return (
    <section>
      <h2 className="mb-2 px-0.5 font-semibold text-[13px]">Modules in this chat</h2>
      <div className="surface-raised rounded-2xl px-3">
        <ul className="divide-y divide-border/80">
          {lists.map(({ m, l, meta }) => {
            const checked = on(m.id, l.name);
            const pending = toggle.isPending && toggle.variables.module === m.id && toggle.variables.list === l.name;
            return (
              <li key={`${m.id}/${l.name}`} className="flex items-center gap-2.5 py-2.5">
                <Tile
                  tone={meta.tone}
                  icon={meta.icon}
                  size="sm"
                  className={cn(!m.enabled && "opacity-55 saturate-50")}
                />
                <span className="min-w-0 flex-1">
                  <span className="block truncate font-medium text-sm">{meta.title}</span>
                  {!m.enabled && <span className="block text-muted-foreground text-xs">Module is off</span>}
                </span>
                <Switch
                  checked={pending ? toggle.variables.on : checked}
                  disabled={pending}
                  onCheckedChange={(v) => toggle.mutate({ module: m.id, list: l.name, on: v })}
                  aria-label={`${meta.title} answers in this chat`}
                />
              </li>
            );
          })}
          {lists.length === 0 && (
            <li className="py-4 text-muted-foreground text-sm">No module answers in chosen chats.</li>
          )}
        </ul>
      </div>
    </section>
  );
}

function About({ chat }: { chat: Schemas["ChatOutputBody"] }) {
  const days = chat.days ?? [];
  const week = Array.from({ length: 7 }, (_, i) => {
    const d = new Date(Date.now() - (6 - i) * 86_400_000).toISOString().slice(0, 10);
    return { day: d, messages: days.find((x) => x.day === d)?.messages ?? 0 };
  });
  const peak = Math.max(1, ...week.map((d) => d.messages));
  const facts = [
    { label: "Platform", value: chatKind(chat) },
    { label: "Bot account", value: chat.account },
    { label: "First seen", value: format(new Date(chat.first_seen), "MMM d, HH:mm") },
    { label: "Messages, 7 days", value: week.reduce((n, d) => n + d.messages, 0) },
  ];
  return (
    <section>
      <h2 className="mb-2 px-0.5 font-semibold text-[13px]">About this chat</h2>
      <div className="surface-raised rounded-2xl px-3">
        <Name chat={chat} />
        <dl className="divide-y divide-border/80 border-t border-border/80">
          {facts.map((f) => (
            <div key={f.label} className="flex justify-between gap-3 py-2 text-[13px]">
              <dt className="text-muted-foreground">{f.label}</dt>
              <dd className="truncate font-medium tabular">{f.value}</dd>
            </div>
          ))}
        </dl>
        <div className="border-t border-border/80 py-3">
          <div className="flex h-9 items-end gap-1.5" role="img" aria-label="New messages per day over the last 7 days">
            {week.map((d, i) => (
              <div key={d.day} className="flex flex-1 flex-col items-center gap-1">
                <div
                  className={cn("w-full rounded-[3px]", i === week.length - 1 ? "bg-neutral-800" : "bg-neutral-300")}
                  style={{ height: `${Math.max(2, (d.messages / peak) * 32)}px` }}
                  title={`${d.day}: ${d.messages}`}
                />
              </div>
            ))}
          </div>
          <div className="mt-1 flex justify-between text-[10.5px] text-muted-foreground">
            <span>{format(new Date(week[0]?.day ?? Date.now()), "MMM d")}</span>
            <span>Today</span>
          </div>
        </div>
      </div>
    </section>
  );
}

function Name({ chat }: { chat: Schemas["ChatOutputBody"] }) {
  const [editing, setEditing] = useState(false);
  const [name, setName] = useState(chat.name ?? "");
  const queryClient = useQueryClient();
  const save = useMutation({
    mutationFn: () => data(api.PATCH("/api/chats/{chat}", { params: { path: { chat: chat.id } }, body: { name } })),
    onSuccess: async () => {
      setEditing(false);
      await queryClient.invalidateQueries({ queryKey: ["chats"] });
    },
  });
  if (!editing) {
    return (
      <div className="flex items-center gap-2 py-2.5">
        <div className="min-w-0 flex-1 text-[13px]">
          <div className="text-muted-foreground">Name</div>
          <div className="truncate font-medium">
            {chat.name || (
              <span className="text-muted-foreground">{chat.title ? `${chat.title} (from the platform)` : "None"}</span>
            )}
          </div>
        </div>
        <Button variant="ghost" size="icon-sm" aria-label="Rename" onClick={() => setEditing(true)}>
          <Pencil />
        </Button>
      </div>
    );
  }
  return (
    <form
      className="flex items-center gap-2 py-2.5"
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate();
      }}
    >
      <Input
        size="sm"
        value={name}
        onChange={(e) => setName(e.target.value)}
        placeholder={chat.title || "Name"}
        autoFocus
      />
      <Button size="sm" type="submit" loading={save.isPending}>
        Save
      </Button>
    </form>
  );
}
