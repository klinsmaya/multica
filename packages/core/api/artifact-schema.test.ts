import { describe, expect, it } from "vitest";
import { parseWithFallback } from "./schema";
import {
  ArtifactListSchema,
  ArtifactPreviewSchema,
  EMPTY_ARTIFACT_LIST,
  EMPTY_ARTIFACT_PREVIEW,
} from "./schemas";
import type { ArtifactList, ArtifactPreview } from "../types/artifact";

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
});
