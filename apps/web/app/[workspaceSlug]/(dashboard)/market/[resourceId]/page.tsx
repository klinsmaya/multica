"use client";

import { use } from "react";
import { MarketDetailPage } from "@multica/views/market";

export default function MarketDetailRoute({
  params,
}: {
  params: Promise<{ resourceId: string }>;
}) {
  const { resourceId } = use(params);
  return <MarketDetailPage resourceId={resourceId} />;
}
