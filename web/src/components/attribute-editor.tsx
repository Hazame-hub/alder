import { useId, useState } from "react";
import { Plus, Search as SearchIconAlias, X } from "lucide-react";
import type { EntryAttribute } from "@/lib/api";
import { booleanPairFor, inputType, multiline, textValue } from "@/lib/values";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Input, Textarea } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui";
import { DnPicker } from "@/components/dn-picker";

/**
 * One attribute, as a field.
 *
 * This lives here rather than beside the entry editor because creating an entry
 * and editing one are the same problem: take what the schema says about an
 * attribute and offer the right control for it. They used to be separate
 * implementations, and the creation form fell a long way behind — a plain text
 * box for every field, including the ones the editor gave a Boolean control, an
 * entry picker, or the attribute's own description.
 */

/** How many values are rendered before the rest are collapsed behind a button. */
const valuesShownAtFirst = 50;

/** Not a legal Boolean, so it cannot be confused with a value the server holds. */
const unsetOption = "__unset";

/**
 * The control for an attribute whose syntax is RFC 4517 Boolean.
 *
 * Three things it does that a two-option select does not. An attribute with no
 * value reads as "not set" rather than defaulting to TRUE — showing TRUE over
 * an absent value is how an editor invents a change nobody made. Not set is
 * offered as a choice, so an optional Boolean can be cleared without hunting
 * for a remove button. And a value that is neither TRUE nor FALSE is shown as
 * it is, because the alternative is a blank box over a value the server really
 * does hold.
 */
function BooleanValue({
  value,
  required,
  onChange,
}: {
  value: string;
  required: boolean;
  onChange: (v: string) => void;
}) {
  const recognised = value === "TRUE" || value === "FALSE";
  return (
    <Select
      value={value === "" ? unsetOption : value}
      onValueChange={(v) => onChange(v === unsetOption ? "" : v)}
    >
      <SelectTrigger className="max-w-64">
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        <SelectItem value="TRUE">TRUE</SelectItem>
        <SelectItem value="FALSE">FALSE</SelectItem>
        <SelectItem value={unsetOption} disabled={required}>
          {required ? "not set — this attribute is required" : "not set"}
        </SelectItem>
        {!recognised && value !== "" ? (
          <SelectItem value={value}>{value} — not TRUE or FALSE</SelectItem>
        ) : null}
      </SelectContent>
    </Select>
  );
}

/**
 * What the schema says about an attribute, in the schema's own words.
 *
 * The description is the server's DESC and nothing else. Alder writes no prose
 * about attributes it did not define: a hand-written gloss is one more thing to
 * drift out of step with the directory in front of you, and the server has
 * already answered the question.
 */
function FieldDoc({
  kind,
  distinctiveDescs,
}: {
  kind: EntryAttribute["kind"];
  /** When given, only a description unique to this entry is shown here. */
  distinctiveDescs?: Set<string>;
}) {
  if (!kind.desc) return null;
  if (distinctiveDescs && !distinctiveDescs.has(kind.desc)) return null;
  return (
    <p className="mb-2 max-w-prose text-xs leading-relaxed text-muted-foreground">
      {kind.desc}
    </p>
  );
}

