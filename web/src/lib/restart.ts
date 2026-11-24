/** Section is one section of the settings outside modules, as much of the
 * server's list as this reads. */
export type Section = { id: string; revision: number; applies: "now" | "restart"; running: number };

/** pendingRestart is the sections with a revision saved that the process
 * has not started with: their changes wait for a restart. */
export function pendingRestart<T extends Section>(sections: T[]): T[] {
  return sections.filter((s) => s.applies === "restart" && s.revision > s.running);
}
