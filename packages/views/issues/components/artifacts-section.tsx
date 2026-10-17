"use client";

import { useEffect, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronRight, Download } from "lucide-react";
import { api, errorCode } from "@multica/core/api";
import { ARTIFACTS_V1_FLAG } from "@multica/core/feature-flags";
import { useFeatureEnabled } from "@multica/core/config";
import { useActorName } from "@multica/core/workspace/hooks";
import type { ArtifactEditSession, ArtifactPreview, ArtifactSummary } from "@multica/core/types";
import { useT } from "../../i18n";
import { formatBytes } from "../../common/format-bytes";
import { ReadonlyContent } from "../../editor/readonly-content";

/**
 * Artifacts owned by this issue. Hidden unless artifacts_v1 is on. Comment
 * attachments stay in Deliverables; this list does not import them.
 */
export function ArtifactsSection({ issueId }: { issueId: string }) {
  const enabled = useFeatureEnabled(ARTIFACTS_V1_FLAG, false);
  const { t } = useT("issues");
  const [open, setOpen] = useState(true);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const query = useQuery({
    queryKey: ["issue-artifacts", issueId] as const,
    enabled: enabled && issueId !== "",
    queryFn: () => api.listArtifacts({ ownerScope: "issue", ownerId: issueId }),
  });
  if (!enabled) return null;
  const artifacts = query.data?.artifacts ?? [];
  if (artifacts.length === 0) return null;
  const selected = artifacts.find((item) => item.id === selectedId) ?? null;

  return (
    <div data-testid="issue-artifacts">
      <button
        type="button"
        className={`mb-2 flex w-full items-center gap-1 rounded-md px-2 py-1 text-caption font-medium transition-colors hover:bg-accent/70 ${open ? "" : "text-muted-foreground hover:text-foreground"}`}
        onClick={() => setOpen(!open)}
        aria-expanded={open}
      >
        {t(($) => $.artifacts.section_title)}{" "}
        <span className="rounded-xs bg-muted px-1 text-micro font-medium tabular-nums text-muted-foreground">
          {artifacts.length}
        </span>
        <ChevronRight className={`!size-3 shrink-0 stroke-[2.5] text-muted-foreground transition-transform ${open ? "rotate-90" : ""}`} />
      </button>
      {open && (
        <div className="pl-2">
          {artifacts.map((artifact) => (
            <ArtifactRow
              key={artifact.id}
              artifact={artifact}
              selected={artifact.id === selectedId}
              onOpen={() => setSelectedId(artifact.id === selectedId ? null : artifact.id)}
            />
          ))}
          {selected?.head_revision_id && (
            <ArtifactRevisionPanel
              artifactId={selected.id}
              revisionId={selected.head_revision_id}
            />
          )}
        </div>
      )}
    </div>
  );
}

function ArtifactRow({
  artifact,
  selected,
  onOpen,
}: {
  artifact: ArtifactSummary;
  selected: boolean;
  onOpen: () => void;
}) {
  const { t } = useT("issues");
  const { getActorName } = useActorName();
  const format = artifact.filename || artifact.media_type || artifact.kind;
  const when = artifact.updated_at ? new Date(artifact.updated_at).toLocaleString() : "";
  const updater = artifact.updated_by ? getActorName("member", artifact.updated_by) : "";
  return (
    <button
      type="button"
      className={`flex w-full flex-col gap-0.5 rounded-md px-2 py-1.5 text-left text-caption transition-colors hover:bg-accent/50 ${selected ? "bg-accent/60" : ""}`}
      aria-pressed={selected}
      onClick={onOpen}
    >
      <span className="truncate font-medium">{artifact.title}</span>
      <span className="truncate text-muted-foreground">
        {format}
        {artifact.revision_no ? ` · ${t(($) => $.artifacts.revision, { version: artifact.revision_no })}` : ""}
        {artifact.status ? ` · ${artifact.status}` : ""}
        {typeof artifact.size_bytes === "number" ? ` · ${formatBytes(artifact.size_bytes)}` : ""}
      </span>
      {(updater || when) && (
        <span className="truncate text-muted-foreground">
          {[updater, when].filter(Boolean).join(" · ")}
        </span>
      )}
    </button>
  );
}

