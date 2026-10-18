import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { configStore } from "@multica/core/config";
import { ARTIFACTS_V1_FLAG } from "@multica/core/feature-flags";
import type { ArtifactPreview } from "@multica/core/types";
import { renderWithI18n } from "../../test/i18n";

const apiMock = vi.hoisted(() => ({
  listArtifacts: vi.fn(),
  listArtifactRevisions: vi.fn(),
  getArtifactPreview: vi.fn(),
  getArtifactStaticPreview: vi.fn(),
  getArtifactOfficePreview: vi.fn(),
  getArtifactRevisionText: vi.fn(),
  getArtifactRevisionBlob: vi.fn(),
  createArtifactEditSession: vi.fn(),
  heartbeatArtifactEditSession: vi.fn(),
  saveArtifactDraft: vi.fn(),
  commitArtifactRevision: vi.fn(),
}));

function errorCode(err: unknown): string | undefined {
  if (!err || typeof err !== "object") return undefined;
  const record = err as { code?: unknown; body?: { code?: unknown } };
  if (typeof record.body?.code === "string") return record.body.code;
  return typeof record.code === "string" ? record.code : undefined;
}

vi.mock("@multica/core/api", () => ({ api: apiMock, errorCode }));

vi.mock("./artifact-docx-editor", () => ({
  ArtifactDocxEditor: () => <div data-testid="artifact-docx-editor" />,
}));

vi.mock("@multica/core/workspace/hooks", () => ({
  useActorName: () => ({
    getActorName: (_type: string, id: string) => `Member ${id}`,
  }),
}));

import { ArtifactPreviewBody, ArtifactsSection } from "./artifacts-section";

const editSession = {
  edit_session_id: "sess-1",
  artifact_id: "art-1",
  base_revision_id: "rev-1",
  fencing_token: 1,
  lease_token: "lease-1",
  lease_expires_at: "2026-10-17T00:02:00Z",
  editor_type: "human",
  state: "open",
  editable: true,
};

const markdownPreview: ArtifactPreview = {
  artifact_id: "art-1",
  revision_id: "rev-1",
  filename: "report.md",
  media_type: "text/markdown",
  size_bytes: 12,
  revision_no: 1,
  preview_kind: "markdown",
  content_path: "/api/artifacts/art-1/revisions/rev-1/content",
  content_disposition: "inline",
  active_content: false,
};

const htmlPreview: ArtifactPreview = {
  artifact_id: "art-1",
  revision_id: "rev-1",
  filename: "page.html",
  media_type: "text/html",
  size_bytes: 32,
  revision_no: 1,
  preview_kind: "download",
  content_path: "/api/artifacts/art-1/revisions/rev-1/content",
  content_disposition: "attachment",
  active_content: false,
};

function renderSection() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return renderWithI18n(
    <QueryClientProvider client={client}>
      <ArtifactsSection issueId="issue-1" />
    </QueryClientProvider>,
  );
}

