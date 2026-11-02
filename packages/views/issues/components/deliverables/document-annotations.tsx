"use client";

import { useCallback, useMemo, useState, type ComponentProps } from "react";
import { useAuthStore } from "@multica/core/auth";
import type { Attachment } from "@multica/core/types";
import { AttachmentPreviewPanel } from "../../../editor";
import { useCommentAnnotations } from "../use-comment-annotations";

/** Local source key of a quoted file, like `description:<issueId>` for descriptions. */
export function attachmentAnnotationSourceId(attachmentId: string): string {
  return `attachment:${attachmentId}`;
}

/**
 * Selection annotations over the file shown in the deliverable focus. They
 * join the issue's top-level reply draft (`new:<issueId>`, the one the thread
 * beside the file composes into), so the reply box needs no second store.
 */
export function useDocumentAnnotations({ issueId, attachment, enabled }: {
  issueId: string;
  attachment: Pick<Attachment, "id" | "filename"> | undefined;
  enabled: boolean;
}) {
  const signedIn = useAuthStore((s) => !!s.user);
  const [host, setHost] = useState<HTMLElement | null>(null);
  const attachmentId = attachment?.id;
  const filename = attachment?.filename;
  const sources = useMemo(() => attachmentId && filename ? [{
    id: attachmentAnnotationSourceId(attachmentId),
    name: filename,
    source: { type: "attachment" as const, id: attachmentId, filename },
  }] : [], [attachmentId, filename]);
  const annotations = useCommentAnnotations({
    draftKey: `new:${issueId}`,
    sources,
    enabled: enabled && signedIn && sources.length > 0,
    // Inside the dialog's tree, or its focus trap rejects the note field.
    container: host,
  });
  const { cardRef, editAnnotation } = annotations;
  const hostRef = useCallback((element: HTMLDivElement | null) => {
    cardRef.current = element;
    setHost(element);
  }, [cardRef]);
  const editSource = useCallback((id: string) => editAnnotation(id, true), [editAnnotation]);
  return { annotations, hostRef, editSource, sourceId: sources[0]?.id };
}

export type DocumentAnnotations = ReturnType<typeof useDocumentAnnotations>;

/**
 * The viewer panel with its Markdown or text body open to selection
 * annotations. Other kinds (image, HTML, structured, table) expose no
 * `data-comment-content` region, so nothing is capturable there.
 */
export function AnnotatablePreviewPanel({ documentAnnotations, ...panel }: ComponentProps<typeof AttachmentPreviewPanel> & {
  documentAnnotations: DocumentAnnotations;
}) {
  const { annotations, hostRef, sourceId } = documentAnnotations;
  return (
    <div ref={hostRef} {...annotations.captureProps} className="flex min-h-0 min-w-0 flex-1 flex-col">
      <AttachmentPreviewPanel {...panel} annotationSource={sourceId} />
      {annotations.popup}
    </div>
  );
}