function ArtifactRevisionPanel({
  artifactId,
  revisionId,
}: {
  artifactId: string;
  revisionId: string;
}) {
  const { t } = useT("issues");
  const queryClient = useQueryClient();
  const [picked, setPicked] = useState(revisionId);
  const [editing, setEditing] = useState(false);
  const revisions = useQuery({
    queryKey: ["artifact-revisions", artifactId] as const,
    queryFn: () => api.listArtifactRevisions(artifactId),
  });
  const preview = useQuery({
    queryKey: ["artifact-preview", artifactId, picked] as const,
    queryFn: async () => {
      const entry = await api.getArtifactPreview(artifactId, picked);
      if (
        artifactPreviewIsSourceOnly(entry) ||
        entry.preview_kind === "markdown" ||
        entry.preview_kind === "text" ||
        entry.preview_kind === "table"
      ) {
        const text = await api.getArtifactRevisionText(artifactId, picked);
        return { entry, text, objectUrl: "" };
      }
      if (
        entry.preview_kind === "image" ||
        entry.preview_kind === "pdf" ||
        entry.preview_kind === "video" ||
        entry.preview_kind === "audio"
      ) {
        const blob = await api.getArtifactRevisionBlob(artifactId, picked);
        return { entry, text: "", objectUrl: URL.createObjectURL(blob) };
      }
      return { entry, text: "", objectUrl: "" };
    },
  });
  useEffect(() => {
    return () => {
      if (preview.data?.objectUrl) URL.revokeObjectURL(preview.data.objectUrl);
    };
  }, [preview.data?.objectUrl]);

  async function download() {
    const entry = preview.data?.entry;
    const blob = await api.getArtifactRevisionBlob(artifactId, picked);
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = entry?.filename || "artifact";
    anchor.click();
    URL.revokeObjectURL(url);
  }

  return (
    <div className="mt-1 rounded-md border border-border p-2" data-testid="artifact-preview">
      {revisions.data && revisions.data.revisions.length > 1 && (
        <div className="mb-2 flex flex-wrap gap-1">
          {revisions.data.revisions.map((revision) => (
            <button
              key={revision.id}
              type="button"
              className={`rounded-md px-1.5 py-0.5 text-caption ${revision.id === picked ? "bg-accent" : "text-muted-foreground"}`}
              aria-pressed={revision.id === picked}
              onClick={() => {
                setEditing(false);
                setPicked(revision.id);
              }}
            >
              {t(($) => $.artifacts.revision, { version: revision.revision_no })}
            </button>
          ))}
        </div>
      )}
      {editing && preview.data ? (
        <ArtifactTextEditor
          artifactId={artifactId}
          filename={preview.data.entry.filename}
          initialText={preview.data.text}
          onClose={() => setEditing(false)}
          onCommitted={(nextRevisionId) => {
            setEditing(false);
            if (nextRevisionId) setPicked(nextRevisionId);
            void queryClient.invalidateQueries({ queryKey: ["issue-artifacts"] });
            void queryClient.invalidateQueries({ queryKey: ["artifact-revisions", artifactId] });
            void queryClient.invalidateQueries({ queryKey: ["artifact-preview", artifactId] });
          }}
        />
      ) : preview.data ? (
        <ArtifactPreviewBody
          preview={preview.data.entry}
          text={preview.data.text}
          objectUrl={preview.data.objectUrl}
        />
      ) : preview.isError ? (
        <p className="text-caption text-muted-foreground">{t(($) => $.artifacts.preview_failed)}</p>
      ) : null}
      {!editing && preview.data && picked === revisionId && artifactTextEditable(preview.data.entry.filename, preview.data.entry.media_type) && (
        <button
          type="button"
          data-testid="artifact-edit"
          className="mt-2 text-caption text-muted-foreground hover:text-foreground"
          onClick={() => setEditing(true)}
        >
          {t(($) => $.artifacts.edit)}
        </button>
      )}
      <button
        type="button"
        className="mt-2 inline-flex items-center gap-1 text-caption text-muted-foreground hover:text-foreground"
        onClick={() => void download()}
      >
        <Download className="size-3.5" />
        {t(($) => $.artifacts.download)}
      </button>
    </div>
  );
}

