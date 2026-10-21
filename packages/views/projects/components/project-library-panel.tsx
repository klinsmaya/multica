"use client";

import { useMemo, useRef, useState, type ChangeEvent } from "react";
import { useQueries, useQueryClient, type UseQueryResult } from "@tanstack/react-query";
import { ChevronRight, LayoutGrid, List, Loader2, Plus } from "lucide-react";
import { toast } from "sonner";
import { api } from "@multica/core/api";
import { useWorkspaceId } from "@multica/core/hooks";
import { toUploadResult } from "@multica/core/hooks/use-file-upload";
import { useCreateCommentOnIssue } from "@multica/core/issues/mutations";
import { useModalStore } from "@multica/core/modals";
import { MAX_FILE_SIZE } from "@multica/core/constants/upload";
import {
  projectAttachmentKeys,
  projectAttachmentsOptions,
  useProjectLibraryViewStore,
  type ProjectLibraryViewMode,
} from "@multica/core/projects";
import type { ListProjectAttachmentsResponse, ProjectAttachment } from "@multica/core/types";
import { useActorName } from "@multica/core/workspace/hooks";
import { Button } from "@multica/ui/components/ui/button";
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@multica/ui/components/ui/collapsible";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { cn } from "@multica/ui/lib/utils";
import { formatBytes } from "../../common/format-bytes";
import { escapeMarkdownLabel } from "../../editor/utils/escape-markdown-label";
import { useT, useTimeAgo } from "../../i18n";
import {
  DELIVERABLE_CATEGORIES,
  deliverableCategory,
  type DeliverableCategory,
} from "../../issues/components/deliverables/deliverable-kind";
import { DeliverableThumbnail } from "../../issues/components/deliverables/deliverable-thumbnail";
import { useOpenFeedIssue } from "../../issues/components/feed-message-card";

const PAGE_SIZE = 40;

type Filter = "all" | DeliverableCategory;

// Pages are loaded one query each; this folds them into one list. A row that
// moved between pages while the library changed is kept once.
function combinePages(results: UseQueryResult<ListProjectAttachmentsResponse>[]) {
  const seen = new Set<string>();
  const rows: ProjectAttachment[] = [];
  for (const result of results) {
    for (const row of result.data?.attachments ?? []) {
      if (seen.has(row.id)) continue;
      seen.add(row.id);
      rows.push(row);
    }
  }
  const first = results[0];
  return {
    rows,
    firstPage: first?.data,
    isLoading: !first?.data && (first?.isPending ?? true),
    isError: !first?.data && !!first?.isError,
    loadingMore: results.some((r) => r.isFetching && !r.data),
  };
}

/**
 * The project's file library: everything its issues carry, in two groups. An
 * agent's uploads are deliverables, a member's are files. Rows open in the
 * full-screen deliverable focus; "Add" uploads to the project's own
 * "Project files" issue and announces the upload with a comment there.
 */
