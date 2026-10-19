// GET /auth/login?return_to=/path: starts a sign-in at the provider (ADR-018).
import { withBff } from "../../../lib/session/server";

export const dynamic = "force-dynamic";

export function GET(request: Request): Promise<Response> {
  return withBff((h) => h.login(request));
}
