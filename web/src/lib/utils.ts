import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

/** cn merges Tailwind classes, letting a caller's class win over a default. */
export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}
