import createClient from "openapi-fetch";
import type { components, paths } from "./api.gen";

// One typed client for the whole app, generated from the same api/openapi.yaml
// the Go server is generated from. A field the server does not send is a type
// error here, which is the entire point of the spec-first arrangement.
export const api = createClient<paths>({
  baseUrl: "/api/v1",
  // The session cookie is httpOnly and SameSite=Strict; the browser attaches it
  // and no script here can read it.
  credentials: "same-origin",
});

export type Schemas = components["schemas"];

export type SessionInfo = Schemas["SessionInfo"];
export type Capabilities = Schemas["Capabilities"];
export type TreeNode = Schemas["TreeNode"];
export type TreePage = Schemas["TreePage"];
export type EntryView = Schemas["EntryView"];
export type EntryAttribute = Schemas["EntryAttribute"];
export type AttributeKind = Schemas["AttributeKind"];
export type AttributeValue = Schemas["AttributeValue"];
export type Requirements = Schemas["Requirements"];
export type SearchResponse = Schemas["SearchResponse"];
export type SearchResultEntry = Schemas["SearchResultEntry"];
export type SchemaView = Schemas["SchemaView"];
export type ObjectClassSummary = Schemas["ObjectClassSummary"];
export type ObjectClassDetail = Schemas["ObjectClassDetail"];
export type AttributeTypeSummary = Schemas["AttributeTypeSummary"];
export type AttributeTypeDetail = Schemas["AttributeTypeDetail"];
export type SyntaxSummary = Schemas["SyntaxSummary"];
export type MatchingRuleSummary = Schemas["MatchingRuleSummary"];
export type ChangeRequest = Schemas["ChangeRequest"];
export type ChangePreview = Schemas["ChangePreview"];
export type ChangeMod = Schemas["ChangeMod"];
export type ApplyResult = Schemas["ApplyResult"];
export type SchemaWrite = Schemas["SchemaWrite"];
export type SchemaTarget = Schemas["SchemaTarget"];
export type ChangesetPreview = Schemas["ChangesetPreview"];
export type ChangesetResult = Schemas["ChangesetResult"];
export type ChangesetOutcome = Schemas["ChangesetOutcome"];
export type ImportResult = Schemas["ImportResult"];
export type ApiError = Schemas["Error"];

/**
 * ApiFailure carries the server's structured error so a component can show the
 * message the API wrote rather than inventing its own.
 */
export class ApiFailure extends Error {
  readonly status: number;
  readonly code: ApiError["error"] | "unknown";
  readonly detail?: string;
  readonly ldapCode?: number;
  /** What the result code usually means for what was attempted. */
  readonly hint?: string;

  constructor(status: number, body?: ApiError) {
    super(body?.message ?? `Request failed with status ${status}`);
    this.name = "ApiFailure";
    this.status = status;
    this.code = body?.error ?? "unknown";
    this.detail = body?.detail;
    this.ldapCode = body?.ldapCode;
    this.hint = body?.hint;
  }

  /** True when the session is gone and the user must connect again. */
  get isUnauthorized() {
    return this.status === 401;
  }
}

/**
 * unwrap turns openapi-fetch's `{ data, error }` into a value or a throw, which
 * is what TanStack Query expects.
 */
export function unwrap<T>(res: { data?: T; error?: unknown; response: Response }): T {
  if (res.error !== undefined || res.data === undefined) {
    throw new ApiFailure(res.response.status, res.error as ApiError | undefined);
  }
  return res.data;
}
