import { describe, expect, it } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { SubjectRow, Verdict } from "./access";
import { subjectView } from "@/lib/access-subject";
import type { components } from "@/lib/api.gen";

type Effective = NonNullable<components["schemas"]["AccessReport"]["effective"]>;

// The access dialog is a Radix portal and renders to nothing here, so what is
// checked is the two pieces the honesty of the feature lives in: the heading
// on the verdict, and the control that changes who it is about.

const ME = "cn=admin,dc=alder,dc=test";
const OTHER = "cn=svc-alder,ou=services,dc=alder,dc=test";

function markup(node: React.ReactElement): string {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return renderToStaticMarkup(<QueryClientProvider client={client}>{node}</QueryClientProvider>);
}

function textOf(html: string): string {
  return html
    .replace(/<[^>]+>/g, " ")
    .replace(/&#x27;/g, "'")
    .replace(/&quot;/g, '"')
    .replace(/&amp;/g, "&")
    .replace(/\s+/g, " ")
    .trim();
}

const effective = (subject: string): Effective => ({
  subject,
  entry: "v",
  entryWords: ["read"],
  attributes: [{ name: "userPassword", rights: "", words: [] }],
});

describe("whose rights the verdict is about", () => {
  it("says 'you' when nobody else was asked about", () => {
    const html = markup(<Verdict effective={effective(ME)} view={subjectView("", ME)} />);
    expect(textOf(html)).toContain("What the server says you may do here");
    // Even though the server echoed a subject back — it always does, because
    // the handler fills a blank one with the session's own bind DN.
    expect(textOf(html)).not.toContain("not you");
  });

  it("says whose it is, and that it is not yours, when somebody else was asked about", () => {
    const html = markup(<Verdict effective={effective(OTHER)} view={subjectView(OTHER, ME)} />);
    const text = textOf(html);
    expect(text).toContain("cn=svc-alder");
    expect(text).toContain("not you");
    // And it loses the reassuring tint: somebody else's rights are not
    // reassurance about yours.
    expect(html).not.toContain("bg-success/5");
  });

  it("does not pretend the server attributed the verdict", () => {
    // There used to be a warning here comparing effective.subject against
    // what was asked. It could never fire: the Get Effective Rights response
    // carries no subject, so that field is the string Alder sent, and both
    // sides of the comparison came from the same request. A guard that
    // cannot fire is worse than none — it reads as a check that has been
    // done.
    const html = markup(<Verdict effective={effective("cn=someone-else")} view={subjectView(OTHER, ME)} />);
    expect(textOf(html)).not.toContain("which is not what was asked");
    // What it does say is whose question it was, which is the honest part.
    expect(textOf(html)).toContain("cn=svc-alder");
  });
});

describe("the control for asking about another identity", () => {
  it("is absent on a server that would answer the same thing regardless", () => {
    // Not a disabled field with a tooltip, and not a paragraph either: the
    // report already carries the server's own note in the verdict's place,
    // and saying it twice is one more line on a long screen.
    const html = markup(
      <SubjectRow dn="uid=a,dc=alder,dc=test" view={subjectView("", ME)} canAsk={false} contexts={[]} onAsk={() => {}} />,
    );
    expect(html).toBe("");
  });

  it("offers a field and a picker where the server answers, and a way back", () => {
    const mine = markup(
      <SubjectRow
        dn="uid=a,ou=people,dc=alder,dc=test"
        view={subjectView("", ME)}
        canAsk={true}
        contexts={["dc=alder,dc=test"]}
        onAsk={() => {}}
      />,
    );
    expect(textOf(mine)).toContain("Rights for you");
    expect(mine).toContain("<input");
    // No reset while the verdict is already yours: there is nothing to go
    // back from.
    expect(textOf(mine)).not.toContain("Back to my own rights");

    const theirs = markup(
      <SubjectRow
        dn="uid=a,ou=people,dc=alder,dc=test"
        view={subjectView(OTHER, ME)}
        canAsk={true}
        contexts={["dc=alder,dc=test"]}
        onAsk={() => {}}
      />,
    );
    expect(textOf(theirs)).toContain("Rights for cn=svc-alder");
    expect(textOf(theirs)).toContain("Back to my own rights");
  });
});

describe("which attributes the verdict puts first", () => {
  // The finding this answers: the verdict was opened on a locked account to
  // find out who could clear the lock, and nsAccountLock was not on screen.
  // It is one of a hundred and forty-one rows the server answers about, and
  // alphabetically it sits far below the fold.
  const right = (name: string, present: boolean) => ({
    name,
    rights: "rsc",
    words: ["read"],
    ...(present ? { present: true } : {}),
  });

  const verdict = (attributes: ReturnType<typeof right>[]) =>
    textOf(
      markup(
        <Verdict
          effective={{ subject: ME, entry: "rsc", entryWords: ["read"], attributes }}
          view={subjectView("", ME)}
        />,
      ),
    );

  it("lists the attributes the entry holds before the ones it does not", () => {
    const text = verdict([
      right("aci", false),
      right("altServer", false),
      right("nsAccountLock", true),
      right("uid", true),
    ]);
    expect(text.indexOf("nsAccountLock")).toBeGreaterThan(-1);
    expect(text.indexOf("nsAccountLock")).toBeLessThan(text.indexOf("aci"));
    expect(text.indexOf("uid")).toBeLessThan(text.indexOf("altServer"));
  });

  it("shows a held attribute even when the server answers about many", () => {
    // Twenty-five filler names sorting before it, which is what buried the
    // real one: without the held-first ordering it falls past the cut.
    const filler = Array.from({ length: 25 }, (_, i) =>
      right(`aaattr${String(i).padStart(2, "0")}`, false),
    );
    expect(verdict([...filler, right("nsAccountLock", true)])).toContain("nsAccountLock");
  });

  it("does not cut the held ones off at eight when the entry holds more", () => {
    // Twenty-four held attributes is an ordinary person entry here. A fixed
    // eight-row fold would show a third of what the entry actually has and
    // give no sign the rest were held rather than absent.
    const held = Array.from({ length: 24 }, (_, i) =>
      right(`held${String(i).padStart(2, "0")}`, true),
    );
    const text = verdict([...held, right("zzUnheld", false)]);
    expect(text).toContain("held15");
  });

  it("leaves the server's order alone when it marked nothing as held", () => {
    // An older server, or one whose response carries no attribute list:
    // inventing an order there would be a claim Alder cannot support.
    const text = verdict([right("aci", false), right("zz", false)]);
    expect(text.indexOf("aci")).toBeLessThan(text.indexOf("zz"));
  });
});
