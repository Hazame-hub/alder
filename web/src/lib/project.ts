import type { AttributeValue, ChangeRequest, Schemas } from "./api";
import { changeset, maxStagedChanges } from "./changeset";

/**
 * Opening an Alder project in the browser (docs/proposals/web-projects.md).
 *
 * Everything that decides what a project means -- the format, the globs, the
 * validation rules, the secrets check, DN equality -- is the server's, the
 * same code `alder project validate` and `plan` run. What is here is only
 * what the browser has to decide for itself: which of a chosen folder's
 * files it reads and sends, and how a plan is staged. Nothing here parses
 * alder.yaml or an LDIF file; a second validator would be a project that
 * validates in the browser and not in CI.
 */

export type ProjectPlan = Schemas["ProjectPlan"];
export type ProjectFile = Schemas["ProjectFile"];

/** The project file, always at the top of the chosen folder. */
export const projectFileName = "alder.yaml";

/** How many files one request may carry; the server refuses more. */
export const maxProjectFiles = 1000;

/**
 * The server's body limit. A project larger than this stays a command-line
 * project; the browser says so rather than sending part of it.
 */
export const maxRequestBytes = 16 << 20;

/** A file as the folder picker hands it over: its path includes the folder. */
export type PickedFile<F> = { relativePath: string; file: F };

export type Selection<F> =
  | {
      ok: true;
      /** The chosen folder's own name, for the heading. Untrusted text. */
      folder: string;
      /** What will be read and sent, by path below the folder. */
      send: { path: string; file: F }[];
    }
  | { ok: false; message: string };

function isLdif(name: string): boolean {
  return name.toLowerCase().endsWith(".ldif");
}

/**
 * Which of a chosen folder's files are read and sent.
 *
 * `alder.yaml` at the top of the folder, and every `*.ldif` below it outside
 * dot-directories, by path relative to the folder. Nothing else is read: not
 * `.git`, not a README, not a key file somebody left beside the project.
 * Names are all the page sees of the rest, and they are not sent.
 */
export function selectProjectFiles<F>(picked: PickedFile<F>[]): Selection<F> {
  let folder = "";
  const send: { path: string; file: F }[] = [];
  const deeperProjects: string[] = [];

  for (const { relativePath, file } of picked) {
    const parts = relativePath.split("/").filter((p) => p !== "");
    if (parts.length < 2) continue;
    folder = folder || parts[0]!;
    const below = parts.slice(1);
    const dirs = below.slice(0, -1);
    const name = below[below.length - 1]!;
    if (dirs.some((d) => d.startsWith("."))) continue;
    const path = below.join("/");
    if (path === projectFileName) {
      send.push({ path, file });
    } else if (name === projectFileName) {
      deeperProjects.push(dirs.join("/"));
    } else if (isLdif(name)) {
      send.push({ path, file });
    }
  }

  if (!send.some((s) => s.path === projectFileName)) {
    if (deeperProjects.length > 0) {
      const where = deeperProjects.sort()[0];
      return {
        ok: false,
        message:
          `There is no ${projectFileName} at the top of this folder; there is one in ${where}/. ` +
          "Choose the folder that holds alder.yaml itself.",
      };
    }
    return {
      ok: false,
      message: `There is no ${projectFileName} at the top of this folder, so it is not an Alder project.`,
    };
  }
  if (send.length > maxProjectFiles) {
    return {
      ok: false,
      message:
        `This folder has ${send.length} files to send, and one request carries at most ${maxProjectFiles}. ` +
        "Plan it with alder project plan.",
    };
  }
  // The same order every time, so "Plan again" sends exactly what it sent.
  send.sort((a, b) => (a.path < b.path ? -1 : a.path > b.path ? 1 : 0));
  return { ok: true, folder, send };
}

/** The request as it will be sent, and whether the server would take it. */
export function projectRequest(files: ProjectFile[]): { body: { files: ProjectFile[] }; tooLarge: string | null } {
  const body = { files };
  const bytes = new TextEncoder().encode(JSON.stringify(body)).length;
  return {
    body,
    tooLarge:
      bytes > maxRequestBytes
        ? `This project is ${(bytes / (1 << 20)).toFixed(1)} MiB as a request, more than the ` +
          `${maxRequestBytes >> 20} MiB the server accepts in one. Plan it with alder project plan.`
        : null,
  };
}

/** What a changeset staged from a project says above itself. */
export type ProjectOrigin = {
  kind: "project";
  folder: string;
  subtrees: number;
  entries: number;
  host: string;
  bindDn: string;
  environments: string[];
};

export function originOf(folder: string, result: ProjectPlan): ProjectOrigin {
  return {
    kind: "project",
    folder,
    subtrees: result.project.subtrees.length,
    entries: result.project.entries,
    host: result.host,
    bindDn: result.bindDn,
    environments: result.project.environments,
  };
}

/** The plan's changes, as the changeset stages them. */
export function projectChanges(plan: ProjectPlan["plan"]): { change: ChangeRequest; label: string }[] {
  return plan.items
    .filter((item) => item.record !== undefined && item.baseline !== undefined)
    .map((item) => ({
      change: item.record as ChangeRequest,
      label: `Project: ${item.action} ${item.dn}`,
    }));
}

function withheld(v: AttributeValue): boolean {
  return v.text === undefined && v.base64 === undefined;
}

/** Whether a planned record carries a value the plan withheld. */
export function hasWithheldValue(change: ChangeRequest): boolean {
  return (
    (change.attributes ?? []).some((a) => (a.values ?? []).some(withheld)) ||
    (change.mods ?? []).some((m) => (m.values ?? []).some(withheld))
  );
}

export type ProjectStageOutcome = {
  ok: boolean;
  message: string;
  /** Set when the refusal is the changeset already holding something. */
  stagedAlready?: number;
};

/**
 * Stage a project's plan for review, into an empty changeset only.
 *
 * A changeset that is "the project plus three other things" is no longer the
 * project's plan, and the heading above it would be false. So a non-empty
 * changeset is refused with its count, and replacing what is there is the
 * operator's own action, never a side effect: replacing silently loses work.
 *
 * What is staged is each change the plan would apply, as the plan's own
 * record and without its baseline: the changeset plans it again against the
 * directory as it is when it is checked, and applies with that plan's
 * baselines, recovery bundle and Ansible export, as it does everything.
 */
export function stageProjectPlan(origin: ProjectOrigin, plan: ProjectPlan["plan"]): ProjectStageOutcome {
  const already = changeset.all().length;
  if (already > 0) {
    return {
      ok: false,
      stagedAlready: already,
      message:
        `The changeset already holds ${already} change${already === 1 ? "" : "s"}. A project is staged on its own, ` +
        "so that what is reviewed is the project's plan and nothing else: apply or clear what is there first.",
    };
  }
  const items = projectChanges(plan);
  if (items.length === 0) {
    return { ok: false, message: "Nothing to stage: the directory already matches the project." };
  }
  if (items.length > maxStagedChanges) {
    return {
      ok: false,
      message:
        `This plan has ${items.length} changes, more than a changeset holds (${maxStagedChanges}). ` +
        "Read it here, and apply it with alder project apply.",
    };
  }
  if (items.some((i) => hasWithheldValue(i.change))) {
    return {
      ok: false,
      message:
        "A planned change carries a value the plan withheld as sensitive, which the changeset cannot send. " +
        "Apply this project with alder project apply.",
    };
  }
  changeset.addMany(items);
  changeset.setOrigin(origin);
  return {
    ok: true,
    message: `${items.length} change${items.length === 1 ? " is" : "s are"} staged. Nothing has been sent to the directory.`,
  };
}
