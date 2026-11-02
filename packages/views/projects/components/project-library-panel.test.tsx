import { beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ListProjectAttachmentsResponse, ProjectAttachment } from "@multica/core/types";
import { useModalStore } from "@multica/core/modals";
import { useProjectLibraryViewStore } from "@multica/core/projects";
import { renderWithI18n } from "../../test/i18n";

const mocks = vi.hoisted(() => ({
  listProjectAttachments: vi.fn(),
  getProjectLibraryIssue: vi.fn(),
  uploadFile: vi.fn(),
  createComment: vi.fn(),
  openIssue: vi.fn(),
  openIssueFor: vi.fn(),
  toastError: vi.fn(),
}));

vi.mock("@multica/core/api", () => ({
  api: {
    listProjectAttachments: mocks.listProjectAttachments,
    getProjectLibraryIssue: mocks.getProjectLibraryIssue,
    uploadFile: mocks.uploadFile,
    createComment: mocks.createComment,
  },
}));

vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));

vi.mock("@multica/core/workspace/hooks", () => ({
  useActorName: () => ({
    getActorName: (type: string, id: string) => (type === "agent" ? `Agent ${id}` : `Member ${id}`),
  }),
}));

vi.mock("sonner", () => ({ toast: { error: mocks.toastError, success: vi.fn() } }));

vi.mock("../../issues/components/feed-message-card", () => ({
  useOpenFeedIssue: (issueId: string) => () => mocks.openIssueFor(issueId),
}));

vi.mock("../../issues/components/deliverables/deliverable-thumbnail", () => ({
  DeliverableThumbnail: ({ attachment }: { attachment: { filename: string } }) => (
    <span data-testid="thumbnail">{attachment.filename}</span>
  ),
}));

import { ProjectLibraryPanel } from "./project-library-panel";

function row(over: Partial<ProjectAttachment> & { id: string; filename: string }): ProjectAttachment {
  return {
    workspace_id: "ws-1",
    issue_id: "issue-1",
    comment_id: null,
    chat_session_id: null,
    chat_message_id: null,
    uploader_type: "agent",
    uploader_id: "lambda",
    url: `https://cdn.example.test/${over.id}`,
    download_url: `https://cdn.example.test/${over.id}?sig=1`,
    markdown_url: `https://cdn.example.test/${over.id}`,
    content_type: "application/octet-stream",
    size_bytes: 2048,
    created_at: "2026-09-20T10:00:00Z",
    issue: { id: "issue-1", identifier: "MUL-1", title: "Ship it" },
    ...over,
  };
}

const REPORT = row({ id: "a-report", filename: "report.pdf", content_type: "application/pdf" });
const SHOT = row({ id: "a-shot", filename: "shot.png", content_type: "image/png" });
const NOTES = row({
  id: "m-notes",
  filename: "notes.txt",
  content_type: "text/plain",
  uploader_type: "member",
  uploader_id: "alice",
  issue_id: "issue-2",
  issue: { id: "issue-2", identifier: "MUL-2", title: "Plan" },
});

function page(
  attachments: ProjectAttachment[],
  totals: { deliverable_total: number; upload_total: number } = {
    deliverable_total: attachments.filter((a) => a.uploader_type === "agent").length,
    upload_total: attachments.filter((a) => a.uploader_type !== "agent").length,
  },
): ListProjectAttachmentsResponse {
  return { attachments, total: totals.deliverable_total + totals.upload_total, ...totals };
}

function renderPanel() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  renderWithI18n(
    <QueryClientProvider client={qc}>
      <ProjectLibraryPanel projectId="proj-1" />
    </QueryClientProvider>,
  );
  return qc;
}

beforeEach(() => {
  vi.clearAllMocks();
  useModalStore.setState({ modal: null, data: null });
  useProjectLibraryViewStore.setState({ viewMode: "list" });
  mocks.listProjectAttachments.mockResolvedValue(page([REPORT, SHOT, NOTES]));
});

