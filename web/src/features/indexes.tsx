import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { FileSearch, Gauge, Loader2, Plus, Trash2 } from "lucide-react";
import { api, ApiFailure, unwrap } from "@/lib/api";
import type { ChangeRequest, SessionInfo } from "@/lib/api";
import { inConfigTree } from "@/lib/config-tree";
import type { components } from "@/lib/api.gen";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Checkbox } from "@/components/ui";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { ChangeDialog, ErrorNote } from "@/components/change-dialog";
import { safeText } from "@/lib/display";

type IndexReport = components["schemas"]["IndexReport"];
type IndexBackend = components["schemas"]["IndexBackend"];
type IndexEntry = components["schemas"]["IndexEntry"];
type IndexAdvice = components["schemas"]["IndexAdvice"];
type IndexSuggestion = components["schemas"]["IndexSuggestion"];

/**
 * What this backend indexes, and the two things you can do about it.
 *
 * Alder could create and remove an index from 1.23, and only from inside a
 * configuration comparison -- a row appeared where this server differed from
 * a snapshot of one that already had the index you wanted. So the feature was
 * really "copy an index another server has". The ordinary reason to add one,
 * a slow search you have just diagnosed, had no path at all: an audit looked
 * on the database entry, in the editor and on the directory screen, found
 * nothing, and concluded you were back in ldapmodify.
 *
 * This is the same derivation behind a door you can find. Nothing here writes
 * anything: both buttons produce the change the server derived and hand it to
 * the review dialog every other write goes through.
 */

/** The index types both servers understand, and what each is for. */
const TYPES: { id: string; label: string; why: string }[] = [
  { id: "eq", label: "eq", why: "Exact matches — (mail=a@b). The one almost every search needs." },
  { id: "sub", label: "sub", why: "Substring matches — (cn=*smith*). Large, and what a slow wildcard search wants." },
  { id: "pres", label: "pres", why: "Presence — (mail=*). Worth having only where most entries lack the attribute." },
];

export function IndexesButton({ dn, readOnly }: { dn: string; readOnly: boolean }) {
  const [open, setOpen] = useState(false);

  // Reading this captures the whole configuration tree, so it is asked for
  // only where the answer could be yes: an entry inside the configuration
  // tree. The first version of this said so in a comment and did not do it,
  // which is worse than not saying it -- every entry view, including every
  // ordinary person, paid for a configuration capture (115 to 175ms against
  // the harness 389 DS), and on a session with no configuration identity
  // every entry view also put a 400 in the browser console. That console is
  // where 1.29 put failures so the next person finds one in ten seconds, and
  // filling it with expected refusals is how it stops working.
  const queryClient = useQueryClient();
  const session = queryClient.getQueryData<SessionInfo>(["session"]);
  const report = useQuery<IndexReport, ApiFailure>({
    queryKey: ["config-indexes"],
    enabled: inConfigTree(session, dn),
    retry: false,
    staleTime: 60 * 1000,
    queryFn: async () => unwrap(await api.GET("/config/indexes")),
  });

  const backend = backendAt(report.data, dn);
  if (!backend) return null;
  return (
    <>
      <Button
        variant="ghost"
        size="sm"
        onClick={() => setOpen(true)}
        title="What this backend indexes, and what can be added or taken away"
      >
        <Gauge />
        Indexes
        <Badge variant="secondary">{backend.indexes.length}</Badge>
      </Button>
      <IndexesDialog backend={backend} open={open} onOpenChange={setOpen} readOnly={readOnly} />
    </>
  );
}

/**
 * The backend this entry is, if it is one.
 *
 * Matched on the DN the report gives rather than on the shape of the DN,
 * because the two servers keep their backends in entirely different places
 * and neither is guessable from the other.
 */
export function backendAt(report: IndexReport | undefined, dn: string): IndexBackend | undefined {
  const folded = dn.trim().toLowerCase();
  return report?.backends.find((b) => b.dn.trim().toLowerCase() === folded);
}