const TEXT_EDIT_EXTENSIONS = new Set(["md", "markdown", "txt", "json", "csv"]);
const TEXT_EDIT_MEDIA = new Set([
  "text/markdown",
  "text/x-markdown",
  "text/plain",
  "application/json",
  "text/csv",
]);
const EDIT_HEARTBEAT_MS = 30_000;

export function artifactTextEditable(filename: string, mediaType: string): boolean {
  const name = filename.toLowerCase();
  const media = mediaType.toLowerCase().split(";")[0]?.trim() ?? "";
  if (
    name.endsWith(".html") ||
    name.endsWith(".htm") ||
    name.endsWith(".xhtml") ||
    name.endsWith(".svg") ||
    media === "text/html" ||
    media === "application/xhtml+xml" ||
    media === "image/svg+xml"
  ) {
    return false;
  }
  const dot = name.lastIndexOf(".");
  const ext = dot >= 0 ? name.slice(dot + 1) : "";
  return TEXT_EDIT_EXTENSIONS.has(ext) || TEXT_EDIT_MEDIA.has(media);
}

export function artifactPreviewIsSourceOnly(preview: ArtifactPreview): boolean {
  const media = preview.media_type.toLowerCase();
  const name = preview.filename.toLowerCase();
  return (
    preview.active_content ||
    preview.preview_kind === "html" ||
    preview.preview_kind === "svg" ||
    media.includes("html") ||
    media.includes("svg") ||
    name.endsWith(".html") ||
    name.endsWith(".htm") ||
    name.endsWith(".xhtml") ||
    name.endsWith(".svg")
  );
}

export function ArtifactPreviewBody({
  preview,
  text,
  objectUrl,
}: {
  preview: ArtifactPreview;
  text: string;
  objectUrl?: string;
}) {
  if (artifactPreviewIsSourceOnly(preview)) {
    return <pre data-testid="artifact-source">{text}</pre>;
  }
  if (preview.preview_kind === "image" && objectUrl) {
    return <img alt={preview.filename} src={objectUrl} />;
  }
  if (preview.preview_kind === "pdf" && objectUrl) {
    return <iframe title={preview.filename} src={objectUrl} />;
  }
  if (preview.preview_kind === "video" && objectUrl) {
    return <video controls src={objectUrl} />;
  }
  if (preview.preview_kind === "audio" && objectUrl) {
    return <audio controls src={objectUrl} />;
  }
  if (preview.preview_kind === "markdown") {
    return <ReadonlyContent content={text} />;
  }
  if (preview.preview_kind === "text" || preview.preview_kind === "table") {
    return <pre data-testid="artifact-source">{text}</pre>;
  }
  return null;
}

