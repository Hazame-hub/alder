import { useMemo, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, Loader2, Pencil, Plus, Search as SearchIcon, Trash2 } from "lucide-react";
import { api, unwrap } from "@/lib/api";
import type { SchemaView } from "@/lib/api";
import { cn } from "@/lib/utils";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui";
import { LdifBlock } from "@/components/ldif-block";
import { ChangeDialog } from "@/components/change-dialog";
import { SchemaEditorDialog } from "@/features/schema-editor";
import type { SchemaEditorRequest } from "@/features/schema-editor";
import type { ChangeRequest, SchemaWrite } from "@/lib/api";

type Section = "objectClasses" | "attributeTypes" | "syntaxes" | "matchingRules";

/**
 * SchemaBrowser is the reason someone reaches for Alder over ldapsearch.
 *
 * Everything cross-links: a class lists its attributes and each is a link; an
 * attribute lists the classes that require or permit it and each is a link. The
 * search runs over names, OIDs and descriptions at once, because "which
 * attribute holds a phone number" is the question people actually have.
 */
export function SchemaBrowser() {
  const [section, setSection] = useState<Section>("objectClasses");
  const [query, setQuery] = useState("");
  const [selected, setSelected] = useState<string | null>(null);
  const queryClient = useQueryClient();

  // Editing runs in two steps on purpose: the form builds a change, and the
  // ordinary confirmation dialog applies it. Holding both here is what lets the
  // second one be the same dialog every other write in the application uses.
  const [editing, setEditing] = useState<SchemaEditorRequest | null>(null);
  const [editorKey, setEditorKey] = useState(0);
  const [pending, setPending] = useState<{
    change: ChangeRequest;
    title: string;
    destructive: boolean;
  } | null>(null);

  const schema = useQuery({
    queryKey: ["schema"],
    queryFn: async () => unwrap(await api.GET("/schema")),
  });

  const session = useQuery({
    queryKey: ["session"],
    queryFn: async () => unwrap(await api.GET("/session")),
  });
  const write: SchemaWrite | undefined = session.data?.capabilities?.schemaWrite;
  const canEdit =
    session.data?.readOnly !== true && write !== undefined && write.style !== "none";

  // A fresh key on every open, so the form never opens showing the previous
  // definition's fields.
  const openEditor = (req: SchemaEditorRequest) => {
    setEditorKey((k) => k + 1);
    setEditing(req);
  };

  if (schema.isPending) {
    return (
      <div className="flex items-center gap-2 p-8 text-sm text-muted-foreground">
        <Loader2 className="size-4 animate-spin" />
        Reading the schema…
      </div>
    );
  }
  if (schema.isError) {
    return (
      <p className="p-6 text-sm text-destructive">
        The schema could not be read. {(schema.error as Error).message}
      </p>
    );
  }

  const data = schema.data;

  return (
    <div className="flex h-full min-h-0">
      <div className="flex w-80 shrink-0 flex-col border-r border-border">
        <div className="space-y-2 border-b border-border p-3">
          <Tabs value={section} onValueChange={(v) => setSection(v as Section)}>
            <TabsList className="grid w-full grid-cols-4">
              <TabsTrigger value="objectClasses" title="Object classes">
                Classes
              </TabsTrigger>
              <TabsTrigger value="attributeTypes" title="Attribute types">
                Attrs
              </TabsTrigger>
              <TabsTrigger value="syntaxes" title="LDAP syntaxes">
                Syntax
              </TabsTrigger>
              <TabsTrigger value="matchingRules" title="Matching rules">
                Rules
              </TabsTrigger>
            </TabsList>
          </Tabs>
          <div className="relative">
            <SearchIcon className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
            <Input
              value={query}
              placeholder="name, OID or description"
              className="pl-8"
              onChange={(e) => setQuery(e.target.value)}
            />
          </div>
          {canEdit && (section === "objectClasses" || section === "attributeTypes") ? (
            <Button
              variant="outline"
              size="sm"
              className="w-full"
              onClick={() =>
                openEditor({
                  kind: section === "objectClasses" ? "objectClass" : "attributeType",
                  op: "add",
                })
              }
            >
              <Plus />
              New {section === "objectClasses" ? "object class" : "attribute type"}
            </Button>
          ) : null}
          {write && write.style === "none" && write.unavailable ? (
            <p className="text-xs text-muted-foreground">{write.unavailable}</p>
          ) : null}
        </div>
        <SchemaList
          schema={data}
          section={section}
          query={query}
          selected={selected}
          onSelect={setSelected}
        />
      </div>

      <div className="min-w-0 flex-1 overflow-y-auto">
        {selected ? (
          section === "attributeTypes" ? (
              <AttributeTypeDetailPane
              name={selected}
              onNavigate={setSelected}
              onSection={setSection}
              canEdit={canEdit}
              onEdit={openEditor}
            />
          ) : section === "objectClasses" ? (
            <ObjectClassDetailPane
              name={selected}
              onNavigate={setSelected}
              onSection={setSection}
              canEdit={canEdit}
              onEdit={openEditor}
            />
          ) : (
            <SimpleDetail schema={data} section={section} id={selected} />
          )
        ) : (
          <SchemaOverview schema={data} />
        )}
      </div>

      {write ? (
        <SchemaEditorDialog
          key={editorKey}
          request={editing}
          write={write}
          open={editing !== null}
          onOpenChange={(o) => {
            if (!o) setEditing(null);
          }}
          onBuilt={(change, title, destructive) =>
            setPending({ change, title, destructive })
          }
        />
      ) : null}

      <ChangeDialog
        change={pending?.change ?? null}
        open={pending !== null}
        onOpenChange={(o) => {
          if (!o) setPending(null);
        }}
        title={pending?.title}
        destructive={pending?.destructive}
        onApplied={() => {
          // A schema change invalidates more than the schema view: the entry
          // editor decides what an entry may hold from the same schema, so a
          // stale copy would offer an attribute the directory then refuses.
          void queryClient.invalidateQueries({ queryKey: ["schema"] });
          void queryClient.invalidateQueries({ queryKey: ["objectclass"] });
          void queryClient.invalidateQueries({ queryKey: ["attributetype"] });
          void queryClient.invalidateQueries({ queryKey: ["entry"] });
          setPending(null);
          setSelected(null);
        }}
      />
    </div>
  );
}

