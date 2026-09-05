import { useState } from "react";
import { Minus, UserMinus, UserPlus } from "lucide-react";
import type { ChangeRequest, EntryView } from "@/lib/api";
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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { ChangeDialog } from "@/components/change-dialog";
import { DnPicker } from "@/components/dn-picker";
import { displayText, rdnOf } from "@/lib/values";

/**
 * Adding and removing a member, as one action rather than an edit.
 *
 * The reason is concurrency, and it is the whole point of the feature. Adding
 * one person to a fifty-person group through the editor means posting the list
 * you read, which silently removes anybody another administrator added in
 * between — and group membership is the most concurrently edited attribute a
 * directory has. These produce `add: member` with one value, and `delete:
 * member` with one value, which succeed for both administrators.
 *
 * The controls appear because the entry's own object classes permit a
 * membership attribute, not because it currently holds one: an empty group is
 * still a group, and needing a member before you can add the first one would be
 * a poor arrangement.
 */

export function MembershipActions({
  entry,
  readOnly,
  pickerBase,
  onChanged,
}: {
  entry: EntryView;
  readOnly: boolean;
  /** Where the picker searches, normally the entry's own naming context. */
  pickerBase: string;
  onChanged: () => void;
}) {
  const attributes = entry.membershipAttributes ?? [];
  const [attribute, setAttribute] = useState(attributes[0] ?? "");
  const [picking, setPicking] = useState(false);
  const [removing, setRemoving] = useState(false);
  const [change, setChange] = useState<ChangeRequest | null>(null);

  if (attributes.length === 0 || readOnly) return null;

  const chosen = attributes.includes(attribute) ? attribute : (attributes[0] as string);
  const current = valuesOf(entry, chosen);

  return (
    <>
      <Button variant="outline" size="sm" onClick={() => setPicking(true)}>
        <UserPlus />
        Add member
      </Button>
      <Button
        variant="outline"
        size="sm"
        disabled={current.length === 0}
        title={
          current.length === 0 ? "This group holds no members yet" : undefined
        }
        onClick={() => setRemoving(true)}
      >
        <UserMinus />
        Remove
      </Button>

      {picking ? (
        <AddMemberDialog
          entry={entry}
          attributes={attributes}
          attribute={chosen}
          onAttribute={setAttribute}
          pickerBase={pickerBase}
          onClose={() => setPicking(false)}
          onPicked={(dn) => {
            setPicking(false);
            setChange(memberChange(entry.dn, chosen, dn, "add"));
          }}
        />
      ) : null}

      {removing ? (
        <RemoveMemberDialog
          entry={entry}
          attributes={attributes}
          attribute={chosen}
          onAttribute={setAttribute}
          onClose={() => setRemoving(false)}
          onChose={(value) => {
            setRemoving(false);
            setChange(memberChange(entry.dn, chosen, value, "delete"));
          }}
        />
      ) : null}

      <ChangeDialog
        change={change}
        open={change !== null}
        onOpenChange={(open) => !open && setChange(null)}
        title={change?.mods?.[0]?.op === "delete" ? "Remove this member" : "Add this member"}
        onApplied={() => {
          setChange(null);
          onChanged();
        }}
      />
    </>
  );
}

/**
 * memberChange builds the narrow modification.
 *
 * One value, one operation, naming only the member being moved. Nothing here
 * reads the current list, so nothing here can overwrite it.
 */
function memberChange(
  dn: string,
  attribute: string,
  value: string,
  op: "add" | "delete",
): ChangeRequest {
  return {
    dn,
    type: "modify",
    mods: [{ op, name: attribute, values: [{ text: value }] }],
  };
}

function valuesOf(entry: EntryView, attribute: string): string[] {
  const folded = attribute.toLowerCase();
  const attr = entry.attributes.find(
    (a) => (a.name.split(";")[0] ?? a.name).toLowerCase() === folded,
  );
  return (attr?.values ?? []).map(displayText).filter((v) => v !== "");
}

