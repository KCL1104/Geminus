// H-062: a model made from a photo or floor plan is created first and its
// photo sent after. When the photo was not sent, the new-model page opens
// the model and leaves it the refusal here, for the model page to say so
// beside its own photo button. The note lives in this tab's memory only (no
// URL, no storage): the model page reads it when it opens and clears it, so
// it is shown once, and a reload or a later visit shows the model as it is.
import type { PhotoRefusal } from "../ai-proposals/photo-form";

const notes = new Map<string, PhotoRefusal>();

/** The photo of the new model modelId was not sent, for refusal. */
export function notePhotoNotSent(modelId: string, refusal: PhotoRefusal): void {
  notes.set(modelId, refusal);
}

/** The refusal left for modelId, or null. Reading does not clear it (React may run a state initializer twice). */
export function photoNotSent(modelId: string): PhotoRefusal | null {
  return notes.get(modelId) ?? null;
}

/** Forgets the refusal left for modelId. */
export function clearPhotoNotSent(modelId: string): void {
  notes.delete(modelId);
}
