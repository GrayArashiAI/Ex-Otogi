// Secrets as settings refer to them: a field holds ${NAME}, never the value.

/** Secret is one name in the server's list, as much of it as these helpers read. */
export type Secret = { name: string; source: "panel" | "environment" | "missing"; references: string[] };

/** sources is where a secret's value comes from, in the panel's status vocabulary. */
export const sources = {
  panel: { label: "Panel", tone: "ok" },
  environment: { label: "Environment", tone: "off" },
  // What refers to a missing secret cannot be built.
  missing: { label: "Missing", tone: "error" },
} as const;

/** reference is how settings refer to the secret of a name. */
export function reference(name: string): string {
  return `\${${name}}`;
}

/** referenceName is the name a field's ${NAME} refers to; none is "". */
export function referenceName(value: unknown): string {
  return (typeof value === "string" && /^\$\{([A-Za-z_][A-Za-z0-9_]*)\}$/.exec(value)?.[1]) || "";
}

/** nameProblem says why a new secret cannot have a name. A name only the
 * environment has, or one that is missing, can: the new value is then its. */
export function nameProblem(name: string, secrets: Secret[]): string | undefined {
  if (!name) return "Required";
  if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(name)) return "Letters, digits, and _, not starting with a digit";
  if (secrets.some((s) => s.name === name && s.source === "panel")) {
    return "A secret of this name is set already: replace it on the Secrets page";
  }
  return undefined;
}

/** choices are the secrets a field can refer to: the known ones, and the
 * one it refers to now when the list does not have it, as missing. */
export function choices<T extends Secret>(secrets: T[], name: string): (T | Secret)[] {
  if (!name || secrets.some((s) => s.name === name)) return secrets;
  return [...secrets, { name, source: "missing", references: [] }];
}

/** sectionOf is the section an ID such as system:llm names: what refers to
 * a secret is a module, whose ID has no colon, or a section. */
export function sectionOf(id: string): string | undefined {
  return id.startsWith("system:") ? id.slice("system:".length) : undefined;
}

const list = new Intl.ListFormat("en");

/** titlesList names things in a sentence: Decisions and todou. */
export function titlesList(titles: string[]): string {
  return list.format(titles);
}

/** restartNote says what a secret's new value restarts: the running modules
 * whose settings refer to it, by title, and, when the language model
 * providers refer to it, the running modules that use one. Which those are
 * the server says only once it has restarted them. */
export function restartNote(modules: string[], providers: boolean): string {
  const named =
    modules.length > 0 && `${titlesList(modules)} ${modules.length === 1 ? "restarts" : "restart"} with it at once.`;
  if (!providers) return named || "No running module refers to it, so nothing restarts.";
  const rebuilt = "The language model providers are rebuilt with it, and the running modules that use one restart.";
  return named ? `${named} ${rebuilt}` : rebuilt;
}
