import { describe, expect, it } from "vitest";
import { parseWithFallback } from "./schema";
import {
  ArtifactEditSessionSchema,
  ArtifactListSchema,
  ArtifactPreviewSchema,
  EMPTY_ARTIFACT_EDIT_SESSION,
  EMPTY_ARTIFACT_LIST,
  EMPTY_ARTIFACT_PREVIEW,
} from "./schemas";
import type { ArtifactEditSession, ArtifactList, ArtifactPreview } from "../types/artifact";

describe("artifact read schemas", () => {
  it("drops a malformed artifact list instead of throwing", () => {
    const parsed = parseWithFallback(
      { artifacts: "nope" },
      ArtifactListSchema,
      EMPTY_ARTIFACT_LIST,
      { endpoint: "GET /api/artifacts" },
    );
    expect(parsed.artifacts).toEqual([]);
  });

  it("keeps a committed artifact summary", () => {
    const parsed = parseWithFallback<ArtifactList>(
      {
        artifacts: [
          {
            id: "art-1",
            title: "Weekly report",
            filename: "report.md",
            revision_no: 2,
            status: "active",
            updated_by: "user-1",
            updated_at: "2026-10-17T00:00:00Z",
          },
        ],
      },
      ArtifactListSchema,
      EMPTY_ARTIFACT_LIST,
      { endpoint: "GET /api/artifacts" },
    );
    expect(parsed.artifacts[0]?.title).toBe("Weekly report");
    expect(parsed.artifacts[0]?.revision_no).toBe(2);
    expect(parsed.artifacts[0]?.filename).toBe("report.md");
  });

  it("treats a malformed preview as a non-active download", () => {
    const parsed = parseWithFallback<ArtifactPreview>(
      { preview_kind: 12, active_content: "yes" },
      ArtifactPreviewSchema,
      EMPTY_ARTIFACT_PREVIEW,
      { endpoint: "GET /api/artifacts/{id}/revisions/{rid}/preview" },
    );
    expect(parsed.preview_kind).toBe("download");
    expect(parsed.active_content).toBe(false);
    expect(parsed.content_disposition).toBe("attachment");
  });

  it("keeps a static preview path and does not turn it into active content", () => {
    const parsed = parseWithFallback<ArtifactPreview>(
      {
        preview_kind: "html",
        static_preview_path: "/api/artifacts/art-1/revisions/rev-1/static-preview",
        active_content: false,
      },
      ArtifactPreviewSchema,
      EMPTY_ARTIFACT_PREVIEW,
      { endpoint: "GET /api/artifacts/{id}/revisions/{rid}/preview" },
    );
    expect(parsed.preview_kind).toBe("html");
    expect(parsed.static_preview_path).toBe("/api/artifacts/art-1/revisions/rev-1/static-preview");
    expect(parsed.active_content).toBe(false);
  });

  it("keeps a lease and drops a malformed edit session", () => {
    const parsed = parseWithFallback<ArtifactEditSession>(
      {
        edit_session_id: "sess-1",
        fencing_token: 4,
        draft_content: "keep me\n",
        editable: true,
      },
      ArtifactEditSessionSchema,
      EMPTY_ARTIFACT_EDIT_SESSION,
      { endpoint: "POST /api/artifacts/{id}/edit-sessions" },
    );
    expect(parsed.edit_session_id).toBe("sess-1");
    expect(parsed.fencing_token).toBe(4);
    expect(parsed.draft_content).toBe("keep me\n");

    const broken = parseWithFallback(
      { fencing_token: "nope", editable: "yes" },
      ArtifactEditSessionSchema,
      EMPTY_ARTIFACT_EDIT_SESSION,
      { endpoint: "POST /api/artifacts/{id}/edit-sessions" },
    );
    expect(broken.edit_session_id).toBe("");
    expect(broken.fencing_token).toBe(0);
    expect(broken.editable).toBe(false);
  });
});
