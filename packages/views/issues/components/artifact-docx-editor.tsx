"use client";

import { useEffect, useRef, useState } from "react";
import { DocxEditor, type DocxEditorRef } from "@docx-editor.dev/react";
import "@docx-editor.dev/react/styles.css";
import { api, errorCode } from "@multica/core/api";
import type { ArtifactEditSession } from "@multica/core/types";
import { useT } from "../../i18n";

const EDIT_HEARTBEAT_MS = 30_000;

/**
 * In-browser DOCX editor. The bytes it serializes are not a revision until
 * they go through the open lease and POST /api/artifacts/{id}/revisions.
 * XLSX and PPTX do not use this component.
 */
export function ArtifactDocxEditor({
  artifactId,
  filename,
  onClose,
  onCommitted,
}: {
  artifactId: string;
  filename: string;
  onClose: () => void;
  onCommitted: (revisionId: string) => void;
}) {
  const { t } = useT("issues");
  const editorRef = useRef<DocxEditorRef>(null);
  const [session, setSession] = useState<ArtifactEditSession | null>(null);
  const [bytes, setBytes] = useState<Uint8Array | null>(null);
  const [notice, setNotice] = useState("");
  const [readOnly, setReadOnly] = useState(false);
  const [busy, setBusy] = useState(false);
  const sessionRef = useRef(session);
  sessionRef.current = session;
  const saveRef = useRef<() => Promise<void>>(async () => {});

  useEffect(() => {
    let gone = false;
    api.createArtifactEditSession(artifactId).then(async (opened) => {
      if (gone) return;
      setSession(opened);
      const blob = await api.getArtifactRevisionBlob(artifactId, opened.base_revision_id);
      if (gone) return;
      setBytes(new Uint8Array(await blob.arrayBuffer()));
    }).catch((err: unknown) => {
      if (gone) return;
      setReadOnly(true);
      setNotice(errorCode(err) === "OFFICE_FEATURE_BLOCKED"
        ? t(($) => $.artifacts.docx_blocked)
        : t(($) => $.artifacts.read_only));
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

  async function saveRevision() {
    const current = sessionRef.current;
    if (!current?.lease_token || readOnly || busy) return;
    setBusy(true);
    try {
      const buffer = await editorRef.current?.save();
      if (!buffer) {
        setNotice(t(($) => $.artifacts.read_only));
        return;
      }
      const saved = await api.commitArtifactRevision({
        artifactId,
        baseRevisionId: current.base_revision_id,
        filename,
        content: new Uint8Array(buffer),
        editSessionId: current.edit_session_id,
        leaseToken: current.lease_token,
        fencingToken: current.fencing_token,
      });
      onCommitted(saved.revision_id);
    } catch (err) {
      const code = errorCode(err);
      if (code === "OFFICE_FEATURE_DROPPED") {
        setNotice(t(($) => $.artifacts.docx_dropped));
        return;
      }
      if (code === "REVISION_CONFLICT") {
        setNotice(t(($) => $.artifacts.conflict));
        try {
          setSession(await api.createArtifactEditSession(artifactId));
        } catch {
          setReadOnly(true);
          setNotice(t(($) => $.artifacts.lease_expired));
        }
        return;
      }
      setReadOnly(true);
      setNotice(code === "LEASE_EXPIRED" || code === "LEASE_HELD"
        ? t(($) => $.artifacts.lease_expired)
        : t(($) => $.artifacts.read_only));
    } finally {
      setBusy(false);
    }
  }
  saveRef.current = saveRevision;

  const held = busy || readOnly || !session?.lease_token || !bytes;
  return (
    <div data-testid="artifact-docx-editor">
      {notice ? (
        <p className="mt-1 text-caption text-muted-foreground" data-testid="artifact-editor-notice">
          {notice}
        </p>
      ) : null}
      {bytes ? (
        <div className="mt-1 h-96">
          <DocxEditor
            ref={editorRef}
            document={bytes}
            title={filename}
            className="h-full"
            onSave={() => void saveRef.current()}
            menu={{ reportIssue: false, onOpen: () => undefined, onSave: () => void saveRef.current() }}
          />
        </div>
      ) : null}
      <div className="mt-2 flex flex-wrap gap-2">
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
