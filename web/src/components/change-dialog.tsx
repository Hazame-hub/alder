import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, Loader2, ShieldAlert } from "lucide-react";
import { api, ApiFailure, unwrap } from "@/lib/api";
import type { ApplyResult, ChangeRequest } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/misc";
import { LdifBlock } from "@/components/ldif-block";

/**
 * ChangeDialog is the confirmation step every write goes through.
 *
 * There is no path in the application that applies a change without this
 * dialog: the components that build a ChangeRequest hand it here, and only the
 * button in this footer calls /changes/apply. That is what "no modification
 * reaches the server without showing the exact LDIF first" means in code rather
 * than in a README.
 */
export function ChangeDialog({
  change,
  open,
  onOpenChange,
  onApplied,
  title,
  destructive,
}: {
  change: ChangeRequest | null;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onApplied?: (result: ApplyResult) => void;
  title?: string;
  destructive?: boolean;
}) {
  const queryClient = useQueryClient();

  const preview = useQuery({
    // The preview is rendered by the server from the same ChangeRecord that
    // /changes/apply will act on, so what is shown and what is sent cannot
    // disagree. Rendering the LDIF in the browser instead would reintroduce
    // exactly that gap.
    queryKey: ["preview", change],
    enabled: open && change !== null,
    retry: false,
    queryFn: async () =>
      unwrap(await api.POST("/changes/preview", { body: change as ChangeRequest })),
  });

  const apply = useMutation({
    mutationFn: async () =>
      unwrap(await api.POST("/changes/apply", { body: change as ChangeRequest })),
    onSuccess: (result) => {
      void queryClient.invalidateQueries({ queryKey: ["entry"] });
      void queryClient.invalidateQueries({ queryKey: ["tree"] });
      void queryClient.invalidateQueries({ queryKey: ["search"] });
      onOpenChange(false);
      onApplied?.(result);
    },
  });

  const previewError = preview.error as ApiFailure | null;
  const applyError = apply.error as ApiFailure | null;
  const data = preview.data;

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) apply.reset();
        onOpenChange(next);
      }}
    >
      <DialogContent wide>
        <DialogHeader>
          <DialogTitle>{title ?? "Review this change"}</DialogTitle>
          <DialogDescription>
            {data ? (
              <span className="font-dn">{data.summary}</span>
            ) : (
              "Rendering the change…"
            )}
          </DialogDescription>
        </DialogHeader>

        <div className="min-h-0 flex-1 overflow-y-auto px-5 py-4">
          {preview.isPending ? (
            <div className="flex items-center gap-2 py-8 text-sm text-muted-foreground">
              <Loader2 className="size-4 animate-spin" />
              Rendering…
            </div>
          ) : null}

          {previewError ? (
            <ErrorNote title="This change cannot be rendered" error={previewError} />
          ) : null}

          {data ? (
            <>
              {data.warnings?.length ? (
                <div className="mb-4 rounded-md border border-warning/40 bg-warning/10 p-3">
                  <div className="mb-1.5 flex items-center gap-1.5 text-sm font-medium text-warning-tint-foreground">
                    <AlertTriangle className="size-4" />
                    The schema has something to say about this
                  </div>
                  <ul className="ml-5 list-disc space-y-1 text-sm text-warning-tint-foreground/90">
                    {data.warnings.map((w) => (
                      <li key={w}>{w}</li>
                    ))}
                  </ul>
                  <p className="mt-2 text-xs text-muted-foreground">
                    The directory decides, not Alder. You can still apply this.
                  </p>
                </div>
              ) : null}

              <Tabs defaultValue="ldif">
                <div className="mb-3 flex items-center justify-between gap-3">
                  <TabsList>
                    <TabsTrigger value="ldif">LDIF</TabsTrigger>
                    <TabsTrigger value="ansible">Ansible</TabsTrigger>
                  </TabsList>
                  {data.affectedAttributes?.length ? (
                    <div className="flex flex-wrap items-center gap-1">
                      <span className="text-xs text-muted-foreground">touches</span>
                      {data.affectedAttributes.map((a) => (
                        <Badge key={a} variant="outline" className="font-mono">
                          {a}
                        </Badge>
                      ))}
                    </div>
                  ) : null}
                </div>

                <TabsContent value="ldif">
                  <LdifBlock text={data.ldif} filename="change.ldif" />
                  <p className="mt-2 text-xs text-muted-foreground">
                    This is the exact change record Alder will send. The download
                    is folded at 76 columns as RFC 2849 asks; what you see here is
                    not, so it stays readable.
                  </p>
                </TabsContent>

                <TabsContent value="ansible">
                  <LdifBlock
                    text={data.ansible}
                    language="yaml"
                    filename="change.task.yaml"
                  />
                  <p className="mt-2 text-xs text-muted-foreground">
                    Rendered from the same change record as the LDIF. The
                    connection settings are variables on purpose: a bind password
                    does not belong in a generated file.
                  </p>
                </TabsContent>
              </Tabs>
            </>
          ) : null}

          {applyError ? (
            <div className="mt-4">
              <ErrorNote title="The directory refused this change" error={applyError} />
            </div>
          ) : null}
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button
            variant={destructive ? "destructive" : "default"}
            disabled={!data || apply.isPending}
            onClick={() => apply.mutate()}
          >
            {apply.isPending ? <Loader2 className="animate-spin" /> : null}
            {destructive ? "Apply and delete" : "Apply to the directory"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

export function ErrorNote({ title, error }: { title: string; error: ApiFailure }) {
  return (
    <div className="rounded-md border border-destructive/40 bg-destructive/8 p-3">
      <div className="flex items-center gap-1.5 text-sm font-medium text-destructive">
        <ShieldAlert className="size-4" />
        {title}
      </div>
      <p className="mt-1 text-sm">{error.message}</p>
      {error.detail ? (
        <p className="mt-1 font-mono text-xs text-muted-foreground">{error.detail}</p>
      ) : null}
      {error.ldapCode !== undefined ? (
        <p className="mt-1 text-xs text-muted-foreground">
          LDAP result code {error.ldapCode}
        </p>
      ) : null}
    </div>
  );
}
