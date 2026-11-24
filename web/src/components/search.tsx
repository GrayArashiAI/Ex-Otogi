import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Activity, Blocks, MessagesSquare, ScrollText, Search as SearchIcon, Settings } from "lucide-react";
import { Fragment, type ReactNode, useEffect, useMemo, useRef, useState } from "react";
import { LoadError } from "@/components/load-error";
import { Tile } from "@/components/tile";
import { Dialog, DialogPopup, DialogTitle, DialogTrigger } from "@/components/ui/dialog";
import { chatKind, chatName } from "@/lib/chats";
import { listMeta, moduleMeta, platformMeta } from "@/lib/meta";
import { settingsPages } from "@/lib/settings";
import { cn } from "@/lib/utils";
import { chatsQuery } from "@/queries/chats";
import { modulesQuery } from "@/queries/modules";

/** Search jumps to a page, a module, an agent, or a chat: Ctrl K anywhere. */
export function Search() {
  const [open, setOpen] = useState(false);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setOpen((o) => !o);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger
        aria-label="Search"
        className="hit-target relative flex size-8 items-center justify-center gap-2 rounded-lg border bg-background text-muted-foreground text-sm shadow-xs/5 outline-none transition-colors hover:bg-accent/50 focus-visible:ring-2 focus-visible:ring-ring md:h-auto md:w-52 md:justify-start lg:w-60 md:px-2.5 md:py-1.5"
      >
        <SearchIcon className="size-4" />
        <span className="max-md:hidden">Search</span>
        <kbd className="ml-auto rounded-md border bg-muted px-1.5 font-mono text-xs max-md:hidden">Ctrl K</kbd>
      </DialogTrigger>
      <DialogPopup className="top-[12vh] sm:max-w-lg" showCloseButton={false}>
        <DialogTitle className="sr-only">Search</DialogTitle>
        {open && <Results onPick={() => setOpen(false)} />}
      </DialogPopup>
    </Dialog>
  );
}

type LinkProps = {
  ref: (el: HTMLAnchorElement | null) => void;
  id: string;
  role: "option";
  "aria-selected": boolean;
  tabIndex: -1;
  onClick: () => void;
  onMouseMove: () => void;
  className: string;
  children: ReactNode;
};

const pages = [
  { to: "/status", title: "Status", icon: Activity },
  { to: "/modules", title: "Modules", icon: Blocks },
  { to: "/chats", title: "Chats", icon: MessagesSquare },
  { to: "/events", title: "Event log", icon: ScrollText },
  { to: "/settings", title: "Settings", icon: Settings },
  ...settingsPages,
] as const;

type Result = {
  key: string;
  title: string;
  detail: string;
  meta: ReturnType<typeof moduleMeta>;
  link: (p: LinkProps) => ReactNode;
};

function Results({ onPick }: { onPick: () => void }) {
  const modulesList = useQuery(modulesQuery);
  const chatsList = useQuery(chatsQuery);
  const { data: modules = [] } = modulesList;
  const { data: chats = [] } = chatsList;
  const failed = modulesList.error ?? chatsList.error;
  const [q, setQ] = useState("");
  const [at, setAt] = useState(0);
  const links = useRef<(HTMLAnchorElement | null)[]>([]);
  const results = useMemo(() => {
    const all: Result[] = [
      ...pages.map((p) => ({
        key: `p${p.to}`,
        title: p.title,
        detail: "Page",
        meta: { ...moduleMeta(""), tone: "slate" as const, icon: p.icon },
        link: (props: LinkProps) => <Link to={p.to} {...props} />,
      })),
      ...modules.flatMap((m) => [
        {
          key: `m/${m.id}`,
          title: moduleMeta(m.id).title,
          detail: m.id,
          meta: moduleMeta(m.id),
          link: (p: LinkProps) => <Link to="/modules/$module" params={{ module: m.id }} {...p} />,
        },
        ...(m.id === "chat"
          ? m.allowlists.map((l, i) => ({
              key: `l/${l.name}`,
              title: l.title,
              detail: l.about ? "agent tools" : "chat agent",
              meta: listMeta(m.id, l, i),
              link: (p: LinkProps) => (
                <Link to="/modules/$module" params={{ module: m.id }} hash={`list-${l.name}`} {...p} />
              ),
            }))
          : []),
      ]),
      ...chats.map((c) => ({
        key: `c/${c.id}`,
        title: chatName(c),
        detail: `${chatKind(c)} · ${c.id}`,
        meta: { ...moduleMeta(""), ...platformMeta(c.platform) },
        link: (p: LinkProps) => <Link to="/chats/$chat" params={{ chat: c.id }} {...p} />,
      })),
    ];
    const words = q.trim().toLowerCase();
    return (words ? all.filter((r) => `${r.title} ${r.detail}`.toLowerCase().includes(words)) : all).slice(0, 12);
  }, [modules, chats, q]);
  const move = (to: number) => {
    setAt(to);
    links.current[to]?.scrollIntoView({ block: "nearest" });
  };
  return (
    <div className="flex min-h-0 flex-col">
      <div className="flex items-center gap-2 border-b px-4">
        <SearchIcon className="size-4 text-muted-foreground" />
        <input
          // biome-ignore lint/a11y/noAutofocus: the dialog opens to type into
          autoFocus
          role="combobox"
          aria-label="Search pages, modules, agents, and chats"
          aria-expanded
          aria-controls="search-results"
          aria-autocomplete="list"
          aria-activedescendant={results.length ? `search-result-${at}` : undefined}
          className="h-12 flex-1 bg-transparent text-base outline-none placeholder:text-muted-foreground sm:text-sm"
          placeholder="Pages, modules, agents, and chats"
          value={q}
          onChange={(e) => {
            setQ(e.target.value);
            setAt(0);
          }}
          onKeyDown={(e) => {
            if (e.key === "ArrowDown" || e.key === "ArrowUp") {
              e.preventDefault();
              move(Math.max(0, Math.min(results.length - 1, at + (e.key === "ArrowDown" ? 1 : -1))));
            } else if (e.key === "Enter") {
              links.current[at]?.click();
            }
          }}
        />
      </div>
      {failed && (
        <LoadError
          className="border-b px-4"
          what={modulesList.error ? "the modules" : "the chats"}
          error={failed}
          retry={() => {
            void modulesList.refetch();
            void chatsList.refetch();
          }}
        />
      )}
      <div
        id="search-results"
        role="listbox"
        aria-label="Results"
        tabIndex={-1}
        className="max-h-96 overflow-y-auto p-2 outline-none"
      >
        {results.map((r, i) => (
          <Fragment key={r.key}>
            {r.link({
              ref: (el) => {
                links.current[i] = el;
              },
              id: `search-result-${i}`,
              role: "option",
              "aria-selected": i === at,
              tabIndex: -1,
              onClick: onPick,
              onMouseMove: () => setAt(i),
              className: cn(
                "flex items-center gap-3 rounded-lg px-2 py-1.5 outline-none pointer-coarse:py-2",
                i === at && "bg-accent",
              ),
              children: (
                <>
                  <Tile tone={r.meta.tone} icon={r.meta.icon} size="sm" />
                  <span className="min-w-0 flex-1">
                    <span className="block truncate font-medium text-sm">{r.title}</span>
                    <span className="block truncate text-muted-foreground text-xs">{r.detail}</span>
                  </span>
                </>
              ),
            })}
          </Fragment>
        ))}
        {results.length === 0 && <p className="py-8 text-center text-muted-foreground text-sm">Nothing matches.</p>}
      </div>
    </div>
  );
}
