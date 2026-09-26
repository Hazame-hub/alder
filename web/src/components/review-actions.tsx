import { useState, type ReactNode } from "react";
import { ListChecks } from "lucide-react";
import type { ChangeRequest } from "@/lib/api";
import { changeset } from "@/lib/changeset";
import { Button } from "@/components/ui/button";
import { ChangeDialog } from "@/components/change-dialog";

/**
 * Where a selection is reviewed.
 *
 * One change goes to the dialog every other screen uses: it is planned there,
 * shown as the server's own LDIF, and applied from there. Several go to the
 * changeset, which reads a set as one document and applies it in order.
 */
export function reviewsInDialog(changes: number): boolean {
  return changes === 1;
}

/**
 * What a comparison offers once differences are selected.
 *
 * Every other screen in Alder reaches an apply through [ChangeDialog]: the
 * change is planned, the plan's findings and the server's own LDIF are shown,
 * and applying sends the reviewed record with the plan's token. A comparison
 * used to be the exception -- its only offer was to stage, after which the
 * operator changed screen, planned again and applied there. Four interactions
 * to apply something the comparison had already derived, and the plain entry
 * editor did the same job in one dialog.
 *
 * So: one selected change opens that dialog here. Several go to the changeset,
 * which is what the changeset is for -- reading a set as one document and
 * applying it in order -- and they go in one click rather than two.
 *
 * What does not change is the rule underneath both: a plan is made, seen, and
 * only then applied. Nothing here shortens that.
 */
export function ReviewActions({
  changes,
  onReviewChangeset,
  onApplied,
  onStaged,
  destructive,
  disabled,
  stagedNote,
}: {
  /** The changes the selection derives, in the order they must be applied. */
  changes: { change: ChangeRequest; label: string }[];
  /** Go to the changeset view. */
  onReviewChangeset: () => void;
  /** A change was applied from here, so the comparison is out of date. */
  onApplied?: () => void;
  /** The selection was staged, so the caller can clear it. */
  onStaged?: () => void;
  /** Any of the selected changes removes something. */
  destructive?: boolean;
  /** The selection cannot be acted on, for a reason the caller is showing. */
  disabled?: boolean;
  /** What to say about the staged set, where the caller knows something extra. */
  stagedNote?: ReactNode;
}) {
  const [reviewing, setReviewing] = useState(false);
  const [staged, setStaged] = useState<{ for: string; count: number } | null>(null);
  const [refused, setRefused] = useState<string | null>(null);

  // What was staged is said about the selection it was staged from. Tick
  // another row and "3 staged" is about a set that no longer exists, which is
  // how somebody stages the same change twice.
  const signature = JSON.stringify(changes.map((c) => [c.change.dn, c.label]));
  const stagedNow = staged?.for === signature ? staged.count : null;

  const one = reviewsInDialog(changes.length) ? changes[0] : null;

  const stage = (): boolean => {
    const result = changeset.addMany(changes);
    if (result.refused > 0) {
      setRefused(
        `The changeset holds ${result.capacity} more change${result.capacity === 1 ? "" : "s"}, and this is ${result.refused}. Nothing was staged.`,
      );
      return false;
    }
    setRefused(null);
    setStaged({ for: signature, count: result.staged });
    onStaged?.();
    return true;
  };

  return (
    <div className="flex flex-wrap items-center gap-2">
      <Button
        size="sm"
        disabled={changes.length === 0 || disabled === true}
        onClick={() => {
          if (one) {
            setReviewing(true);
            return;
          }
          if (stage()) onReviewChangeset();
        }}
      >
        Review {changes.length} change{changes.length === 1 ? "" : "s"}
      </Button>
      <Button
        size="sm"
        variant="outline"
        disabled={changes.length === 0 || disabled === true}
        onClick={() => stage()}
      >
        <ListChecks />
        Add to changeset
      </Button>
      {stagedNow !== null ? (
        <>
          <span className="text-sm">
            {stagedNow} staged.{stagedNote ? <> {stagedNote}</> : null}
          </span>
          <Button size="sm" variant="ghost" onClick={onReviewChangeset}>
            Review the changeset
          </Button>
        </>
      ) : null}
      {refused ? <span className="text-sm text-destructive">{refused}</span> : null}

      <ChangeDialog
        change={one?.change ?? null}
        open={reviewing && one !== null}
        onOpenChange={setReviewing}
        title={one?.label}
        destructive={destructive}
        onApplied={() => onApplied?.()}
        onStaged={() => {
          setStaged({ for: signature, count: 1 });
          onStaged?.();
        }}
      />
    </div>
  );
}
