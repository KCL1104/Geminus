// /api/v1/...: the same-origin proxy to the Go API (ADR-018). The browser
// never holds a token; the BFF adds the session's.
import { withBff } from "../../../../lib/session/server";

export const dynamic = "force-dynamic";

type Context = { params: Promise<{ path: string[] }> };

async function forward(request: Request, ctx: Context): Promise<Response> {
  const { path } = await ctx.params;
  return withBff((h) => h.proxy(request, path));
}

export const GET = forward;
export const HEAD = forward;
export const POST = forward;
export const PATCH = forward;
export const PUT = forward;
export const DELETE = forward;
