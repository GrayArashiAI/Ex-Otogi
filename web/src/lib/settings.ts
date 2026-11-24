import { BrainCircuit, KeyRound, SlidersHorizontal } from "lucide-react";
import { moduleMeta } from "@/lib/meta";
import { sectionOf } from "@/lib/secrets";

/** The pages under Settings. One with a section edits that section of the
 * settings outside modules; effect ends "They are saved as revision 3, and
 * …" with what the bot does with a saved revision of it. */
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

export const secretsPage = {
  to: "/settings/secrets",
  title: "Secrets",
  detail: "Tokens and keys",
  icon: KeyRound,
  tone: "amber",
} as const;

/** settingsPages are the pages in the order of the Settings list. */
export const settingsPages = [generalPage, llmPage, secretsPage] as const;

/** sectionPage is the page of the section an ID such as system:llm names. */
export function sectionPage(id: string) {
  const section = sectionOf(id);
  return [generalPage, llmPage].find((p) => p.section === section);
}

/** referenceTitle names what refers to a secret: a section's page, or a module. */
export function referenceTitle(id: string): string {
  return sectionPage(id)?.title ?? moduleMeta(id).title;
}