function IndexesDialog({
  backend,
  open,
  onOpenChange,
  readOnly,
}: {
  backend: IndexBackend;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  readOnly: boolean;
}) {
  const queryClient = useQueryClient();
  const [change, setChange] = useState<ChangeRequest | null>(null);
  const [title, setTitle] = useState("Change an index");

  const applied = () => {
    // The list this dialog is showing is what just changed.
    void queryClient.invalidateQueries({ queryKey: ["config-indexes"] });
    void queryClient.invalidateQueries({ queryKey: ["entry"] });
  };

  return (
    <>
      <Dialog open={open && change === null} onOpenChange={onOpenChange}>
        <DialogContent className="screen-shell max-w-3xl">
          <DialogHeader className="border-b border-border pb-4">
            <p className="screen-kicker">What the searches land on</p>
            <DialogTitle className="text-xl tracking-tight">Indexes</DialogTitle>
            <DialogDescription>
              <span className="font-dn">{safeText(backend.name)}</span>
            </DialogDescription>
          </DialogHeader>

          <div className="space-y-3">
            <div className="rounded-md border">
              <div className="border-b border-border px-3 py-2 text-sm font-medium">
                {backend.indexes.length === 1
                  ? "1 attribute indexed"
                  : `${backend.indexes.length} attributes indexed`}
              </div>
              {backend.indexes.length === 0 ? (
                <p className="px-3 py-2 text-xs text-muted-foreground">
                  This backend indexes nothing, so every search on it reads every entry.
                </p>
              ) : (
                <ul className="divide-y divide-border">
                  {backend.indexes.map((index) => (
                    <IndexRow
                      key={index.id}
                      index={index}
                      readOnly={readOnly}
                      onRemove={(request) => {
                        setTitle(`Remove the index on ${index.attribute}`);
                        setChange(request);
                      }}
                    />
                  ))}
                </ul>
              )}
            </div>

            <FindMissingIndexes
              backend={backend}
              readOnly={readOnly}
              onDerived={(request, attribute) => {
                setTitle(`Index ${attribute}`);
                setChange(request);
              }}
            />

            {readOnly ? null : (
              <AddIndex
                backend={backend}
                onDerived={(request, attribute) => {
                  setTitle(`Index ${attribute}`);
                  setChange(request);
                }}
              />
            )}

            <p className="rounded-md border border-warning/40 bg-warning/10 p-3 text-xs text-warning-tint-foreground">
              An index changes how the server searches, not what it holds. Adding one on a
              populated directory makes the server build it, which takes as long as it takes;
              removing one makes every search that relied on it read more entries.
            </p>
          </div>

          <DialogFooter>
            <Button variant="outline" onClick={() => onOpenChange(false)}>
              Close
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <ChangeDialog
        change={change}
        open={change !== null}
        onOpenChange={(next) => !next && setChange(null)}
        title={title}
        onApplied={applied}
      />
    </>
  );
}

function IndexRow({
  index,
  readOnly,
  onRemove,
}: {
  index: IndexEntry;
  readOnly: boolean;
  onRemove: (change: ChangeRequest) => void;
}) {
  return (
    <li className="flex flex-wrap items-center gap-2 px-3 py-2 text-sm">
      <span className="font-dn font-medium">{safeText(index.attribute)}</span>
      {(index.types ?? []).map((t) => (
        <Badge key={t} variant="outline">
          {safeText(t)}
        </Badge>
      ))}
      {(index.types ?? []).length === 0 ? (
        <span className="text-xs text-muted-foreground">the server records no type for it</span>
      ) : null}
      <span className="ml-auto flex items-center gap-2">
        {index.remove && !readOnly ? (
          <Button
            variant="ghost"
            size="sm"
            className="text-destructive hover:bg-destructive/10"
            onClick={() => onRemove(index.remove as ChangeRequest)}
          >
            <Trash2 />
            Remove
          </Button>
        ) : null}
        {/*
          The reason in words, not the code. `index_value_shared` on its own
          is a code golf answer to "why can I not remove this", and an audit
          read exactly that off the comparison screen.
        */}
        {index.blocked ? (
          <span className="max-w-md text-right text-xs text-muted-foreground">
            {safeText(index.blockedDetail ?? index.blocked)}
          </span>
        ) : null}
      </span>
    </li>
  );
}

