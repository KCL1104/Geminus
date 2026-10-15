// Every way an API call can fail, as one discriminated union the UI renders
// (T-014). Built only from what the server actually returned: a missing
// request ID stays null and an unexpected body is a "protocol" problem, never
// a guessed error code.
import type { ErrorCode, ValidationIssue } from "../contracts";

export type ProblemKind =
  | "unauthenticated" // 401: no or expired session
  | "forbidden" // 403: authenticated, role does not allow this
  | "not_found" // 404: missing or in another tenant (indistinguishable by design)
  | "conflict" // 409: stale revision/base, lifecycle, idempotency, result not ready
  | "invalid" // 400, 413, 415, 422: the request or model is not acceptable
  | "precondition" // 428: a required If-Match / precondition was missing
  | "rate_limited" // 429
  | "unavailable" // 503, or the API is not configured in this deployment
  | "internal" // 500
  | "network" // the request never produced an HTTP response
  | "protocol"; // a response that does not follow the API contract

export type Problem = {
  kind: ProblemKind;
  /** The server's error code, when the server sent a well-formed error body. */
  code: ErrorCode | null;
  status: number | null;
  message: string;
  requestId: string | null;
  details: { path: string; reason: string; code?: string; severity?: "error" | "warning" }[];
  /** Seconds from Retry-After, only for rate_limited / unavailable responses that sent it. */
  retryAfterSeconds: number | null;
};

/**
 * D-025 closed catalogue: each code has exactly one HTTP status. Kept equal to
 * schemas/common.schema.json `x-http-status` by tests/api.test.ts.
 */
export const errorCodeStatus = {
  INVALID_REQUEST: 400,
  IDEMPOTENCY_KEY_REQUIRED: 400,
  UNAUTHENTICATED: 401,
  FORBIDDEN: 403,
  NOT_FOUND: 404,
  MODEL_VERSION_CONFLICT: 409,
  IDEMPOTENCY_KEY_CONFLICT: 409,
  LIFECYCLE_CONFLICT: 409,
  RESULT_NOT_READY: 409,
  PROPOSAL_ALREADY_DECIDED: 409,
  PAYLOAD_TOO_LARGE: 413,
  UNSUPPORTED_MEDIA_TYPE: 415,
  MODEL_VALIDATION_FAILED: 422,
  REQUEST_VALIDATION_FAILED: 422,
  PRECONDITION_REQUIRED: 428,
  RATE_LIMITED: 429,
  INTERNAL_ERROR: 500,
  SERVICE_UNAVAILABLE: 503,
  AI_PROVIDER_NOT_CONFIGURED: 503,
  AI_POLICY_NOT_APPROVED: 503,
} as const satisfies Record<ErrorCode, number>;

const kindByStatus: Record<number, ProblemKind> = {
  400: "invalid",
  401: "unauthenticated",
  403: "forbidden",
  404: "not_found",
  409: "conflict",
  413: "invalid",
  415: "invalid",
  422: "invalid",
  428: "precondition",
  429: "rate_limited",
  500: "internal",
  503: "unavailable",
};

const REQUEST_ID = /^req_[0-7][0-9A-HJKMNP-TV-Z]{25}$/;

function problem(kind: ProblemKind, fields: Partial<Problem>): Problem {
  return { kind, code: null, status: null, message: "", requestId: null, details: [], retryAfterSeconds: null, ...fields };
}

export function networkProblem(message: string): Problem {
  return problem("network", { message });
}

export function notConfiguredProblem(what: string): Problem {
  return problem("unavailable", { message: `${what} is not configured in this deployment.` });
}

export function unauthenticatedProblem(): Problem {
  return problem("unauthenticated", { message: "Sign in to continue." });
}

export function protocolProblem(status: number | null, message: string, requestId: string | null = null): Problem {
  return problem("protocol", { status, message, requestId });
}

/** A request the client refuses to send because the contract would reject it. */
export function invalidRequestProblem(message: string): Problem {
  return problem("invalid", { message });
}

/** The X-Request-ID header, only when it is a D-001 request ID. */
export function requestIdFrom(headers: Headers): string | null {
  const id = headers.get("X-Request-ID");
  return id !== null && REQUEST_ID.test(id) ? id : null;
}

function retryAfter(headers: Headers, kind: ProblemKind): number | null {
  if (kind !== "rate_limited" && kind !== "unavailable") return null;
  const raw = headers.get("Retry-After");
  if (raw === null || !/^\d{1,6}$/.test(raw)) return null; // HTTP-date form is not used by this API
  return Number(raw);
}

function isDetail(v: unknown): v is Problem["details"][number] {
  if (typeof v !== "object" || v === null) return false;
  const d = v as Record<string, unknown>;
  return (
    typeof d.path === "string" &&
    typeof d.reason === "string" &&
    (d.code === undefined || typeof d.code === "string") &&
    (d.severity === undefined || d.severity === "error" || d.severity === "warning")
  );
}

/**
 * Turns a non-2xx response into a Problem. `body` is the parsed JSON (or
 * undefined when the body was not JSON). A body whose code is unknown, or
 * whose code belongs to a different status, is a protocol problem: the UI must
 * not act on a code the server did not mean.
 */
export function problemFromResponse(status: number, headers: Headers, body: unknown): Problem {
  const fallbackId = requestIdFrom(headers);
  const err = (body as { error?: Record<string, unknown> } | undefined)?.error;
  if (typeof err !== "object" || err === null) {
    return protocolProblem(status, `HTTP ${status} without an API error body.`, fallbackId);
  }
  const code = err.code;
  if (typeof code !== "string" || !(code in errorCodeStatus)) {
    return protocolProblem(status, `HTTP ${status} with an unknown error code.`, fallbackId);
  }
  const known = code as ErrorCode;
  if (errorCodeStatus[known] !== status) {
    return protocolProblem(status, `Error code ${known} does not belong to HTTP ${status}.`, fallbackId);
  }
  const kind = kindByStatus[status];
  if (kind === undefined) {
    return protocolProblem(status, `HTTP ${status} is not part of the API contract.`, fallbackId);
  }
  const requestId = typeof err.request_id === "string" && REQUEST_ID.test(err.request_id) ? err.request_id : fallbackId;
  const details = Array.isArray(err.details) ? err.details.filter(isDetail) : [];
  return problem(kind, {
    code: known,
    status,
    message: typeof err.message === "string" ? err.message : "",
    requestId,
    details,
    retryAfterSeconds: retryAfter(headers, kind),
  });
}

/** Issues a 422 MODEL_VALIDATION_FAILED carries, in ValidationIssue form for the editor. */
export function validationIssues(p: Problem): ValidationIssue[] {
  return p.details.map((d) => ({
    code: d.code ?? "REQUEST_VALIDATION_FAILED",
    path: d.path,
    message: d.reason,
    severity: d.severity ?? "error",
  }));
}
