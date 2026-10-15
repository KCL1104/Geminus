// How every API failure is shown (T-014): one alert per Problem, with the
// server's request ID when it sent one. 404 says the same for "missing" and
// "another tenant's" so the UI never confirms that a foreign resource exists:
// neither its message nor its details are shown.
import type { Problem, ProblemKind } from "../../lib/api/problem";
import type { ErrorCode } from "../../lib/contracts";

export const problemHeadings: Record<ProblemKind, string> = {
  unauthenticated: "Sign in required",
  forbidden: "You do not have permission to do this",
  not_found: "Not found",
  conflict: "This conflicts with the current state",
  invalid: "The request was not accepted",
  precondition: "Reload before changing this",
  rate_limited: "Too many requests",
  unavailable: "Service unavailable",
  internal: "The server failed to handle the request",
  network: "The API could not be reached",
  protocol: "Unexpected response from the API",
};

const guidance: Partial<Record<ProblemKind, string>> = {
  unauthenticated: "Your session is missing or has expired.",
  forbidden: "Your role in this organization does not allow this action.",
  not_found: "It does not exist, or it is not available in this organization.",
  precondition: "The request did not say which version it changes.",
};

// A 409 means different things; say what this one means (D-030).
const conflictGuidance: Partial<Record<ErrorCode, string>> = {
  MODEL_VERSION_CONFLICT: "Someone else changed it. Nothing was overwritten; reload to see the current version.",
  IDEMPOTENCY_KEY_CONFLICT: "This request was already sent with different content.",
  LIFECYCLE_CONFLICT: "This is not possible in its current state.",
  RESULT_NOT_READY: "The result is not ready yet.",
  PROPOSAL_ALREADY_DECIDED: "This proposal has already been decided.",
};

function guidanceFor(p: Problem): string | undefined {
  return p.kind === "conflict" && p.code !== null ? conflictGuidance[p.code] : guidance[p.kind];
}

export function ProblemView({ problem }: { problem: Problem }) {
  return (
    <section role="alert" data-problem={problem.kind}>
      <h2>{problemHeadings[problem.kind]}</h2>
      {guidanceFor(problem) && <p>{guidanceFor(problem)}</p>}
      {problem.message && problem.kind !== "not_found" && <p>{problem.message}</p>}
      {problem.details.length > 0 && problem.kind !== "not_found" && (
        <ul>
          {problem.details.map((d, i) => (
            <li key={i}>
              <code>{d.path || "/"}</code> {d.reason}
            </li>
          ))}
        </ul>
      )}
      {problem.retryAfterSeconds !== null && <p>Try again in {problem.retryAfterSeconds} s.</p>}
      {problem.requestId && (
        <p>
          Request ID: <code>{problem.requestId}</code>
        </p>
      )}
    </section>
  );
}
