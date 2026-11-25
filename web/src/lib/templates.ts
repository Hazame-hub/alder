import type { EntryTemplate, TemplateRendering } from "./api";

/** The templates "New ..." offers on a view: the usable ones for that view. */
export function templatesFor(all: EntryTemplate[] | undefined, view: string): EntryTemplate[] {
  return (all ?? []).filter((t) => t.view === view);
}

/** "New user", "New group": the button's label for a view. */
export function newLabel(view: string): string {
  switch (view) {
    case "users":
      return "New user";
    case "groups":
      return "New group";
    default:
      return "New entry";
  }
}

/**
 * What was allocated, in a sentence the review dialog carries: the number is a
 * value in the change like any other, and the reviewer should know where it
 * came from.
 */
export function allocationLine(r: Pick<TemplateRendering, "allocated">): string {
  return r.allocated
    .map((a) =>
      a.highestInUse === undefined
        ? `${a.attribute} ${a.value}, the first of its range (none in use)`
        : `${a.attribute} ${a.value}, the next free after ${a.highestInUse}`,
    )
    .join("; ");
}