describe("ProjectLibraryPanel", () => {
  it("lists deliverables and files in two groups with their counts", async () => {
    renderPanel();

    const deliverables = await screen.findByRole("button", { name: /Deliverables/ });
    const files = screen.getByRole("button", { name: /Files/ });
    expect(deliverables).toHaveTextContent("2");
    expect(files).toHaveTextContent("1");

    expect(screen.getByTitle("report.pdf")).toBeInTheDocument();
    expect(screen.getByTitle("shot.png")).toBeInTheDocument();
    expect(screen.getByTitle("notes.txt")).toBeInTheDocument();
    // One request labels both groups.
    expect(mocks.listProjectAttachments).toHaveBeenCalledTimes(1);
    expect(mocks.listProjectAttachments).toHaveBeenCalledWith("proj-1", { limit: 40, offset: 0 });
  });

  it("shows who uploaded a row, when and how big", async () => {
    renderPanel();
    const notes = await screen.findByTitle("notes.txt");
    expect(notes).toHaveTextContent("Member alice");
    expect(notes).toHaveTextContent("2 KB");
    expect(screen.getByTitle("report.pdf")).toHaveTextContent("Agent lambda");
  });

  it("narrows to one kind when a category is picked", async () => {
    const user = userEvent.setup();
    renderPanel();
    await screen.findByTitle("report.pdf");

    await user.click(screen.getByRole("button", { name: "Images" }));

    expect(screen.getByTitle("shot.png")).toBeInTheDocument();
    expect(screen.queryByTitle("report.pdf")).not.toBeInTheDocument();
    expect(screen.queryByTitle("notes.txt")).not.toBeInTheDocument();
    // The member group has nothing left in this kind, so it drops out.
    expect(screen.queryByRole("button", { name: /Files/ })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Deliverables/ })).toHaveTextContent("1");

    await user.click(screen.getByRole("button", { name: "Videos" }));
    expect(screen.getByText("No files match this filter")).toBeInTheDocument();
  });

  it("opens the full-screen focus with the issue and attachment on a row click", async () => {
    const user = userEvent.setup();
    renderPanel();

    await user.click(await screen.findByTitle("notes.txt"));

    expect(useModalStore.getState().modal).toBe("deliverable-focus");
    expect(useModalStore.getState().data).toEqual({ issueId: "issue-2", attachmentId: "m-notes" });
  });

  it("opens the issue from its chip without opening the file", async () => {
    const user = userEvent.setup();
    renderPanel();
    await screen.findByTitle("notes.txt");

    await user.click(screen.getByRole("button", { name: "Open issue MUL-2" }));

    expect(mocks.openIssueFor).toHaveBeenCalledWith("issue-2");
    expect(useModalStore.getState().modal).toBeNull();
  });

  it("switches to the grid and remembers it", async () => {
    const user = userEvent.setup();
    renderPanel();
    await screen.findByTitle("report.pdf");

    await user.click(screen.getByRole("button", { name: "Grid view" }));

    expect(useProjectLibraryViewStore.getState().viewMode).toBe("grid");
    expect(screen.getByRole("button", { name: "Grid view" })).toHaveAttribute("aria-pressed", "true");
    // Tiles carry the file name and the issue chip too.
    expect(screen.getByTitle("report.pdf")).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: "Open issue MUL-1" })).toHaveLength(2);
  });

  it("collapses a group", async () => {
    const user = userEvent.setup();
    renderPanel();
    await screen.findByTitle("notes.txt");

    await user.click(screen.getByRole("button", { name: /Files/ }));

    await waitFor(() => expect(screen.queryByTitle("notes.txt")).not.toBeInTheDocument());
    expect(screen.getByTitle("report.pdf")).toBeInTheDocument();
  });

  it("loads the next page when there are more files than loaded", async () => {
    const user = userEvent.setup();
    mocks.listProjectAttachments
      .mockResolvedValueOnce(page([REPORT], { deliverable_total: 2, upload_total: 0 }))
      .mockResolvedValueOnce(page([SHOT], { deliverable_total: 2, upload_total: 0 }));
    renderPanel();
    await screen.findByTitle("report.pdf");

    await user.click(screen.getByRole("button", { name: "Load more" }));

    await screen.findByTitle("shot.png");
    expect(mocks.listProjectAttachments).toHaveBeenLastCalledWith("proj-1", { limit: 40, offset: 40 });
    expect(screen.queryByRole("button", { name: "Load more" })).not.toBeInTheDocument();
  });

  it("shows the empty state for a project without files", async () => {
    mocks.listProjectAttachments.mockResolvedValue(page([]));
    renderPanel();

    expect(await screen.findByText("No files yet")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Deliverables/ })).not.toBeInTheDocument();
  });

  it("uploads to the project files issue, then comments the file card there", async () => {
    const user = userEvent.setup();
    mocks.getProjectLibraryIssue.mockResolvedValue({ id: "holder", identifier: "MUL-9" });
    mocks.uploadFile.mockResolvedValue({
      id: "att-new",
      filename: "plan.pdf",
      url: "https://cdn.example.test/plan.pdf",
      download_url: "",
      markdown_url: "/api/attachments/att-new/download",
    });
    mocks.createComment.mockResolvedValue({ id: "c-new", issue_id: "holder", attachments: [] });
    renderPanel();
    await screen.findByTitle("report.pdf");

    const file = new File(["x"], "plan.pdf", { type: "application/pdf" });
    await user.upload(screen.getByTestId("project-library-file-input"), file);

    await waitFor(() => expect(mocks.createComment).toHaveBeenCalledTimes(1));
    expect(mocks.getProjectLibraryIssue).toHaveBeenCalledWith("proj-1");
    expect(mocks.uploadFile).toHaveBeenCalledWith(file, { issueId: "holder" });
    expect(mocks.createComment).toHaveBeenCalledWith(
      "holder",
      "!file[plan.pdf](/api/attachments/att-new/download)",
      undefined,
      undefined,
      ["att-new"],
      undefined,
      undefined,
    );
    // The library reloads once the server has the file.
    await waitFor(() => expect(mocks.listProjectAttachments).toHaveBeenCalledTimes(2));
    expect(mocks.toastError).not.toHaveBeenCalled();
  });

  it("keeps the button busy while uploading", async () => {
    const user = userEvent.setup();
    let finish: (value: unknown) => void = () => {};
    mocks.getProjectLibraryIssue.mockReturnValue(new Promise((resolve) => { finish = resolve; }));
    renderPanel();
    await screen.findByTitle("report.pdf");

    await user.upload(
      screen.getByTestId("project-library-file-input"),
      new File(["x"], "plan.pdf", { type: "application/pdf" }),
    );

    const add = screen.getByRole("button", { name: "Add" });
    expect(add).toBeDisabled();
    expect(add).toHaveAttribute("aria-busy", "true");

    finish({ id: "", identifier: "" });
    await waitFor(() => expect(screen.getByRole("button", { name: "Add" })).toBeEnabled());
  });

  it("reports a failed upload and posts no comment", async () => {
    const user = userEvent.setup();
    mocks.getProjectLibraryIssue.mockResolvedValue({ id: "holder", identifier: "MUL-9" });
    mocks.uploadFile.mockRejectedValue(new Error("Upload failed: 500"));
    renderPanel();
    await screen.findByTitle("report.pdf");

    await user.upload(
      screen.getByTestId("project-library-file-input"),
      new File(["x"], "plan.pdf", { type: "application/pdf" }),
    );

    await waitFor(() => expect(mocks.toastError).toHaveBeenCalledWith("Upload failed: 500"));
    expect(mocks.createComment).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "Add" })).toBeEnabled();
  });
});
