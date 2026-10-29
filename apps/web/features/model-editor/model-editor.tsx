"use client";
// The 2D model editor (T-015, hackathon slice; H-027 Q2 flow, DESIGN.md
// Simplicity). The server is the source of truth: the page shows the saved
// draft with the pending edits applied (one IR, the draft session's ops),
// Save sends them as JSON Patch with If-Match, a 409 keeps them and offers
// to reload and reapply them (only when asked, and only where the newer
// revision did not change what they change), and Publish stays blocked,
// with its reason beside it, while the saved draft has errors or anything
// is unsaved. One primary action (Publish); the step panel is the Add step
// menu over the canvas; the properties panel is the one secondary panel.
import { ReactFlowProvider, useReactFlow } from "@xyflow/react";
import { type ReactNode, useCallback, useEffect, useId, useMemo, useRef, useState, useSyncExternalStore } from "react";
import { ProblemView } from "../../app/_components/problem-view";
import { ReadOnlyNotice } from "../../app/_components/states";
import { newIdempotencyKey } from "../../lib/api/client";
import type { Problem } from "../../lib/api/problem";
import { useApi } from "../../lib/api/provider";
import { canEdit, useAuth } from "../../lib/auth/session";
import type { Model, ModelIR } from "../../lib/contracts";
import { useI18n } from "../../lib/i18n/provider";
import { canPublish, type DraftEvent, type DraftSession, nextSave, pendingOps } from "../../lib/state/draft";
import { createSelectionStore } from "../../lib/state/selection";
import { useSelectedEntity } from "../../lib/state/use-selection";
import { Button } from "../../ui/button";
import { Callout } from "../../ui/callout";
import { ConfirmDialog } from "../../ui/dialog";
import { CloseIcon, FitIcon, PlusIcon, PublishIcon, SaveIcon } from "../../ui/icons";
import { ActionMenu } from "../../ui/menu";
import { type Fact, PageHeader } from "../../ui/page-header";
import { StatusRegion } from "../../ui/status";
import { Canvas, READABLE_ZOOM } from "./canvas";
import { addStep, connectionsOf, connectSteps, deleteStep, disconnect, type EditResult, moveStep, nodeTypes, renameStep, stepLabel } from "./edits";
import styles from "./editor.module.css";
import { IssueList } from "./issue-list";
import { locateIssues, type StepIssue, worst } from "./issues";
import { applyPatch, type Op, reapplyPatch } from "./patch";
import { Properties } from "./properties";
import { Segmented } from "../../ui/segmented";
import { Twin3D } from "../twin-3d/viewer";

const WIDE = "(min-width: 721px)";
const subscribeWide = (cb: () => void) => {
  const m = window.matchMedia(WIDE);
  m.addEventListener("change", cb);
  return () => m.removeEventListener("change", cb);
};
/** Topology is edited on a computer, not a phone (DESIGN.md Layout; H-012). */
function useWide(): boolean {
  return useSyncExternalStore(subscribeWide, () => window.matchMedia(WIDE).matches, () => true);
}

type Dialog = { kind: "delete"; id: string } | { kind: "discard" } | { kind: "publish" } | null;

/** The issues a refused save named in the candidate it built (422 details that point into the IR). */
function saveIssues(candidate: ModelIR, problem: Problem): StepIssue[] {
  return locateIssues(
    candidate,
    problem.details.map((d) => ({ code: d.code ?? "", path: d.path, message: d.reason, severity: d.severity ?? "error" })),
  ).filter((i) => i.target !== null);
}

export function ModelEditor(props: EditorProps) {
  return (
    <ReactFlowProvider>
      <Editor {...props} />
    </ReactFlowProvider>
  );
}

type EditorProps = {
  model: Model;
  session: DraftSession;
  dispatch: (e: DraftEvent) => void;
  /** Loads the model and its draft again (after a publish). */
  reloadPage: () => Promise<Problem | null>;
  /** The page's header band (H-040): the way back and the model's facts; the editor adds the draft and its actions. */
  header: { trail: { href: string; label: string }[]; facts: Fact[] };
  /** The AI request (T-031), at the top of the side panel above the step's properties (H-040). */
  ask?: ReactNode;
};

