import type { Schemas } from "@/api/client";

type Chat = Pick<Schemas["SeenChat"], "name" | "title" | "conversation" | "platform" | "kind">;

/** chatName is what a chat is called: the operator's name, the platform's
 * title, or a short form of its ID. */
export function chatName(c: Chat | undefined, id?: string): string {
  if (c?.name) return c.name;
  if (c?.title) return c.title;
  const conversation = c?.conversation ?? id?.split(":").slice(1).join(":") ?? "";
  const [kind, raw = ""] = conversation.split(":");
  const short = raw.length > 10 ? `${raw.slice(0, 8)}…` : raw;
  return kind === "group" ? `Group ${short}` : kind === "user" ? `User ${short}` : conversation;
}

export function chatKind(c: Pick<Schemas["SeenChat"], "kind" | "platform">): string {
  const platform = c.platform === "qq" ? "QQ" : c.platform === "telegram" ? "Telegram" : c.platform;
  const kind = { direct: "private chat", group: "group", channel: "channel" }[c.kind ?? ""] ?? "chat";
  return `${platform} ${kind}`;
}

/** preview is a message's text in one line, with who said it. */
export function preview(c: Schemas["SeenChat"]): string {
  const event = c.last_message?.payload as
    | {
        actor?: { name?: string; id?: string };
        message?: { parts?: { kind: string; text?: string; media?: { role?: string } }[] };
      }
    | undefined;
  if (!event) return "";
  const parts = event.message?.parts ?? [];
  const text = parts.find((p) => p.kind === "text")?.text ?? (parts.some((p) => p.media) ? "Photo" : "");
  const fromPeer = c.kind === "direct" && event.actor?.id === c.conversation;
  const who = event.actor?.name || (fromPeer && (c.name || c.title)) || event.actor?.id?.split(":").pop();
  return who ? `${who}: ${text}` : text;
}
