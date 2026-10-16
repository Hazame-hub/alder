import { describe, expect, it } from "vitest";
import { strToU8, zipSync, type Zippable } from "fflate";
import { selectProjectFiles } from "./project";
import {
  maxWalkedEntries,
  maxZipBytes,
  maxZipMembers,
  readZip,
  zipSizeRefusal,
  walkDroppedFolder,
  type DroppedDirectoryEntry,
  type DroppedEntry,
  type Picked,
} from "./project-sources";

// A dropped folder and a .zip end as the folder picker's list, so what is
// sent is decided in one place. What is pinned here is that neither reads
// more than the picker would send, and that a zip names no place outside
// itself.

async function texts(picked: Picked[]): Promise<Record<string, string>> {
  const out: Record<string, string> = {};
  for (const p of picked) out[p.relativePath] = await p.file.text();
  return out;
}

function zip(files: Record<string, string>): Uint8Array {
  const z: Zippable = {};
  for (const [name, text] of Object.entries(files)) z[name] = strToU8(text);
  return zipSync(z);
}

describe("a project from a .zip", () => {
  it("keeps the folder a zipped folder has at its top, and inflates only project files", async () => {
    const r = readZip(
      "anything.zip",
      zip({
        "directory/alder.yaml": "version: 1\n",
        "directory/people/a.ldif": "dn: uid=a\n",
        "directory/README.md": "not read",
        "directory/.git/config": "not read",
        "directory/.drafts/x.ldif": "not read",
      }),
    );
    expect(r.ok).toBe(true);
    if (!r.ok) return;
    expect(await texts(r.picked)).toEqual({
      "directory/alder.yaml": "version: 1\n",
      "directory/people/a.ldif": "dn: uid=a\n",
    });
    const s = selectProjectFiles(r.picked);
    expect(s.ok && s.folder).toBe("directory");
  });

  it("lists a zip of a folder's contents under the zip's own name", async () => {
    const r = readZip("Directory.ZIP", zip({ "alder.yaml": "version: 1\n", "groups.ldif": "dn: cn=g\n" }));
    expect(r.ok).toBe(true);
    if (!r.ok) return;
    expect(Object.keys(await texts(r.picked)).sort()).toEqual(["Directory/alder.yaml", "Directory/groups.ldif"]);
  });

  it("never lists a member whose path leaves the zip", async () => {
    const r = readZip(
      "p.zip",
      zip({
        "p/alder.yaml": "version: 1\n",
        "p/../../etc/evil.ldif": "x",
        "/abs.ldif": "x",
        "C:/win.ldif": "x",
      }),
    );
    expect(r.ok).toBe(true);
    if (!r.ok) return;
    expect(r.picked.map((p) => p.relativePath)).toEqual(["p/alder.yaml"]);
  });

  it("says so for a file that is not a zip", () => {
    const r = readZip("x.zip", strToU8("not a zip at all"));
    expect(r.ok).toBe(false);
    if (r.ok) return;
    expect(r.message).toContain("could not be read as a .zip");
  });

  it("refuses a .zip too large to open by its size alone, before it is read", () => {
    expect(zipSizeRefusal(maxZipBytes)).toBeNull();
    expect(zipSizeRefusal(3 * 2 ** 30)).toContain("3072.0 MiB");
  });

  it("refuses a member that does not inflate to its declared size, rather than planning it cut short", () => {
    const data = zipSync({ "p/alder.yaml": strToU8("version: 1\n"), "p/a.ldif": strToU8("dn: dc=example\n") }, { level: 0 });
    // The central directory's uncompressed size of p/a.ldif, made smaller
    // than what the member holds.
    const view = new DataView(data.buffer, data.byteOffset, data.byteLength);
    const name = strToU8("p/a.ldif");
    let patched = false;
    for (let i = 0; i + 46 < data.length; i++) {
      if (view.getUint32(i, true) !== 0x02014b50) continue;
      const n = view.getUint16(i + 28, true);
      if (n === name.length && data.subarray(i + 46, i + 46 + n).every((b, j) => b === name[j])) {
        view.setUint32(i + 24, 4, true);
        patched = true;
      }
    }
    expect(patched).toBe(true);
    const r = readZip("p.zip", data);
    expect(r.ok).toBe(false);
    if (r.ok) return;
    expect(r.message).toContain("p/a.ldif does not inflate to the size the zip declares");
  });

  it("stops listing a tiny .zip that claims more members than a project could hold", () => {
    // A zip64 end record claiming far more entries than the file holds, which
    // fflate visits one by one.
    const out = new Uint8Array(56 + 20 + 22);
    const v = new DataView(out.buffer);
    v.setUint32(0, 0x06064b50, true);
    v.setBigUint64(4, 44n, true);
    v.setBigUint64(24, 1_000_000_000n, true);
    v.setBigUint64(32, 1_000_000_000n, true);
    v.setUint32(56, 0x07064b50, true);
    v.setBigUint64(56 + 8, 0n, true);
    v.setUint32(76, 0x06054b50, true);
    v.setUint16(76 + 8, 0xffff, true);
    v.setUint16(76 + 10, 0xffff, true);
    v.setUint32(76 + 16, 0xffffffff, true);
    const started = Date.now();
    const r = readZip("tiny.zip", out);
    expect(Date.now() - started).toBeLessThan(5000);
    expect(r.ok).toBe(false);
    if (r.ok) return;
    expect(r.message).toContain(`more than ${maxZipMembers} members`);
  });

  it("refuses project files larger than one request, before inflating past it", () => {
    const big = "x".repeat(9 << 20);
    const r = readZip("p.zip", zip({ "p/alder.yaml": "version: 1\n", "p/a.ldif": big, "p/b.ldif": big }));
    expect(r.ok).toBe(false);
    if (r.ok) return;
    expect(r.message).toContain("alder project plan");
  });
});

