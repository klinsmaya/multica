"use client";

import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import type { DeliverableFile } from "@multica/core/attachments/deliverables";
import {
  diffParagraphs,
  parsePendingSections,
  writingProgress,
} from "@multica/core/attachments/progressive-document";
import { agentTaskSnapshotOptions } from "@multica/core/agents";
import { useCurrentWorkspace } from "@multica/core/paths";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../../i18n";
import type { MarkdownRenderer } from "../../../editor";
import { useAttachmentHtmlText } from "../../../editor/hooks/use-attachment-html-text";
import { getPreviewKind } from "../../../editor/utils/preview";
import { selectIssueTasks } from "../../surface/activity";
import { ProgressiveMarkdown } from "./progressive-markdown";

// How long the "written just now" marks stay once a version has landed.
const MARK_MS = 8000;
const NONE: number[] = [];

interface Seen {
  key: string;
  id: string;
  text: string;
}

/**
 * The focus view's side of the progressive-document convention
 * (`@multica/core/attachments/progressive-document`): follows the latest
 * version of a Markdown file as the agent re-uploads it, marks what the new
 * version added, and reports how many sections are still pending.
 */
export function useProgressiveDocument({
  issueId,
  file,
  viewedId,
  onFollow,
}: {
  issueId: string;
  file: DeliverableFile | undefined;
  /** The upload on screen. */
  viewedId: string;
  /** Asks the host to show `id`, the new latest version. */
  onFollow: (id: string) => void;
}): { renderMarkdown: MarkdownRenderer; status: ReactNode } {
  const { t } = useT("issues");
  const latest = file?.latest;
  const isMarkdown = !!latest && getPreviewKind(latest.content_type, latest.filename) === "markdown";
  const versionCount = file?.versions.length ?? 0;

  // The same query the preview itself reads, so nothing is fetched twice.
  const latestText = useAttachmentHtmlText(isMarkdown ? latest?.id : null);
  const text = latestText.data?.text;
  const settled = latestText.isSuccess || latestText.isError;

  const lastLatest = useRef<{ key: string; id: string } | null>(null);
  const followTo = useRef<string | null>(null);
  const shown = useRef<Seen | null>(null);
  const [changed, setChanged] = useState<{ id: string; indexes: number[] } | null>(null);

  useEffect(() => {
    if (!file || !latest || !isMarkdown) {
      lastLatest.current = null;
      followTo.current = null;
      shown.current = null;
      return;
    }
    const before = lastLatest.current;
    if (before && before.key === file.key && before.id !== latest.id && viewedId === before.id) {
      followTo.current = latest.id;
    }
    lastLatest.current = { key: file.key, id: latest.id };

    // The new version is shown only once its text is in hand, so the reader's
    // place in the document survives the swap instead of a loading state.
    if (followTo.current === latest.id && settled) {
      followTo.current = null;
      onFollow(latest.id);
    }

    if (text === undefined) return;
    const prev = shown.current;
    if (prev && prev.key === file.key && prev.id !== latest.id) {
      const { changedIndexes } = diffParagraphs(prev.text, text);
      setChanged(changedIndexes.length > 0 ? { id: latest.id, indexes: changedIndexes } : null);
    }
    shown.current = { key: file.key, id: latest.id, text };
  }, [file, latest, isMarkdown, viewedId, settled, text, onFollow]);

  useEffect(() => {
    if (!changed) return;
    const timer = setTimeout(() => setChanged(null), MARK_MS);
    return () => clearTimeout(timer);
  }, [changed]);

  const marks = changed && changed.id === viewedId && changed.id === latest?.id ? changed.indexes : NONE;
  const renderMarkdown = useCallback<MarkdownRenderer>(
    (body, ctx) => (
      <ProgressiveMarkdown
        text={body}
        changedIndexes={marks}
        className={ctx.className}
        attachments={ctx.attachments}
      />
    ),
    [marks],
  );

  // The outline is the first upload; it says how many sections were promised.
  const hasPending = text !== undefined && parsePendingSections(text).length > 0;
  const outlineId = hasPending && versionCount > 1 ? file?.versions[0]?.id : null;
  const outline = useAttachmentHtmlText(outlineId);
  const outlineText = versionCount > 1 ? outline.data?.text : text;
  const progress =
    hasPending && text !== undefined && outlineText !== undefined
      ? writingProgress(
          parsePendingSections(outlineText).length > 0 ? outlineText : text,
          text,
        )
      : null;

  // Queued counts as alive: the next run is what will write the rest.
  const wsId = useCurrentWorkspace()?.id ?? "";
  const { data: agentActive } = useQuery({
    ...agentTaskSnapshotOptions(wsId),
    enabled: !!wsId && hasPending,
    select: (snapshot) => {
      const { running, queued } = selectIssueTasks(snapshot, issueId);
      return running.length + queued.length > 0;
    },
  });

  let status: ReactNode = null;
  if (progress && progress.total > 0) {
    const stalled = agentActive === false;
    const counts = { done: progress.done, total: progress.total };
    status = (
      <span
        data-testid="writing-progress"
        className={cn(
          "shrink-0 rounded-xs bg-secondary px-1 text-micro font-medium tabular-nums",
          stalled ? "text-warning" : "text-muted-foreground",
        )}
      >
        {stalled
          ? t(($) => $.deliverables.progressive.unfinished, counts)
          : t(($) => $.deliverables.progressive.writing, counts)}
      </span>
    );
  }
  return { renderMarkdown, status };
}