function AddIndex({
  backend,
  onDerived,
}: {
  backend: IndexBackend;
  onDerived: (change: ChangeRequest, attribute: string) => void;
}) {
  const [attribute, setAttribute] = useState("");
  const [types, setTypes] = useState<string[]>(["eq"]);
  const [exists, setExists] = useState(false);
  const [blocked, setBlocked] = useState<string | null>(null);

  const derive = useMutation({
    mutationKey: ["config-index-candidate"],
    mutationFn: async () =>
      unwrap(
        await api.POST("/config/indexes/candidate", {
          body: { backend: backend.name, attribute: attribute.trim(), types },
        }),
      ),
    onSuccess: (candidate) => {
      setExists(candidate.exists === true);
      setBlocked(candidate.blocked ? (candidate.blockedDetail ?? candidate.blocked) : null);
      if (candidate.change) onDerived(candidate.change, attribute.trim());
    },
  });

  const toggle = (id: string) =>
    setTypes((current) =>
      current.includes(id) ? current.filter((t) => t !== id) : [...current, id],
    );

  return (
    <div className="space-y-2 rounded-md border p-3">
      <div className="text-sm font-medium">Index another attribute</div>
      <div className="flex flex-wrap items-end gap-3">
        <div className="space-y-1.5">
          <Label htmlFor="index-attr">Attribute</Label>
          <Input
            id="index-attr"
            className="font-dn w-56"
            value={attribute}
            placeholder="telephoneNumber"
            onChange={(e) => {
              setAttribute(e.target.value);
              setExists(false);
              setBlocked(null);
            }}
          />
        </div>
        <div className="space-y-1.5">
          <Label>What it covers</Label>
          <div className="flex items-center gap-3">
            {TYPES.map((t) => (
              <label key={t.id} className="flex items-center gap-1.5 text-sm" title={t.why}>
                <Checkbox
                  checked={types.includes(t.id)}
                  onCheckedChange={() => toggle(t.id)}
                />
                {t.label}
              </label>
            ))}
          </div>
        </div>
        <Button
          disabled={attribute.trim() === "" || derive.isPending}
          onClick={() => derive.mutate()}
        >
          {derive.isPending ? <Loader2 className="animate-spin" /> : <Plus />}
          Review
        </Button>
      </div>
      {/*
        Nothing is written here. The button derives the change the server
        wants -- which is a value on a database entry for OpenLDAP and an
        entry of its own for 389 Directory Server -- and hands it to the
        same review the rest of the product uses.
      */}
      <p className="text-xs text-muted-foreground">
        Nothing is sent until you have read the LDIF and confirmed it. With nothing ticked, the
        index is for equality, which is what both servers make of a request that says nothing.
      </p>
      {exists ? (
        <p className="text-xs text-warning-tint-foreground">
          This backend already indexes that attribute.
        </p>
      ) : null}
      {blocked ? <p className="text-xs text-destructive">{safeText(blocked)}</p> : null}
      {derive.isError ? (
        <ErrorNote title="The change could not be worked out" error={derive.error as ApiFailure} />
      ) : null}
    </div>
  );
}

/**
 * The upload the request body can carry. The server takes 16 MiB, and a log
 * sent as JSON grows a little in escaping; past this the CLI is the answer.
 */
export const MAX_ADVICE_LOG_BYTES = 15 * 1024 * 1024;

/** The suggestions that belong to this backend, in the order the server ranked them. */
export function suggestionsFor(advice: IndexAdvice, backend: IndexBackend): IndexSuggestion[] {
  const name = backend.name.trim().toLowerCase();
  return advice.suggestions.filter((s) => s.backend.trim().toLowerCase() === name);
}

/**
 * Which indexes a server log says the searches were missing (1.41).
 *
 * The log is the operator's: uploaded here, read by the server against the
 * live index list, and dropped when the answer is written. Nothing comes
 * back from it but attribute names and masked filters. A suggestion is
 * staged through the same candidate path as the form below, so it is
 * reviewed like any other change.
 */
