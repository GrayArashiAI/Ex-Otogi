import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Search as SearchIcon } from "lucide-react";
import { type ReactNode, useEffect, useMemo, useRef, useState } from "react";
import { Tile } from "@/components/tile";
import { Dialog, DialogPopup, DialogTitle } from "@/components/ui/dialog";
import { chatKind, chatName } from "@/lib/chats";
import { listMeta, moduleMeta, platformMeta } from "@/lib/meta";
import { cn } from "@/lib/utils";
import { chatsQuery } from "@/queries/chats";
import { modulesQuery } from "@/queries/modules";

/** Search jumps to a module, an agent, or a chat: Ctrl K anywhere. */
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
    <>
      <button
        type="button"
        onClick={() => setOpen(true)}
        className="ml-auto hidden w-60 items-center gap-2 rounded-lg border bg-background px-2.5 py-1.5 text-muted-foreground text-sm shadow-xs/5 outline-none transition-colors hover:bg-accent/50 focus-visible:ring-2 focus-visible:ring-ring md:flex"
      >
        <SearchIcon className="size-4" />
        Search
        <kbd className="ml-auto rounded-md border bg-muted px-1.5 font-mono text-[11px]">Ctrl K</kbd>
      </button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogPopup className="top-[12vh] sm:max-w-lg" showCloseButton={false}>
          <DialogTitle className="sr-only">Search</DialogTitle>
          {open && <Results onPick={() => setOpen(false)} />}
        </DialogPopup>
      </Dialog>
    </>
  );
}

type LinkProps = {
  ref: (el: HTMLAnchorElement | null) => void;
  onClick: () => void;
  onMouseMove: () => void;
  className: string;
  children: ReactNode;
};

type Result = {
  key: string;
  title: string;
  detail: string;
  meta: ReturnType<typeof moduleMeta>;
  link: (p: LinkProps) => ReactNode;
};

function Results({ onPick }: { onPick: () => void }) {
  const { data: modules = [] } = useQuery(modulesQuery);
  const { data: chats = [] } = useQuery(chatsQuery);
  const [q, setQ] = useState("");
  const [at, setAt] = useState(0);
  const links = useRef<(HTMLAnchorElement | null)[]>([]);
  const results = useMemo(() => {
    const all: Result[] = [
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
  return (
    <div className="flex flex-col">
      <div className="flex items-center gap-2 border-b px-4">
        <SearchIcon className="size-4 text-muted-foreground" />
        <input
          // biome-ignore lint/a11y/noAutofocus: the dialog opens to type into
          autoFocus
          className="h-12 flex-1 bg-transparent text-sm outline-none placeholder:text-muted-foreground/72"
          placeholder="Modules, agents, and chats"
          value={q}
          onChange={(e) => {
            setQ(e.target.value);
            setAt(0);
          }}
          onKeyDown={(e) => {
            if (e.key === "ArrowDown" || e.key === "ArrowUp") {
              e.preventDefault();
              setAt((i) => Math.max(0, Math.min(results.length - 1, i + (e.key === "ArrowDown" ? 1 : -1))));
            } else if (e.key === "Enter") {
              links.current[at]?.click();
            }
          }}
        />
      </div>
      <ul className="max-h-96 overflow-y-auto p-2">
        {results.map((r, i) => (
          <li key={r.key}>
            {r.link({
              ref: (el) => {
                links.current[i] = el;
              },
              onClick: onPick,
              onMouseMove: () => setAt(i),
              className: cn("flex items-center gap-3 rounded-lg px-2 py-1.5 outline-none", i === at && "bg-accent"),
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
          </li>
        ))}
        {results.length === 0 && <li className="py-8 text-center text-muted-foreground text-sm">Nothing matches.</li>}
      </ul>
    </div>
  );
}
