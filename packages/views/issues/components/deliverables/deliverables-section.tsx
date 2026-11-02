"use client";

import { IssueSidebarSectionHeader } from "../issue-sidebar-section";

import { useState } from "react";
import { LayoutGrid, Maximize2 } from "@multica/ui/icons";
import type { DeliverableFile } from "@multica/core/attachments/deliverables";
import { IconButton } from "@multica/ui/components/common/icon-button";
import { TooltipHint } from "@multica/ui/components/common/tooltip-hint";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../../i18n";
import { formatBytes } from "../../../common/format-bytes";
import { fileIcon } from "../../../editor/utils/file-icon";
import { getPreviewKind } from "../../../editor/utils/preview";
import { DeliverableThumbnail } from "./deliverable-thumbnail";
import { useOpenAttachment } from "./use-open-attachment";
import { VersionBadge } from "./version-badge";

// The sidebar is a summary, not the list: the newest few of each, and the
// overview for the rest.
const RECENT_IMAGES = 3;
const RECENT_FILES = 4;

/**
 * "Deliverables" in the issue sidebar (MUL-7649): the files this issue's
 * comments delivered, as a whole — not grouped by run; each comment still
 * shows its own files. Pull requests keep their own section above: linking
 * one and PR auto-complete are issue workflow, not output. Renders nothing
 * until a comment delivers a file.
 */
export function DeliverablesSection({
  files,
  onOpenOverview,
  onOpenFocus,
}: {
  files: ReadonlyArray<DeliverableFile>;
  onOpenOverview: () => void;
  /** Opens one file full screen with the issue's discussion beside it. */
  onOpenFocus?: (attachmentId: string) => void;
}) {
  const { t } = useT("issues");
  const [open, setOpen] = useState(true);
  const { open: openAttachment, modal } = useOpenAttachment();

  if (files.length === 0) return null;

  const images: DeliverableFile[] = [];
  const others: DeliverableFile[] = [];
  for (const file of files) {
    const isImage =
      getPreviewKind(file.latest.content_type, file.latest.filename) === "image";
    if (isImage && images.length < RECENT_IMAGES) images.push(file);
    else if (!isImage && others.length < RECENT_FILES) others.push(file);
  }

  return (
    <div>
      <IssueSidebarSectionHeader title={t(($) => $.deliverables.section_title)} open={open} onOpenChange={setOpen} count={files.length} actions={
        <IconButton size="icon-xs" label={t(($) => $.deliverables.view_all, { count: files.length })} onClick={onOpenOverview}><LayoutGrid /></IconButton>
      } />
      {open && (
        <div className="px-2">
          {images.length > 0 && (
            <div className="mx-2 mb-1.5 grid grid-cols-3 gap-1.5">
              {images.map((file) => (
                <div key={file.key} className="group/thumb relative aspect-[4/3]">
                  <TooltipHint label={file.latest.filename}>
                    <button
                      type="button"
                      className="relative size-full overflow-hidden rounded-md ring-1 ring-border transition-shadow hover:ring-foreground/25 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                      aria-label={versionedName(file, t)}
                      onClick={() => openAttachment(file.latest)}
                    >
                      <DeliverableThumbnail
                        attachment={file.latest}
                        showTypeLabel={false}
                        className="size-full"
                      />
                    </button>
                  </TooltipHint>
                  {onOpenFocus && (
                    <FocusButton
                      file={file}
                      onOpen={onOpenFocus}
                      className="absolute right-1 top-1 bg-background/80 backdrop-blur-sm"
                    />
                  )}
                </div>
              ))}
            </div>
          )}
          {others.map((file) => (
            <FileRow
              key={file.key}
              file={file}
              onOpen={() => openAttachment(file.latest)}
              onOpenFocus={onOpenFocus}
            />
          ))}

        </div>
      )}
      {modal}
    </div>
  );
}

type IssuesT = ReturnType<typeof useT<"issues">>["t"];

function versionedName(file: DeliverableFile, t: IssuesT): string {
  return file.versions.length > 1
    ? t(($) => $.deliverables.name_with_version, {
        name: file.latest.filename,
        version: file.versions.length,
      })
    : file.latest.filename;
}

function FileRow({
  file,
  onOpen,
  onOpenFocus,
}: {
  file: DeliverableFile;
  onOpen: () => void;
  onOpenFocus?: (attachmentId: string) => void;
}) {
  const { t } = useT("issues");
  const { latest } = file;
  const Icon = fileIcon(latest.content_type, latest.filename);
  return (
    <div className="group/row relative">
      <button
        type="button"
        className="flex w-full items-center gap-2 rounded-md px-2 py-2 text-left text-caption transition-colors hover:bg-accent/50"
        title={latest.filename}
        aria-label={versionedName(file, t)}
        onClick={onOpen}
      >
        <Icon className="size-3.5 shrink-0 text-muted-foreground" />
        <span className="min-w-0 truncate">{latest.filename}</span>
        {file.versions.length > 1 && <VersionBadge version={file.versions.length} />}
        <span
          className={cn(
            "ml-auto shrink-0 pl-2 text-micro tabular-nums text-muted-foreground",
            onOpenFocus && "group-hover/row:opacity-0 group-focus-within/row:opacity-0",
          )}
        >
          {latest.size_bytes > 0 ? formatBytes(latest.size_bytes) : null}
        </span>
      </button>
      {onOpenFocus && (
        <FocusButton
          file={file}
          onOpen={onOpenFocus}
          className="absolute right-1 top-1/2 -translate-y-1/2"
        />
      )}
    </div>
  );
}

// Beside the row, not inside it: a button cannot nest in the open button.
// Shown on hover or keyboard focus of the row, so a resting list stays quiet.
function FocusButton({
  file,
  onOpen,
  className,
}: {
  file: DeliverableFile;
  onOpen: (attachmentId: string) => void;
  className?: string;
}) {
  const { t } = useT("issues");
  return (
    <button
      type="button"
      className={cn(
        "flex size-6 items-center justify-center rounded-md text-muted-foreground opacity-0 transition-opacity hover:bg-accent hover:text-foreground focus-visible:opacity-100 group-hover/row:opacity-100 group-hover/thumb:opacity-100 group-focus-within/row:opacity-100 group-focus-within/thumb:opacity-100",
        className,
      )}
      title={t(($) => $.deliverables.open_focus)}
      aria-label={t(($) => $.deliverables.open_focus_named, { name: file.latest.filename })}
      onClick={() => onOpen(file.latest.id)}
    >
      <Maximize2 className="size-3.5" />
    </button>
  );
}