function Editor({ model, session, dispatch, reloadPage, header, ask }: EditorProps) {
  const api = useApi();
  const { role } = useAuth();
  const { t, gap } = useI18n();
  const wide = useWide();
  const flow = useReactFlow();
  const statusId = useId();
  const reasonId = useId();
  const canvasRef = useRef<HTMLDivElement>(null);
  const addRef = useRef<HTMLDivElement>(null);
  const saveRef = useRef<HTMLButtonElement>(null);
  const connectRef = useRef<HTMLDivElement>(null);

  const remote = session.remote;
  const pending = pendingOps(session);
  // The session changes only on an event, so the working IR is computed once per change.
  // In a conflict the edits are shown as reapplying would put them (reapplyPatch: nothing
  // someone else changed is overwritten, T-015 review r1 M1).
  const applied = useMemo(() => (session.status === "conflict" ? reapplyPatch : applyPatch)(session.remote.ir, pendingOps(session)), [session]);
  // After a reload in a conflict the edits may no longer apply: the page then shows the server's draft.
  const working = applied.ok ? applied.doc : remote.ir;
  const editable = canEdit(role) && session.status !== "conflict";

  const [store] = useState(() => createSelectionStore(working));
  useEffect(() => store.setModel(working), [store, working]);
  const selected = useSelectedEntity(store);
  const node = working.nodes.find((n) => n.id === selected) ?? null;

  const [outcome, setOutcome] = useState<string | null>(null);
  // The canvas's projection (T-033): the 2D editor, or the read-only 3D view of the same steps.
  const [view, setView] = useState<"2d" | "3d">("2d");
  const [refused, setRefused] = useState<StepIssue[]>([]);
  const [publishProblem, setPublishProblem] = useState<Problem | null>(null);
  const [reloadProblem, setReloadProblem] = useState<Problem | null>(null);
  const [reapplyFailed, setReapplyFailed] = useState(false);
  const [dialog, setDialog] = useState<Dialog>(null);
  const [nameFocus, setNameFocus] = useState<string | null>(null);
  const [base, setBase] = useState<{ id: string; hash: string } | null>(null);
  const sent = useRef<Op[]>([]);
  const publishKey = useRef<{ revision: number; key: string } | null>(null);

  // The base version's hash says whether the draft has anything to publish.
  useEffect(() => {
    const ctl = new AbortController();
    api.getModelVersion(remote.base_version_id, { signal: ctl.signal }).then((r) => {
      if (r.ok && !ctl.signal.aborted) setBase({ id: r.data.id, hash: r.data.hash });
    });
    return () => ctl.abort();
  }, [api, remote.base_version_id]);

  const draftIssues = useMemo(() => locateIssues(remote.ir, remote.issues), [remote.ir, remote.issues]);
  const stepIssues = useMemo(() => [...draftIssues, ...refused], [draftIssues, refused]);
  const severity = useCallback((id: string) => worst(stepIssues.filter((i) => i.step === id)), [stepIssues]);
  const errors = remote.issues.filter((i) => i.severity === "error").length;

  const focusStep = useCallback((id: string) => {
    setNameFocus(null);
    store.select(id);
    const n = flow.getNode(id);
    if (n) flow.setCenter(n.position.x + (n.measured?.width ?? 0) / 2, n.position.y + (n.measured?.height ?? 0) / 2, { zoom: Math.max(flow.getZoom(), READABLE_ZOOM) });
    requestAnimationFrame(() => canvasRef.current?.querySelector<HTMLElement>(`.react-flow__node[data-id="${CSS.escape(id)}"]`)?.focus());
  }, [flow, store]);

  /** Queues an edit's ops; returns the IR they lead to. */
  const edit = (r: EditResult): ModelIR | null => {
    if (!r.ok || r.ops.length === 0) return null;
    const next = applyPatch(working, r.ops);
    if (!next.ok) return null;
    setOutcome(null);
    setPublishProblem(null);
    dispatch({ type: "edit", ops: r.ops });
    store.setModel(next.doc);
    return next.doc;
  };

  const add = (type: (typeof nodeTypes)[number]) => {
    const r = addStep(working, type);
    if (!r.ok || r.id === undefined || edit(r) === null) return;
    store.select(r.id);
    setNameFocus(r.id);
    setOutcome(t("editor.added", { id: r.id }));
  };
  const connect = (from: string, to: string) => {
    const r = connectSteps(working, from, to);
    if (edit(r) === null) return;
    const target = working.nodes.find((n) => n.id === to);
    setOutcome(t("editor.connected", { to: target ? stepLabel(target) : to }));
  };
  const remove = (flowId: string) => {
    const f = working.flows.find((x) => x.id === flowId);
    const target = working.nodes.find((n) => n.id === f?.to);
    if (edit(disconnect(working, flowId)) === null) return;
    setOutcome(t("editor.disconnected", { to: target ? stepLabel(target) : (f?.to ?? flowId) }));
    requestAnimationFrame(() => connectRef.current?.querySelector("button")?.focus());
  };
  const confirmDelete = (id: string) => {
    const n = working.nodes.find((x) => x.id === id);
    if (n === undefined || edit(deleteStep(working, id)) === null) return;
    setOutcome(t("editor.deleted", { name: stepLabel(n) }));
    requestAnimationFrame(() => addRef.current?.querySelector("button")?.focus());
  };

  const save = async () => {
    const next = nextSave(session);
    if (next === null) return;
    sent.current = next.ops;
    setOutcome(null);
    setPublishProblem(null);
    dispatch({ type: "saveStarted", revision: next.revision, ops: next.ops });
    const r = await api.patchDraft(model.id, next.revision, next.ops);
    if (r.ok) {
      setRefused([]);
      dispatch({ type: "saveSucceeded", draft: r.data });
      setOutcome(t("editor.saved", { revision: String(r.data.revision) }));
    } else {
      const candidate = applyPatch(remote.ir, sent.current);
      setRefused(candidate.ok ? saveIssues(candidate.doc, r.problem) : []);
      dispatch({ type: "saveFailed", problem: r.problem });
    }
  };

  const reapply = async () => {
    setReloadProblem(null);
    setReapplyFailed(false);
    const r = await api.getDraft(model.id);
    if (!r.ok) {
      setReloadProblem(r.problem);
      return;
    }
    dispatch({ type: "remoteLoaded", draft: r.data });
    // Each change tests the value it replaces and each add needs its member
    // still absent, so edits to anything changed in the newer revision do not
    // apply: then only discarding them is offered (REQ-006, no silent overwrite).
    if (reapplyPatch(r.data.ir, pending).ok) {
      dispatch({ type: "reapplyLocal" });
      setOutcome(t("editor.reapplied", { revision: String(r.data.revision) }));
      requestAnimationFrame(() => saveRef.current?.focus());
    } else {
      setReapplyFailed(true);
    }
  };

  const discard = () => {
    dispatch({ type: "discardLocal" });
    setRefused([]);
    setReapplyFailed(false);
    setOutcome(t("editor.discarded"));
    requestAnimationFrame(() => saveRef.current?.focus());
  };

  const publish = async () => {
    const revision = remote.revision;
    if (publishKey.current?.revision !== revision) publishKey.current = { revision, key: newIdempotencyKey() };
    setPublishProblem(null);
    const r = await api.publishVersion(model.id, revision, { expected_base_version_id: remote.base_version_id }, publishKey.current.key);
    if (!r.ok) {
      setPublishProblem(r.problem);
      return;
    }
    publishKey.current = null;
    setOutcome(t("editor.published", { version: String(r.data.version) }));
    const failed = await reloadPage();
    if (failed) setPublishProblem(failed);
  };

  // Why Publish is blocked, said beside it (DESIGN.md: a blocked action's reason).
  const unchanged = base !== null && base.id === remote.base_version_id && base.hash === remote.hash;
  const nextVersion = model.latest_version.version + 1;
  const publishReason =
    pending.length > 0 || session.status !== "clean"
      ? t("editor.publish.saveFirst")
      : remote.out_of_date
        ? t("editor.publish.outOfDate")
        : errors > 0
          ? t("editor.publish.errors", { count: errors })
          : unchanged
            ? t("editor.publish.noChanges", { version: String(model.latest_version.version) })
            : null;
  const publishable = publishReason === null && canPublish(session);
  const saveable = nextSave(session) !== null;

  const statusLine = (
    <p role="status" id={statusId} className={styles.status} data-draft-status={session.status}>
      {t(`draft.status.${session.status}`)}
      {gap}
      {t("draft.revision", { revision: String(remote.revision) })}
    </p>
  );

  const band = (actions?: ReactNode) => (
    <PageHeader trail={header.trail} title={model.name} facts={[...header.facts, { label: t("band.draft"), value: statusLine }]} actions={actions} />
  );

  if (!wide) {
    return (
      <section aria-label={t("editor.label")} className={styles.editor}>
        {band()}
        <Callout tone="info">{t("editor.narrow")}</Callout>
        {/* A phone edits no topology, but the AI can still be asked (review M4). */}
        {ask}
      </section>
    );
  }

  const deleting = dialog?.kind === "delete" ? working.nodes.find((n) => n.id === dialog.id) : undefined;
  const deletingLinks = deleting ? connectionsOf(working, deleting.id).length : 0;
  const addable = nodeTypes.filter((type) => type !== "source" || working.entity_types.length > 0);

  return (
    <section aria-label={t("editor.label")} className={styles.editor}>
      {band(
        canEdit(role) && (
          <>
            {publishReason !== null && (
              <p id={reasonId} className={styles.reason}>
                {publishReason}
              </p>
            )}
            {pending.length > 0 && session.status !== "saving" && (
              <Button variant="danger" icon={<CloseIcon />} onPress={() => setDialog({ kind: "discard" })}>
                {t("draft.discard")}
              </Button>
            )}
            <Button ref={saveRef} icon={<SaveIcon />} blocked={!saveable} aria-describedby={saveable ? undefined : statusId} onPress={save}>
              {t("editor.save")}
            </Button>
            <Button
              variant="primary"
              icon={<PublishIcon />}
              blocked={!publishable}
              aria-describedby={publishReason === null ? undefined : reasonId}
              onPress={() => setDialog({ kind: "publish" })}
            >
              {t("editor.publish")}
            </Button>
          </>
        ),
      )}

      <ReadOnlyNotice role={role} />
      {remote.out_of_date && <Callout tone="warning">{t("draft.outOfDate")}</Callout>}
      {session.problem && <ProblemView problem={session.problem} />}
      {session.status === "conflict" && canEdit(role) && (
        <div className={styles.conflict}>
          <p>{t("draft.kept")}</p>
          {reapplyFailed ? <Callout tone="warning">{t("editor.reapplyFailed")}</Callout> : <Button onPress={reapply}>{t("editor.reapply")}</Button>}
        </div>
      )}
      {reloadProblem && <ProblemView problem={reloadProblem} />}
      {publishProblem && <ProblemView problem={publishProblem} />}
      <StatusRegion message={outcome} />

      <div className={styles.stage}>
        <div className={styles.column}>
          <div className={styles.toolbar}>
            {/* 3D is a read-only projection (T-033): no step is added or fitted there. */}
            {view === "2d" && (
              <div className={styles.tools}>
                {editable && (
                  <div ref={addRef}>
                    <ActionMenu label={t("editor.addStep")} icon={<PlusIcon />} items={addable.map((type) => ({ id: type, label: t(`editor.type.${type}`) }))} onAction={(id) => add(id as (typeof nodeTypes)[number])} />
                  </div>
                )}
                <Button variant="quiet" icon={<FitIcon />} onPress={() => void flow.fitView({ padding: 0.1 })}>
                  {t("editor.fit")}
                </Button>
              </div>
            )}
            <div className={styles.views}>
              <Segmented label={t("editor.view")} items={[{ id: "2d", label: t("editor.view.2d") }, { id: "3d", label: t("editor.view.3d") }]} value={view} onChange={setView} />
            </div>
          </div>
          {view === "3d" ? (
            <div className={styles.twin3d}>
              <Twin3D ir={working} selected={selected} onSelect={(id) => store.select(id)} />
            </div>
          ) : (
          <div ref={canvasRef}>
            <Canvas
              ir={working}
              selected={selected}
              severity={severity}
              editable={editable}
              onSelect={(id) => {
                // The name takes the focus only right after its step was added.
                if (id !== nameFocus) setNameFocus(null);
                store.select(id);
              }}
              onMove={(id, to) => edit(moveStep(working, id, to))}
              onConnect={connect}
              onDelete={(id) => setDialog({ kind: "delete", id })}
            />
          </div>
          )}
          <IssueList
            issues={draftIssues}
            revision={remote.revision}
            unsaved={pending.length > 0}
            stepName={(id) => {
              const n = working.nodes.find((x) => x.id === id);
              return n ? stepLabel(n) : null;
            }}
            onGo={focusStep}
          />
        </div>
        <div className={styles.side}>
        {ask}
        <Properties
          key={node?.id ?? ""}
          ir={working}
          node={node}
          issues={stepIssues.filter((i) => node !== null && (i.step === node.id || (i.target?.kind === "resource" && node.type === "process" && node.requires.some((r) => r.resource_id === i.target!.id))))}
          editable={editable}
          autoFocusName={node !== null && nameFocus === node.id}
          connectRef={connectRef}
          onEdit={edit}
          onRename={(name) => node && edit(renameStep(working, node.id, name))}
          onConnect={(to) => node && connect(node.id, to)}
          onDisconnect={remove}
          onDelete={() => node && setDialog({ kind: "delete", id: node.id })}
        />
        </div>
      </div>

      <ConfirmDialog
        isOpen={dialog?.kind === "delete" && deleting !== undefined}
        onOpenChange={(open) => !open && setDialog(null)}
        heading={t("editor.delete.heading", { name: deleting ? stepLabel(deleting) : "" })}
        confirmLabel={t("editor.delete.confirm")}
        cancelLabel={t("editor.cancel")}
        onConfirm={() => deleting && confirmDelete(deleting.id)}
      >
        <p>{deletingLinks === 0 ? t("editor.delete.bodyNone") : t("editor.delete.body", { count: deletingLinks })}</p>
      </ConfirmDialog>
      <ConfirmDialog
        isOpen={dialog?.kind === "discard"}
        onOpenChange={(open) => !open && setDialog(null)}
        heading={t("editor.discard.heading")}
        confirmLabel={t("editor.discard.confirm")}
        cancelLabel={t("editor.keepEditing")}
        onConfirm={discard}
      >
        <p>{t("editor.discard.body", { revision: String(remote.revision) })}</p>
      </ConfirmDialog>
      <ConfirmDialog
        isOpen={dialog?.kind === "publish"}
        onOpenChange={(open) => !open && setDialog(null)}
        heading={t("editor.publish.heading", { version: String(nextVersion) })}
        confirmLabel={t("editor.publish.confirm")}
        cancelLabel={t("editor.cancel")}
        tone="primary"
        onConfirm={() => void publish()}
      >
        <p>{t("editor.publish.body", { version: String(nextVersion), previous: String(model.latest_version.version) })}</p>
      </ConfirmDialog>
    </section>
  );
}
