import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiClient } from "./client";

afterEach(() => {
  vi.unstubAllGlobals();
});

function respondWith(body: unknown) {
  const fetchMock = vi.fn().mockResolvedValue(
    new Response(JSON.stringify(body), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    }),
  );
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

const row = {
  id: "att-1",
  workspace_id: "ws-1",
  issue_id: "issue-1",
  comment_id: null,
  uploader_type: "agent",
  uploader_id: "agent-1",
  filename: "report.pdf",
  url: "https://cdn.example.test/report.pdf",
  download_url: "/api/attachments/att-1/download",
  content_type: "application/pdf",
  size_bytes: 1024,
  created_at: "2026-01-01T00:00:00Z",
  issue: { id: "issue-1", identifier: "MUL-1", title: "Ship it" },
};

describe("ApiClient project library", () => {
  it("lists a project's attachments with the source and paging in the query", async () => {
    const fetchMock = respondWith({
      attachments: [row],
      total: 7,
      deliverable_total: 4,
      upload_total: 3,
    });
    const client = new ApiClient("https://api.example.test");

    const result = await client.listProjectAttachments("proj-1", {
      source: "deliverable",
      limit: 30,
      offset: 30,
    });

    expect(String(fetchMock.mock.calls[0]?.[0])).toBe(
      "https://api.example.test/api/projects/proj-1/attachments?source=deliverable&limit=30&offset=30",
    );
    expect(result.attachments).toHaveLength(1);
    expect(result.attachments[0]?.issue.identifier).toBe("MUL-1");
    expect(result).toMatchObject({ total: 7, deliverable_total: 4, upload_total: 3 });
  });

  it("defaults the fields an older server omits", async () => {
    respondWith({ attachments: [{ id: "att-2", issue: { id: "issue-1" } }] });
    const client = new ApiClient("https://api.example.test");

    const result = await client.listProjectAttachments("proj-1");

    expect(result.total).toBe(0);
    expect(result.deliverable_total).toBe(0);
    expect(result.upload_total).toBe(0);
    expect(result.attachments[0]).toMatchObject({
      id: "att-2",
      filename: "",
      content_type: "",
      size_bytes: 0,
      uploader_type: "",
      markdown_url: "",
      issue: { id: "issue-1", identifier: "", title: "" },
    });
  });

  it("drops a malformed row and keeps the rest", async () => {
    respondWith({
      attachments: [row, { filename: "no id", issue: { id: "issue-1" } }, { id: "att-3" }, null, "x"],
      total: 5,
    });
    const client = new ApiClient("https://api.example.test");

    const result = await client.listProjectAttachments("proj-1");

    expect(result.attachments.map((a) => a.id)).toEqual(["att-1"]);
    expect(result.total).toBe(5);
  });

  it("falls back to an empty library when the body is not an object", async () => {
    respondWith("nope");
    const client = new ApiClient("https://api.example.test");

    const result = await client.listProjectAttachments("proj-1");

    expect(result).toEqual({ attachments: [], total: 0, deliverable_total: 0, upload_total: 0 });
  });

  it("posts for the library issue and falls back to an empty one on a bad body", async () => {
    const fetchMock = respondWith({ id: "issue-9", identifier: "MUL-9" });
    const client = new ApiClient("https://api.example.test");

    await expect(client.getProjectLibraryIssue("proj-1")).resolves.toMatchObject({
      id: "issue-9",
      identifier: "MUL-9",
    });
    expect(String(fetchMock.mock.calls[0]?.[0])).toBe(
      "https://api.example.test/api/projects/proj-1/library-issue",
    );
    expect(fetchMock.mock.calls[0]?.[1]?.method).toBe("POST");

    respondWith({ identifier: "MUL-9" });
    await expect(client.getProjectLibraryIssue("proj-1")).resolves.toEqual({ id: "", identifier: "" });
  });
});
