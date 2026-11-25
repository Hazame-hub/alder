import { CircleCheck, Info, X } from "lucide-react";
import { safeText } from "@/lib/display";
import { toast, useToasts } from "@/lib/toast";
import { cn } from "@/lib/utils";

/**
 * Where toasts appear: bottom right, above everything, announced politely to
 * a screen reader. Mounted once, by the app.
 */
export function Toaster() {
  const toasts = useToasts();
  return (
    <div
      role="status"
      aria-live="polite"
      className="pointer-events-none fixed right-4 bottom-4 z-[100] flex w-[min(24rem,calc(100vw-2rem))] flex-col gap-2"
    >
      {toasts.map((t) => (
        <div
          key={t.id}
          className={cn(
            "pointer-events-auto flex items-start gap-2 rounded-md border bg-background p-3 text-sm shadow-lg",
            t.tone === "success" ? "border-success/40" : "border-border",
          )}
        >
          {t.tone === "success" ? (
            <CircleCheck className="mt-0.5 size-4 shrink-0 text-success" />
          ) : (
            <Info className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
          )}
          <div className="min-w-0 flex-1">
            <div className="font-medium">{t.title}</div>
            {t.description ? (
              <div className="mt-0.5 truncate font-dn text-xs text-muted-foreground">
                {safeText(t.description)}
              </div>
            ) : null}
            {t.action ? (
              <button
                type="button"
                className="mt-1.5 text-xs font-medium underline underline-offset-2 hover:text-foreground"
                onClick={() => {
                  t.action?.run();
                  toast.dismiss(t.id);
                }}
              >
                {t.action.label}
              </button>
            ) : null}
          </div>
          <button
            type="button"
            aria-label="Dismiss"
            className="text-muted-foreground hover:text-foreground"
            onClick={() => toast.dismiss(t.id)}
          >
            <X className="size-3.5" />
          </button>
        </div>
      ))}
    </div>
  );
}
