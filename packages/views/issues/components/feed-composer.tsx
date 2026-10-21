"use client";

import { forwardRef, useCallback, useEffect, useImperativeHandle, useMemo, useRef, useState } from "react";
import { X } from "lucide-react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useAuthStore } from "@multica/core/auth";
import { useWorkspaceId } from "@multica/core/hooks";
import {
  useCreateCommentOnIssue,
  useCreateIssue,
  useMarkIssueRead,
} from "@multica/core/issues/mutations";
import { composeFeedForwardComment } from "@multica/core/issues/feed-forward";
import { parseFeedMessage } from "@multica/core/issues/feed-message";
import {
  feedComposerDraftKey,
  useFeedComposerStore,
  useFeedReplyTargetsStore,
  type FeedReplyTarget,
} from "@multica/core/issues/stores";
import { projectDetailOptions } from "@multica/core/projects/queries";
import type { CreateIssueRequest, Issue } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { SubmitButton } from "@multica/ui/components/common/submit-button";
import { Spinner } from "@multica/ui/components/ui/spinner";
import { cn } from "@multica/ui/lib/utils";
import { ContentEditor, type ContentEditorRef } from "../../editor";
import { useT } from "../../i18n";

interface PendingMessage {
  phase: "sending" | "failed";
  /** "reply" posts a comment to each selected waiting thread instead of creating an issue. */
  mode: "issue" | "reply";
  /** The submitted markdown; a retry sends exactly this, not the live editor. */
  content: string;
  title: string;
}

interface FeedComposerProps {
  projectId: string;
  /** Called once the server has created the issue. */
  onSent?: (issue: Issue) => void;
  /** Called when any send starts, before the server answers. */
  onSendStart?: () => void;
  /** Called once a reply has reached every selected thread, with all of them. */
  onForwarded?: (targets: FeedReplyTarget[]) => void;
}

export interface FeedComposerHandle {
  /** Put the caret in the editor, e.g. from the "new issue" quick action. */
  focus: () => void;
}

/**
 * Bottom composer of the project feed. The first line becomes the title, the
 * rest the description, and an @agent / @squad assigns the new issue; without
 * one it goes to the project lead. The text stays in the editor until the
 * server accepts it, with a pending row above showing the send.
 *
 * With reply targets selected on the feed's "waiting on you" cards, the same
 * text instead goes as one comment per target, behind a quoted origin marker.
 */
export const FeedComposer = forwardRef<FeedComposerHandle, FeedComposerProps>(
  function FeedComposer({ projectId, onSent, onSendStart, onForwarded }, ref) {
    const wsId = useWorkspaceId();
    // A project switch remounts the composer so the editor opens on that
    // project's own draft instead of carrying the previous text over.
    return (
      <FeedComposerBody
        key={feedComposerDraftKey(wsId, projectId)}
        ref={ref}
        wsId={wsId}
        projectId={projectId}
        onSent={onSent}
        onSendStart={onSendStart}
        onForwarded={onForwarded}
      />
    );
  },
);

