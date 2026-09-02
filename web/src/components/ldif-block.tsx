import { useState } from "react";
import { Check, Copy, Download } from "lucide-react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

/**
 * LdifBlock renders an LDIF document with light syntax colouring.
 *
 * The colouring is deliberately minimal: the attribute name, the directives
 * that say what a record does, and comments. An LDIF preview is read to check
 * one thing — "is this the change I meant" — and more colour makes that
 * harder, not easier.
 */
export function LdifBlock({
  text,
  className,
  filename,
  language = "ldif",
}: {
  text: string;
  className?: string;
  filename?: string;
  language?: "ldif" | "yaml";
}) {
  return (
    <div className={cn("relative", className)}>
      <div className="absolute right-2 top-2 z-10 flex gap-1">
        <CopyButton text={text} />
        {filename ? <DownloadButton text={text} filename={filename} /> : null}
      </div>
      <pre className="max-h-[55vh] overflow-auto rounded-md border border-border bg-muted/40 p-3 pr-20 font-mono text-[12.5px] leading-relaxed">
        <code>
          {text.split("\n").map((line, i) => (
            <LdifLine key={i} line={line} language={language} />
          ))}
        </code>
      </pre>
    </div>
  );
}

const directives = new Set([
  "dn",
  "changetype",
  "add",
  "delete",
  "replace",
  "increment",
  "newrdn",
  "deleteoldrdn",
  "newsuperior",
  "version",
]);

function LdifLine({ line, language }: { line: string; language: "ldif" | "yaml" }) {
  if (line.startsWith("#")) {
    return (
      <div>
        <span className="ldif-comment">{line}</span>
      </div>
    );
  }
  if (line === "-") {
    return (
      <div>
        <span className="ldif-directive">-</span>
      </div>
    );
  }
  if (language === "yaml") {
    return <div>{line || " "}</div>;
  }
  // A continuation line begins with a single space and carries no name.
  if (line.startsWith(" ")) {
    return <div className="ldif-b64">{line}</div>;
  }

  const colon = line.indexOf(":");
  if (colon < 0) {
    return <div>{line || " "}</div>;
  }
  const name = line.slice(0, colon);
  const rest = line.slice(colon);
  const isDirective = directives.has(name.toLowerCase());
  const isBase64 = rest.startsWith("::");

  return (
    <div>
      <span className={isDirective ? "ldif-directive" : "ldif-attr"}>{name}</span>
      <span className={isBase64 ? "ldif-b64" : undefined}>{rest}</span>
    </div>
  );
}

export function CopyButton({ text, label }: { text: string; label?: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <Button
      type="button"
      variant="secondary"
      size={label ? "sm" : "icon-sm"}
      onClick={() => {
        void navigator.clipboard.writeText(text).then(() => {
          setCopied(true);
          window.setTimeout(() => setCopied(false), 1600);
        });
      }}
      title="Copy to clipboard"
    >
      {copied ? <Check className="text-success" /> : <Copy />}
      {label ? <span>{copied ? "Copied" : label}</span> : null}
    </Button>
  );
}

export function DownloadButton({
  text,
  filename,
  label,
  mime = "text/plain;charset=utf-8",
}: {
  text: string;
  filename: string;
  label?: string;
  mime?: string;
}) {
  return (
    <Button
      type="button"
      variant="secondary"
      size={label ? "sm" : "icon-sm"}
      title={`Download ${filename}`}
      onClick={() => {
        const url = URL.createObjectURL(new Blob([text], { type: mime }));
        const a = document.createElement("a");
        a.href = url;
        a.download = filename;
        a.click();
        URL.revokeObjectURL(url);
      }}
    >
      <Download />
      {label ? <span>{label}</span> : null}
    </Button>
  );
}