function SchemaList({
  schema,
  section,
  query,
  selected,
  onSelect,
}: {
  schema: SchemaView;
  section: Section;
  query: string;
  selected: string | null;
  onSelect: (id: string) => void;
}) {
  const items = useMemo(() => {
    const q = query.trim().toLowerCase();
    const match = (...fields: (string | undefined)[]) =>
      q === "" || fields.some((f) => (f ?? "").toLowerCase().includes(q));

    switch (section) {
      case "objectClasses":
        return (schema.objectClasses ?? [])
          .filter((c) => match(c.name, c.oid, c.desc, ...(c.names ?? [])))
          .map((c) => ({
            id: c.name,
            label: c.name,
            hint: c.desc ?? c.oid,
            tag: c.kind,
          }));
      case "attributeTypes":
        return (schema.attributeTypes ?? [])
          .filter((a) => match(a.name, a.oid, a.desc, ...(a.names ?? [])))
          .map((a) => ({
            id: a.name,
            label: a.name,
            hint: a.desc ?? a.syntaxLabel ?? a.oid,
            tag: a.singleValue ? "single" : undefined,
          }));
      case "syntaxes":
        return (schema.syntaxes ?? [])
          .filter((s) => match(s.oid, s.desc))
          .map((s) => ({
            id: s.oid,
            label: s.desc ?? s.oid,
            hint: s.oid,
            tag: s.usedByCount ? `${s.usedByCount} attrs` : undefined,
          }));
      case "matchingRules":
        return (schema.matchingRules ?? [])
          .filter((r) => match(r.name, r.oid, r.desc))
          .map((r) => ({ id: r.name, label: r.name, hint: r.desc ?? r.oid, tag: undefined }));
    }
  }, [schema, section, query]);

  return (
    <div className="min-h-0 flex-1 overflow-y-auto">
      <p className="px-3 py-1.5 text-xs text-muted-foreground">
        {items.length} {items.length === 1 ? "definition" : "definitions"}
      </p>
      {items.map((item) => (
        <button
          key={item.id}
          type="button"
          onClick={() => onSelect(item.id)}
          className={cn(
            "block w-full border-l-2 px-3 py-1.5 text-left transition-colors",
            selected === item.id
              ? "border-l-primary bg-primary/8"
              : "border-l-transparent hover:bg-accent/60",
          )}
        >
          <div className="flex items-baseline gap-2">
            <span className="truncate font-dn">{item.label}</span>
            {item.tag ? (
              <Badge variant="outline" className="shrink-0">
                {item.tag}
              </Badge>
            ) : null}
          </div>
          {item.hint ? (
            <p className="truncate text-xs text-muted-foreground">{item.hint}</p>
          ) : null}
        </button>
      ))}
    </div>
  );
}

