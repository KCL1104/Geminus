// GET /auth/callback: the provider's redirect back after sign-in (ADR-018).
import { withBff } from "../../../lib/session/server";

export const dynamic = "force-dynamic";

export function GET(request: Request): Promise<Response> {
  return withBff((h) => h.callback(request));
}
