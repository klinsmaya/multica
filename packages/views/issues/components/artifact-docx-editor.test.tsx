import { forwardRef, useImperativeHandle } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { renderWithI18n } from "../../test/i18n";

const apiMock = vi.hoisted(() => ({
  createArtifactEditSession: vi.fn(),
  heartbeatArtifactEditSession: vi.fn(),
  getArtifactRevisionBlob: vi.fn(),
  commitArtifactRevision: vi.fn(),
  saveArtifactDraft: vi.fn(),
}));

function errorCode(err: unknown): string | undefined {
  if (!err || typeof err !== "object") return undefined;
  const record = err as { body?: { code?: unknown } };
  return typeof record.body?.code === "string" ? record.body.code : undefined;
}

vi.mock("@multica/core/api", () => ({ api: apiMock, errorCode }));

vi.mock("@docx-editor.dev/react", () => ({
  DocxEditor: forwardRef(function DocxEditor(_props, ref) {
    useImperativeHandle(ref, () => ({
      save: async () => new Uint8Array([80, 75, 3, 4]).buffer,
    }));
    return <div data-testid="docx-surface" />;
  }),
}));

vi.mock("@docx-editor.dev/react/styles.css", () => ({}));

import { ArtifactDocxEditor } from "./artifact-docx-editor";

const session = {
  edit_session_id: "sess-1",
  artifact_id: "art-1",
  base_revision_id: "rev-1",
  fencing_token: 4,
  lease_token: "lease-1",
  lease_expires_at: "2026-10-17T00:02:00Z",
  editor_type: "human",
  state: "open",
  editable: true,
};

describe("ArtifactDocxEditor", () => {
  beforeEach(() => {
    apiMock.createArtifactEditSession.mockReset();
    apiMock.getArtifactRevisionBlob.mockReset();
    apiMock.commitArtifactRevision.mockReset();
    apiMock.saveArtifactDraft.mockReset();
  });

  it("commits editor bytes through the revision API", async () => {
    apiMock.createArtifactEditSession.mockResolvedValue(session);
    apiMock.getArtifactRevisionBlob.mockResolvedValue(new Blob([new Uint8Array([1, 2, 3])]));
    apiMock.commitArtifactRevision.mockResolvedValue({ revision_id: "rev-2" });
    const onCommitted = vi.fn();
    renderWithI18n(
      <ArtifactDocxEditor artifactId="art-1" filename="notes.docx" onClose={() => undefined} onCommitted={onCommitted} />,
    );
    await screen.findByTestId("docx-surface");
    fireEvent.click(screen.getByTestId("artifact-save-revision"));
    expect(apiMock.saveArtifactDraft).not.toHaveBeenCalled();
    await waitFor(() => expect(apiMock.commitArtifactRevision).toHaveBeenCalledWith(expect.objectContaining({
      artifactId: "art-1",
      baseRevisionId: "rev-1",
      filename: "notes.docx",
      editSessionId: "sess-1",
      leaseToken: "lease-1",
      fencingToken: 4,
    })));
    await waitFor(() => expect(onCommitted).toHaveBeenCalledWith("rev-2"));
    const content = apiMock.commitArtifactRevision.mock.calls[0]?.[0].content as Uint8Array;
    expect(Array.from(content)).toEqual([80, 75, 3, 4]);
  });

  it("stays read only when the server blocks the file", async () => {
    apiMock.createArtifactEditSession.mockRejectedValue({ body: { code: "OFFICE_FEATURE_BLOCKED" } });
    renderWithI18n(
      <ArtifactDocxEditor artifactId="art-1" filename="notes.docx" onClose={() => undefined} onCommitted={() => undefined} />,
    );
    expect((await screen.findByTestId("artifact-editor-notice")).textContent).toContain("Macros, comments, or tracked changes");
    expect(screen.queryByTestId("docx-surface")).toBeNull();
    expect(apiMock.commitArtifactRevision).not.toHaveBeenCalled();
  });

  it("does not commit when the save would drop a part", async () => {
    apiMock.createArtifactEditSession.mockResolvedValue(session);
    apiMock.getArtifactRevisionBlob.mockResolvedValue(new Blob([new Uint8Array([1])]));
    apiMock.commitArtifactRevision.mockRejectedValue({ body: { code: "OFFICE_FEATURE_DROPPED" } });
    const onCommitted = vi.fn();
    renderWithI18n(
      <ArtifactDocxEditor artifactId="art-1" filename="notes.docx" onClose={() => undefined} onCommitted={onCommitted} />,
    );
    await screen.findByTestId("docx-surface");
    fireEvent.click(screen.getByTestId("artifact-save-revision"));
    expect((await screen.findByTestId("artifact-editor-notice")).textContent).toContain("Save blocked");
    expect(onCommitted).not.toHaveBeenCalled();
  });
});
