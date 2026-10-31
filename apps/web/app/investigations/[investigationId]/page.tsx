// An AI investigation (H-061 A8, PROPOSED): what the AI did for the person
// who asked, what it found and how it ended; it keeps going in the
// background while the page is closed. Not found unless the build offers
// investigations (NEXT_PUBLIC_GEMINUS_INVESTIGATIONS=1, fixed at build time).
import { notFound } from "next/navigation";
import { InvestigationRoute } from "../../../features/investigations/investigation-view";
import { investigationsOffered } from "../../../features/investigations/flags";
import { RequireSession } from "../../_components/states";

export default async function InvestigationPage({ params }: { params: Promise<{ investigationId: string }> }) {
  if (!investigationsOffered()) notFound();
  const { investigationId } = await params;
  return (
    <RequireSession>
      <InvestigationRoute investigationId={investigationId} />
    </RequireSession>
  );
}
