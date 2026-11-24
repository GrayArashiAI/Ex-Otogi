import { Combobox } from "@base-ui/react/combobox";
import { useQuery } from "@tanstack/react-query";
import { ChevronsUpDown, X } from "lucide-react";
import type { Schemas } from "@/api/client";
import { LoadError } from "@/components/load-error";
import { Tile } from "@/components/tile";
import { selectTriggerVariants } from "@/components/ui/select";
import { chatKind, chatName } from "@/lib/chats";
import { platformMeta } from "@/lib/meta";
import { cn } from "@/lib/utils";
import { chatsQuery } from "@/queries/chats";

type Chat = Schemas["SeenChat"];

const words = (c: Chat) => `${chatName(c)} ${c.title ?? ""} ${c.id}`.toLowerCase();

/** ChatFilter picks one chat the bot has seen, by name, to filter by its
 * conversation; a conversation the directory does not know shows as its ID. */
export function ChatFilter({
  value,
  onChange,
  className,
}: {
  value?: string;
  onChange: (conversation: string | undefined) => void;
  className?: string;
}) {
  const chats = useQuery(chatsQuery);
  const selected = chats.data?.find((c) => c.conversation === value) ?? null;
  return (
    <div className={cn("relative", className)}>
      <Combobox.Root<Chat>
        items={chats.data ?? []}
        value={selected}
        onValueChange={(c) => onChange(c?.conversation)}
        itemToStringLabel={chatName}
        isItemEqualToValue={(a, b) => a.id === b.id}
        filter={(c, q) => words(c).includes(q.trim().toLowerCase())}
      >
        <Combobox.Trigger
          aria-label={value ? `Chat: ${selected ? chatName(selected) : value}` : "Filter by chat"}
          className={cn(selectTriggerVariants(), "min-w-0", value && "pe-8")}
        >
          <span className={cn("flex-1 truncate", !value && "text-muted-foreground")}>
            {selected ? chatName(selected) : (value ?? "Any chat")}
          </span>
          {!value && <ChevronsUpDown className="-me-1" />}
        </Combobox.Trigger>
        <Combobox.Portal>
          <Combobox.Positioner align="start" sideOffset={4} className="z-50">
            <Combobox.Popup className="flex max-h-[min(24rem,var(--available-height))] w-80 max-w-(--available-width) origin-(--transform-origin) flex-col rounded-lg border bg-popover bg-clip-padding text-popover-foreground shadow-lg/5 outline-none">
              <Combobox.Input
                placeholder="Search by name or ID"
                aria-label="Search chats"
                className="h-10 w-full flex-none border-b bg-transparent px-3 text-base outline-none placeholder:text-muted-foreground sm:h-9 sm:text-sm"
              />
              {chats.error && (
                <LoadError className="px-3" what="the chats" error={chats.error} retry={() => void chats.refetch()} />
              )}
              {!chats.error && (
                <Combobox.Empty className="px-3 py-6 text-center text-muted-foreground text-sm empty:hidden">
                  No chats match.
                </Combobox.Empty>
              )}
              <Combobox.List className="min-h-0 flex-1 overflow-y-auto overscroll-contain p-1 empty:hidden">
                {(c: Chat) => {
                  const p = platformMeta(c.platform);
                  return (
                    <Combobox.Item
                      key={c.id}
                      value={c}
                      className="flex cursor-default items-center gap-2.5 rounded-md px-2 py-1.5 outline-none data-highlighted:bg-accent data-selected:font-medium pointer-coarse:py-2"
                    >
                      <Tile tone={p.tone} icon={p.icon} size="sm" />
                      <span className="min-w-0 flex-1">
                        <span className="block truncate text-sm">{chatName(c)}</span>
                        <span className="block truncate text-muted-foreground text-xs">
                          {chatKind(c)} · <span className="font-mono">{c.id}</span>
                        </span>
                      </span>
                    </Combobox.Item>
                  );
                }}
              </Combobox.List>
            </Combobox.Popup>
          </Combobox.Positioner>
        </Combobox.Portal>
      </Combobox.Root>
      {value && (
        <button
          type="button"
          onClick={() => onChange(undefined)}
          aria-label="Clear the chat filter"
          className="hit-target absolute top-1/2 right-1 grid size-6 -translate-y-1/2 place-items-center rounded-md text-muted-foreground outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring"
        >
          <X className="size-3.5" />
        </button>
      )}
    </div>
  );
}