describe("ArtifactsSection", () => {
  beforeEach(() => {
    configStore.getState().setFeatureFlags({});
    apiMock.listArtifacts.mockReset();
    apiMock.listArtifactRevisions.mockReset();
    apiMock.getArtifactPreview.mockReset();
    apiMock.getArtifactStaticPreview.mockReset();
    apiMock.getArtifactOfficePreview.mockReset();
    apiMock.getArtifactRevisionText.mockReset();
    apiMock.getArtifactRevisionBlob.mockReset();
    apiMock.createArtifactEditSession.mockReset();
    apiMock.heartbeatArtifactEditSession.mockReset();
    apiMock.saveArtifactDraft.mockReset();
    apiMock.commitArtifactRevision.mockReset();
    apiMock.createArtifactEditSession.mockResolvedValue(editSession);
    apiMock.saveArtifactDraft.mockResolvedValue(editSession);
    apiMock.commitArtifactRevision.mockResolvedValue({
      artifact_id: "art-1",
      revision_id: "rev-2",
      operation_id: "op-1",
      state: "committed",
      revision_no: 2,
    });
    apiMock.listArtifacts.mockResolvedValue({
      artifacts: [
        {
          id: "art-1",
          workspace_id: "ws-1",
          owner_scope: "issue",
          owner_id: "issue-1",
          title: "Weekly report",
          kind: "file",
          status: "active",
          head_revision_id: "rev-1",
          revision_no: 1,
          media_type: "text/html",
          filename: "page.html",
          size_bytes: 32,
          updated_by: "user-1",
          created_at: "2026-10-17T00:00:00Z",
          updated_at: "2026-10-17T00:00:00Z",
          row_version: 1,
        },
      ],
    });
    apiMock.listArtifactRevisions.mockResolvedValue({ revisions: [] });
    apiMock.getArtifactPreview.mockResolvedValue(htmlPreview);
    apiMock.getArtifactRevisionText.mockResolvedValue("<script>alert(1)</script>");
  });

  it("stays hidden and does not fetch while artifacts_v1 is off", () => {
    renderSection();
    expect(screen.queryByTestId("issue-artifacts")).toBeNull();
    expect(apiMock.listArtifacts).not.toHaveBeenCalled();
  });

  it("lists a committed artifact when the flag is on", async () => {
    configStore.getState().setFeatureFlags({ [ARTIFACTS_V1_FLAG]: true });
    renderSection();
    expect(await screen.findByText("Weekly report")).toBeTruthy();
    expect(screen.getByText(/page\.html/)).toBeTruthy();
    expect(screen.getByText(/Member user-1/)).toBeTruthy();
    expect(screen.getByText(/v1/)).toBeTruthy();
  });

  it("shows HTML as source text and does not mount an active document", async () => {
    configStore.getState().setFeatureFlags({ [ARTIFACTS_V1_FLAG]: true });
    const view = renderSection();
    fireEvent.click(await screen.findByText("Weekly report"));
    const source = await screen.findByTestId("artifact-source");
    expect(source.textContent).toContain("<script>alert(1)</script>");
    expect(view.container.querySelector("iframe")).toBeNull();
    expect(view.container.querySelector("script")).toBeNull();
    expect(screen.queryByTestId("artifact-edit")).toBeNull();
    await waitFor(() => expect(apiMock.getArtifactPreview).toHaveBeenCalledWith("art-1", "rev-1"));
  });

  it("opens a sandboxed static preview and falls back to source", async () => {
    configStore.getState().setFeatureFlags({ [ARTIFACTS_V1_FLAG]: true });
    apiMock.getArtifactPreview.mockResolvedValue({
      ...htmlPreview,
      preview_kind: "html",
      static_preview_path: "/api/artifacts/art-1/revisions/rev-1/static-preview",
    });
    apiMock.getArtifactStaticPreview.mockResolvedValue("<p>Hello</p>");
    const view = renderSection();
    fireEvent.click(await screen.findByText("Weekly report"));
    const frame = await screen.findByTestId("artifact-static-preview");
    expect(frame.getAttribute("sandbox")).toBe("");
    expect(frame.getAttribute("srcdoc")).toBe("<p>Hello</p>");
    expect(frame.getAttribute("csp")).toContain("script-src 'none'");
    expect(frame.getAttribute("csp")).not.toContain("allow-scripts");
    expect(view.container.querySelector("script")).toBeNull();
    expect(apiMock.getArtifactRevisionText).not.toHaveBeenCalled();

    apiMock.getArtifactStaticPreview.mockRejectedValue(new Error("preview failed"));
    apiMock.getArtifactPreview.mockResolvedValue({
      ...htmlPreview,
      preview_kind: "html",
      revision_id: "rev-2",
      static_preview_path: "/api/artifacts/art-1/revisions/rev-2/static-preview",
    });
    apiMock.listArtifacts.mockResolvedValue({
      artifacts: [
        {
          id: "art-1",
          workspace_id: "ws-1",
          owner_scope: "issue",
          owner_id: "issue-1",
          title: "Weekly report",
          kind: "file",
          status: "active",
          head_revision_id: "rev-2",
          revision_no: 2,
          media_type: "text/html",
          filename: "page.html",
          size_bytes: 32,
          updated_by: "user-1",
          created_at: "2026-10-17T00:00:00Z",
          updated_at: "2026-10-17T00:00:00Z",
          row_version: 2,
        },
      ],
    });
    view.unmount();
    const fallback = renderSection();
    fireEvent.click(await screen.findByText("Weekly report"));
    const source = await screen.findByTestId("artifact-source");
    expect(source.textContent).toContain("<script>alert(1)</script>");
    expect(fallback.container.querySelector("iframe")).toBeNull();
  });

  it("shows a read-only office preview and keeps download", async () => {
    configStore.getState().setFeatureFlags({ [ARTIFACTS_V1_FLAG]: true });
    apiMock.listArtifacts.mockResolvedValue({
      artifacts: [
        {
          id: "art-1",
          workspace_id: "ws-1",
          owner_scope: "issue",
          owner_id: "issue-1",
          title: "Weekly report",
          kind: "file",
          status: "active",
          head_revision_id: "rev-1",
          revision_no: 1,
          media_type: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
          filename: "notes.docx",
          size_bytes: 32,
          updated_at: "2026-10-17T00:00:00Z",
          created_at: "2026-10-17T00:00:00Z",
          row_version: 1,
        },
      ],
    });
    apiMock.getArtifactPreview.mockResolvedValue({
      ...htmlPreview,
      filename: "notes.docx",
      media_type: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
      preview_kind: "docx",
      office_status: "ready",
      office_preview_path: "/api/artifacts/art-1/revisions/rev-1/office-preview",
      page_count: 2,
    });
    apiMock.getArtifactOfficePreview.mockResolvedValue("<pre>Hello artifact</pre>");
    renderSection();
    fireEvent.click(await screen.findByText("Weekly report"));
    const frame = await screen.findByTestId("artifact-office-preview");
    expect(frame.getAttribute("sandbox")).toBe("");
    expect(frame.getAttribute("srcdoc")).toContain("Hello artifact");
    expect(screen.getByText("Download")).toBeTruthy();
    expect(screen.getByTestId("artifact-edit")).toBeTruthy();
    expect(apiMock.getArtifactRevisionText).not.toHaveBeenCalled();
  });

  it("opens the docx editor and leaves spreadsheets read only", async () => {
    configStore.getState().setFeatureFlags({ [ARTIFACTS_V1_FLAG]: true });
    apiMock.listArtifacts.mockResolvedValue({
      artifacts: [
        {
          id: "art-1",
          workspace_id: "ws-1",
          owner_scope: "issue",
          owner_id: "issue-1",
          title: "Weekly report",
          kind: "file",
          status: "active",
          head_revision_id: "rev-1",
          revision_no: 1,
          media_type: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
          filename: "notes.docx",
          size_bytes: 32,
          updated_at: "2026-10-17T00:00:00Z",
          created_at: "2026-10-17T00:00:00Z",
          row_version: 1,
        },
      ],
    });
    apiMock.getArtifactPreview.mockResolvedValue({
      ...htmlPreview,
      filename: "notes.docx",
      media_type: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
      preview_kind: "docx",
      office_status: "ready",
      office_preview_path: "/api/artifacts/art-1/revisions/rev-1/office-preview",
    });
    apiMock.getArtifactOfficePreview.mockResolvedValue("<pre>Hello artifact</pre>");
    renderSection();
    fireEvent.click(await screen.findByText("Weekly report"));
    fireEvent.click(await screen.findByTestId("artifact-edit"));
    expect(await screen.findByTestId("artifact-docx-editor")).toBeTruthy();
  });

  it("shows a failed office preview without hiding download", async () => {
    configStore.getState().setFeatureFlags({ [ARTIFACTS_V1_FLAG]: true });
    apiMock.listArtifacts.mockResolvedValue({
      artifacts: [
        {
          id: "art-1",
          workspace_id: "ws-1",
          owner_scope: "issue",
          owner_id: "issue-1",
          title: "Weekly report",
          kind: "file",
          status: "active",
          head_revision_id: "rev-1",
          revision_no: 1,
          media_type: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
          filename: "notes.docx",
          size_bytes: 32,
          updated_at: "2026-10-17T00:00:00Z",
          created_at: "2026-10-17T00:00:00Z",
          row_version: 1,
        },
      ],
    });
    apiMock.getArtifactPreview.mockResolvedValue({
      ...htmlPreview,
      filename: "notes.docx",
      preview_kind: "docx",
      office_status: "failed",
      office_error: "CONVERSION_FAILED",
    });
    renderSection();
    fireEvent.click(await screen.findByText("Weekly report"));
    expect((await screen.findByTestId("artifact-office-status")).textContent).toContain("Preview failed");
    expect(screen.getByText("Download")).toBeTruthy();
    expect(screen.queryByTestId("artifact-office-preview")).toBeNull();
    expect(apiMock.getArtifactOfficePreview).not.toHaveBeenCalled();
  });

  it("shows a pending office preview and keeps download", async () => {
    configStore.getState().setFeatureFlags({ [ARTIFACTS_V1_FLAG]: true });
    apiMock.listArtifacts.mockResolvedValue({
      artifacts: [
        {
          id: "art-1",
          workspace_id: "ws-1",
          owner_scope: "issue",
          owner_id: "issue-1",
          title: "Weekly report",
          kind: "file",
          status: "active",
          head_revision_id: "rev-1",
          revision_no: 1,
          media_type: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
          filename: "budget.xlsx",
          size_bytes: 32,
          updated_at: "2026-10-17T00:00:00Z",
          created_at: "2026-10-17T00:00:00Z",
          row_version: 1,
        },
      ],
    });
    apiMock.getArtifactPreview.mockResolvedValue({
      ...htmlPreview,
      filename: "budget.xlsx",
      preview_kind: "xlsx",
      office_status: "pending",
    });
    renderSection();
    fireEvent.click(await screen.findByText("Weekly report"));
    expect((await screen.findByTestId("artifact-office-status")).textContent).toContain("Preparing a read-only preview");
    expect(screen.getByText("Download")).toBeTruthy();
    expect(screen.queryByTestId("artifact-edit")).toBeNull();
  });

  it("saves a markdown draft and commits a revision", async () => {
    configStore.getState().setFeatureFlags({ [ARTIFACTS_V1_FLAG]: true });
    apiMock.listArtifacts.mockResolvedValue({
      artifacts: [
        {
          id: "art-1",
          workspace_id: "ws-1",
          owner_scope: "issue",
          owner_id: "issue-1",
          title: "Weekly report",
          kind: "file",
          status: "active",
          head_revision_id: "rev-1",
          revision_no: 1,
          media_type: "text/markdown",
          filename: "report.md",
          size_bytes: 12,
          updated_at: "2026-10-17T00:00:00Z",
          created_at: "2026-10-17T00:00:00Z",
          row_version: 1,
        },
      ],
    });
    apiMock.getArtifactPreview.mockResolvedValue(markdownPreview);
    apiMock.getArtifactRevisionText.mockResolvedValue("# report\n");
    renderSection();
    fireEvent.click(await screen.findByText("Weekly report"));
    fireEvent.click(await screen.findByTestId("artifact-edit"));
    const editor = await screen.findByTestId("artifact-editor");
    await waitFor(() => expect((screen.getByTestId("artifact-save-draft") as HTMLButtonElement).disabled).toBe(false));
    fireEvent.change(editor, { target: { value: "keep me\n" } });
    fireEvent.click(screen.getByTestId("artifact-save-draft"));
    await waitFor(() => expect(apiMock.saveArtifactDraft).toHaveBeenCalledWith({
      editSessionId: "sess-1",
      leaseToken: "lease-1",
      fencingToken: 1,
      content: "keep me\n",
    }));
    expect(await screen.findByText("Draft saved")).toBeTruthy();
    fireEvent.click(screen.getByTestId("artifact-save-revision"));
    await waitFor(() => expect(apiMock.commitArtifactRevision).toHaveBeenCalledWith(expect.objectContaining({
      artifactId: "art-1",
      baseRevisionId: "rev-1",
      filename: "report.md",
      content: "keep me\n",
      editSessionId: "sess-1",
      leaseToken: "lease-1",
      fencingToken: 1,
    })));
  });

  it("keeps the draft text when the revision conflicts", async () => {
    configStore.getState().setFeatureFlags({ [ARTIFACTS_V1_FLAG]: true });
    apiMock.listArtifacts.mockResolvedValue({
      artifacts: [
        {
          id: "art-1",
          workspace_id: "ws-1",
          owner_scope: "issue",
          owner_id: "issue-1",
          title: "Weekly report",
          kind: "file",
          status: "active",
          head_revision_id: "rev-1",
          revision_no: 1,
          filename: "notes.txt",
          media_type: "text/plain",
          created_at: "2026-10-17T00:00:00Z",
          updated_at: "2026-10-17T00:00:00Z",
          row_version: 1,
        },
      ],
    });
    apiMock.getArtifactPreview.mockResolvedValue({
      ...markdownPreview,
      filename: "notes.txt",
      media_type: "text/plain",
      preview_kind: "text",
    });
    apiMock.getArtifactRevisionText.mockResolvedValue("original\n");
    apiMock.commitArtifactRevision.mockRejectedValue(Object.assign(new Error("conflict"), { code: "REVISION_CONFLICT" }));
    apiMock.createArtifactEditSession
      .mockResolvedValueOnce(editSession)
      .mockResolvedValueOnce({ ...editSession, edit_session_id: "sess-2", fencing_token: 2, lease_token: "lease-2", base_revision_id: "rev-9" });
    renderSection();
    fireEvent.click(await screen.findByText("Weekly report"));
    fireEvent.click(await screen.findByTestId("artifact-edit"));
    const editor = await screen.findByTestId("artifact-editor") as HTMLTextAreaElement;
    await waitFor(() => expect((screen.getByTestId("artifact-save-revision") as HTMLButtonElement).disabled).toBe(false));
    fireEvent.change(editor, { target: { value: "still here\n" } });
    fireEvent.click(screen.getByTestId("artifact-save-revision"));
    expect(await screen.findByTestId("artifact-editor-notice")).toHaveProperty("textContent", "This file changed. Your draft is still here.");
    expect((screen.getByTestId("artifact-editor") as HTMLTextAreaElement).value).toBe("still here\n");
    await waitFor(() => expect(apiMock.createArtifactEditSession).toHaveBeenCalledTimes(2));
  });

  it("stays read only when a lease cannot be opened and keeps the text", async () => {
    configStore.getState().setFeatureFlags({ [ARTIFACTS_V1_FLAG]: true });
    apiMock.listArtifacts.mockResolvedValue({
      artifacts: [
        {
          id: "art-1",
          workspace_id: "ws-1",
          owner_scope: "issue",
          owner_id: "issue-1",
          title: "Weekly report",
          kind: "file",
          status: "active",
          head_revision_id: "rev-1",
          revision_no: 1,
          filename: "rows.csv",
          media_type: "text/csv",
          created_at: "2026-10-17T00:00:00Z",
          updated_at: "2026-10-17T00:00:00Z",
          row_version: 1,
        },
      ],
    });
    apiMock.getArtifactPreview.mockResolvedValue({
      ...markdownPreview,
      filename: "rows.csv",
      media_type: "text/csv",
      preview_kind: "table",
    });
    apiMock.getArtifactRevisionText.mockResolvedValue("a,b\n");
    apiMock.createArtifactEditSession.mockRejectedValue(Object.assign(new Error("held"), { code: "LEASE_HELD" }));
    renderSection();
    fireEvent.click(await screen.findByText("Weekly report"));
    fireEvent.click(await screen.findByTestId("artifact-edit"));
    const editor = await screen.findByTestId("artifact-editor") as HTMLTextAreaElement;
    await waitFor(() => expect(editor.readOnly).toBe(true));
    expect(editor.value).toBe("a,b\n");
    expect((screen.getByTestId("artifact-save-draft") as HTMLButtonElement).disabled).toBe(true);
    expect(apiMock.saveArtifactDraft).not.toHaveBeenCalled();
  });
});

