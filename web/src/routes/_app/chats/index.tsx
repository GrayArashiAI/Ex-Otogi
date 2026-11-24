import { createFileRoute } from "@tanstack/react-router";
import { MessagesSquare } from "lucide-react";
import { Tile } from "@/components/tile";

export const Route = createFileRoute("/_app/chats/")({ component: NoChat });

function NoChat() {
  return (
    <div className="hidden min-h-0 flex-col items-center justify-center gap-3 text-center md:flex">
      <Tile tone="slate" icon={MessagesSquare} size="lg" />
      <div>
        <p className="font-medium">Pick a chat</p>
        <p className="text-muted-foreground text-sm">Read what is said there, and turn modules on or off for it.</p>
      </div>
    </div>
  );
}
