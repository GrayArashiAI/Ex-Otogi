import { BrainCircuit, Cpu, FileText, KeyRound, SlidersHorizontal, Users } from "lucide-react";
import { moduleMeta } from "@/lib/meta";
import { sectionOf } from "@/lib/secrets";

/** The pages under Settings. One with a section edits that section of the
 * settings outside modules; effect ends "They are saved as revision 3, and
 * …" with what the bot does with a saved revision of it. A section the bot
 * reads when it starts says so with applies. */
export const generalPage = {
  to: "/settings/general",
  title: "General",
  detail: "Operators, commands, logs",
  icon: SlidersHorizontal,
  tone: "slate",
  section: "general",
  effect: "the bot uses them at once: nothing restarts",
} as const;

export const llmPage = {
  to: "/settings/llm",
  title: "Language models",
  detail: "Providers and their keys",
  icon: BrainCircuit,
  tone: "violet",
  section: "llm",
  effect:
    "the running modules that use a provider restart with them. If one of those cannot be rebuilt, nothing is saved",
} as const;

const onRestart = {
  applies: "restart",
  effect: "the bot uses them from its next start: until it is restarted, it runs with the ones it started with",
} as const;

export const accountsPage = {
  to: "/settings/accounts",
  title: "Accounts",
  detail: "Telegram and QQ accounts",
  icon: Users,
  tone: "sky",
  section: "accounts",
  ...onRestart,
} as const;

export const processPage = {
  to: "/settings/process",
  title: "Process",
  detail: "Workers, timeouts, log format",
  icon: Cpu,
  tone: "teal",
  section: "process",
  ...onRestart,
} as const;

export const secretsPage = {
  to: "/settings/secrets",
  title: "Secrets",
  detail: "Tokens and keys",
  icon: KeyRound,
  tone: "amber",
} as const;

export const startupPage = {
  to: "/settings/startup",
  title: "Startup file",
  detail: "What stays in the config file",
  icon: FileText,
  tone: "zinc",
} as const;

/** sectionPages are the pages that edit a section. */
export const sectionPages = [generalPage, llmPage, accountsPage, processPage] as const;

/** settingsPages are the pages in the order of the Settings list. */
export const settingsPages = [...sectionPages, secretsPage, startupPage] as const;

/** sectionPage is the page of the section an ID such as system:llm names. */
export function sectionPage(id: string) {
  const section = sectionOf(id);
  return sectionPages.find((p) => p.section === section);
}

/** referenceTitle names what refers to a secret: a section's page, or a module. */
export function referenceTitle(id: string): string {
  return sectionPage(id)?.title ?? moduleMeta(id).title;
}