/** Which attribute to write, when the entry's classes permit more than one. */
function AttributeChoice({
  attributes,
  attribute,
  onAttribute,
}: {
  attributes: string[];
  attribute: string;
  onAttribute: (a: string) => void;
}) {
  if (attributes.length < 2) {
    return (
      <p className="text-xs text-muted-foreground">
        Written to <span className="font-dn">{attribute}</span>.
      </p>
    );
  }
  return (
    <div className="space-y-1.5">
      <Label htmlFor="membership-attr">Membership attribute</Label>
      <Select value={attribute} onValueChange={onAttribute}>
        <SelectTrigger id="membership-attr">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {attributes.map((a) => (
            <SelectItem key={a} value={a}>
              {a}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      <p className="text-xs text-muted-foreground">
        This entry's classes permit more than one, and they are not
        interchangeable — a member added to one is not a member of the other.
      </p>
    </div>
  );
}

function AddMemberDialog({
  entry,
  attributes,
  attribute,
  onAttribute,
  pickerBase,
  onClose,
  onPicked,
}: {
  entry: EntryView;
  attributes: string[];
  attribute: string;
  onAttribute: (a: string) => void;
  pickerBase: string;
  onClose: () => void;
  onPicked: (dn: string) => void;
}) {
  const [choosing, setChoosing] = useState(attributes.length < 2);

  // memberUid holds a name, not a DN, so the entry picker would be the wrong
  // control entirely. Typing it is correct here rather than a shortcoming.
  const isDnValued = attribute.toLowerCase() !== "memberuid";
  const [typed, setTyped] = useState("");

  if (choosing && isDnValued) {
    return (
      <DnPicker
        open
        onOpenChange={(o) => !o && onClose()}
        baseDn={pickerBase}
        title={`Add a member to ${rdnOf(entry.dn)}`}
        suggestedFilter="(|(objectClass=person)(objectClass=groupOfNames))"
        onPick={onPicked}
      />
    );
  }

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Add a member</DialogTitle>
          <DialogDescription>
            to <span className="font-dn">{entry.dn}</span>
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-4 px-5 py-4">
          <AttributeChoice
            attributes={attributes}
            attribute={attribute}
            onAttribute={onAttribute}
          />
          {isDnValued ? null : (
            <div className="space-y-1.5">
              <Label htmlFor="member-uid">User name</Label>
              <Input
                id="member-uid"
                className="font-dn"
                value={typed}
                onChange={(e) => setTyped(e.target.value)}
              />
              <p className="text-xs text-muted-foreground">
                <span className="font-dn">{attribute}</span> holds a login name,
                not a distinguished name, so there is nothing to browse to.
              </p>
            </div>
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          {isDnValued ? (
            <Button onClick={() => setChoosing(true)}>Choose an entry</Button>
          ) : (
            <Button disabled={typed.trim() === ""} onClick={() => onPicked(typed.trim())}>
              Review the change
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function RemoveMemberDialog({
  entry,
  attributes,
  attribute,
  onAttribute,
  onClose,
  onChose,
}: {
  entry: EntryView;
  attributes: string[];
  attribute: string;
  onAttribute: (a: string) => void;
  onClose: () => void;
  onChose: (value: string) => void;
}) {
  const members = valuesOf(entry, attribute);
  const [filter, setFilter] = useState("");
  const shown = members.filter((m) => m.toLowerCase().includes(filter.toLowerCase()));

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent wide>
        <DialogHeader>
          <DialogTitle>Remove a member</DialogTitle>
          <DialogDescription>
            from <span className="font-dn">{entry.dn}</span>
          </DialogDescription>
        </DialogHeader>
        <div className="min-h-0 flex-1 space-y-3 overflow-y-auto px-5 py-4">
          <AttributeChoice
            attributes={attributes}
            attribute={attribute}
            onAttribute={onAttribute}
          />
          <div className="space-y-1.5">
            <Label htmlFor="member-filter">Filter</Label>
            <Input
              id="member-filter"
              className="font-dn"
              value={filter}
              placeholder="part of a name"
              onChange={(e) => setFilter(e.target.value)}
            />
          </div>
          <div className="flex items-center gap-2 text-sm">
            <Badge variant="outline" className="tabular-nums">
              {members.length} {members.length === 1 ? "member" : "members"}
            </Badge>
            {shown.length !== members.length ? (
              <span className="text-muted-foreground">{shown.length} shown</span>
            ) : null}
          </div>
          <ul className="divide-y divide-border rounded-md border border-border">
            {shown.map((value) => (
              <li key={value} className="flex items-center gap-2 px-3 py-1.5">
                <span className="min-w-0 flex-1 truncate font-dn text-sm" title={value}>
                  {value}
                </span>
                <Button
                  variant="ghost"
                  size="sm"
                  className="text-destructive"
                  onClick={() => onChose(value)}
                >
                  <Minus />
                  Remove
                </Button>
              </li>
            ))}
            {shown.length === 0 ? (
              <li className="px-3 py-4 text-sm text-muted-foreground">
                {members.length === 0
                  ? "This group holds no members."
                  : "Nothing here matches that."}
              </li>
            ) : null}
          </ul>
          <p className="text-xs text-muted-foreground">
            Removing names only the member you chose, so it cannot disturb anyone
            added since this page was loaded.
          </p>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
