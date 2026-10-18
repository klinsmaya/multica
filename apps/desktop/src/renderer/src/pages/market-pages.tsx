import { useParams } from "react-router-dom";
import { MarketInstallPage, MarketListingPage } from "@multica/views/market";

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
