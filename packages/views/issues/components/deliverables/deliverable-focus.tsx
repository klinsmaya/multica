"use client";

import { useCallback, useEffect, useMemo, useState, type CSSProperties } from "react";
import { useQuery } from "@tanstack/react-query";
import { Loader2, PanelRight } from "lucide-react";
import {
  collectDeliverableFiles,
  findDeliverableVersion,
} from "@multica/core/attachments/deliverables";
import { issueTimelineOptions } from "@multica/core/issues/queries";
import {
  Dialog,
  DialogContent,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../../i18n";
import {
  ChromeButton,
  ChromeDivider,
  ownsArrowKeys,
} from "../../../editor";
import { useImmersiveMode } from "../../../platform";
import { IssueDetail } from "../issue-detail";
import { AnnotatablePreviewPanel, useDocumentAnnotations } from "./document-annotations";
import { VersionBadge } from "./version-badge";
import { useProgressiveDocument } from "./use-progressive-document";

// The focus covers the whole window, drag strip included, so it opts out of
// the desktop window-drag region itself (see AttachmentPreviewModal).
const NO_DRAG = { WebkitAppRegion: "no-drag" } as CSSProperties;

/**
 * "Open full screen" for one deliverable (MUL-7649): the file takes the
 * window, the issue's discussion sits beside it, and prev / next page through
 * the issue's deliverables. Hosted by `ModalRegistry`, so it works the same
 * from the sidebar, the viewer's info panel and any later entry point.
 */
export function DeliverableFocusModal({
  onClose,
  data,
}: {
  onClose: () => void;
  data: Record<string, unknown> | null;
}) {
  const issueId = typeof data?.issueId === "string" ? data.issueId : "";
  const attachmentId = typeof data?.attachmentId === "string" ? data.attachmentId : "";
  return (
    <Dialog
      open
      onOpenChange={(next) => {
        if (!next) onClose();
      }}
    >
      <DialogContent
        showCloseButton={false}
        style={NO_DRAG}
        className="top-0 left-0 flex h-dvh max-h-none w-screen max-w-none translate-x-0 translate-y-0 flex-row gap-0 overflow-hidden rounded-none bg-background p-0 shadow-none ring-0 sm:max-w-none"
      >
        <DeliverableFocus
          issueId={issueId}
          initialAttachmentId={attachmentId}
          onClose={onClose}
        />
      </DialogContent>
    </Dialog>
  );
}

function wideEnoughForDiscussion(): boolean {
  try {
    return window.matchMedia?.("(min-width: 768px)").matches ?? true;
  } catch {
    return true;
  }
}

function DeliverableFocus({
  issueId,
  initialAttachmentId,
  onClose,
}: {
  issueId: string;
  initialAttachmentId: string;
  onClose: () => void;
}) {
  const { t } = useT("issues");
  // The bar starts at the window's top-left corner, where the macOS traffic
  // lights would sit on the file name.
  useImmersiveMode(true);

  // Deliverables come from the timeline, as everywhere else: the issue-wide
  // attachment list is not refreshed when an upload binds to its comment.
  const { data: timeline, isPending } = useQuery(issueTimelineOptions(issueId));
  const files = useMemo(() => collectDeliverableFiles(timeline ?? []), [timeline]);

  const [attachmentId, setAttachmentId] = useState(initialAttachmentId);
  const [discussionOpen, setDiscussionOpen] = useState(wideEnoughForDiscussion);

  const current = findDeliverableVersion(files, attachmentId);
  const index = current ? files.indexOf(current.file) : -1;

  const goTo = useCallback(
    (target: number) => {
      const file = files[target];
      if (file) setAttachmentId(file.latest.id);
    },
    [files],
  );
  const hasPrev = index > 0;
  const hasNext = index >= 0 && index < files.length - 1;
  const onPrev = useMemo(() => (hasPrev ? () => goTo(index - 1) : undefined), [hasPrev, goTo, index]);
  const onNext = useMemo(() => (hasNext ? () => goTo(index + 1) : undefined), [hasNext, goTo, index]);

  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if (e.metaKey || e.ctrlKey || e.altKey || e.shiftKey) return;
      // A menu or a field in the discussion owns its arrow keys.
      if (e.target instanceof Element && e.target.closest('[role="menu"]')) return;
      if (ownsArrowKeys(e.target)) return;
      if (e.key === "ArrowLeft" && onPrev) {
        e.preventDefault();
        onPrev();
      } else if (e.key === "ArrowRight" && onNext) {
        e.preventDefault();
        onNext();
      }
    };
    document.addEventListener("keydown", handler);
    return () => document.removeEventListener("keydown", handler);
  }, [onPrev, onNext]);

  const attachment = current?.file.versions[current.version - 1];
  const title = attachment?.filename ?? "";
  const documentAnnotations = useDocumentAnnotations({ issueId, attachment, enabled: discussionOpen });
  const progressive = useProgressiveDocument({
    issueId,
    file: current?.file,
    viewedId: attachmentId,
    onFollow: setAttachmentId,
  });
  const versionBadge =
    current && current.file.versions.length > 1 ? (
      <VersionBadge version={current.version} className="bg-secondary" />
    ) : undefined;

  return (
    <>
      <div className="flex h-full min-w-0 flex-1 flex-col bg-black/95">
        {/* Names the dialog for assistive tech; the bar below shows it. */}
        <DialogTitle className="sr-only">{title || t(($) => $.deliverables.section_title)}</DialogTitle>
        {attachment && current ? (
          <AnnotatablePreviewPanel
            documentAnnotations={documentAnnotations}
            attachment={attachment}
            sequence={{ index, total: files.length, onPrev, onNext }}
            titleAccessory={
              progressive.status ? (
                <>
                  {versionBadge}
                  {progressive.status}
                </>
              ) : (
                versionBadge
              )
            }
            renderMarkdown={progressive.renderMarkdown}
            extraActions={
              <>
                <ChromeDivider />
                <ChromeButton
                  label={t(($) => $.deliverables.focus_comments)}
                  pressed={discussionOpen}
                  onClick={() => setDiscussionOpen((open) => !open)}
                >
                  <PanelRight className="size-4" />
                </ChromeButton>
              </>
            }
            onClose={onClose}
          />
        ) : (
          <FocusPlaceholder loading={isPending} onClose={onClose} />
        )}
      </div>
      {discussionOpen && issueId && (
        <aside
          aria-label={t(($) => $.deliverables.focus_comments)}
          className={cn(
            "flex h-full w-[400px] min-w-0 shrink-0 flex-col border-l bg-background",
            "max-md:absolute max-md:inset-y-0 max-md:right-0 max-md:w-full",
          )}
          style={NO_DRAG}
        >
          <IssueDetail
            issueId={issueId}
            variant="thread"
            replyContext={attachment ? { attachmentId: attachment.id, filename: attachment.filename } : undefined}
            onEditAnnotationSource={documentAnnotations.editSource}
          />
        </aside>
      )}
    </>
  );
}

function FocusPlaceholder({ loading, onClose }: { loading: boolean; onClose: () => void }) {
  const { t } = useT("issues");
  return (
    <div className="dark flex h-full flex-col items-center justify-center gap-3 text-body text-muted-foreground">
      {loading ? (
        <Loader2 className="size-4 animate-spin" />
      ) : (
        <>
          <p>{t(($) => $.deliverables.focus_unavailable)}</p>
          <button
            type="button"
            className="rounded-md border border-input bg-secondary px-3 py-1.5 text-foreground transition-colors hover:bg-muted"
            onClick={onClose}
          >
            {t(($) => $.deliverables.overview_close)}
          </button>
        </>
      )}
    </div>
  );
}
