import { useRef, useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { FileArchive, FolderGit2, Loader2, RefreshCw, SearchCheck, ShieldAlert, Trash2, X } from "lucide-react";
import { api, ApiFailure, unwrap } from "@/lib/api";
import { changeset, useChangeset } from "@/lib/changeset";
import {
  originOf,
  projectRequest,
  selectProjectFiles,
  stageProjectPlan,
  type ProjectFile,
  type ProjectOrigin,
  type ProjectPlan,
  type ProjectStageOutcome,
} from "@/lib/project";
import {
  isZipName,
  readZip,
  walkDroppedFolder,
  type DroppedDirectoryEntry,
  type Picked,
  type SourceResult,
} from "@/lib/project-sources";
import { Button } from "@/components/ui/button";
import { ErrorNote } from "@/components/change-dialog";
import { PlanSummary } from "@/components/plan-summary";
import { safeText } from "@/lib/display";

/**
 * Open project: an Alder project (docs/PROJECT.md) planned against the
 * directory this session is connected to.
 *
 * The person chooses a folder, drops one on the panel, or opens a .zip of
 * one. The page finds alder.yaml (at the top, or the one folder below that
 * has it, or the one the person picks among several), reads it and every
 * *.ldif below it outside dot-directories, and sends them to POST
 * /project/plan, which validates them with the code `alder project validate`
 * runs, plans them as `alder project plan` does and reports the entries the
 * project does not describe. The files are held in this page's memory, never
 * in browser storage, so "Plan again" can resend them; edits made on disk are
 * picked up by choosing the folder again.
 *
 * The environments in alder.yaml are never used here. The plan is the
 * session's, and the heading above it says which directory that is, from
 * the session and never from the files.
 */
export function ProjectPanel({
  onBrowse,
  onReviewChangeset,
}: {
  onBrowse: (dn: string) => void;
  onReviewChangeset: () => void;
}) {
  const input = useRef<HTMLInputElement>(null);
  const zipInput = useRef<HTMLInputElement>(null);
  const [opened, setOpened] = useState<{ folder: string; files: ProjectFile[] } | null>(null);
  const [localError, setLocalError] = useState<string | null>(null);
  // Several projects below the chosen folder and none at its top: the files
  // as they came, and the folders to pick from.
  const [choosing, setChoosing] = useState<{ picked: Picked[]; projects: string[] } | null>(null);
  const [reading, setReading] = useState(false);
  const [dragging, setDragging] = useState(false);
  const [staging, setStaging] = useState<ProjectStageOutcome | null>(null);
  const staged = useChangeset();

  const plan = useMutation<ProjectPlan, ApiFailure, ProjectFile[]>({
    mutationFn: async (files) => {
      const request = projectRequest(files);
      if (request.tooLarge) throw new ApiFailure(413, { error: "bad_request", message: request.tooLarge });
      return unwrap(await api.POST("/project/plan", { body: request.body }));
    },
  });

  const close = () => {
    setOpened(null);
    setLocalError(null);
    setChoosing(null);
    setStaging(null);
    plan.reset();
  };

  // Every way in ends here: the picker's list, a dropped folder's, a zip's.
  const open = async (picked: Picked[], root?: string) => {
    close();
    const selection = selectProjectFiles(picked, root);
    if (!selection.ok) {
      setLocalError(selection.message);
      if (selection.projects?.length) setChoosing({ picked, projects: selection.projects });
      return;
    }
    setReading(true);
    try {
      const files = await Promise.all(
        selection.send.map(async ({ path, file }) => ({ path, content: await file.text() })),
      );
      setOpened({ folder: selection.folder, files });
      plan.mutate(files);
    } catch (e) {
      setLocalError(`The folder could not be read: ${e instanceof Error ? e.message : String(e)}`);
    } finally {
      setReading(false);
    }
  };

  // A dropped folder or a .zip is read before anything is selected; a
  // failure there is said the same way as a folder without a project.
  const openSource = async (source: Promise<SourceResult>) => {
    close();
    setReading(true);
    let result: SourceResult;
    try {
      result = await source;
    } catch (e) {
      result = { ok: false, message: `The files could not be read: ${e instanceof Error ? e.message : String(e)}` };
    } finally {
      setReading(false);
    }
    if (!result.ok) {
      setLocalError(result.message);
      return;
    }
    await open(result.picked);
  };

  const openZip = (file: File) =>
    void openSource(file.arrayBuffer().then((buffer) => readZip(file.name, new Uint8Array(buffer))));

  const onDrop = (e: React.DragEvent) => {
    e.preventDefault();
    setDragging(false);
    if (reading || plan.isPending) return;
    // Entries must be taken while the drop event is being handled: the
    // browser empties the list as soon as it returns.
    const entries = Array.from(e.dataTransfer.items)
      .map((item) => item.webkitGetAsEntry?.() ?? null)
      .filter((entry) => entry !== null);
    const files = Array.from(e.dataTransfer.files);
    if (entries.length === 1 && entries[0]!.isDirectory) {
      void openSource(walkDroppedFolder(entries[0] as unknown as DroppedDirectoryEntry));
    } else if (files.length === 1 && isZipName(files[0]!.name)) {
      openZip(files[0]!);
    } else {
      close();
      setLocalError("Drop one folder holding an Alder project, or one .zip of it.");
    }
  };

  const result = plan.data ?? null;

  return (
    <section
      className={`rounded-lg border p-3 ${dragging ? "border-primary bg-muted/40" : ""}`}
      onDragOver={(e) => {
        e.preventDefault();
        setDragging(true);
      }}
      onDragLeave={(e) => {
        if (!e.currentTarget.contains(e.relatedTarget as Node | null)) setDragging(false);
      }}
      onDrop={onDrop}
    >
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="min-w-0">
          <h3 className="text-sm font-medium">Open project</h3>
          <p className="text-xs text-muted-foreground">
            Choose or drop the folder holding an Alder project&apos;s <code className="font-mono">alder.yaml</code>,
            or a .zip of it. It is checked and planned against this session&apos;s directory, the way{" "}
            <code className="font-mono">alder project plan</code> does; nothing is applied, and nothing is kept.
          </p>
        </div>
        <div className="flex shrink-0 items-center gap-1.5">
          {opened ? (
            <Button
              variant="outline"
              size="sm"
              disabled={plan.isPending}
              title="Send the same files again"
              onClick={() => {
                setStaging(null);
                plan.mutate(opened.files);
              }}
            >
              {plan.isPending ? <Loader2 className="animate-spin" /> : <RefreshCw />}
              Plan again
            </Button>
          ) : null}
          <Button
            variant="outline"
            size="sm"
            disabled={reading || plan.isPending}
            onClick={() => input.current?.click()}
          >
            {reading ? <Loader2 className="animate-spin" /> : <FolderGit2 />}
            {opened ? "Choose again" : "Open project"}
          </Button>
          <Button
            variant="ghost"
            size="sm"
            disabled={reading || plan.isPending}
            title="Open a project from a .zip of its folder"
            onClick={() => zipInput.current?.click()}
          >
            <FileArchive />
            .zip
          </Button>
          {opened || localError ? (
            <Button variant="ghost" size="icon" title="Close" onClick={close}>
              <X />
            </Button>
          ) : null}
        </div>
        <input
          ref={input}
          type="file"
          className="hidden"
          multiple
          // A folder, not a file: the attribute React has no name for.
          {...{ webkitdirectory: "", directory: "" }}
          onChange={(e) => {
            const list = e.target.files;
            if (list && list.length > 0) {
              void open(
                Array.from(list).map((file) => ({ relativePath: file.webkitRelativePath || file.name, file })),
              );
            }
            e.target.value = "";
          }}
        />
        <input
          ref={zipInput}
          type="file"
          className="hidden"
          accept=".zip,application/zip"
          onChange={(e) => {
            const file = e.target.files?.[0];
            if (file) openZip(file);
            e.target.value = "";
          }}
        />
      </div>

      {localError ? (
        // Several projects to pick from is a question, not a failure.
        <p className={`mt-2 text-sm ${choosing ? "text-muted-foreground" : "text-destructive"}`}>
          {safeText(localError)}
        </p>
      ) : null}

      {choosing ? (
        <ProjectChoice projects={choosing.projects} onChoose={(root) => void open(choosing.picked, root)} />
      ) : null}

      {plan.isPending && !result ? (
        <div className="mt-3 flex items-center gap-2 text-sm text-muted-foreground">
          <Loader2 className="size-4 animate-spin" />
          Checking and planning the project…
        </div>
      ) : null}

      {plan.error ? (
        <div className="mt-3">
          <ProjectFailure error={plan.error} />
        </div>
      ) : null}

      {result && opened ? (
        <div className="mt-3 space-y-3">
          <ProjectResult folder={opened.folder} result={result} onBrowse={onBrowse} />
          <div className="flex flex-wrap items-center gap-2">
            <Button
              size="sm"
              onClick={() => setStaging(stageProjectPlan(originOf(opened.folder, result), result.plan))}
            >
              <SearchCheck />
              Stage for review
            </Button>
            <span className="text-xs text-muted-foreground">
              The changeset plans it again and applies it with baselines, as it applies everything. Nothing
              unmanaged is staged.
            </span>
          </div>
          {staging ? (
            <StageNotice
              outcome={staging}
              stagedNow={staged.length}
              onClear={() => {
                changeset.clear();
                setStaging(null);
              }}
              onReview={onReviewChangeset}
            />
          ) : null}
        </div>
      ) : null}
    </section>
  );
}

/** The projects below a chosen folder, when it has several and none at its top. */
export function ProjectChoice({ projects, onChoose }: { projects: string[]; onChoose: (root: string) => void }) {
  return (
    <div className="mt-2 flex flex-wrap items-center gap-1.5" role="group" aria-label="Projects in this folder">
      {projects.map((root) => (
        <Button key={root} variant="outline" size="sm" className="font-mono" onClick={() => onChoose(root)}>
          <FolderGit2 />
          {safeText(`${root}/`)}
        </Button>
      ))}
    </div>
  );
}

/** A refusal, with every problem when the project is not valid. */
export function ProjectFailure({ error }: { error: ApiFailure }) {
  if (error.code !== "project_invalid" || !error.problems?.length) {
    return <ErrorNote title="This project could not be planned" error={error} />;
  }
  return (
    <div className="rounded-md border border-destructive/40 bg-destructive/8 p-3">
      <div className="flex items-center gap-1.5 text-sm font-medium text-destructive">
        <ShieldAlert className="size-4" />
        This project is not valid, so nothing was planned
      </div>
      <p className="mt-1 text-xs text-muted-foreground">
        The same problems <code className="font-mono">alder project validate</code> reports, every one of them.
      </p>
      <ul className="mt-2 space-y-0.5 font-mono text-xs">
        {error.problems.map((p, i) => (
          <li key={i}>
            {p.file ? (
              <span className="text-muted-foreground">
                {safeText(p.file)}
                {p.line ? `:${p.line}` : ""}:{" "}
              </span>
            ) : null}
            {safeText(p.message)}
          </li>
        ))}
      </ul>
    </div>
  );
}

/**
 * The line above a project's plan, here and over the changeset it is staged
 * into: which project, then which directory. The directory is the session's,
 * never a value from alder.yaml.
 */
export function ProjectHeading({ origin }: { origin: ProjectOrigin }) {
  const envs = origin.environments;
  return (
    <div className="space-y-0.5 rounded-md border bg-muted/40 px-3 py-2 text-sm">
      <div>
        <span className="font-medium">
          Project <span className="font-dn">{safeText(origin.folder)}/</span>
        </span>
        <span className="text-muted-foreground">
          {" "}
          — {origin.subtrees} subtree{origin.subtrees === 1 ? "" : "s"}, {origin.entries}{" "}
          {origin.entries === 1 ? "entry" : "entries"}, from alder.yaml
        </span>
      </div>
      <div>
        <span className="font-medium">
          Planned against <span className="font-dn">{safeText(origin.host)}</span>
          {origin.bindDn ? (
            <>
              {" "}
              as <span className="font-dn">{safeText(origin.bindDn)}</span>
            </>
          ) : (
            " anonymously"
          )}
        </span>
        <span className="text-muted-foreground">
          {" "}
          — this session&apos;s directory. The environments in alder.yaml
          {envs.length ? ` (${envs.map(safeText).join(", ")})` : ""} are not used here.
        </span>
      </div>
    </div>
  );
}

export function ProjectResult({
  folder,
  result,
  onBrowse,
}: {
  folder: string;
  result: ProjectPlan;
  onBrowse: (dn: string) => void;
}) {
  return (
    <div className="space-y-3">
      <ProjectHeading origin={originOf(folder, result)} />
      <PlanSummary plan={result.plan} />
      <UnmanagedList unmanaged={result.unmanaged} onBrowse={onBrowse} />
    </div>
  );
}

/**
 * What the managed subtrees hold that no file describes. Reported, never
 * acted on: a project is not a deleter, so there is no delete button here.
 */
export function UnmanagedList({
  unmanaged,
  onBrowse,
}: {
  unmanaged: ProjectPlan["unmanaged"];
  onBrowse: (dn: string) => void;
}) {
  const total = unmanaged.reduce((n, u) => n + u.entries.length, 0);
  const unreadable = unmanaged.filter((u) => u.error).length;
  return (
    <div className="rounded-md border p-3 text-sm">
      <div className="font-medium">
        {total === 0
          ? unreadable === 0
            ? "Unmanaged: none. Every entry in the managed subtrees is one the project describes."
            : "Unmanaged: none in the subtrees that could be read."
          : `Unmanaged: ${total} ${total === 1 ? "entry" : "entries"} the directory holds that no project file describes. Reported, never deleted.`}
      </div>
      <ul className="mt-2 space-y-2">
        {unmanaged.map((u) => (
          <li key={u.base}>
            <div className="font-dn text-xs text-muted-foreground">{safeText(u.base)}</div>
            {u.error ? (
              <p className="text-xs text-warning-tint-foreground">Unreadable: {safeText(u.error)}</p>
            ) : u.entries.length === 0 ? (
              <p className="text-xs text-muted-foreground">Nothing unmanaged.</p>
            ) : (
              <ul className="mt-0.5 max-h-48 space-y-0.5 overflow-y-auto">
                {u.entries.map((dn) => (
                  <li key={dn}>
                    <button
                      className="truncate text-left font-dn text-xs hover:underline"
                      title={`Browse to ${safeText(dn)}`}
                      onClick={() => onBrowse(dn)}
                    >
                      {safeText(dn)}
                    </button>
                  </li>
                ))}
              </ul>
            )}
          </li>
        ))}
      </ul>
    </div>
  );
}

function StageNotice({
  outcome,
  stagedNow,
  onClear,
  onReview,
}: {
  outcome: ProjectStageOutcome;
  stagedNow: number;
  onClear: () => void;
  onReview: () => void;
}) {
  return (
    <div
      className={`flex flex-wrap items-center gap-2 rounded-md border px-3 py-2 text-sm ${
        outcome.ok ? "border-border bg-accent/40" : "border-warning/40 bg-warning/10 text-warning-tint-foreground"
      }`}
    >
      <span>{outcome.message}</span>
      {outcome.stagedAlready && stagedNow > 0 ? (
        <Button size="sm" variant="outline" onClick={onClear}>
          <Trash2 />
          Clear changeset ({stagedNow})
        </Button>
      ) : null}
      {outcome.ok || outcome.stagedAlready ? (
        <Button size="sm" variant="outline" onClick={onReview}>
          {outcome.ok ? "Review the changeset" : "Open the changeset"}
        </Button>
      ) : null}
    </div>
  );
}