function FindMissingIndexes({
  backend,
  readOnly,
  onDerived,
}: {
  backend: IndexBackend;
  readOnly: boolean;
  onDerived: (change: ChangeRequest, attribute: string) => void;
}) {
  const [tooLarge, setTooLarge] = useState(false);
  const advise = useMutation<IndexAdvice, ApiFailure, string>({
    mutationKey: ["config-index-advice"],
    mutationFn: async (log) =>
      unwrap(await api.POST("/config/indexes/advice", { body: { log } })),
  });
  const stage = useMutation({
    mutationKey: ["config-index-candidate"],
    mutationFn: async (s: IndexSuggestion) =>
      unwrap(
        await api.POST("/config/indexes/candidate", {
          body: { backend: backend.name, attribute: s.attribute, types: [s.type] },
        }),
      ),
    onSuccess: (candidate, s) => {
      if (candidate.change) onDerived(candidate.change, s.attribute);
    },
  });

  const read = async (file: File | undefined) => {
    if (!file) return;
    setTooLarge(file.size > MAX_ADVICE_LOG_BYTES);
    if (file.size > MAX_ADVICE_LOG_BYTES) return;
    advise.mutate(await file.text());
  };

  const mine = advise.data ? suggestionsFor(advise.data, backend) : [];
  const elsewhere = advise.data ? advise.data.suggestions.length - mine.length : 0;

  return (
    <div className="space-y-2 rounded-md border p-3">
      <div className="flex flex-wrap items-center gap-2 text-sm font-medium">
        <FileSearch className="size-4 text-muted-foreground" />
        Find missing indexes in a server log
      </div>
      <p className="text-xs text-muted-foreground">
        A 389 Directory Server access log, or an OpenLDAP log written with{" "}
        <code className="font-mono">loglevel stats</code>. It is read for the searches the server
        marked unindexed or that took 100ms or more, and dropped. Only attribute names come back,
        and example filters have their values replaced.
      </p>
      <Input
        type="file"
        aria-label="Server log"
        className="w-auto"
        disabled={advise.isPending}
        onChange={(e) => void read(e.target.files?.[0])}
      />
      {tooLarge ? (
        <p className="text-xs text-warning-tint-foreground">
          That log is larger than the 15 MiB this page can send. Upload an excerpt, or run{" "}
          <code className="font-mono">alder indexes advise</code> where the log is, which reads any
          size and sends it nowhere.
        </p>
      ) : null}
      {advise.isPending ? <Loader2 className="size-4 animate-spin" /> : null}
      {advise.isError ? <ErrorNote title="The log could not be read" error={advise.error} /> : null}
      {advise.data ? (
        <div className="space-y-2">
          <p className="text-xs text-muted-foreground">
            {advise.data.lines} lines, {advise.data.searches} searches, {advise.data.counted} counted
            {advise.data.format ? ` (${advise.data.format})` : " (no line this advisor knows)"}.
            {elsewhere > 0 ? ` ${elsewhere} suggestion(s) are for another backend.` : ""}
          </p>
          {mine.length === 0 ? (
            <p className="text-xs">Nothing to suggest for this backend.</p>
          ) : (
            <ul className="divide-y divide-border rounded-md border">
              {mine.map((s) => (
                <SuggestionRow
                  key={`${s.attribute}/${s.type}`}
                  suggestion={s}
                  canStage={!readOnly && s.status === "candidate"}
                  staging={stage.isPending && stage.variables === s}
                  onStage={() => stage.mutate(s)}
                />
              ))}
            </ul>
          )}
          {mine.length > 0 ? (
            <p className="text-xs text-warning-tint-foreground">{safeText(advise.data.caveat)}</p>
          ) : null}
        </div>
      ) : null}
      {stage.isError ? (
        <ErrorNote title="The change could not be worked out" error={stage.error as ApiFailure} />
      ) : null}
    </div>
  );
}

function SuggestionRow({
  suggestion: s,
  canStage,
  staging,
  onStage,
}: {
  suggestion: IndexSuggestion;
  canStage: boolean;
  staging: boolean;
  onStage: () => void;
}) {
  return (
    <li className="space-y-1 px-3 py-2 text-sm">
      <div className="flex flex-wrap items-center gap-2">
        <span className="font-dn font-medium">{safeText(s.attribute)}</span>
        <Badge variant="outline">{safeText(s.type)}</Badge>
        <span className="text-xs text-muted-foreground">
          {s.searches} searches, {s.totalSeconds.toFixed(2)}s in all
          {s.markedUnindexed > 0 ? `, ${s.markedUnindexed} marked unindexed by the server` : ""}
        </span>
        {canStage ? (
          <Button size="sm" variant="outline" className="ml-auto" onClick={onStage} disabled={staging}>
            {staging ? <Loader2 className="animate-spin" /> : <Plus />}
            Review
          </Button>
        ) : null}
      </div>
      {s.reason ? <p className="text-xs text-muted-foreground">{safeText(s.reason)}</p> : null}
      {s.examples.map((e) => (
        <code key={e} className="block font-mono text-[11px] text-muted-foreground [overflow-wrap:anywhere]">
          {safeText(e)}
        </code>
      ))}
    </li>
  );
}