function ArtifactTextEditor({
  artifactId,
  filename,
  initialText,
  onClose,
  onCommitted,
}: {
  artifactId: string;
  filename: string;
  initialText: string;
  onClose: () => void;
  onCommitted: (revisionId: string) => void;
}) {
  const { t } = useT("issues");
  const [text, setText] = useState(initialText);
  const [session, setSession] = useState<ArtifactEditSession | null>(null);
  const [notice, setNotice] = useState("");
  const [readOnly, setReadOnly] = useState(false);
  const [busy, setBusy] = useState(false);
  const dirty = useRef(false);
  const textRef = useRef(text);
  textRef.current = text;

  useEffect(() => {
    let gone = false;
    api.createArtifactEditSession(artifactId).then((opened) => {
      if (gone) return;
      setSession(opened);
      if (!dirty.current && opened.draft_content) setText(opened.draft_content);
    }).catch(() => {
      if (gone) return;
      setReadOnly(true);
      setNotice(t(($) => $.artifacts.read_only));
    });
    return () => {
      gone = true;
    };
    // t is read once when the editor opens. Listing it would take a second lease.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [artifactId]);

  useEffect(() => {
    if (!session?.lease_token || readOnly) return;
    const current = session;
    const timer = setInterval(() => {
      void api.heartbeatArtifactEditSession({
        editSessionId: current.edit_session_id,
        leaseToken: current.lease_token ?? "",
        fencingToken: current.fencing_token,
      }).catch((err: unknown) => {
        if (errorCode(err) === "LEASE_EXPIRED") {
          setReadOnly(true);
          setNotice(t(($) => $.artifacts.lease_expired));
        }
      });
    }, EDIT_HEARTBEAT_MS);
    return () => clearInterval(timer);
  }, [session, readOnly, t]);

  async function reopen(local: string) {
    setText(local);
    try {
      const opened = await api.createArtifactEditSession(artifactId);
      setSession(opened);
    } catch {
      setReadOnly(true);
      setNotice(t(($) => $.artifacts.lease_expired));
    }
  }

  function lock(err: unknown) {
    const code = errorCode(err);
    if (code === "REVISION_CONFLICT") {
      setNotice(t(($) => $.artifacts.conflict));
      void reopen(textRef.current);
      return;
    }
    setReadOnly(true);
    setNotice(code === "LEASE_EXPIRED" || code === "LEASE_HELD"
      ? t(($) => $.artifacts.lease_expired)
      : t(($) => $.artifacts.read_only));
  }

  async function saveDraft() {
    if (!session?.lease_token || readOnly) return;
    setBusy(true);
    try {
      await api.saveArtifactDraft({
        editSessionId: session.edit_session_id,
        leaseToken: session.lease_token,
        fencingToken: session.fencing_token,
        content: text,
      });
      setNotice(t(($) => $.artifacts.draft_saved));
    } catch (err) {
      lock(err);
    } finally {
      setBusy(false);
    }
  }

  async function saveRevision() {
    if (!session?.lease_token || readOnly) return;
    setBusy(true);
    try {
      const saved = await api.commitArtifactRevision({
        artifactId,
        baseRevisionId: session.base_revision_id,
        filename,
        content: text,
        editSessionId: session.edit_session_id,
        leaseToken: session.lease_token,
        fencingToken: session.fencing_token,
      });
      onCommitted(saved.revision_id);
    } catch (err) {
      lock(err);
    } finally {
      setBusy(false);
    }
  }

  const held = busy || readOnly || !session?.lease_token;
  return (
    <div data-testid="artifact-text-editor">
      <textarea
        data-testid="artifact-editor"
        aria-label={t(($) => $.artifacts.edit)}
        className="mt-1 min-h-28 w-full rounded-md border border-border bg-transparent p-2 text-caption"
        value={text}
        readOnly={readOnly}
        onChange={(event) => {
          dirty.current = true;
          setText(event.target.value);
        }}
      />
      {notice ? (
        <p className="mt-1 text-caption text-muted-foreground" data-testid="artifact-editor-notice">
          {notice}
        </p>
      ) : null}
      <div className="mt-2 flex flex-wrap gap-2">
        <button type="button" data-testid="artifact-save-draft" className="text-caption" disabled={held} onClick={() => void saveDraft()}>
          {t(($) => $.artifacts.save_draft)}
        </button>
        <button type="button" data-testid="artifact-save-revision" className="text-caption" disabled={held} onClick={() => void saveRevision()}>
          {t(($) => $.artifacts.save_revision)}
        </button>
        <button type="button" className="text-caption text-muted-foreground" onClick={onClose}>
          {t(($) => $.artifacts.cancel)}
        </button>
      </div>
    </div>
  );
}
