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
  getArtifactRevisionText: vi.fn(),
  getArtifactRevisionBlob: vi.fn(),
}));

vi.mock("@multica/core/api", () => ({ api: apiMock }));

vi.mock("@multica/core/workspace/hooks", () => ({
  useActorName: () => ({
    getActorName: (_type: string, id: string) => `Member ${id}`,
  }),
}));

import { ArtifactPreviewBody, ArtifactsSection } from "./artifacts-section";

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
    apiMock.getArtifactRevisionText.mockReset();
    apiMock.getArtifactRevisionBlob.mockReset();
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
    await waitFor(() => expect(apiMock.getArtifactPreview).toHaveBeenCalledWith("art-1", "rev-1"));
  });
});

describe("ArtifactPreviewBody", () => {
  it("does not render HTML in a frame even when the kind says html", () => {
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
});
