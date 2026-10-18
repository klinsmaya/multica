import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { configStore } from "@multica/core/config";
import { I18nProvider } from "@multica/core/i18n/react";
import { WorkspaceSlugProvider } from "@multica/core/paths";
import type { MarketListing, OpenPreflight, ReadyPreflight } from "@multica/core/types";
import { NavigationProvider, type NavigationAdapter } from "../navigation";
import enMarket from "../locales/en/market.json";

const apiCalls = vi.hoisted(() => [] as { method: string; args: unknown[] }[]);
const apiResults = vi.hoisted(() => ({}) as Record<string, unknown>);

// Every api method is answered by this proxy and recorded, so a test can assert the
// exact set of calls a page makes, including calls nobody thought to stub.
vi.mock("@multica/core/api", async () => {
  const actual = await vi.importActual<typeof import("@multica/core/api")>("@multica/core/api");
  const api = new Proxy(
    {},
    {
      get: (_target, method) => {
        if (typeof method !== "string" || method === "then") return undefined;
        return (...args: unknown[]) => {
          apiCalls.push({ method, args });
          return method in apiResults
            ? Promise.resolve(apiResults[method])
            : Promise.reject(new Error(`unexpected api.${method}`));
        };
      },
    },
  );
  return { ApiError: actual.ApiError, api };
});

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

import { MarketBrowsePage, MarketInstallPage, MarketListingPage } from "./index";

const LISTING_ID = "acme.reviewer";
const RUNTIME = "11111111-1111-4111-8111-111111111111";
const PRIVATE_RUNTIME = "22222222-2222-4222-8222-222222222222";

const listing: MarketListing = {
  source: "live",
  listing: {
    id: LISTING_ID,
    name: "PR Reviewer",
    summary: "Reviews pull requests.",
    publisher: "Acme",
    icon_url: null,
    latest_version: "1.0.0",
  },
  version: "1.0.0",
  description: "",
  agent: { name: "Reviewer", description: "" },
  requirements: {
    model: { provider: "claude", models: [] },
    tools: [],
    connections: [],
    knowledge: [],
    unsupported: [],
  },
};

const meta = {
  listing_id: LISTING_ID,
  version: "1.0.0",
  target_scope: "workspace",
  evaluated_for: { user_id: "user-1", role: "member" },
} as const;

const readyReport: ReadyPreflight = {
  ...meta,
  source: "live",
  status: "ready",
  model: {
    provider: "claude",
    status: "satisfied",
    runtime_id: RUNTIME,
    model: null,
    options: [{ runtime_id: RUNTIME, label: "Claude (host)", usable: true, reason: null, models: [] }],
  },
  tools: [{ name: "github", status: "satisfied", binding: { action: "use_existing", id: "mcp-1" } }],
  connections: [],
  knowledge: [{ name: "code-review", status: "satisfied", binding: { action: "provision", id: null } }],
  unsupported: [],
};

const blockedReport: OpenPreflight = {
  ...meta,
  source: "live",
  status: "blocked",
  model: {
    provider: "claude",
    status: "choose",
    options: [
      { runtime_id: RUNTIME, label: "Claude (host)", usable: true, reason: null, models: [] },
      { runtime_id: PRIVATE_RUNTIME, label: "Dana's Claude", usable: false, reason: "runtime_private", models: [] },
    ],
  },
  tools: [],
  connections: [{ name: "github", status: "blocked", reason: "connection_not_connected" }],
  knowledge: [],
  unsupported: [],
};

let navigation: NavigationAdapter;

function renderPage(page: ReactNode, search = "") {
  navigation = {
    push: vi.fn(),
    replace: vi.fn(),
    back: vi.fn(),
    pathname: `/acme/market/${LISTING_ID}`,
    searchParams: new URLSearchParams(search),
    hash: "",
    getShareableUrl: (path) => path,
  };
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={queryClient}>
      <I18nProvider locale="en" resources={{ en: { market: enMarket } }}>
        <WorkspaceSlugProvider slug="acme">
          <NavigationProvider value={navigation}>{page}</NavigationProvider>
        </WorkspaceSlugProvider>
      </I18nProvider>
    </QueryClientProvider>,
  );
}