export function ProjectLibraryPanel({ projectId }: { projectId: string }) {
  const { t } = useT("projects");
  const { t: tIssues } = useT("issues");
  const wsId = useWorkspaceId();
  const qc = useQueryClient();
  const viewMode = useProjectLibraryViewStore((s) => s.viewMode);
  const setViewMode = useProjectLibraryViewStore((s) => s.setViewMode);
  const openModal = useModalStore((s) => s.open);
  const createComment = useCreateCommentOnIssue();

  const [filter, setFilter] = useState<Filter>("all");
  const [pages, setPages] = useState(1);
  const [uploading, setUploading] = useState(false);
  const fileInput = useRef<HTMLInputElement>(null);

  // One page per query keeps every page cached and refetched independently;
  // the first page's totals label both groups without a second round trip.
  const { rows, firstPage, isLoading, isError, loadingMore } = useQueries({
    queries: Array.from({ length: pages }, (_, page) =>
      projectAttachmentsOptions(wsId, projectId, { limit: PAGE_SIZE, offset: page * PAGE_SIZE }),
    ),
    combine: combinePages,
  });

  const visible = useMemo(
    () =>
      filter === "all"
        ? rows
        : rows.filter((row) => deliverableCategory(row.content_type, row.filename) === filter),
    [rows, filter],
  );
  const deliverables = visible.filter((row) => row.uploader_type === "agent");
  const files = visible.filter((row) => row.uploader_type !== "agent");

  const deliverableTotal = firstPage?.deliverable_total ?? 0;
  const uploadTotal = firstPage?.upload_total ?? 0;
  const total = deliverableTotal + uploadTotal;
  const hasMore = rows.length < total;
  const filtering = filter !== "all";

  const open = (row: ProjectAttachment) =>
    openModal("deliverable-focus", { issueId: row.issue.id, attachmentId: row.id });

  const handlePick = async (event: ChangeEvent<HTMLInputElement>) => {
    const picked = Array.from(event.target.files ?? []);
    event.target.value = "";
    if (picked.length === 0 || uploading) return;
    setUploading(true);
    let failure: unknown = null;
    const uploaded = [];
    try {
      const holder = await api.getProjectLibraryIssue(projectId);
      if (!holder.id) throw new Error("Project files issue unavailable");
      for (const file of picked) {
        if (file.size > MAX_FILE_SIZE) {
          failure = new Error("File exceeds 100 MB limit");
          break;
        }
        try {
          uploaded.push(toUploadResult(await api.uploadFile(file, { issueId: holder.id })));
        } catch (err) {
          failure = err;
          break;
        }
      }
      // The files are already bound to the issue and listed; the comment makes
      // them show up in its timeline.
      if (uploaded.length > 0) {
        try {
          await createComment.mutateAsync({
            issueId: holder.id,
            content: uploaded
              .map((a) => `!file[${escapeMarkdownLabel(a.filename || "file")}](${a.markdownLink})`)
              .join("\n\n"),
            attachmentIds: uploaded.map((a) => a.id),
          });
        } catch (err) {
          failure ??= err;
        }
      }
    } catch (err) {
      failure = err;
    } finally {
      setUploading(false);
      void qc.invalidateQueries({ queryKey: projectAttachmentKeys.all(wsId) });
    }
    if (failure) {
      toast.error(failure instanceof Error && failure.message ? failure.message : t(($) => $.library.add_failed));
    }
  };

  const filterLabels: Record<Filter, string> = {
    all: tIssues(($) => $.deliverables.filter_all),
    image: tIssues(($) => $.deliverables.filter_image),
    document: tIssues(($) => $.deliverables.filter_document),
    video: tIssues(($) => $.deliverables.filter_video),
    other: tIssues(($) => $.deliverables.filter_misc),
  };

  return (
    <div className="flex min-h-0 flex-1 flex-col" data-project-panel="project-library-panel">
      <div className="shrink-0 space-y-2 border-b px-3 py-2">
        <div className="flex items-center justify-between gap-2">
          <ViewToggle value={viewMode} onChange={setViewMode} />
          <Button
            type="button"
            size="sm"
            variant="outline"
            disabled={uploading}
            aria-busy={uploading}
            onClick={() => fileInput.current?.click()}
          >
            {uploading ? <Loader2 className="animate-spin" /> : <Plus />}
            {t(($) => $.library.add)}
          </Button>
          <input
            ref={fileInput}
            type="file"
            multiple
            hidden
            data-testid="project-library-file-input"
            onChange={handlePick}
          />
        </div>
        <div
          role="group"
          aria-label={t(($) => $.library.filter_label)}
          className="flex flex-wrap gap-1"
        >
          {(["all", ...DELIVERABLE_CATEGORIES] as const).map((key) => (
            <Button
              key={key}
              type="button"
              size="xs"
              variant={filter === key ? "brand" : "ghost"}
              aria-pressed={filter === key}
              onClick={() => setFilter(key)}
            >
              {filterLabels[key]}
            </Button>
          ))}
        </div>
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto px-3 py-2">
        {isLoading ? (
          <LibrarySkeleton />
        ) : isError ? (
          <div className="space-y-2 pt-8 text-center">
            <p className="text-body text-muted-foreground">{t(($) => $.library.load_failed)}</p>
            <Button type="button" size="sm" variant="outline" onClick={() => void qc.invalidateQueries({ queryKey: projectAttachmentKeys.all(wsId) })}>
              {t(($) => $.library.retry)}
            </Button>
          </div>
        ) : total === 0 ? (
          <p className="pt-8 text-center text-body text-muted-foreground">
            {t(($) => $.library.empty)}
          </p>
        ) : (
          <div className="space-y-3">
            <LibraryGroup
              label={t(($) => $.library.deliverables)}
              count={filtering ? deliverables.length : deliverableTotal}
              rows={deliverables}
              viewMode={viewMode}
              onOpen={open}
            />
            <LibraryGroup
              label={t(($) => $.library.files)}
              count={filtering ? files.length : uploadTotal}
              rows={files}
              viewMode={viewMode}
              onOpen={open}
            />
            {filtering && visible.length === 0 && (
              <p className="py-4 text-center text-body text-muted-foreground">
                {t(($) => $.library.empty_filtered)}
              </p>
            )}
            {hasMore && (
              <Button
                type="button"
                size="sm"
                variant="ghost"
                className="w-full"
                disabled={loadingMore}
                aria-busy={loadingMore}
                onClick={() => setPages((n) => n + 1)}
              >
                {loadingMore && <Loader2 className="animate-spin" />}
                {t(($) => $.library.load_more)}
              </Button>
            )}
          </div>
        )}
      </div>
    </div>
  );
}

function ViewToggle({
  value,
  onChange,
}: {
  value: ProjectLibraryViewMode;
  onChange: (mode: ProjectLibraryViewMode) => void;
}) {
  const { t } = useT("projects");
  const options = [
    { mode: "list", Icon: List, label: t(($) => $.library.view_list) },
    { mode: "grid", Icon: LayoutGrid, label: t(($) => $.library.view_grid) },
  ] as const;
  return (
    <div className="flex items-center gap-0.5 rounded-md bg-muted p-0.5">
      {options.map(({ mode, Icon, label }) => (
        <Button
          key={mode}
          type="button"
          size="icon-xs"
          variant="ghost"
          aria-label={label}
          aria-pressed={value === mode}
          title={label}
          className={cn(value === mode && "bg-background shadow-sm hover:bg-background")}
          onClick={() => onChange(mode)}
        >
          <Icon />
        </Button>
      ))}
    </div>
  );
}