function SchemaOverview({ schema }: { schema: SchemaView }) {
  const c = schema.counts;
  const stats: [string, number | undefined][] = [
    ["Object classes", c.objectClasses],
    ["Attribute types", c.attributeTypes],
    ["Syntaxes", c.syntaxes],
    ["Matching rules", c.matchingRules],
    ["Matching rule uses", c.matchingRuleUses],
    ["Content rules", c.ditContentRules],
    ["Name forms", c.nameForms],
  ];
  return (
    <div className="p-6">
      <h2 className="text-lg font-semibold">Schema</h2>
      <p className="mt-1 font-dn text-sm text-muted-foreground">
        read from {schema.subschemaDn}
      </p>

      <dl className="mt-5 grid max-w-2xl grid-cols-2 gap-3 sm:grid-cols-3">
        {stats.map(([label, value]) => (
          <div key={label} className="rounded-lg border border-border p-3">
            <dt className="text-xs text-muted-foreground">{label}</dt>
            <dd className="text-2xl font-semibold tabular-nums">{value ?? 0}</dd>
          </div>
        ))}
      </dl>

      {schema.errors?.length ? (
        <div className="mt-6 max-w-3xl rounded-lg border border-warning/40 bg-warning/10 p-4">
          <div className="flex items-center gap-1.5 font-medium text-warning-tint-foreground">
            <AlertTriangle className="size-4" />
            {schema.errors.length} definition
            {schema.errors.length === 1 ? "" : "s"} could not be parsed
          </div>
          <p className="mt-1 text-sm text-muted-foreground">
            The rest of the schema loaded. These are shown rather than hidden,
            because a browser that silently omits what it could not read is
            lying about the directory.
          </p>
          <ul className="mt-3 space-y-2">
            {schema.errors.slice(0, 20).map((e, i) => (
              <li key={i} className="rounded border border-border bg-card p-2">
                <p className="text-xs text-muted-foreground">{e.message}</p>
                <code className="mt-1 block break-all font-mono text-[11px]">
                  {e.definition}
                </code>
              </li>
            ))}
          </ul>
        </div>
      ) : (
        <p className="mt-6 max-w-2xl text-sm text-muted-foreground">
          Every definition the server published parsed cleanly.
        </p>
      )}

      <p className="mt-6 max-w-2xl text-sm text-muted-foreground">
        Pick a definition on the left. Everything cross-links: a class lists the
        attributes it requires and permits, and each attribute lists the classes
        that use it.
      </p>
    </div>
  );
}

