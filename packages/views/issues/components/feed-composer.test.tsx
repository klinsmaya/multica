import { forwardRef, useImperativeHandle, useRef, useState } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useFeedComposerStore } from "@multica/core/issues/stores";
import type { Issue, Project } from "@multica/core/types";
import { renderWithI18n } from "../../test/i18n";
import { FeedComposer } from "./feed-composer";

const mockCreateIssue = vi.hoisted(() => vi.fn());
const mockGetProject = vi.hoisted(() => vi.fn());
const editorProps = vi.hoisted(() => ({ submitOnEnter: undefined as boolean | undefined }));

vi.mock("@multica/core/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/api")>()),
  api: { createIssue: mockCreateIssue, getProject: mockGetProject },
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

// The real editor is Tiptap; its Enter / Shift+Enter / IME behavior is covered
// by the submit-shortcut tests. The stand-in only reports markdown and fires
// onSubmit the way the extension does for a plain Enter.
vi.mock("../../editor", () => ({
  ContentEditor: forwardRef(function MockContentEditor(
    {
      defaultValue,
      placeholder,
      onUpdate,
      onSubmit,
      submitOnEnter,
    }: {
      defaultValue?: string;
      placeholder?: string;
      onUpdate?: (markdown: string) => void;
      onSubmit?: () => void;
      submitOnEnter?: boolean;
    },
    ref,
  ) {
    editorProps.submitOnEnter = submitOnEnter;
    const [value, setValue] = useState(defaultValue ?? "");
    const latest = useRef(value);
    latest.current = value;
    useImperativeHandle(ref, () => ({
      getMarkdown: () => latest.current,
      clearContent: () => {
        latest.current = "";
        setValue("");
        onUpdate?.("");
      },
      flushPendingUpdate: () => null,
      focus: () => {},
    }));
    return (
      <textarea
        data-testid="editor"
        value={value}
        placeholder={placeholder}
        onChange={(event) => {
          latest.current = event.target.value;
          setValue(event.target.value);
          onUpdate?.(event.target.value);
        }}
        onKeyDown={(event) => {
          if (event.key === "Enter" && !event.shiftKey) {
            event.preventDefault();
            onSubmit?.();
          }
        }}
      />
    );
  }),
}));

const AGENT_ID = "11111111-1111-4111-8111-111111111111";
const LEAD_ID = "22222222-2222-4222-8222-222222222222";
const SQUAD_ID = "33333333-3333-4333-8333-333333333333";

function project(overrides: Partial<Project> = {}): Project {
  return { id: "p-1", lead_type: null, lead_id: null, ...overrides } as Project;
}

function createdIssue(id = "issue-new"): Issue {
  return { id, parent_issue_id: null, created_at: "2026-06-01T00:00:00Z" } as Issue;
}

function renderComposer(projectId = "p-1", onSent = vi.fn()) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const ui = (id: string) => (
    <QueryClientProvider client={queryClient}>
      <FeedComposer projectId={id} onSent={onSent} />
    </QueryClientProvider>
  );
  const view = renderWithI18n(ui(projectId));
  return {
    onSent,
    switchTo: (id: string) => view.rerender(ui(id)),
  };
}

function type(text: string) {
  fireEvent.change(screen.getByTestId("editor"), { target: { value: text } });
}

async function sendWithButton() {
  await act(async () => {
    fireEvent.click(screen.getByRole("button", { name: "Send" }));
  });
}

beforeEach(() => {
  vi.clearAllMocks();
  useFeedComposerStore.setState({ drafts: {} });
  mockGetProject.mockResolvedValue(project());
  mockCreateIssue.mockResolvedValue(createdIssue());
});