export function AttributeEditor({
  name,
  kind,
  required,
  values,
  onChange,
  onRemove,
  isNew,
  pickerBase,
  distinctiveDescs,
}: {
  name: string;
  kind: EntryAttribute["kind"];
  required: boolean;
  values: string[];
  onChange: (values: string[]) => void;
  onRemove?: () => void;
  isNew?: boolean;
  /** Search base for the DN picker, when this attribute holds DNs. */
  pickerBase?: string;
  /** The descriptions worth showing beside a field, rather than on hover. */
  distinctiveDescs?: Set<string>;
}) {
  const single = kind.singleValue === true;
  const asText = multiline(kind, values.map(textValue));
  // A DN-valued attribute gets a picker. Typing a full distinguished name by
  // hand is the step where "add this person to that group" goes wrong.
  const isDn = kind.kind === "dn" && pickerBase !== undefined;
  const [picking, setPicking] = useState<number | null>(null);
  // The same limit as the reader, and for a stronger reason: a thousand text
  // inputs is not an editor, it is a stalled tab.
  const [showAllValues, setShowAllValues] = useState(false);
  const editable = showAllValues ? values : values.slice(0, valuesShownAtFirst);
  const listId = useId();

  /*
   * The word pair this attribute's values sit between, if any.
   *
   * Seeded once from what the server sent rather than recomputed as the box is
   * typed into, so clearing the field to retype it does not make the suggestion
   * flicker out from under the cursor. It is per attribute, not per value:
   * every value of one attribute shares a vocabulary.
   */
  const [wordPair] = useState<[string, string] | null>(() => {
    if (kind.kind === "boolean") return null; // it has a real control already
    for (const v of values) {
      const pair = booleanPairFor(v);
      if (pair) return pair;
    }
    return null;
  });

  const setAt = (i: number, v: string) => {
    const next = [...values];
    next[i] = v;
    onChange(next);
  };

  return (
    <div
      className={cn(
        "rounded-lg border p-3",
        isNew ? "border-primary/40 bg-primary/5" : "border-border",
      )}
    >
      <div className="mb-2 flex flex-wrap items-center gap-2">
        <Label className="font-dn">
          {kind.desc ? (
            <Tooltip>
              <TooltipTrigger asChild>
                <span className="cursor-help underline decoration-dotted decoration-muted-foreground/50 underline-offset-4">
                  {name}
                </span>
              </TooltipTrigger>
              <TooltipContent className="max-w-xs">
                <p className="text-xs leading-relaxed">{kind.desc}</p>
              </TooltipContent>
            </Tooltip>
          ) : (
            name
          )}
          {required ? <span className="ml-0.5 text-destructive">*</span> : null}
        </Label>
        {single ? <Badge variant="outline">single-valued</Badge> : null}
        {kind.known === false ? (
          <Badge variant="warning">not in the schema</Badge>
        ) : kind.syntaxLabel ? (
          <Tooltip>
            <TooltipTrigger asChild>
              <Badge variant="secondary" className="cursor-help">
                {kind.syntaxLabel}
              </Badge>
            </TooltipTrigger>
            <TooltipContent className="max-w-xs">
              <p className="font-dn text-xs">{kind.syntax}</p>
              {kind.maxLength ? (
                <p className="text-xs">at most {kind.maxLength} characters</p>
              ) : null}
              {kind.oid ? <p className="font-dn text-xs">{kind.oid}</p> : null}
            </TooltipContent>
          </Tooltip>
        ) : null}
        {kind.sensitive ? <Badge variant="destructive">secret</Badge> : null}
        {onRemove ? (
          <Button
            variant="ghost"
            size="icon-sm"
            className="ml-auto"
            onClick={onRemove}
            aria-label={`Remove ${name}`}
          >
            <X />
          </Button>
        ) : null}
      </div>

      <FieldDoc kind={kind} distinctiveDescs={distinctiveDescs} />

      <div className="space-y-2">
        {editable.map((value, i) => (
          <div key={i} className="flex items-start gap-2">
            {kind.kind === "boolean" ? (
              <BooleanValue
                value={value}
                required={required}
                onChange={(v) => setAt(i, v)}
              />
            ) : asText ? (
              <Textarea
                value={value}
                rows={3}
                className="font-dn"
                onChange={(e) => setAt(i, e.target.value)}
              />
            ) : (
              <>
                <Input
                  value={value}
                  type={inputType(kind)}
                  maxLength={kind.maxLength}
                  className="font-dn"
                  // A datalist, not a select. The suggestion is a suggestion:
                  // the box stays free text and sends exactly what it holds.
                  list={wordPair ? `${listId}-words` : undefined}
                  onChange={(e) => setAt(i, e.target.value)}
                />
                {wordPair ? (
                  <datalist id={`${listId}-words`}>
                    <option value={wordPair[0]} />
                    <option value={wordPair[1]} />
                  </datalist>
                ) : null}
              </>
            )}
            {values.length > 1 ? (
              <Button
                variant="ghost"
                size="icon"
                onClick={() => onChange(values.filter((_, j) => j !== i))}
                aria-label="Remove this value"
              >
                <X />
              </Button>
            ) : null}
          </div>
        ))}
        {!showAllValues && values.length > editable.length ? (
          <Button variant="outline" size="sm" onClick={() => setShowAllValues(true)}>
            Show all {values.length} values
          </Button>
        ) : null}
        {wordPair ? (
          <p className="text-xs text-muted-foreground">
            The schema calls this {kind.syntaxLabel ?? "text"}, not a Boolean, so
            this stays a text box. <span className="font-dn">{wordPair[0]}</span>{" "}
            and <span className="font-dn">{wordPair[1]}</span> are offered because
            that is what the value looks like — anything else is still accepted,
            and what you type is what is sent.
          </p>
        ) : null}
      </div>

      <div className="mt-2 flex items-center gap-1">
        {!single ? (
          <Button variant="ghost" size="sm" onClick={() => onChange([...values, ""])}>
            <Plus />
            Add a value
          </Button>
        ) : null}
        {isDn ? (
          <Button
            variant="ghost"
            size="sm"
            onClick={() => setPicking(single ? 0 : values.length)}
          >
            <SearchIconAlias />
            Find an entry
          </Button>
        ) : null}
      </div>

      {isDn && picking !== null ? (
        <DnPicker
          open
          onOpenChange={(o) => !o && setPicking(null)}
          baseDn={pickerBase as string}
          title={`Choose a value for ${name}`}
          suggestedFilter={suggestedFilterFor(name)}
          onPick={(dn) => {
            const next = [...values];
            next[picking] = dn;
            // Picking into the slot past the end is how "add another" works.
            onChange(next.map((v) => v ?? ""));
            setPicking(null);
          }}
        />
      ) : null}
    </div>
  );
}

/**
 * suggestedFilterFor narrows the picker to what the attribute usually holds.
 *
 * It is a starting point shown in an editable box, not a rule: a group can
 * legitimately contain another group, and the user can widen it.
 */
function suggestedFilterFor(attribute: string): string {
  switch (attribute.toLowerCase()) {
    case "member":
    case "uniquemember":
    case "memberof":
      return "(|(objectClass=person)(objectClass=groupOfNames))";
    case "manager":
    case "secretary":
    case "owner":
      return "(objectClass=person)";
    default:
      return "(objectClass=*)";
  }
}

export function AddAttribute({
  available,
  onAdd,
}: {
  available: string[];
  onAdd: (name: string) => void;
}) {
  const [value, setValue] = useState("");
  if (available.length === 0) return null;
  return (
    <div className="flex items-end gap-2 rounded-lg border border-dashed border-border p-3">
      <div className="flex-1 space-y-1.5">
        <Label>Add an attribute</Label>
        <p className="text-xs text-muted-foreground">
          Only attributes this entry's object classes permit are offered.
        </p>
        <Select value={value} onValueChange={setValue}>
          <SelectTrigger className="max-w-sm">
            <SelectValue placeholder={`${available.length} available`} />
          </SelectTrigger>
          <SelectContent>
            {available.map((name) => (
              <SelectItem key={name} value={name}>
                {name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
      <Button
        variant="outline"
        disabled={!value}
        onClick={() => {
          onAdd(value);
          setValue("");
        }}
      >
        <Plus />
        Add
      </Button>
    </div>
  );
}