function ObjectClassDetailPane({
  name,
  onNavigate,
  onSection,
  canEdit,
  onEdit,
}: {
  name: string;
  onNavigate: (id: string) => void;
  onSection: (s: Section) => void;
  canEdit: boolean;
  onEdit: (req: SchemaEditorRequest) => void;
}) {
  const detail = useQuery({
    queryKey: ["objectclass", name],
    queryFn: async () =>
      unwrap(
        await api.GET("/schema/objectclasses/{name}", {
          params: { path: { name } },
        }),
      ),
  });

  if (detail.isPending) return <Pending />;
  if (detail.isError) return <NotFound name={name} />;

  const d = detail.data;
  const s = d.summary;

  const goToAttr = (attr: string) => {
    onSection("attributeTypes");
    onNavigate(attr);
  };

  return (
    <div className="space-y-5 p-6">
      <header>
        <div className="flex flex-wrap items-center gap-2">
          <h2 className="font-dn text-lg font-semibold">{s.name}</h2>
          <Badge variant={s.kind === "AUXILIARY" ? "warning" : "secondary"}>
            {s.kind}
          </Badge>
          {s.obsolete ? <Badge variant="destructive">obsolete</Badge> : null}
        </div>
        {s.desc ? <p className="mt-1 text-sm">{s.desc}</p> : null}
        <p className="mt-1 font-dn text-xs text-muted-foreground">{s.oid}</p>
        {(s.names ?? []).length > 1 ? (
          <p className="mt-1 text-xs text-muted-foreground">
            also known as {(s.names ?? []).slice(1).join(", ")}
          </p>
        ) : null}
        {canEdit ? (
          <div className="mt-3 flex gap-2">
            <Button
              variant="outline"
              size="sm"
              onClick={() =>
                onEdit({
                  kind: "objectClass",
                  op: "replace",
                  initial: {
                    oid: s.oid,
                    names: (s.names ?? [s.name]).join(" "),
                    desc: s.desc ?? "",
                    obsolete: s.obsolete === true,
                    classKind: s.kind as "STRUCTURAL" | "ABSTRACT" | "AUXILIARY",
                    superNames: (s.superiors ?? []).join(" "),
                    // Only what this class declares itself. Prefilling the
                    // inherited attributes as well would redeclare them here on
                    // the next save, which changes the definition's meaning.
                    must: (d.must ?? []).join(" "),
                    may: (d.may ?? []).join(" "),
                  },
                  raw: d.raw,
                })
              }
            >
              <Pencil />
              Edit
            </Button>
            <Button
              variant="outline"
              size="sm"
              className="text-destructive"
              onClick={() =>
                onEdit({ kind: "objectClass", op: "delete", initial: { oid: s.oid }, raw: d.raw })
              }
            >
              <Trash2 />
              Remove
            </Button>
          </div>
        ) : null}
      </header>

      {d.superiorChain?.length ? (
        <Section title="Inherits from">
          <div className="flex flex-wrap items-center gap-1">
            {d.superiorChain.map((sup, i) => (
              <span key={sup} className="flex items-center gap-1">
                {i > 0 ? <span className="text-muted-foreground">→</span> : null}
                <LinkChip label={sup} onClick={() => onNavigate(sup)} />
              </span>
            ))}
          </div>
        </Section>
      ) : null}

      <Section title="Requires (MUST)">
        <ChipList names={d.must ?? []} onClick={goToAttr} empty="nothing of its own" />
        {d.inheritedMust?.length ? (
          <>
            <p className="mb-1 mt-3 text-xs text-muted-foreground">
              inherited from its superiors
            </p>
            <ChipList names={d.inheritedMust} onClick={goToAttr} muted />
          </>
        ) : null}
      </Section>

      <Section title="Permits (MAY)">
        <ChipList names={d.may ?? []} onClick={goToAttr} empty="nothing of its own" />
        {d.inheritedMay?.length ? (
          <>
            <p className="mb-1 mt-3 text-xs text-muted-foreground">
              inherited from its superiors
            </p>
            <ChipList names={d.inheritedMay} onClick={goToAttr} muted />
          </>
        ) : null}
      </Section>

      {d.subclasses?.length ? (
        <Section title="Direct subclasses">
          <ChipList names={d.subclasses} onClick={onNavigate} />
        </Section>
      ) : null}

      <Section title="As the server published it">
        <LdifBlock text={d.raw ?? ""} />
      </Section>
    </div>
  );
}

