import { unzipSync } from "fflate";
import { isProjectFileName, maxRequestBytes, type PickedFile } from "./project";

/**
 * Where an opened project's files come from, besides the folder picker: a
 * folder dropped on the page, or a .zip of one. Both end as the picker's
 * list -- a path that starts with the folder's name, and something to read
 * the text from -- so selectProjectFiles decides what is sent the same way
 * whichever way the files came in.
 *
 * Neither reads more than the picker would send: alder.yaml and *.ldif
 * outside dot-directories. A dropped folder's other files are never opened
 * and its dot-directories never walked; a zip's other members are never
 * inflated.
 */

/** Something whose text can be read: a File, or a member of a zip. */
export type Readable = { text(): Promise<string> };

export type Picked = PickedFile<Readable>;

export type SourceResult = { ok: true; picked: Picked[] } | { ok: false; message: string };

/** A .zip larger than this is not opened in the browser. */
export const maxZipBytes = 64 << 20;

/**
 * Entries a dropped folder may hold before walking it stops. A project is a
 * few hundred files; a folder with this many is a home directory or a
 * checkout with its dependencies, dropped by mistake.
 */
export const maxWalkedEntries = 20_000;

export function isZipName(name: string): boolean {
  return name.toLowerCase().endsWith(".zip");
}

/** A path inside a zip that names a place outside it, or nothing at all. */
function unsafeMember(name: string): boolean {
  if (name.startsWith("/") || name.includes("\\") || /^[A-Za-z]:/.test(name)) return true;
  return name.split("/").some((p) => p === "..");
}

/** Whether a member is one the picker would send: by name, outside dot-directories. */
function wanted(name: string): boolean {
  if (name.endsWith("/") || unsafeMember(name)) return false;
  const parts = name.split("/").filter((p) => p !== "" && p !== ".");
  const base = parts[parts.length - 1] ?? "";
  if (parts.slice(0, -1).some((d) => d.startsWith("."))) return false;
  return isProjectFileName(base);
}

/**
 * The project files in a .zip, as the folder picker would list them.
 *
 * A zip made by zipping a folder holds that folder at its top, and is used as
 * it is; a zip of a folder's contents is listed under the zip's own name, so
 * the heading still names something. Only the members the picker would send
 * are inflated, and only while their declared sizes stay under what one
 * request can carry.
 */
export function readZip(zipName: string, data: Uint8Array): SourceResult {
  if (data.byteLength > maxZipBytes) {
    return {
      ok: false,
      message: `This .zip is ${(data.byteLength / (1 << 20)).toFixed(1)} MiB, more than the ${maxZipBytes >> 20} MiB opened in the browser. Unzip it and open the folder, or plan it with alder project plan.`,
    };
  }
  let declared = 0;
  let tooLarge = false;
  let members: Record<string, Uint8Array>;
  try {
    members = unzipSync(data, {
      filter: (file) => {
        if (!wanted(file.name)) return false;
        declared += file.originalSize;
        if (declared > maxRequestBytes) tooLarge = true;
        return !tooLarge;
      },
    });
  } catch (e) {
    return { ok: false, message: `This file could not be read as a .zip: ${e instanceof Error ? e.message : String(e)}` };
  }
  if (tooLarge) {
    return {
      ok: false,
      message: `The project files in this .zip come to more than the ${maxRequestBytes >> 20} MiB the server accepts in one request. Plan it with alder project plan.`,
    };
  }

  const names = Object.keys(members).map((n) => n.split("/").filter((p) => p !== "" && p !== "."));
  // Inflated sizes are checked again: a member's declared size is the zip's
  // word, and the request limit is the server's.
  const inflated = Object.values(members).reduce((n, m) => n + m.byteLength, 0);
  if (inflated > maxRequestBytes) {
    return {
      ok: false,
      message: `The project files in this .zip come to more than the ${maxRequestBytes >> 20} MiB the server accepts in one request. Plan it with alder project plan.`,
    };
  }
  const tops = new Set(names.map((p) => (p.length > 1 ? p[0] : "")));
  const wrapped = tops.size === 1 && !tops.has("");
  const own = zipName.replace(/\.zip$/i, "") || "project";
  const decoder = new TextDecoder();

  const picked: Picked[] = Object.values(members).map((bytes, i) => {
    const parts = names[i]!;
    return {
      relativePath: (wrapped ? parts : [own, ...parts]).join("/"),
      file: { text: async () => decoder.decode(bytes) },
    };
  });
  return { ok: true, picked };
}

/*
 * The parts of the File and Directory Entries API a dropped folder is read
 * through, typed here because TypeScript's DOM library types the callbacks
 * loosely, and so a test can hand in a folder of its own.
 */
export type DroppedEntry = DroppedFileEntry | DroppedDirectoryEntry;
export type DroppedFileEntry = {
  isFile: true;
  isDirectory: false;
  name: string;
  fullPath: string;
  file(success: (file: File) => void, failure?: (err: unknown) => void): void;
};
export type DroppedDirectoryEntry = {
  isFile: false;
  isDirectory: true;
  name: string;
  fullPath: string;
  createReader(): {
    readEntries(success: (entries: DroppedEntry[]) => void, failure?: (err: unknown) => void): void;
  };
};

function readAll(dir: DroppedDirectoryEntry): Promise<DroppedEntry[]> {
  const reader = dir.createReader();
  const all: DroppedEntry[] = [];
  // readEntries hands a directory over in batches, and an empty batch is the end.
  return new Promise((resolve, reject) => {
    const next = () =>
      reader.readEntries((batch) => {
        if (batch.length === 0) {
          resolve(all);
          return;
        }
        all.push(...batch);
        next();
      }, reject);
    next();
  });
}

function fileOf(entry: DroppedFileEntry): Promise<File> {
  return new Promise((resolve, reject) => entry.file(resolve, reject));
}

/**
 * The project files in a dropped folder, as the folder picker would list them.
 *
 * Dot-directories are not walked at all, so a dropped repository's .git is
 * never read; files are opened only when the picker would send them; and the
 * walk stops at maxWalkedEntries rather than enumerate a home directory.
 */
export async function walkDroppedFolder(root: DroppedDirectoryEntry): Promise<SourceResult> {
  const picked: Picked[] = [];
  let seen = 0;
  const queue: DroppedDirectoryEntry[] = [root];
  try {
    while (queue.length > 0) {
      const dir = queue.shift()!;
      for (const entry of await readAll(dir)) {
        if (++seen > maxWalkedEntries) {
          return {
            ok: false,
            message: `This folder holds more than ${maxWalkedEntries} files and folders, which is not a project. Drop the folder that holds alder.yaml.`,
          };
        }
        if (entry.isDirectory) {
          if (!entry.name.startsWith(".")) queue.push(entry);
        } else if (isProjectFileName(entry.name)) {
          picked.push({ relativePath: entry.fullPath.replace(/^\/+/, ""), file: await fileOf(entry) });
        }
      }
    }
  } catch (e) {
    return { ok: false, message: `The folder could not be read: ${e instanceof Error ? e.message : String(e)}` };
  }
  return { ok: true, picked };
}
