// Every response's security headers (T-057, lib/security/headers.ts): pages,
// the BFF's route handlers (/auth/*, /api/v1/*) and static files alike, so
// there is no matcher. A new nonce per request goes on the response's
// Content-Security-Policy and on the request: Next.js reads it from the
// request's policy for its scripts, the root layout from NONCE_HEADER for
// React Aria's style elements. A client's own values of both are replaced.
// Pages render per request (the root layout reads cookies), so each page
// carries its request's nonce.
import { type NextRequest, NextResponse } from "next/server";
import { contentSecurityPolicy, newNonce, NONCE_HEADER, type PolicyOptions, securityHeaders, servedOverHttps } from "./lib/security/headers";

export function proxy(request: NextRequest): NextResponse {
  const options: PolicyOptions = {
    nonce: newNonce(),
    https: servedOverHttps(process.env.BFF_APP_ORIGIN),
    development: process.env.NODE_ENV === "development",
  };
  const forward = new Headers(request.headers);
  forward.set(NONCE_HEADER, options.nonce);
  forward.set("content-security-policy", contentSecurityPolicy(options));
  const response = NextResponse.next({ request: { headers: forward } });
  for (const [name, value] of securityHeaders(options)) response.headers.set(name, value);
  return response;
}
