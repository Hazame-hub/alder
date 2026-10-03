import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { CircleHelp, Loader2, Search } from "lucide-react";
import { api, unwrap } from "@/lib/api";
import type { ApiFailure, ChangeOutcome, ChangeRequest } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { safeText } from "@/lib/display";

/**
 * A change that was sent and never accounted for.
 *
 * The connection died while the change was going out. The directory may have
 * applied it and may not; nothing the client saw settles it. This is not a
 * failure panel and deliberately does not look like one -- "the directory
 * refused this change" is a sentence that would be false here, and acting on
 * it would mean sending the change a second time.
 *
 * So the panel says the one true thing, offers the one useful action, and
 * offers no way at all to resend. Checking reads the directory; it does not
 * write, and it is allowed even where writing is not.
 *
 * Where the check finds the change did not land, the way to make it again is
 * the ordinary one -- close this, make the change, read the LDIF, confirm it.
 * Not a button here. The review that preceded the interruption was a review
 * of a directory that may since have moved, and re-offering it would be
 * offering a plan nobody has looked at.
 */
export function UnknownOutcome({
  change,
  onResolved,
}: {
  change: ChangeRequest;
  /** Called once the directory has been read, with what it said. */
  onResolved?: (outcome: ChangeOutcome) => void;
}) {
  const [outcome, setOutcome] = useState<ChangeOutcome | null>(null);

  const check = useMutation<ChangeOutcome, ApiFailure>({
    mutationFn: async () =>
      unwrap(await api.POST("/changes/outcome", { body: change })),
    onSuccess: (result) => {
      setOutcome(result);
      onResolved?.(result);
    },
  });

  return (
    <div className="rounded-md border border-warning/40 bg-warning/10 p-3 text-warning-tint-foreground">
      <div className="flex items-center gap-1.5 text-sm font-medium">
        <CircleHelp className="size-4" />
        This change may already have been applied
      </div>
      <p className="mt-1 text-sm">
        The connection was lost while the change was being sent. The directory may have
        applied it before the connection went, or it may never have arrived. Alder will
        not send it again by itself.
      </p>

      {outcome ? (
        <Verdict outcome={outcome} />
      ) : (
        <div className="mt-2.5 flex flex-wrap items-center gap-2">
          <Button size="sm" variant="outline" onClick={() => check.mutate()} disabled={check.isPending}>
            {check.isPending ? <Loader2 className="animate-spin" /> : <Search />}
            Check the directory
          </Button>
          <span className="text-xs">Reads the entry. Changes nothing.</span>
        </div>
      )}

      {check.error ? (
        <p className="mt-2 text-sm">
          The directory could not be read, so the outcome is still unknown:{" "}
          {safeText((check.error as ApiFailure).message)}
        </p>
      ) : null}
    </div>
  );
}

/**
 * What the directory said, and what follows from it.
 *
 * Each verdict gets its own sentence about what to do next, because the four
 * of them call for four different things and a shared one would have to be
 * vague enough to be useless.
 */
function Verdict({ outcome }: { outcome: ChangeOutcome }) {
  return (
    <div className="mt-2.5 rounded border border-border/60 bg-background/60 p-2.5">
      <div className="text-sm font-medium text-foreground">{headline(outcome.verdict)}</div>
      <p className="mt-1 text-sm text-foreground">{safeText(outcome.reason)}</p>
      <p className="mt-1.5 text-sm text-muted-foreground">{whatNow(outcome)}</p>
    </div>
  );
}

function headline(verdict: ChangeOutcome["verdict"]): string {
  switch (verdict) {
    case "applied":
      return "The change is in the directory.";
    case "not_applied":
      return "The change is not in the directory.";
    case "conflicted":
      return "Something else has changed this entry.";
    default:
      return "This cannot be settled by reading.";
  }
}

function whatNow(outcome: ChangeOutcome): string {
  switch (outcome.verdict) {
    case "applied":
      return "Nothing to do. Sending it again would be a second change.";
    case "not_applied":
      // Resolvable, but not from here. The route is the ordinary one, and
      // saying so is the whole point: a plan made before the interruption
      // was made against a directory that may since have moved.
      return "Make the change again from the entry. It will be planned against the directory as it is now, and you will see the LDIF before anything is sent.";
    case "conflicted":
      return "Look at the entry before doing anything. This change was reviewed against a state the directory no longer holds, so applying it now would overwrite somebody else's work.";
    default:
      return "The outcome stays unresolved. Alder will not guess, and will not send the change again.";
  }
}
