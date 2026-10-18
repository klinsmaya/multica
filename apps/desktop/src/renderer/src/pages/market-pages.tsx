import { useParams } from "react-router-dom";
import { MarketInstallPage, MarketListingPage } from "@multica/views/market";

// These wrappers sit outside the market_v1 gate, so they read params and nothing else.
export function MarketListingRoute() {
  const { listingId } = useParams<{ listingId: string }>();
  if (!listingId) return null;
  return <MarketListingPage listingId={listingId} />;
}

export function MarketInstallRoute() {
  const { listingId } = useParams<{ listingId: string }>();
  if (!listingId) return null;
  return <MarketInstallPage listingId={listingId} />;
}