function AttributeTypeDetailPane({
  name,
  onNavigate,
  onSection,
  canEdit,
  onEdit,
}: {
  name: string;
  onNavigate: (id: string) => void;
  onSection: (s: Section) => void;
  canEdit: boolean;
  onEdit: (req: SchemaEditorRequest) => void;
}) {
  const detail = useQuery({
    queryKey: ["attributetype", name],
    queryFn: async () =>
      unwrap(
        await api.GET("/schema/attributetypes/{name}", {
          params: { path: { name } },
        }),
      ),
  });

  if (detail.isPending) return <Pending />;
  if (detail.isError) return <NotFound name={name} />;

  const d = detail.data;
  const s = d.summary;

  const goToClass = (cls: string) => {
    onSection("objectClasses");
    onNavigate(cls);
  };

  return (
    <div className="space-y-5 p-6">
      <header>
        <div className="flex flex-wrap items-center gap-2">
          <h2 className="font-dn text-lg font-semibold">{s.name}</h2>
          {s.singleValue ? <Badge variant="secondary">single-valued</Badge> : null}
          {s.operational ? <Badge variant="outline">operational</Badge> : null}
          {d.kind?.readOnly ? <Badge variant="outline">read-only</Badge> : null}
          {d.kind?.sensitive ? <Badge variant="destructive">secret</Badge> : null}
          {s.obsolete ? <Badge variant="destructive">obsolete</Badge> : null}
        </div>
        {s.desc ? <p className="mt-1 text-sm">{s.desc}</p> : null}
        <p className="mt-1 font-dn text-xs text-muted-foreground">{s.oid}</p>
        {canEdit ? (
          <div className="mt-3 flex gap-2">
            <Button
              variant="outline"
              size="sm"
              onClick={() =>
                onEdit({
                  kind: "attributeType",
                  op: "replace",
                  initial: {
                    oid: s.oid,
                    names: (s.names ?? [s.name]).join(" "),
                    desc: s.desc ?? "",
                    obsolete: s.obsolete === true,
                    superName: s.superior ?? "",
                    equality: s.equality ?? "",
                    syntax: s.syntax ?? "",
                    singleValue: s.singleValue === true,
                  },
                  raw: d.raw,
                })
              }
            >
              <Pencil />
              Edit
            </Button>
            <Button
              variant="outline"
              size="sm"
              className="text-destructive"
              onClick={() =>
                onEdit({ kind: "attributeType", op: "delete", initial: { oid: s.oid }, raw: d.raw })
              }
            >
              <Trash2 />
              Remove
            </Button>
          </div>
        ) : null}
      </header>

      <Section title="Syntax and matching">
        <dl className="grid max-w-xl grid-cols-[9rem_1fr] gap-y-1.5 text-sm">
          <dt className="text-muted-foreground">Syntax</dt>
          <dd className="font-dn">
            {s.syntaxLabel ?? "—"}{" "}
            {s.syntax ? (
              <span className="text-muted-foreground">({s.syntax})</span>
            ) : null}
          </dd>
          <dt className="text-muted-foreground">Editor shows</dt>
          <dd className="font-dn">{d.kind?.kind ?? "string"}</dd>
          <dt className="text-muted-foreground">Equality</dt>
          <dd className="font-dn">{s.equality || "—"}</dd>
          {d.kind?.maxLength ? (
            <>
              <dt className="text-muted-foreground">Advisory length</dt>
              <dd className="font-dn">{d.kind.maxLength}</dd>
            </>
          ) : null}
        </dl>
      </Section>

      {d.superiorChain?.length ? (
        <Section title="Derived from">
          <div className="flex flex-wrap items-center gap-1">
            {d.superiorChain.map((sup, i) => (
              <span key={sup} className="flex items-center gap-1">
                {i > 0 ? <span className="text-muted-foreground">→</span> : null}
                <LinkChip label={sup} onClick={() => onNavigate(sup)} />
              </span>
            ))}
          </div>
          <p className="mt-1.5 text-xs text-muted-foreground">
            Where this attribute declares no syntax or matching rule of its own,
            it inherits the nearest ancestor's.
          </p>
        </Section>
      ) : null}

      <Section title="Required by">
        <ChipList names={d.requiredBy ?? []} onClick={goToClass} empty="no object class" />
      </Section>

      <Section title="Permitted in">
        <ChipList names={d.optionalIn ?? []} onClick={goToClass} empty="no object class" />
      </Section>

      <Section title="As the server published it">
        <LdifBlock text={d.raw ?? ""} />
      </Section>
    </div>
  );
}

