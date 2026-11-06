import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, Search } from "lucide-react";
import { useMemo, useState } from "react";
import { api, data } from "@/api/client";
import { Segmented } from "@/components/segmented";
import { Tile } from "@/components/tile";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogPanel,
  DialogPopup,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { chatKind, chatName } from "@/lib/chats";
import { platformMeta } from "@/lib/meta";
import { ago } from "@/lib/time";
import { cn } from "@/lib/utils";
import { chatsQuery } from "@/queries/chats";

/** ChatPicker adds chats the bot has seen to an allowlist, recent first. */
export function ChatPicker({
  module,
  list,
  title,
  on,
  open,
  onOpenChange,
}: {
  module: string;
  list: string;
  title: string;
  on: string[];
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const { data: chats = [] } = useQuery({ ...chatsQuery, enabled: open });
  const [search, setSearch] = useState("");
  const [platform, setPlatform] = useState("all");
  const [picked, setPicked] = useState<string[]>([]);
  const queryClient = useQueryClient();
  const shown = useMemo(() => {
    const q = search.trim().toLowerCase();
    return chats.filter(
      (c) =>
        !on.includes(c.id) &&
        (platform === "all" || c.platform === platform) &&
        (!q || `${chatName(c)} ${c.title ?? ""} ${c.id}`.toLowerCase().includes(q)),
    );
  }, [chats, on, platform, search]);
  const add = useMutation({
    mutationFn: async () => {
      for (const chat of picked) {
        await data(
          api.PUT("/api/modules/{module}/allowlists/{list}/chats/{chat}", { params: { path: { module, list, chat } } }),
        );
      }
    },
    onSettled: async () => {
      await queryClient.invalidateQueries({ queryKey: ["modules"] });
      await queryClient.invalidateQueries({ queryKey: ["chats"] });
    },
    onSuccess: () => {
      setPicked([]);
      onOpenChange(false);
    },
  });
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogPopup className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>Add chats to {title}</DialogTitle>
          <DialogDescription>Chats the bot has seen, most recent first.</DialogDescription>
        </DialogHeader>
        <DialogPanel className="flex flex-col gap-3">
          <div className="relative">
            <Search className="pointer-events-none absolute top-1/2 left-2.5 z-10 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              className="ps-7"
              placeholder="Search by name or ID"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              autoFocus
            />
          </div>
          <Segmented
            value={platform}
            onChange={setPlatform}
            options={[
              { value: "all", label: "All" },
              { value: "telegram", label: "Telegram" },
              { value: "qq", label: "QQ" },
            ]}
          />
          <ul className="-mx-1 flex max-h-[min(24rem,50vh)] flex-col overflow-y-auto">
            {shown.map((c) => {
              const p = platformMeta(c.platform);
              const checked = picked.includes(c.id);
              return (
                <li key={c.id}>
                  <button
                    type="button"
                    aria-pressed={checked}
                    onClick={() => setPicked(checked ? picked.filter((x) => x !== c.id) : [...picked, c.id])}
                    className={cn(
                      "flex w-full items-center gap-3 rounded-xl px-2 py-2 text-left outline-none transition-colors hover:bg-accent focus-visible:ring-2 focus-visible:ring-ring",
                      checked && "bg-accent",
                    )}
                  >
                    <Tile tone={p.tone} icon={p.icon} size="sm" />
                    <span className="min-w-0 flex-1">
                      <span className="block truncate font-medium text-sm">{chatName(c)}</span>
                      <span className="block truncate text-muted-foreground text-xs">
                        {chatKind(c)} · <span className="font-mono">{c.id}</span>
                      </span>
                    </span>
                    <time className="text-muted-foreground text-xs" dateTime={c.last_seen}>
                      {ago(c.last_seen)}
                    </time>
                    <span
                      className={cn(
                        "grid size-5 place-items-center rounded-md border transition-colors",
                        checked ? "border-primary bg-primary text-primary-foreground" : "bg-background",
                      )}
                    >
                      {checked && <Check className="size-3.5" />}
                    </span>
                  </button>
                </li>
              );
            })}
            {shown.length === 0 && (
              <li className="px-2 py-8 text-center text-muted-foreground text-sm">
                {chats.length === 0 ? "The bot has not seen any chats yet." : "No chats match."}
              </li>
            )}
          </ul>
        </DialogPanel>
        <DialogFooter>
          <Button disabled={picked.length === 0} loading={add.isPending} onClick={() => add.mutate()}>
            {picked.length > 1 ? `Add ${picked.length} chats` : "Add chat"}
          </Button>
        </DialogFooter>
      </DialogPopup>
    </Dialog>
  );
}
