import { act, cleanup, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { agentTaskSnapshotOptions } from "@multica/core/agents";
import { taskMessagesOptions } from "@multica/core/chat/queries";
import type { AgentTask, TaskMessagePayload } from "@multica/core/types";
import { renderWithI18n } from "../../test/i18n";
import { FeedRunStatus, latestRunActivity } from "./feed-run-status";

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/workspace/hooks", () => ({
  useActorName: () => ({
    getActorName: (_type: string, id: string) => (id === "agent-1" ? "Coder" : "Unknown Agent"),
  }),
}));

vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: ({ actorId }: { actorId: string }) => <span data-testid="avatar">{actorId}</span>,
}));

const TASK_ID = "11111111-1111-4111-8111-111111111111";
const NOW = Date.parse("2026-10-02T10:10:00Z");

function task(overrides: Partial<AgentTask>): AgentTask {
  return {
    id: TASK_ID,
    agent_id: "agent-1",
    runtime_id: "runtime-1",
    issue_id: "issue-1",
    status: "running",
    priority: 0,
    dispatched_at: "2026-10-02T10:00:00Z",
    started_at: "2026-10-02T10:05:00Z",
    completed_at: null,
    result: null,
    error: null,
    created_at: "2026-10-02T10:00:00Z",
    ...overrides,
  };
}

function message(seq: number, overrides: Partial<TaskMessagePayload>): TaskMessagePayload {
  return { task_id: TASK_ID, issue_id: "issue-1", seq, type: "text", ...overrides };
}

let queryClient: QueryClient;

function renderStatus(tasks: AgentTask[], messages?: TaskMessagePayload[], issueId = "issue-1") {
  queryClient.setQueryData(agentTaskSnapshotOptions("ws-1").queryKey, tasks);
  if (messages) queryClient.setQueryData(taskMessagesOptions(TASK_ID).queryKey, messages);
  return renderWithI18n(
    <QueryClientProvider client={queryClient}>
      <FeedRunStatus issueId={issueId} />
    </QueryClientProvider>,
  );
}

describe("FeedRunStatus", () => {
  beforeEach(() => {
    vi.useFakeTimers({ toFake: ["Date", "setInterval", "clearInterval"] });
    vi.setSystemTime(NOW);
    queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  });

  afterEach(() => {
    cleanup();
    queryClient.clear();
    vi.useRealTimers();
  });

  it("shows the agent, elapsed minutes and the latest tool call for a running task", () => {
    renderStatus(
      [task({})],
      [
        message(1, { type: "text", content: "Looking at the repo" }),
        message(2, { type: "tool_use", tool: "Read", input: { file_path: "/repo/src/index.ts" } }),
        message(3, { type: "tool_result", tool: "Read", output: "file body" }),
      ],
    );

    const line = screen.getByTestId("feed-run-status");
    expect(line).toHaveTextContent("Coder");
    expect(line).toHaveTextContent("Running · 5 min");
    expect(line).toHaveTextContent("index.ts");
    expect(screen.getByTestId("avatar")).toHaveTextContent("agent-1");
  });

  it("refreshes the elapsed time every 30 seconds", () => {
    renderStatus([task({})], []);
    expect(screen.getByTestId("feed-run-status")).toHaveTextContent("Running · 5 min");

    act(() => {
      vi.advanceTimersByTime(60_000);
    });
    expect(screen.getByTestId("feed-run-status")).toHaveTextContent("Running · 6 min");
  });

  it("shows a bare running label before the first full minute", () => {
    renderStatus([task({ started_at: "2026-10-02T10:09:40Z" })], []);
    const line = screen.getByTestId("feed-run-status");
    expect(line).toHaveTextContent("Running");
    expect(line).not.toHaveTextContent("min");
  });

  it("shows only the queued label, with no transcript, for a queued task", () => {
    renderStatus([task({ status: "queued", started_at: null })], [
      message(1, { type: "text", content: "should not show" }),
    ]);

    const line = screen.getByTestId("feed-run-status");
    expect(line).toHaveTextContent("Coder");
    expect(line).toHaveTextContent("Queued");
    expect(line).not.toHaveTextContent("should not show");
  });

  it("prefers a running task over a queued one", () => {
    renderStatus([
      task({ id: "22222222-2222-4222-8222-222222222222", status: "queued", started_at: null }),
      task({}),
    ]);
    expect(screen.getByTestId("feed-run-status")).toHaveTextContent("Running · 5 min");
  });

  it("renders nothing when the issue has no active task", () => {
    const { container } = renderStatus([
      task({ status: "completed", completed_at: "2026-10-02T10:08:00Z" }),
      task({ id: "33333333-3333-4333-8333-333333333333", issue_id: "child-issue" }),
    ]);
    expect(container).toBeEmptyDOMElement();
  });

  it("ignores tasks that belong to a sub-issue", () => {
    const { container } = renderStatus([task({ issue_id: "child-issue" })]);
    expect(container).toBeEmptyDOMElement();
  });
});

describe("latestRunActivity", () => {
  it("returns the newest tool call, text or thinking summary and skips results", () => {
    expect(
      latestRunActivity([
        message(1, { type: "tool_use", tool: "Bash", input: { command: "pnpm test" } }),
        message(2, { type: "tool_result", tool: "Bash", output: "ok" }),
      ]),
    ).toBe("pnpm test");
    expect(
      latestRunActivity([
        message(1, { type: "tool_use", tool: "Bash", input: { command: "pnpm test" } }),
        message(2, { type: "thinking", content: "Checking the failure\nsecond line" }),
      ]),
    ).toBe("Checking the failure");
  });

  it("falls back to the tool name and redacts secrets", () => {
    expect(latestRunActivity([message(1, { type: "tool_use", tool: "TodoWrite", input: {} })])).toBe("TodoWrite");
    expect(
      latestRunActivity([message(1, { type: "text", content: "token sk-ant-api03-abcdefghijklmnopqrstuvwxyz0123" })]),
    ).not.toContain("abcdefghijklmnopqrstuvwxyz0123");
  });

  it("is empty for an empty or result-only transcript", () => {
    expect(latestRunActivity([])).toBe("");
    expect(latestRunActivity([message(1, { type: "tool_result", output: "x" })])).toBe("");
  });
});
