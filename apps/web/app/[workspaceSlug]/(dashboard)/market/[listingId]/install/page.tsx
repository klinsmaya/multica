"use client";

import { use } from "react";
import { MarketInstallPage } from "@multica/views/market";

export default function MarketInstallRoute({
  params,
}: {
  params: Promise<{ listingId: string }>;
}) {
  const { listingId } = use(params);
  return <MarketInstallPage listingId={listingId} />;
}
