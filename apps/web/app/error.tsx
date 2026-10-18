"use client";
// A route that failed to render, inside the root layout (and its locale).
import { CrashNotice } from "./_components/crash";

export default function ErrorPage({ error, retry }: { error: Error & { digest?: string }; retry: () => void }) {
  return <CrashNotice error={error} retry={retry} />;
}
