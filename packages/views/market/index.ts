"use client";

import { BrowsePage } from "./browse-page";
import { InstallPage } from "./install-page";
import { ListingPage } from "./listing-page";
import { withMarketFlag } from "./market-flag";

export const MarketBrowsePage = withMarketFlag(BrowsePage);
export const MarketListingPage = withMarketFlag(ListingPage);
export const MarketInstallPage = withMarketFlag(InstallPage);
