import {
  AlarmClock,
  Bot,
  KanbanSquare,
  MessageSquare,
  NotebookPen,
  PlaySquare,
  Repeat2,
  ShoppingBag,
  Wrench,
} from "lucide-react";
import type { ComponentType, SVGProps } from "react";

export type Tone = "emerald" | "violet" | "rose" | "zinc" | "sky" | "orange" | "amber" | "indigo" | "slate" | "teal";

type Icon = ComponentType<SVGProps<SVGSVGElement> & { className?: string }>;

/** XGlyph is X's mark, drawn for the X cards module. */
export function XGlyph(props: SVGProps<SVGSVGElement>) {
  return (
    <svg viewBox="0 0 24 24" fill="currentColor" aria-hidden {...props}>
      <path d="M17.75 3h3.07l-6.7 7.66L22 21h-6.17l-4.83-6.32L5.47 21H2.4l7.17-8.2L2 3h6.33l4.37 5.77L17.75 3Zm-1.08 16.17h1.7L7.4 4.74H5.58l11.1 14.43Z" />
    </svg>
  );
}

/** TelegramGlyph and QQGlyph mark the platforms. */
export function TelegramGlyph(props: SVGProps<SVGSVGElement>) {
  return (
    <svg viewBox="0 0 24 24" fill="currentColor" aria-hidden {...props}>
      <path d="M3.7 11.2 19 5.3c.8-.3 1.4.2 1.2 1.3l-2.6 12.3c-.2.9-.7 1.1-1.5.7l-4.1-3-2 1.9c-.2.2-.4.4-.9.4l.3-4.2 7.6-6.9c.3-.3-.1-.5-.5-.2L7.2 13.5l-4-1.3c-.9-.3-.9-.9.5-1Z" />
    </svg>
  );
}

export function QQGlyph(props: SVGProps<SVGSVGElement>) {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.8}
      strokeLinecap="round"
      aria-hidden
      {...props}
    >
      <path d="M12 3.5c-3 0-5 2.4-5 5.6v1.6c-.9 1.1-2.1 3-2.1 4.6 0 .9.4 1.2.9.7l.9-.9c.4 1.3 1.2 2.4 2.2 3.2-.9.4-1.6 1-1.6 1.5 0 .7 1.6 1 3.4.6.6-.1 1-.2 1.3-.2s.7.1 1.3.2c1.8.4 3.4.1 3.4-.6 0-.5-.7-1.1-1.6-1.5 1-.8 1.8-1.9 2.2-3.2l.9.9c.5.5.9.2.9-.7 0-1.6-1.2-3.5-2.1-4.6V9.1c0-3.2-2-5.6-5-5.6Z" />
    </svg>
  );
}

export type ModuleMeta = { title: string; about: string; icon: Icon; tone: Tone };

const modules: Record<string, ModuleMeta> = {
  chat: {
    title: "Chat agents",
    about: "Language model agents that answer in the chats on their lists.",
    icon: MessageSquare,
    tone: "emerald",
  },
  bilibili: {
    title: "Bilibili cards",
    about: "Answers links to Bilibili videos with a card.",
    icon: PlaySquare,
    tone: "rose",
  },
  booth: {
    title: "Booth cards",
    about: "Answers links to Booth items with a card showing the item's first picture.",
    icon: ShoppingBag,
    tone: "orange",
  },
  twitter: {
    title: "X cards",
    about: "Answers links to X posts with a card, and a translated one for posts not in Chinese.",
    icon: XGlyph,
    tone: "zinc",
  },
  notes: {
    title: "Notes",
    about: "/note keeps notes per chat, and agents can read and add them.",
    icon: NotebookPen,
    tone: "amber",
  },
  reminders: {
    title: "Reminders",
    about: "/remind sets reminders that survive restarts; agents can set them too.",
    icon: AlarmClock,
    tone: "sky",
  },
  todou: {
    title: "todou",
    about: "Links chats to todou projects: digests of their activity, and tools for agents.",
    icon: KanbanSquare,
    tone: "indigo",
  },
  utility: { title: "Utility", about: "/whoami, /ping, /status, and /help.", icon: Wrench, tone: "slate" },
  echo: {
    title: "Echo",
    about: "Replies to every message with its text. For testing only.",
    icon: Repeat2,
    tone: "teal",
  },
};

export function moduleMeta(id: string): ModuleMeta {
  return modules[id] ?? { title: id, about: "", icon: Bot, tone: "slate" };
}

const agentTones: Tone[] = ["emerald", "violet", "sky", "amber", "rose", "indigo"];

/** listMeta is how an allowlist shows: a chat agent's is its own tile. */
export function listMeta(module: string, list: string, index: number): ModuleMeta {
  const m = moduleMeta(module);
  if (module !== "chat") return m;
  return { ...m, title: list, tone: agentTones[index % agentTones.length] ?? "emerald" };
}

export const platforms: Record<string, { title: string; icon: Icon; tone: Tone }> = {
  telegram: { title: "Telegram", icon: TelegramGlyph, tone: "sky" },
  qq: { title: "QQ", icon: QQGlyph, tone: "orange" },
};

export function platformMeta(platform: string) {
  return platforms[platform] ?? { title: platform, icon: MessageSquare, tone: "slate" as Tone };
}
