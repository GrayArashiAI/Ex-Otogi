import {
  AlarmClock,
  Bot,
  ImageIcon,
  KanbanSquare,
  MessageSquare,
  NotebookPen,
  Repeat2,
  Scale,
  ShieldCheck,
  Wrench,
} from "lucide-react";
import type { ComponentType, SVGProps } from "react";

export type Tone =
  | "emerald"
  | "violet"
  | "rose"
  | "red"
  | "zinc"
  | "sky"
  | "orange"
  | "amber"
  | "indigo"
  | "slate"
  | "teal"
  | "cyan";

type Icon = ComponentType<SVGProps<SVGSVGElement> & { className?: string }>;

/** XGlyph is X's mark, drawn for the X cards module. */
export function XGlyph(props: SVGProps<SVGSVGElement>) {
  return (
    <svg viewBox="0 0 24 24" fill="currentColor" aria-hidden {...props}>
      <path d="M17.75 3h3.07l-6.7 7.66L22 21h-6.17l-4.83-6.32L5.47 21H2.4l7.17-8.2L2 3h6.33l4.37 5.77L17.75 3Zm-1.08 16.17h1.7L7.4 4.74H5.58l11.1 14.43Z" />
    </svg>
  );
}

/** BilibiliGlyph is Bilibili's TV mark (Simple Icons, CC0), drawn for the
 * Bilibili cards module. */
export function BilibiliGlyph(props: SVGProps<SVGSVGElement>) {
  return (
    <svg viewBox="0 0 24 24" fill="currentColor" aria-hidden {...props}>
      <path d="M17.813 4.653h.854c1.51.054 2.769.578 3.773 1.574 1.004.995 1.524 2.249 1.56 3.76v7.36c-.036 1.51-.556 2.769-1.56 3.773s-2.262 1.524-3.773 1.56H5.333c-1.51-.036-2.769-.556-3.773-1.56S.036 18.858 0 17.347v-7.36c.036-1.511.556-2.765 1.56-3.76 1.004-.996 2.262-1.52 3.773-1.574h.774l-1.174-1.12a1.234 1.234 0 0 1-.373-.906c0-.356.124-.658.373-.907l.027-.027c.267-.249.573-.373.92-.373.347 0 .653.124.92.373L9.653 4.44c.071.071.134.142.187.213h4.267a.836.836 0 0 1 .16-.213l2.853-2.747c.267-.249.573-.373.92-.373.347 0 .662.151.929.4.267.249.391.551.391.907 0 .355-.124.657-.373.906zM5.333 7.24c-.746.018-1.373.276-1.88.773-.506.498-.769 1.13-.786 1.894v7.52c.017.764.28 1.395.786 1.893.507.498 1.134.756 1.88.773h13.334c.746-.017 1.373-.275 1.88-.773.506-.498.769-1.129.786-1.893v-7.52c-.017-.765-.28-1.396-.786-1.894-.507-.497-1.134-.755-1.88-.773zM8 11.107c.373 0 .684.124.933.373.25.249.383.569.4.96v1.173c-.017.391-.15.711-.4.96-.249.25-.56.374-.933.374s-.684-.125-.933-.374c-.25-.249-.383-.569-.4-.96V12.44c0-.373.129-.689.386-.947.258-.257.574-.386.947-.386zm8 0c.373 0 .684.124.933.373.25.249.383.569.4.96v1.173c-.017.391-.15.711-.4.96-.249.25-.56.374-.933.374s-.684-.125-.933-.374c-.25-.249-.383-.569-.4-.96V12.44c.017-.391.15-.711.4-.96.249-.249.56-.373.933-.373Z" />
    </svg>
  );
}

/** BoothGlyph is BOOTH's booth mark, traced from its app icon, drawn for
 * the BOOTH cards module. */
export function BoothGlyph(props: SVGProps<SVGSVGElement>) {
  return (
    <svg viewBox="0 0 24 24" fill="currentColor" aria-hidden {...props}>
      <path d="M1.05 2.96L3.00 2.96L3.00 2.17L5.55 2.17L5.55 10.88L9.46 1.78L10.83 1.78L13.02 6.48L15.32 1.00L16.89 1.00L21.00 7.75L21.00 9.41L22.95 11.36L22.95 15.47L21.00 15.47L21.00 23.00L19.04 23.00L19.04 19.87C19.04 17.13 15.91 15.37 12.59 15.37C10.04 15.37 8.09 17.13 8.09 19.48L8.09 23.00L5.55 23.00L5.55 14.88Q3.00 14.88 3.00 13.61L3.00 5.69L1.05 5.69Z" />
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
  decision: {
    title: "Decisions",
    about: "Yes/no judgments about a conversation, by TypeSafe Jev, for other modules to ask.",
    icon: Scale,
    tone: "violet",
  },
  media: {
    title: "Media descriptions",
    about: "Describes the images, animations, and videos sent in the chats on its list, for chat agents to read.",
    icon: ImageIcon,
    tone: "orange",
  },
  bilibili: {
    title: "Bilibili cards",
    about: "Answers links to Bilibili videos with a card.",
    icon: BilibiliGlyph,
    tone: "rose",
  },
  booth: {
    title: "BOOTH cards",
    about: "Answers links to BOOTH items with a card showing the item's first picture.",
    icon: BoothGlyph,
    tone: "red",
  },
  twitter: {
    title: "X cards",
    about: "Answers links to X posts with a card, and a translated one for posts not in Chinese.",
    icon: XGlyph,
    tone: "zinc",
  },
  safety: {
    title: "Content safety",
    about: "How card modules show sensitive pictures, and pictures a platform refused.",
    icon: ShieldCheck,
    tone: "cyan",
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

export type ListMeta = ModuleMeta & {
  /** heading titles the list's chats; toggle says what a chat on it gets;
   * empty says what to do while it has none. */
  heading: string;
  toggle: string;
  empty: string;
  /** agent marks a chat agent's list. */
  agent: boolean;
};

/** listMeta is how an allowlist shows: a chat agent's is its own tile, and
 * a list granting another module's capability, such as where agents may
 * use todou's tools, shows that module. */
export function listMeta(module: string, list: { title: string; about?: string }, index: number): ListMeta {
  if (list.about) {
    const about = moduleMeta(list.about);
    return {
      ...about,
      title: list.title,
      heading: list.title,
      toggle: `Agents may use ${about.title} here`,
      empty: "Nowhere yet. Add the chats where agents may use them.",
      agent: false,
    };
  }
  const m = moduleMeta(module);
  const empty = "Nowhere yet. Add the chats it should answer in.";
  if (module !== "chat") {
    return { ...m, heading: "Allowed chats", toggle: `${m.title} answers in this chat`, empty, agent: false };
  }
  return {
    ...m,
    title: list.title,
    tone: agentTones[index % agentTones.length] ?? "emerald",
    heading: `Where ${list.title} answers`,
    toggle: `${list.title} answers in this chat`,
    empty,
    agent: true,
  };
}

export const platforms: Record<string, { title: string; icon: Icon; tone: Tone }> = {
  telegram: { title: "Telegram", icon: TelegramGlyph, tone: "sky" },
  qq: { title: "QQ", icon: QQGlyph, tone: "orange" },
};

export function platformMeta(platform: string) {
  return platforms[platform] ?? { title: platform, icon: MessageSquare, tone: "slate" as Tone };
}
