import { unzipSync } from "fflate";
import { isProjectFileName, maxRequestBytes, type PickedFile } from "./project";
import { safeText } from "./display";

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

/**
 * Members a .zip may list before reading it stops. The count comes from the
 * zip's own directory and is not checked against its length, so a tiny file
 * can claim billions of entries and hold the page while each is visited.
 */
export const maxZipMembers = 100_000;

/** Why a .zip of this many bytes is not opened, or null. Checked before reading it. */
export function zipSizeRefusal(bytes: number): string | null {
  if (bytes <= maxZipBytes) return null;
  return `This .zip is ${(bytes / (1 << 20)).toFixed(1)} MiB, more than the ${maxZipBytes >> 20} MiB opened in the browser. Unzip it and open the folder, or plan it with alder project plan.`;
}

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

const crcTable = Uint32Array.from({ length: 256 }, (_, n) => {
  let c = n;
  for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
  return c;
});

/** CRC-32 as zip records it (IEEE 802.3, reflected, polynomial 0xedb88320). */
export function crc32(bytes: Uint8Array): number {
  let c = 0xffffffff;
  for (const b of bytes) c = crcTable[(c ^ b) & 0xff]! ^ (c >>> 8);
  return (c ^ 0xffffffff) >>> 0;
}

/**
 * The CRC-32 of the first count entries of a zip's central directory, in the
 * order unzipSync lists them, found the way unzipSync finds them. Called once
 * unzipSync has read the same entries, so a zip it refused never gets here.
 */
function centralCRCs(data: Uint8Array, count: number): number[] {
  const v = new DataView(data.buffer, data.byteOffset, data.byteLength);
  let e = data.length - 22;
  while (v.getUint32(e, true) !== 0x06054b50) e -= 1;
  let o = v.getUint32(e + 16, true);
  if (e >= 20 && v.getUint32(e - 20, true) === 0x07064b50) {
    const z = v.getUint32(e - 12, true);
    if (z + 56 <= data.length && v.getUint32(z, true) === 0x06064b50) o = v.getUint32(z + 48, true);
  }
  const out: number[] = [];
  for (let i = 0; i < count; i++) {
    out.push(v.getUint32(o + 16, true));
    o += 46 + v.getUint16(o + 28, true) + v.getUint16(o + 30, true) + v.getUint16(o + 32, true);
  }
  return out;
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
  const size = zipSizeRefusal(data.byteLength);
  if (size !== null) return { ok: false, message: size };
  let declared = 0;
  let tooLarge = false;
  let listed = 0;
  const sizes = new Map<string, number>();
  const order: string[] = [];
  let members: Record<string, Uint8Array>;
  let crcs: number[];
  try {
    members = unzipSync(data, {
      filter: (file) => {
        listed += 1;
        if (listed > maxZipMembers) {
          throw new Error(`it lists more than ${maxZipMembers} members`);
        }
        order.push(file.name);
        if (!wanted(file.name)) return false;
        sizes.set(file.name, file.originalSize);
        declared += file.originalSize;
        if (declared > maxRequestBytes) tooLarge = true;
        return !tooLarge;
      },
    });
    crcs = centralCRCs(data, order.length);
  } catch (e) {
    return { ok: false, message: `This file could not be read as a .zip: ${e instanceof Error ? e.message : String(e)}` };
  }
  if (tooLarge) {
    return {
      ok: false,
      message: `The project files in this .zip come to more than the ${maxRequestBytes >> 20} MiB the server accepts in one request. Plan it with alder project plan.`,
    };
  }

  // fflate inflates a member into a buffer of its declared size and neither
  // grows it nor checks the CRC, so a damaged or misdeclared member comes
  // back cut short -- to exactly the size declared, when it is deflated. One
  // that is not the size the zip says, or whose CRC-32 is not the one its
  // directory records, is refused rather than planned as if it were whole.
  // A name listed twice is the last one listed, as it is in members.
  const crcOf = new Map(order.map((name, i) => [name, crcs[i]]));
  const damaged = Object.entries(members).find(
    ([name, bytes]) => bytes.byteLength !== sizes.get(name) || crc32(bytes) !== crcOf.get(name),
  );
  if (damaged) {
    return {
      ok: false,
      message: `This .zip is damaged: ${safeText(damaged[0])} does not inflate to the size and checksum the zip declares. Unzip it with another tool and open the folder.`,
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