describe("ArtifactPreviewBody", () => {
  it("keeps unsanitized HTML in the source view", () => {
    const view = renderWithI18n(
      <ArtifactPreviewBody
        preview={{ ...htmlPreview, preview_kind: "html" }}
        text={"<!DOCTYPE html><script>alert(1)</script>"}
      />,
    );
    expect(view.container.querySelector("iframe")).toBeNull();
    expect(view.container.querySelector("script")).toBeNull();
    expect(screen.getByTestId("artifact-source").textContent).toContain("<script>alert(1)</script>");
  });

  it("renders a sanitized document in a sandboxed frame", () => {
    const view = renderWithI18n(
      <ArtifactPreviewBody
        preview={{
          ...htmlPreview,
          preview_kind: "html",
          static_preview_path: "/api/artifacts/art-1/revisions/rev-1/static-preview",
        }}
        text=""
        safeDocument="<p>Hello</p>"
      />,
    );
    const frame = screen.getByTestId("artifact-static-preview");
    expect(frame.getAttribute("sandbox")).toBe("");
    expect(frame.getAttribute("srcdoc")).toBe("<p>Hello</p>");
    expect(frame.getAttribute("csp")).toContain("connect-src 'none'");
    expect(frame.getAttribute("csp")).not.toContain("allow-scripts");
    expect(view.container.querySelector("script")).toBeNull();
  });
});
