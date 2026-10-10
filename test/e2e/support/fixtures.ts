import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { test as base, expect, type Locator, type Page } from "@playwright/test";
import * as directory from "./directory";
import { CutProxy } from "./proxy";
import { type DirectoryServer, suffix } from "./servers";

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../../..");

/**
 * The harness CA, pasted into the connection form exactly as an operator
 * would. Verification stays on: a journey that turned it off would not show
 * that the product verifies, and the charter's TLS rule is one of the things
 * this is here to keep true.
 */
const harnessCA = readFileSync(path.join(repoRoot, "test", "compose", "certs", "ca.crt"), "utf8");

/** The connection form and the steps after it, as a person would drive them. */
export class Alder {
  constructor(
    readonly page: Page,
    private server: DirectoryServer,
    /** The proxy this session connects through, for journeys that cut it. */
    readonly proxy: CutProxy,
  ) {}

  /**
   * Connects through the proxy, by filling in the form.
   *
   * Exact label matches throughout: "Password" would otherwise also find
   * "Configuration password", and "Bind DN" its configuration twin.
   */
  async connect(): Promise<void> {
    const { page } = this;
    await page.goto("/");
    await page.getByLabel("Directory host", { exact: true }).fill("127.0.0.1");
    await page.getByLabel("Port", { exact: true }).fill(String(this.proxy.port));
    await page.getByLabel("Bind DN", { exact: true }).fill(this.server.bindDN);
    await page.getByLabel("Password", { exact: true }).fill(this.server.bindPassword);
    await page.getByText("Certificate options").click();
    await page.getByLabel("CA certificate (PEM)").fill(harnessCA);
    await page.getByLabel("Certificate name override").fill("localhost");
    await page.getByRole("button", { name: "Connect", exact: true }).click();
    // The overview is where a connection lands; it names the server it is
    // connected to, which here is the proxy.
    await expect(page.getByRole("heading", { name: `127.0.0.1:${this.proxy.port}` })).toBeVisible();
  }

  /**
   * Opens an entry by its address. The location is the product's own way of
   * sharing a place in the directory, so this is a supported way in rather
   * than a back door around navigation.
   */
  async openEntry(dn: string): Promise<void> {
    await this.page.goto(`/?view=browse&dn=${encodeURIComponent(dn)}`);
    await expect(this.page.getByRole("button", { name: "Edit", exact: true })).toBeVisible();
  }

  /**
   * Opens the changeset by its tab. Not by address: the changeset lives in
   * the page's memory, and loading a URL would empty it.
   */
  async openChangeset(): Promise<void> {
    await this.page.getByRole("button", { name: /^Changeset/ }).click();
  }

  /**
   * Changes one single-valued attribute in the editor and opens the review,
   * returning the dialog. Nothing has been written when this returns.
   */
  async reviewEdit(attribute: string, from: string, to: string): Promise<Locator> {
    const { page } = this;
    await page.getByRole("button", { name: "Edit", exact: true }).click();
    const field = page.getByRole("textbox", { name: attribute, exact: true });
    await expect(field).toHaveValue(from);
    await field.fill(to);
    await page.getByRole("button", { name: "Review 1 change", exact: true }).click();
    const dialog = page.getByRole("dialog");
    await expect(dialog).toContainText(`replace: ${attribute}`);
    return dialog;
  }
}

/** A disposable entry a journey may change freely, removed afterwards. */
export interface Disposable {
  dn: string;
  /** A value unique to this run, so a check cannot pass on a stale entry. */
  token: string;
}

type Fixtures = {
  // Not `proxy`: Playwright already has a browser option by that name.
  cutProxy: CutProxy;
  alder: Alder;
  disposable: Disposable;
  read: (dn: string, attributes?: string[]) => directory.Attributes | null;
};

type Options = { server: DirectoryServer };

export const test = base.extend<Fixtures & Options>({
  // Set per project in playwright.config.ts: every journey runs once per
  // directory server.
  server: [undefined as unknown as DirectoryServer, { option: true }],

  cutProxy: async ({ server }, use) => {
    const proxy = await CutProxy.start(server.ldapsPort);
    await use(proxy);
    await proxy.stop();
  },

  alder: async ({ page, server, cutProxy }, use) => {
    await use(new Alder(page, server, cutProxy));
  },

  read: async ({ server }, use) => {
    await use((dn, attributes = []) => directory.read(server, dn, attributes));
  },

  disposable: async ({ server }, use, testInfo) => {
    // Short on purpose. LDIF folds lines at 76 characters, and a journey
    // asserts on the rendered dn and value lines: a token long enough to fold
    // one would fail the check for a reason that has nothing to do with Alder.
    const token = `${server.name}-${Date.now().toString(36)}${testInfo.workerIndex}${Math.random().toString(36).slice(2, 6)}`;
    const uid = `e2e-${token}`;
    const dn = `uid=${uid},ou=people,${suffix}`;
    directory.add(
      server,
      [
        `dn: ${dn}`,
        "objectClass: top",
        "objectClass: person",
        "objectClass: organizationalPerson",
        "objectClass: inetOrgPerson",
        `uid: ${uid}`,
        "cn: End To End",
        "sn: Journey",
        "description: before",
        "",
      ].join("\n"),
    );
    try {
      await use({ dn, token });
    } finally {
      // Whether the journey passed or failed. An entry left behind breaks
      // the conformance inventory checks on the next run.
      directory.remove(server, dn);
    }
  },
});

export { expect };
