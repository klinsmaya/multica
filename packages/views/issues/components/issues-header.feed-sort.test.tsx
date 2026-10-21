// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createStore } from "zustand/vanilla";
import { setApiInstance } from "@multica/core/api";
import type { ApiClient } from "@multica/core/api/client";
import {
  type IssueViewState,
  viewStoreSlice,
} from "@multica/core/issues/stores/view-store";
import { ViewStoreProvider } from "@multica/core/issues/stores/view-store-context";
import { renderWithI18n } from "../../test/i18n";
import { IssueDisplayControls } from "./issues-header";

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

function renderControls(
  state: Partial<IssueViewState>,
  { allowFeed = true }: { allowFeed?: boolean } = {},
) {
  setApiInstance({
    listIssueStatuses: async () => ({ statuses: [], categories: [], total: 0 }),
    listProperties: async () => ({ properties: [] }),
  } as unknown as ApiClient);
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Infinity } },
  });
  const store = createStore<IssueViewState>()(viewStoreSlice);
  store.setState(state);
  renderWithI18n(
    <QueryClientProvider client={qc}>
      <ViewStoreProvider store={store}>
        <IssueDisplayControls scopedIssues={[]} allowFeed={allowFeed} />
      </ViewStoreProvider>
    </QueryClientProvider>,
  );
  return store;
}

afterEach(() => {
  cleanup();
  document.body.innerHTML = "";
});

describe("IssueDisplayControls feed sort", () => {
  it("shows the two sort options in feed mode and switches between them", () => {
    const store = renderControls({ viewMode: "feed" });

    const created = screen.getByRole("button", { name: "By created" });
    const activity = screen.getByRole("button", { name: "By recent activity" });
    expect(created).toHaveAttribute("aria-pressed", "true");
    expect(activity).toHaveAttribute("aria-pressed", "false");

    fireEvent.click(activity);
    expect(store.getState().feedSort).toBe("activity");
    expect(screen.getByRole("button", { name: "By recent activity" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );

    fireEvent.click(screen.getByRole("button", { name: "By created" }));
    expect(store.getState().feedSort).toBe("created");
  });

  it.each(["board", "list", "table", "swimlane"] as const)(
    "hides the toggle in %s mode",
    (viewMode) => {
      renderControls({ viewMode, feedSort: "activity" });
      expect(screen.queryByRole("button", { name: "By created" })).toBeNull();
      expect(screen.queryByRole("button", { name: "By recent activity" })).toBeNull();
    },
  );

  it("hides the toggle where the feed layout is not offered", () => {
    renderControls({ viewMode: "feed" }, { allowFeed: false });
    expect(screen.queryByRole("button", { name: "By created" })).toBeNull();
  });
});
