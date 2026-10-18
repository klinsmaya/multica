"use client";

import { BrowsePage } from "./browse-page";
import { InstallPage } from "./install-page";
import { ListingPage } from "./listing-page";
import { withMarketFlag } from "./market-flag";

// The only entry for the package. Gating at the export keeps the ungated pages unreachable from apps.
// It is a client module because the gate runs at module scope and server route files re-export it.
export const MarketBrowsePage = withMarketFlag(BrowsePage);
export const MarketListingPage = withMarketFlag(ListingPage);
export const MarketInstallPage = withMarketFlag(InstallPage);
