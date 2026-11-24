const names = (settings: Record<string, unknown>, platform: string): string[] =>
  (Array.isArray(settings[platform]) ? settings[platform] : []).flatMap((a) =>
    typeof a?.account === "string" && a.account ? [a.account] : [],
  );

/** added is the names of a platform's accounts that the settings to be saved
 * have and the saved ones did not. */
export function added(was: Record<string, unknown>, now: Record<string, unknown>, platform: string): string[] {
  return names(now, platform).filter((name) => !names(was, platform).includes(name));
}
