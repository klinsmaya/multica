import { useParams } from "react-router-dom";
import { MarketDetailPage as SharedMarketDetailPage } from "@multica/views/market";
import { useDocumentTitle } from "@/hooks/use-document-title";

export function MarketDetailPage() {
  const { resourceId } = useParams<{ resourceId: string }>();
  useDocumentTitle("Market");
  if (!resourceId) return null;
  return <SharedMarketDetailPage resourceId={resourceId} />;
}
