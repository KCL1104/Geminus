// The reader's locale in server components: the remembered choice, or the
// default. Not authorization: any value is only a language.
import { cookies } from "next/headers";
import { localeCookie, parseLocale, type Locale } from "./locale";

export async function requestLocale(): Promise<Locale> {
  return parseLocale((await cookies()).get(localeCookie)?.value);
}