function LibraryGroup({
  label,
  count,
  rows,
  viewMode,
  onOpen,
}: {
  label: string;
  count: number;
  rows: ProjectAttachment[];
  viewMode: ProjectLibraryViewMode;
  onOpen: (row: ProjectAttachment) => void;
}) {
  const [expanded, setExpanded] = useState(true);
  if (count === 0 && rows.length === 0) return null;
  return (
    <Collapsible open={expanded} onOpenChange={setExpanded}>
      <CollapsibleTrigger className="group flex w-full items-center gap-1.5 rounded-md py-1 text-left text-caption font-medium text-muted-foreground hover:text-foreground">
        <ChevronRight className="size-3.5 transition-transform group-data-panel-open:rotate-90" />
        <span>{label}</span>
        <span className="tabular-nums">{count}</span>
      </CollapsibleTrigger>
      <CollapsibleContent>
        {viewMode === "grid" ? (
          <div className="grid grid-cols-[repeat(auto-fill,minmax(104px,1fr))] gap-x-2 gap-y-3 pt-1">
            {rows.map((row) => (
              <LibraryTile key={row.id} row={row} onOpen={onOpen} />
            ))}
          </div>
        ) : (
          <ul className="pt-0.5">
            {rows.map((row) => (
              <LibraryRow key={row.id} row={row} onOpen={onOpen} />
            ))}
          </ul>
        )}
      </CollapsibleContent>
    </Collapsible>
  );
}

function IssueChip({ row, className }: { row: ProjectAttachment; className?: string }) {
  const { t } = useT("projects");
  const openIssue = useOpenFeedIssue(row.issue.id);
  return (
    <button
      type="button"
      title={row.issue.title}
      aria-label={t(($) => $.library.open_issue, { identifier: row.issue.identifier })}
      onClick={openIssue}
      className={cn(
        "max-w-full shrink-0 truncate rounded-sm bg-muted px-1.5 py-0.5 text-micro font-medium text-muted-foreground tabular-nums transition-colors hover:text-foreground",
        className,
      )}
    >
      {row.issue.identifier}
    </button>
  );
}

function LibraryRow({
  row,
  onOpen,
}: {
  row: ProjectAttachment;
  onOpen: (row: ProjectAttachment) => void;
}) {
  const timeAgo = useTimeAgo();
  const { getActorName } = useActorName();
  const meta = [
    getActorName(row.uploader_type, row.uploader_id),
    row.created_at ? timeAgo(row.created_at) : "",
    row.size_bytes > 0 ? formatBytes(row.size_bytes) : "",
  ].filter(Boolean);
  return (
    <li className="flex items-center gap-2 rounded-md px-1 py-1 hover:bg-accent/50">
      <button
        type="button"
        title={row.filename}
        onClick={() => onOpen(row)}
        className="flex min-w-0 flex-1 items-center gap-2 text-left"
      >
        <span className="size-9 shrink-0 overflow-hidden rounded-md ring-1 ring-border">
          <DeliverableThumbnail attachment={row} showTypeLabel={false} className="size-full" />
        </span>
        <span className="min-w-0 flex-1">
          <span className="block truncate text-body">{row.filename}</span>
          <span className="block truncate text-caption text-muted-foreground">
            {meta.join(" · ")}
          </span>
        </span>
      </button>
      <IssueChip row={row} />
    </li>
  );
}

function LibraryTile({
  row,
  onOpen,
}: {
  row: ProjectAttachment;
  onOpen: (row: ProjectAttachment) => void;
}) {
  return (
    <div className="min-w-0">
      <button
        type="button"
        title={row.filename}
        onClick={() => onOpen(row)}
        className="group block w-full min-w-0 text-left"
      >
        <span className="block aspect-[4/3] overflow-hidden rounded-lg ring-1 ring-border transition-shadow group-hover:ring-foreground/40 group-focus-visible:ring-2 group-focus-visible:ring-ring">
          <DeliverableThumbnail attachment={row} className="size-full" />
        </span>
        <span className="mt-1.5 block truncate text-caption">{row.filename}</span>
      </button>
      <IssueChip row={row} className="mt-0.5" />
    </div>
  );
}

function LibrarySkeleton() {
  return (
    <div className="space-y-2" aria-hidden>
      {Array.from({ length: 5 }, (_, i) => (
        <div key={i} className="flex items-center gap-2 px-1 py-1">
          <Skeleton className="size-9 rounded-md" />
          <div className="flex-1 space-y-1.5">
            <Skeleton className="h-3.5 w-3/4" />
            <Skeleton className="h-3 w-1/2" />
          </div>
        </div>
      ))}
    </div>
  );
}
