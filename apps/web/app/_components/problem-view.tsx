// How every API failure is shown (T-014): one alert per Problem, with the
// server's request ID when it sent one. 404 says the same for "missing" and
// "another tenant's" so the UI never confirms that a foreign resource exists.
import type { Problem, ProblemKind } from "../../lib/api/problem";

export const problemHeadings: Record<ProblemKind, string> = {
  unauthenticated: "Sign in required",
  forbidden: "You do not have permission to do this",
  not_found: "Not found",
  conflict: "This changed since you loaded it",
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
  conflict: "Someone else changed it. Nothing was overwritten; reload to see the current version.",
  precondition: "The request did not say which version it changes.",
};

export function ProblemView({ problem }: { problem: Problem }) {
  return (
    <section role="alert" data-problem={problem.kind}>
      <h2>{problemHeadings[problem.kind]}</h2>
      {guidance[problem.kind] && <p>{guidance[problem.kind]}</p>}
      {problem.message && problem.kind !== "not_found" && <p>{problem.message}</p>}
      {problem.details.length > 0 && (
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
