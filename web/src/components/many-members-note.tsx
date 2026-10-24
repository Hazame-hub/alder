import type { ChangeRequest } from "@/lib/api";
import { isManyMembersRecord } from "@/lib/group-members";
import { safeText } from "@/lib/display";

/**
 * What a stale refusal of one of these records adds: why a busy group refuses
 * it, and that the single-value actions still apply there.
 */
export function ManyMembersStaleNote({ change }: { change: ChangeRequest | null | undefined }) {
  if (!isManyMembersRecord(change)) return null;
  return (
    <p className="mt-2 text-xs text-warning-tint-foreground/90">
      This change adds or removes several members of{" "}
      <span className="font-dn">{safeText(change?.dn ?? "")}</span> in one record, so any change to
      the group&apos;s members since it was planned refuses it, with nothing written. Plan it again to
      see the group as it is now. On a group this busy, <em>Add member</em> and <em>Remove</em> on the
      group&apos;s page change one value at a time, and those still apply.
    </p>
  );
}
