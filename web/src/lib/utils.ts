import { type ClassValue, clsx } from "clsx";
import { twMerge } from "tailwind-merge";

/** textLink styles a link in running text, or one that is a value in a list. */
export const textLink =
  "rounded-sm underline decoration-foreground/24 outline-none hover:decoration-foreground focus-visible:ring-2 focus-visible:ring-ring";

export function cn(...inputs: ClassValue[]): string {
  return twMerge(clsx(inputs));
}
