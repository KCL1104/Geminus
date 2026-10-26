// The reader's theme in server components: the remembered choice, or the
// system's. Not authorization: any value is only a colour theme.
import { cookies } from "next/headers";
import { parseTheme, type Theme, themeCookie } from "./theme";

export async function requestTheme(): Promise<Theme> {
  return parseTheme((await cookies()).get(themeCookie)?.value);
}
