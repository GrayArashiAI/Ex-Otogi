import { KeyRound } from "lucide-react";

/** settingsPages are the pages under Settings, in the order of its list. */
export const settingsPages = [
  { to: "/settings/secrets", title: "Secrets", detail: "Tokens and keys", icon: KeyRound, tone: "amber" },
] as const;