describe("FeedComposer", () => {
  it("sends on plain Enter", async () => {
    renderComposer();
    expect(editorProps.submitOnEnter).toBe(true);

    type("Fix login");
    await act(async () => {
      fireEvent.keyDown(screen.getByTestId("editor"), { key: "Enter" });
    });

    await waitFor(() => expect(mockCreateIssue).toHaveBeenCalledTimes(1));
  });

  it("makes the first line the title and the rest the description in the current project", async () => {
    renderComposer("p-1");
    type("Fix login\n\nSteps:\n- open the page");
    await sendWithButton();

    await waitFor(() => expect(mockCreateIssue).toHaveBeenCalledTimes(1));
    expect(mockCreateIssue).toHaveBeenCalledWith({
      title: "Fix login",
      description: "Steps:\n- open the page",
      project_id: "p-1",
    });
  });

  it("assigns an explicit @agent and @squad mention", async () => {
    renderComposer();
    type(`Review this [@Go](mention://agent/${AGENT_ID})`);
    await sendWithButton();
    await waitFor(() => expect(mockCreateIssue).toHaveBeenCalledTimes(1));
    expect(mockCreateIssue.mock.calls[0]![0]).toMatchObject({
      assignee_type: "agent",
      assignee_id: AGENT_ID,
    });

    type(`[@Core](mention://squad/${SQUAD_ID}) take this`);
    await sendWithButton();
    await waitFor(() => expect(mockCreateIssue).toHaveBeenCalledTimes(2));
    expect(mockCreateIssue.mock.calls[1]![0]).toMatchObject({
      assignee_type: "squad",
      assignee_id: SQUAD_ID,
    });
  });

  it("falls back to the project lead, and an explicit mention wins over it", async () => {
    mockGetProject.mockResolvedValue(project({ lead_type: "agent", lead_id: LEAD_ID }));
    renderComposer();

    type("No mention here");
    await sendWithButton();
    await waitFor(() => expect(mockCreateIssue).toHaveBeenCalledTimes(1));
    expect(mockCreateIssue.mock.calls[0]![0]).toMatchObject({
      assignee_type: "agent",
      assignee_id: LEAD_ID,
    });

    type(`Ask [@Go](mention://agent/${AGENT_ID})`);
    await sendWithButton();
    await waitFor(() => expect(mockCreateIssue).toHaveBeenCalledTimes(2));
    expect(mockCreateIssue.mock.calls[1]![0]).toMatchObject({ assignee_id: AGENT_ID });
  });

  it("assigns a member lead as a member and leaves a leaderless project unassigned", async () => {
    mockGetProject.mockResolvedValue(project({ lead_type: "member", lead_id: LEAD_ID }));
    const first = renderComposer();
    type("For the lead");
    await sendWithButton();
    await waitFor(() => expect(mockCreateIssue).toHaveBeenCalledTimes(1));
    expect(mockCreateIssue.mock.calls[0]![0]).toMatchObject({
      assignee_type: "member",
      assignee_id: LEAD_ID,
    });

    mockGetProject.mockResolvedValue(project({ id: "p-2" }));
    first.switchTo("p-2");
    type("Nobody");
    await sendWithButton();
    await waitFor(() => expect(mockCreateIssue).toHaveBeenCalledTimes(2));
    expect(mockCreateIssue.mock.calls[1]![0]).not.toHaveProperty("assignee_type");
    expect(mockCreateIssue.mock.calls[1]![0]).not.toHaveProperty("assignee_id");
  });

  it("keeps the text until the server answers, then clears it and reports the issue", async () => {
    let resolve!: (issue: Issue) => void;
    mockCreateIssue.mockReturnValue(new Promise<Issue>((r) => (resolve = r)));
    const { onSent } = renderComposer();

    type("Slow one");
    await sendWithButton();

    await waitFor(() => expect(screen.getByTestId("feed-composer-pending")).toHaveTextContent("Slow one"));
    expect(screen.getByTestId("feed-composer-pending")).toHaveTextContent("Sending…");
    expect(screen.getByTestId("editor")).toHaveValue("Slow one");
    expect(onSent).not.toHaveBeenCalled();

    await act(async () => resolve(createdIssue("issue-9")));

    await waitFor(() => expect(screen.queryByTestId("feed-composer-pending")).toBeNull());
    expect(screen.getByTestId("editor")).toHaveValue("");
    expect(useFeedComposerStore.getState().getDraft("ws-1:p-1")).toBeUndefined();
    expect(onSent).toHaveBeenCalledWith(expect.objectContaining({ id: "issue-9" }));
  });

  it("keeps the text on failure and retries the same message", async () => {
    mockCreateIssue.mockRejectedValueOnce(new Error("boom"));
    const { onSent } = renderComposer();

    type("Flaky\n\ndetails");
    await sendWithButton();

    expect(await screen.findByText("Couldn't send")).toBeInTheDocument();
    expect(screen.getByTestId("editor")).toHaveValue("Flaky\n\ndetails");
    expect(useFeedComposerStore.getState().getDraft("ws-1:p-1")).toBe("Flaky\n\ndetails");
    expect(onSent).not.toHaveBeenCalled();

    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    });

    await waitFor(() => expect(mockCreateIssue).toHaveBeenCalledTimes(2));
    expect(mockCreateIssue.mock.calls[1]![0]).toEqual(mockCreateIssue.mock.calls[0]![0]);
    await waitFor(() => expect(screen.getByTestId("editor")).toHaveValue(""));
    expect(screen.queryByTestId("feed-composer-pending")).toBeNull();
    expect(onSent).toHaveBeenCalledTimes(1);
  });

  it("does not send a blank message", async () => {
    renderComposer();
    type("  \n ");

    expect(screen.getByRole("button", { name: "Send" })).toBeDisabled();
    await act(async () => {
      fireEvent.keyDown(screen.getByTestId("editor"), { key: "Enter" });
    });
    expect(mockCreateIssue).not.toHaveBeenCalled();
  });

  it("keeps a separate draft per project", async () => {
    const { switchTo } = renderComposer("p-1");
    type("Draft for one");
    expect(useFeedComposerStore.getState().getDraft("ws-1:p-1")).toBe("Draft for one");

    switchTo("p-2");
    expect(screen.getByTestId("editor")).toHaveValue("");
    type("Draft for two");
    expect(useFeedComposerStore.getState().getDraft("ws-1:p-2")).toBe("Draft for two");

    switchTo("p-1");
    expect(screen.getByTestId("editor")).toHaveValue("Draft for one");
  });
});
