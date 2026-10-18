"use client";

import { use } from "react";
import { MarketListingPage } from "@multica/views/market";

export default function MarketListingRoute({
  params,
}: {
  params: Promise<{ listingId: string }>;
}) {
  const { listingId } = use(params);
  return <MarketListingPage listingId={listingId} />;
}
