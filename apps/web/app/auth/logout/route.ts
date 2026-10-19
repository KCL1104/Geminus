// POST /auth/logout: ends the session and returns the provider's logout URL (ADR-018).
import { withBff } from "../../../lib/session/server";

export const dynamic = "force-dynamic";

export function POST(request: Request): Promise<Response> {
  return withBff((h) => h.logout(request));
}
