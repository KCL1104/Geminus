"use client";
import { type ReactNode, useId } from "react";
import { Dialog, Heading, Modal, ModalOverlay } from "react-aria-components";
import { Button } from "./button";
import styles from "./dialog.module.css";

/**
 * Asks before an action that cannot be taken back (a reset, a discard). An
 * alert dialog: focus starts on Cancel, Escape and Cancel close it without
 * acting, and focus returns to what opened it.
 */
export function ConfirmDialog({ isOpen, onOpenChange, heading, children, confirmLabel, cancelLabel, tone = "danger", onConfirm }: {
  isOpen: boolean;
  onOpenChange: (open: boolean) => void;
  heading: ReactNode;
  children: ReactNode;
  confirmLabel: ReactNode;
  cancelLabel: ReactNode;
  tone?: "danger" | "primary";
  onConfirm: () => void;
}) {
  const body = useId();
  return (
    <ModalOverlay isOpen={isOpen} onOpenChange={onOpenChange} isDismissable={false} className={styles.overlay}>
      <Modal className={styles.modal}>
        <Dialog role="alertdialog" aria-describedby={body} className={styles.dialog}>
          {({ close }) => (
            <>
              <Heading slot="title" className={styles.heading}>
                {heading}
              </Heading>
              <div id={body} className={styles.body}>
                {children}
              </div>
              <div className={styles.actions}>
                <Button onPress={close} autoFocus>
                  {cancelLabel}
                </Button>
                <Button
                  variant={tone}
                  onPress={() => {
                    onConfirm();
                    close();
                  }}
                >
                  {confirmLabel}
                </Button>
              </div>
            </>
          )}
        </Dialog>
      </Modal>
    </ModalOverlay>
  );
}