// A folder as the File and Directory Entries API hands one over: batches from
// readEntries, an empty batch at the end, and a count of what was opened.
function folder(tree: Record<string, unknown>, opened: string[], path = ""): DroppedDirectoryEntry {
  const entries: DroppedEntry[] = Object.entries(tree).map(([name, value]) => {
    const fullPath = `${path}/${name}`;
    if (typeof value === "string") {
      return {
        isFile: true,
        isDirectory: false,
        name,
        fullPath,
        file: (ok: (f: File) => void) => {
          opened.push(fullPath);
          ok(new File([value], name));
        },
      };
    }
    return folder(value as Record<string, unknown>, opened, fullPath);
  });
  const name = path.split("/").pop() ?? "";
  return {
    isFile: false,
    isDirectory: true,
    name,
    fullPath: path,
    createReader: () => {
      // Two batches, then the empty one.
      const batches = [entries.slice(0, 1), entries.slice(1), []];
      return { readEntries: (ok: (e: DroppedEntry[]) => void) => ok(batches.shift() ?? []) };
    },
  };
}

describe("a dropped folder", () => {
  it("lists the project files by path from the folder, and opens nothing else", async () => {
    const opened: string[] = [];
    const dir = folder(
      {
        "alder.yaml": "version: 1\n",
        "README.md": "no",
        people: { "a.ldif": "dn: uid=a\n", "b.ldif": "dn: uid=b\n" },
        ".git": { "x.ldif": "no" },
      },
      opened,
      "/directory",
    );
    const r = await walkDroppedFolder(dir);
    expect(r.ok).toBe(true);
    if (!r.ok) return;
    expect(await texts(r.picked)).toEqual({
      "directory/alder.yaml": "version: 1\n",
      "directory/people/a.ldif": "dn: uid=a\n",
      "directory/people/b.ldif": "dn: uid=b\n",
    });
    expect(opened.sort()).toEqual(["/directory/alder.yaml", "/directory/people/a.ldif", "/directory/people/b.ldif"]);
  });

  it("stops walking a folder far larger than a project", async () => {
    const tree: Record<string, string> = {};
    for (let i = 0; i <= maxWalkedEntries; i++) tree[`f${i}.txt`] = "";
    const r = await walkDroppedFolder(folder(tree, [], "/big"));
    expect(r.ok).toBe(false);
    if (r.ok) return;
    expect(r.message).toContain("Drop the folder that holds alder.yaml");
  });
});