const FeedComposerBody = forwardRef<FeedComposerHandle, FeedComposerProps & { wsId: string }>(
  function FeedComposerBody({ wsId, projectId, onSent, onSendStart, onForwarded }, ref) {
  const { t } = useT("issues");
  const qc = useQueryClient();
  const createIssue = useCreateIssue();
  const createComment = useCreateCommentOnIssue();
  const { mutate: markIssueRead } = useMarkIssueRead();
  const senderName = useAuthStore((s) => s.user?.name);
  const targetMap = useFeedReplyTargetsStore((s) => s.targets);
  const targets = useMemo(() => Object.values(targetMap), [targetMap]);
  // Threads a reply already reached, kept across retries of the same text so
  // the receipt lists all of them.
  const deliveredRef = useRef<{ content: string; targets: FeedReplyTarget[] }>({
    content: "",
    targets: [],
  });
  const draftKey = feedComposerDraftKey(wsId, projectId);
  const setDraft = useFeedComposerStore((s) => s.setDraft);
  const editorRef = useRef<ContentEditorRef>(null);
  useImperativeHandle(ref, () => ({ focus: () => editorRef.current?.focus() }), []);
  const inFlight = useRef(false);
  const mountedRef = useRef(true);
  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  const [initialDraft] = useState(() => useFeedComposerStore.getState().getDraft(draftKey));
  const [hasText, setHasText] = useState(() => !!initialDraft?.trim());
  const [pending, setPending] = useState<PendingMessage | null>(null);

  // Warm the project so the lead is known by the time the first message goes.
  useQuery(projectDetailOptions(wsId, projectId));

  useEffect(() => {
    const flush = () => {
      const md = editorRef.current?.getMarkdown();
      if (md && md.trim()) setDraft(draftKey, md);
    };
    const onVisibility = () => {
      if (document.visibilityState === "hidden") flush();
    };
    document.addEventListener("visibilitychange", onVisibility);
    window.addEventListener("pagehide", flush);
    return () => {
      document.removeEventListener("visibilitychange", onVisibility);
      window.removeEventListener("pagehide", flush);
    };
  }, [draftKey, setDraft]);

  // Only consume the draft that was sent: text typed while the request was in
  // flight stays in the editor and the store.
  const consumeDraft = useCallback(
    (content: string) => {
      const late = editorRef.current?.flushPendingUpdate?.();
      if (late != null) setDraft(draftKey, late);
      const live = useFeedComposerStore.getState().getDraft(draftKey);
      const untouched = live === undefined || live.trim() === content.trim();
      if (untouched) {
        useFeedComposerStore.getState().clearDraft(draftKey);
        if (mountedRef.current) {
          editorRef.current?.clearContent();
          setHasText(false);
        }
      }
    },
    [draftKey, setDraft],
  );

  const sendIssue = useCallback(
    async (content: string) => {
      const message = parseFeedMessage(content);
      if (!message || inFlight.current) return;
      inFlight.current = true;
      onSendStart?.();
      setPending({ phase: "sending", mode: "issue", content, title: message.title });
      try {
        // Read the lead at send time: the cached project may still be loading.
        const project = await qc.ensureQueryData(projectDetailOptions(wsId, projectId));
        const lead =
          project.lead_type && project.lead_id
            ? { type: project.lead_type, id: project.lead_id }
            : null;
        const assignee = message.assignee ?? lead;
        const request: CreateIssueRequest = {
          title: message.title,
          project_id: projectId,
          ...(message.description ? { description: message.description } : {}),
          ...(assignee ? { assignee_type: assignee.type, assignee_id: assignee.id } : {}),
        };
        const issue = await createIssue.mutateAsync(request);

        consumeDraft(content);
        if (mountedRef.current) setPending(null);
        onSent?.(issue);
      } catch {
        if (mountedRef.current) {
          setPending({ phase: "failed", mode: "issue", content, title: message.title });
        }
      } finally {
        inFlight.current = false;
      }
    },
    [qc, wsId, projectId, createIssue, consumeDraft, onSent, onSendStart],
  );

  const sendReply = useCallback(
    async (content: string) => {
      if (inFlight.current) return;
      const remaining = Object.values(useFeedReplyTargetsStore.getState().targets);
      if (remaining.length === 0) {
        if (mountedRef.current) setPending(null);
        return;
      }
      inFlight.current = true;
      onSendStart?.();
      if (deliveredRef.current.content !== content) {
        deliveredRef.current = { content, targets: [] };
      }
      const identifiers = (list: FeedReplyTarget[]) =>
        list.map((target) => target.identifier).join(", ");
      setPending({ phase: "sending", mode: "reply", content, title: identifiers(remaining) });
      const marker = t(($) => $.feed.composer.reply_marker, { name: senderName ?? "" }).replace(
        /\s*·\s*$/,
        "",
      );
      const body = composeFeedForwardComment(content, marker);
      const failed: FeedReplyTarget[] = [];
      // One at a time: the text can @mention agents, and a stable order keeps
      // the receipt and any retry predictable.
      for (const target of remaining) {
        try {
          await createComment.mutateAsync({
            issueId: target.issueId,
            content: body,
            type: "comment",
            parentId: target.commentId,
          });
          markIssueRead(target.issueId);
          deliveredRef.current.targets.push(target);
          useFeedReplyTargetsStore.getState().deselect(target.issueId);
        } catch {
          failed.push(target);
        }
      }
      inFlight.current = false;

      if (failed.length > 0) {
        if (mountedRef.current) {
          setPending({ phase: "failed", mode: "reply", content, title: identifiers(failed) });
        }
        return;
      }
      const delivered = deliveredRef.current.targets;
      deliveredRef.current = { content: "", targets: [] };
      useFeedReplyTargetsStore.getState().clear();
      consumeDraft(content);
      if (mountedRef.current) setPending(null);
      onForwarded?.(delivered);
    },
    [t, senderName, createComment, markIssueRead, consumeDraft, onSendStart, onForwarded],
  );

  const send = useCallback(
    (content: string, mode: PendingMessage["mode"]) =>
      mode === "reply" ? sendReply(content) : sendIssue(content),
    [sendReply, sendIssue],
  );

  const submit = useCallback(() => {
    const raw = editorRef.current?.getMarkdown() ?? "";
    const content = raw.trim();
    if (content) void send(content, targets.length > 0 ? "reply" : "issue");
  }, [send, targets.length]);

  const sending = pending?.phase === "sending";

  return (
    <div className="shrink-0 border-t bg-background">
      <div className="mx-auto w-full max-w-3xl px-4 py-3">
        {pending && (
          <div
            data-testid="feed-composer-pending"
            className="mb-2 flex items-center gap-2 rounded-lg bg-muted/50 px-3 py-2 text-body"
          >
            <span className="min-w-0 flex-1 truncate">{pending.title}</span>
            {sending ? (
              <>
                <Spinner className="size-3.5 text-muted-foreground" />
                <span className="text-muted-foreground">{t(($) => $.feed.composer.sending)}</span>
              </>
            ) : (
              <>
                <span role="alert" className="text-destructive">
                  {t(($) => $.feed.composer.failed)}
                </span>
                <Button
                  type="button"
                  size="xs"
                  variant="outline"
                  onClick={() => void send(pending.content, pending.mode)}
                >
                  {t(($) => $.feed.composer.retry)}
                </Button>
              </>
            )}
          </div>
        )}
        {targets.length > 0 && (
          <div
            data-testid="feed-composer-targets"
            className="mb-2 flex flex-wrap items-center gap-1.5 text-caption"
          >
            <span className="font-medium text-brand">
              {t(($) => $.feed.composer.reply_to, { count: targets.length })}
            </span>
            {targets.map((target) => (
              <span
                key={target.issueId}
                className="inline-flex items-center gap-1 rounded-md bg-brand/10 py-0.5 pl-2 pr-1 text-brand"
              >
                {target.identifier}
                <button
                  type="button"
                  disabled={sending}
                  aria-label={t(($) => $.feed.composer.reply_remove, {
                    identifier: target.identifier,
                  })}
                  className="rounded-sm p-0.5 hover:bg-brand/15 disabled:opacity-50"
                  onClick={() => useFeedReplyTargetsStore.getState().deselect(target.issueId)}
                >
                  <X className="size-3" />
                </button>
              </span>
            ))}
            <Button
              type="button"
              variant="ghost"
              size="xs"
              disabled={sending}
              onClick={() => useFeedReplyTargetsStore.getState().clear()}
            >
              {t(($) => $.feed.composer.reply_clear)}
            </Button>
          </div>
        )}
        <div className="relative flex items-end gap-2 rounded-lg bg-card py-1 pl-3 pr-1.5 ring-1 ring-border">
          <div
            className={cn(
              "max-h-40 min-w-0 flex-1 overflow-y-auto py-1",
              sending && "pointer-events-none opacity-60",
            )}
            aria-busy={sending || undefined}
          >
            <ContentEditor
              ref={editorRef}
              defaultValue={initialDraft}
              placeholder={
                targets.length > 0
                  ? t(($) => $.feed.composer.reply_placeholder, { count: targets.length })
                  : t(($) => $.feed.composer.placeholder)
              }
              onUpdate={(md) => {
                setHasText(!!md.trim());
                setDraft(draftKey, md);
              }}
              onSubmit={submit}
              submitOnEnter
              showBubbleMenu={false}
              debounceMs={100}
              flushPendingOnUnmount
            />
          </div>
          <SubmitButton
            onClick={submit}
            disabled={!hasText}
            loading={sending}
            ariaLabel={t(($) => $.feed.composer.send)}
          />
        </div>
      </div>
    </div>
  );
},
);
