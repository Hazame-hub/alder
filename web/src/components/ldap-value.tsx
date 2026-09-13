import {
  useId,
  useLayoutEffect,
  useRef,
  useState,
  type ReactNode,
  type RefObject,
} from "react";
import { displayText } from "@/lib/values";
import { CopyButton } from "@/components/ldif-block";
import { cn } from "@/lib/utils";

type Value = Parameters<typeof displayText>[0];

/**
 * An attribute's values, compact by default and always inspectable in full.
 *
 * A short value renders as itself. A value that does not fit in two lines is
 * shown clipped to two, with a button that expands it in place — so a DN, an
 * aci or a connection line is never reduced to an ellipsis the reader cannot
 * get past. Clipping is decided by measuring the rendered text against the
 * room it has, not by counting characters: whether a value is long depends on
 * the panel, the font and its neighbours, none of which a length knows.
 *
 * Nothing is cut from the data. Every value is in the DOM whether collapsed or
 * not; collapsing is a max-height, and copying takes the values themselves.
 * Several values stay several values, one per line.
 */
export function LdapValue({
  values,
  expanded: controlled,
  onExpandedChange,
  className,
}: {
  values: Value[];
  /** Controlled expansion, for a caller that lays the expanded value out differently. */
  expanded?: boolean;
  onExpandedChange?: (expanded: boolean) => void;
  className?: string;
}) {
  const [own, setOwn] = useState(false);
  const expanded = controlled ?? own;
  const ref = useRef<HTMLDivElement>(null);
  const clipped = useClipped(ref, !expanded, valueCopyText(values));
  const id = useId();

  return (
    <LdapValueView
      values={values}
      expanded={expanded}
      clipped={clipped}
      contentId={id}
      contentRef={ref}
      className={className}
      onToggle={() => {
        if (controlled === undefined) setOwn(!expanded);
        onExpandedChange?.(!expanded);
      }}
    />
  );
}

/**
 * The presentation alone, with expansion and clipping passed in. Kept separate
 * so what each state renders can be checked without a DOM to measure.
 */
export function LdapValueView({
  values,
  expanded,
  clipped,
  onToggle,
  contentId,
  contentRef,
  className,
}: {
  values: Value[];
  expanded: boolean;
  /** Whether the collapsed presentation hides part of the content. */
  clipped: boolean;
  onToggle: () => void;
  contentId: string;
  contentRef?: RefObject<HTMLDivElement | null>;
  className?: string;
}) {
  const many = values.length > 1;
  return (
    <div className={cn("group/value min-w-0", className)}>
      <div
        id={contentId}
        ref={contentRef}
        data-state={expanded ? "expanded" : "collapsed"}
        className={cn(
          // pre-wrap keeps a value's own spacing; overflow-wrap:anywhere breaks
          // a token only when nothing else fits, so ordinary words and DNs still
          // break at their spaces and commas first.
          "whitespace-pre-wrap [overflow-wrap:anywhere]",
          !expanded && "max-h-[2lh] overflow-hidden",
        )}
      >
        {values.length === 0
          ? "—"
          : values.map((v, i) => (
              <div
                key={i}
                data-value=""
                className={cn(expanded && i > 0 && "mt-1 border-t border-border/60 pt-1")}
              >
                {withBreaks(displayText(v))}
              </div>
            ))}
      </div>

      {clipped || expanded ? (
        <div className="mt-0.5 flex items-center justify-end gap-1.5">
          <CopyButton
            text={valueCopyText(values)}
            ariaLabel={many ? "Copy all values" : "Copy full value"}
            variant="ghost"
            className={cn(
              "size-6 [&_svg]:size-3.5",
              // Out of the way until wanted: on hover, on keyboard focus, and
              // always once expanded, which is also how touch reaches it.
              !expanded && "opacity-0 group-hover/value:opacity-100 focus-visible:opacity-100",
            )}
          />
          <button
            type="button"
            aria-expanded={expanded}
            aria-controls={contentId}
            onClick={onToggle}
            className="rounded-sm text-xs text-primary hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          >
            {expanded ? "Show less" : many ? `Show all ${values.length} values` : "Show full value"}
          </button>
        </div>
      ) : null}
    </div>
  );
}

/**
 * The values exactly, one per line, for the clipboard. A binary value copies as
 * its base64, which is the only exact text form it has.
 */
export function valueCopyText(values: Value[]): string {
  return values.map((v) => v.text ?? v.base64 ?? "").join("\n");
}

/**
 * The text with a line-break opportunity after each comma and closing
 * parenthesis, so a DN breaks between its RDNs and an aci between its clauses
 * rather than wherever the width runs out. <wbr> adds no characters: selecting
 * and copying the rendered text still yields the value unchanged.
 */
export function withBreaks(text: string): ReactNode[] {
  return text
    .split(/(?<=[,)])/)
    .flatMap((part, i) => (i === 0 ? [part] : [<wbr key={i} />, part]));
}

/**
 * Whether an element's content is taller than the element, measured while
 * `measuring` is true and remembered while it is not — so an expanded value
 * keeps its "Show less" button. Re-measured when the element is resized, which
 * is what a narrower panel or a wrapped row does to it, and once fonts load.
 */
export function useClipped(
  ref: RefObject<HTMLElement | null>,
  measuring: boolean,
  content: string,
): boolean {
  const [clipped, setClipped] = useState(false);
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el || !measuring) return;
    let live = true;
    const measure = () => {
      if (live) setClipped(el.scrollHeight > el.clientHeight + 1);
    };
    measure();
    void document.fonts?.ready.then(measure);
    if (typeof ResizeObserver === "undefined") {
      return () => {
        live = false;
      };
    }
    const observer = new ResizeObserver(measure);
    observer.observe(el);
    return () => {
      live = false;
      observer.disconnect();
    };
  }, [ref, measuring, content]);
  return clipped;
}