const calledMethods = () => apiCalls.map((call) => call.method);

describe("market pages", () => {
  beforeEach(() => {
    apiCalls.length = 0;
    for (const key of Object.keys(apiResults)) delete apiResults[key];
    apiResults.getMarketListing = listing;
    configStore.getState().setFeatureFlags({ market_v1: true });
  });

  afterEach(() => {
    configStore.getState().setFeatureFlags({});
  });

  it("renders no listing and no check control while market_v1 is missing", async () => {
    configStore.getState().setFeatureFlags({});
    apiResults.listMarketListings = { source: "live", items: [listing.listing], page: 1, page_size: 24, total: 1 };
    apiResults.preflightMarketInstall = readyReport;

    const { container } = renderPage(
      <>
        <MarketBrowsePage />
        <MarketListingPage listingId={LISTING_ID} />
        <MarketInstallPage listingId={LISTING_ID} />
      </>,
      "version=1.0.0",
    );
    await act(async () => {});

    expect(screen.queryByText("PR Reviewer")).toBeNull();
    expect(screen.queryByText(enMarket.detail.check_install)).toBeNull();
    expect(container).toBeEmptyDOMElement();
    expect(calledMethods()).toEqual([]);
  });

  it("links a listing to the check for its exact version", async () => {
    renderPage(<MarketListingPage listingId={LISTING_ID} />);

    expect(await screen.findByRole("heading", { name: "PR Reviewer" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: enMarket.detail.check_install })).toHaveAttribute(
      "href",
      `/acme/market/${LISTING_ID}/install?version=1.0.0`,
    );
  });

  it("says a ready workspace can accept the listing, offers no install, and only runs preflight", async () => {
    apiResults.preflightMarketInstall = readyReport;
    renderPage(<MarketInstallPage listingId={LISTING_ID} />, "version=1.0.0");

    expect(await screen.findByRole("status")).toHaveTextContent(enMarket.install.ready);
    const controls = [...screen.queryAllByRole("button"), ...screen.queryAllByRole("link")];
    expect(controls.map((el) => el.textContent)).not.toContainEqual(expect.stringMatching(/^install/i));
    expect(apiCalls).toEqual([
      {
        method: "preflightMarketInstall",
        args: [{ listingId: LISTING_ID, version: "1.0.0", runtimeId: undefined, model: undefined }],
      },
    ]);
  });

  it("names each block and puts the choice in the URL", async () => {
    apiResults.preflightMarketInstall = blockedReport;
    const user = userEvent.setup();
    renderPage(<MarketInstallPage listingId={LISTING_ID} />, "version=1.0.0");

    expect(await screen.findByRole("status")).toHaveTextContent(enMarket.install.blocked);
    expect(screen.getByText(enMarket.reasons.connection_not_connected)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: enMarket.install.connect })).toHaveAttribute(
      "href",
      "/acme/settings?tab=apps",
    );
    expect(screen.getByRole("radio", { name: /Dana's Claude/ })).toBeDisabled();
    expect(screen.getByText(enMarket.reasons.runtime_private)).toBeInTheDocument();

    await user.click(screen.getByRole("radio", { name: /Claude \(host\)/ }));
    expect(navigation.replace).toHaveBeenCalledWith(
      `/acme/market/${LISTING_ID}/install?version=1.0.0&runtime=${RUNTIME}`,
    );
  });

  it("asks for a listing instead of checking when the URL has no version", async () => {
    renderPage(<MarketInstallPage listingId={LISTING_ID} />);

    expect(await screen.findByText(enMarket.install.missing_version)).toBeInTheDocument();
    await waitFor(() => expect(calledMethods()).toEqual([]));
  });
});