function SimpleDetail({
  schema,
  section,
  id,
}: {
  schema: SchemaView;
  section: Section;
  id: string;
}) {
  if (section === "syntaxes") {
    const syntax = (schema.syntaxes ?? []).find((s) => s.oid === id);
    if (!syntax) return <NotFound name={id} />;
    const users = (schema.attributeTypes ?? []).filter((a) => a.syntax === syntax.oid);
    return (
      <div className="space-y-5 p-6">
        <header>
          <h2 className="text-lg font-semibold">{syntax.desc ?? syntax.oid}</h2>
          <p className="mt-1 font-dn text-xs text-muted-foreground">{syntax.oid}</p>
          {syntax.kind ? (
            <Badge variant="secondary" className="mt-2">
              editor shows: {syntax.kind}
            </Badge>
          ) : null}
        </header>
        <Section title={`Used by ${users.length} attribute types`}>
          <div className="flex flex-wrap gap-1">
            {users.map((a) => (
              <Badge key={a.oid} variant="outline" className="font-mono">
                {a.name}
              </Badge>
            ))}
          </div>
        </Section>
      </div>
    );
  }

  const rule = (schema.matchingRules ?? []).find((r) => r.name === id);
  if (!rule) return <NotFound name={id} />;
  return (
    <div className="space-y-5 p-6">
      <header>
        <h2 className="font-dn text-lg font-semibold">{rule.name}</h2>
        {rule.desc ? <p className="mt-1 text-sm">{rule.desc}</p> : null}
        <p className="mt-1 font-dn text-xs text-muted-foreground">{rule.oid}</p>
      </header>
      <Section title="Asserts values of syntax">
        <p className="font-dn text-sm">{rule.syntax || "—"}</p>
      </Section>
    </div>
  );
}

/* --- small pieces --------------------------------------------------------- */

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section>
      <h3 className="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
        {title}
      </h3>
      {children}
    </section>
  );
}

function ChipList({
  names,
  onClick,
  empty,
  muted,
}: {
  names: string[];
  onClick: (name: string) => void;
  empty?: string;
  muted?: boolean;
}) {
  if (names.length === 0) {
    return <p className="text-sm italic text-muted-foreground">{empty ?? "none"}</p>;
  }
  return (
    <div className="flex flex-wrap gap-1">
      {names.map((n) => (
        <LinkChip key={n} label={n} onClick={() => onClick(n)} muted={muted} />
      ))}
    </div>
  );
}

function LinkChip({
  label,
  onClick,
  muted,
}: {
  label: string;
  onClick: () => void;
  muted?: boolean;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "rounded-md border px-2 py-0.5 font-mono text-xs transition-colors",
        muted
          ? "border-border text-muted-foreground hover:bg-accent"
          : "border-primary/30 bg-primary/8 text-primary hover:bg-primary/15",
      )}
    >
      {label}
    </button>
  );
}

function Pending() {
  return (
    <div className="flex items-center gap-2 p-8 text-sm text-muted-foreground">
      <Loader2 className="size-4 animate-spin" />
      Loading…
    </div>
  );
}

function NotFound({ name }: { name: string }) {
  return (
    <p className="p-6 text-sm text-muted-foreground">
      This schema has no definition named <span className="font-dn">{name}</span>.
    </p>
  );
}
