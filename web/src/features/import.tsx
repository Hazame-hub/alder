import { useRef, useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { CheckCircle2, FileUp, Loader2, Upload } from "lucide-react";
import { api, ApiFailure, unwrap } from "@/lib/api";
import type { ChangeRequest, ImportResult } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Textarea } from "@/components/ui/input";
import { ChangeDialog, ErrorNote } from "@/components/change-dialog";
import { LdifBlock } from "@/components/ldif-block";

/**
 * ImportPanel parses an LDIF document and lets the user apply its records one
 * at a time.
 *
 * One at a time is the deliberate part. A directory has no transactions across
 * entries, so a bulk apply that fails halfway leaves the directory in a state
 * nobody chose. Every record goes through the same preview and confirm as any
 * other change, and the ones already applied are marked so a partial run can be
 * resumed rather than restarted.
 */
export function ImportPanel() {
  const [text, setText] = useState("");
  const [applied, setApplied] = useState<Set<number>>(new Set());
  const [pending, setPending] = useState<{ index: number; change: ChangeRequest } | null>(
    null,
  );
  const fileInput = useRef<HTMLInputElement>(null);

  const parse = useMutation<ImportResult, ApiFailure>({
    mutationFn: async () => unwrap(await api.POST("/import/ldif", { body: { ldif: text } })),
    onSuccess: () => setApplied(new Set()),
  });

  const loadFile = (file: File) => {
    const reader = new FileReader();
    reader.onload = () => {
      setText(String(reader.result ?? ""));
      parse.reset();
    };
    reader.readAsText(file);
  };

  return (
    <div className="mx-auto max-w-4xl space-y-4 p-6">
      <header>
        <h2 className="text-lg font-semibold">Import LDIF</h2>
        <p className="mt-1 text-sm text-muted-foreground">
          Paste or drop an LDIF document. It is parsed and shown back to you as
          individual changes; nothing is applied until you confirm each one.
        </p>
      </header>

      <div
        onDragOver={(e) => e.preventDefault()}
        onDrop={(e) => {
          e.preventDefault();
          const file = e.dataTransfer.files[0];
          if (file) loadFile(file);
        }}
        className="rounded-lg border border-dashed border-border p-1"
      >
        <Textarea
          value={text}
          rows={12}
          spellCheck={false}
          className="border-0 font-mono text-[12.5px] shadow-none focus-visible:ring-0"
          placeholder={
            "dn: cn=example,ou=people,dc=example,dc=test\n" +
            "changetype: add\n" +
            "objectClass: top\n" +
            "objectClass: person\n" +
            "cn: example\n" +
            "sn: Example\n"
          }
          onChange={(e) => {
            setText(e.target.value);
            parse.reset();
          }}
        />
      </div>

      <div className="flex flex-wrap items-center gap-2">
        <Button onClick={() => parse.mutate()} disabled={!text.trim() || parse.isPending}>
          {parse.isPending ? <Loader2 className="animate-spin" /> : <FileUp />}
          Parse
        </Button>
        <Button variant="outline" onClick={() => fileInput.current?.click()}>
          <Upload />
          Choose a file
        </Button>
        <input
          ref={fileInput}
          type="file"
          accept=".ldif,.txt,text/plain"
          className="hidden"
          onChange={(e) => {
            const file = e.target.files?.[0];
            if (file) loadFile(file);
          }}
        />
        <p className="text-xs text-muted-foreground">
          A record with no <code className="font-mono">changetype</code> is
          treated as an add, which is what an exported entry is.
        </p>
      </div>

      {parse.isError ? <ErrorNote title="The LDIF could not be used" error={parse.error} /> : null}

      {parse.data ? (
        <section className="space-y-3">
          <div className="flex items-center gap-2">
            <h3 className="font-medium">
              {parse.data.changes.length} change
              {parse.data.changes.length === 1 ? "" : "s"}
            </h3>
            {applied.size > 0 ? (
              <Badge variant="success">{applied.size} applied</Badge>
            ) : null}
          </div>

          {parse.data.changes.map((change, i) => {
            const request = parse.data?.requests?.[i];
            const done = applied.has(i);
            return (
              <div
                key={i}
                className={cnCard(done)}
              >
                <div className="flex flex-wrap items-center justify-between gap-2 border-b border-border px-3 py-2">
                  <div className="flex items-center gap-2">
                    {done ? <CheckCircle2 className="size-4 text-success" /> : null}
                    <span className="font-dn text-sm">{change.summary}</span>
                  </div>
                  <Button
                    size="sm"
                    variant={done ? "outline" : "default"}
                    disabled={!request || done}
                    onClick={() => request && setPending({ index: i, change: request })}
                  >
                    {done ? "Applied" : "Review and apply"}
                  </Button>
                </div>
                {change.warnings?.length ? (
                  <ul className="ml-5 list-disc space-y-0.5 px-3 py-2 text-xs text-warning-foreground">
                    {change.warnings.map((w) => (
                      <li key={w}>{w}</li>
                    ))}
                  </ul>
                ) : null}
                <div className="p-3">
                  <LdifBlock text={change.ldif} />
                </div>
              </div>
            );
          })}
        </section>
      ) : null}

      <ChangeDialog
        change={pending?.change ?? null}
        open={pending !== null}
        onOpenChange={(open) => !open && setPending(null)}
        onApplied={() => {
          if (pending) {
            const index = pending.index;
            setApplied((prev) => new Set(prev).add(index));
          }
          setPending(null);
        }}
      />
    </div>
  );
}

function cnCard(done: boolean) {
  return [
    "overflow-hidden rounded-lg border",
    done ? "border-success/40 bg-success/5" : "border-border bg-card",
  ].join(" ");
}
