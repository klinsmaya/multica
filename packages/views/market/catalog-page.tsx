"use client";

import { useQuery } from "@tanstack/react-query";
import { useFeatureEnabled } from "@multica/core/config";
import { MARKET_CATALOG_V1_FLAG } from "@multica/core/feature-flags";
import { useWorkspaceId } from "@multica/core/hooks";
import { listMarketCatalog } from "@multica/core/market";
import { useWorkspacePaths } from "@multica/core/paths";
import { AppLink } from "../navigation";
import { useT } from "../i18n";

export function MarketCatalogPage() {
  const { t } = useT("market");
  const enabled = useFeatureEnabled(MARKET_CATALOG_V1_FLAG, false);
  const workspaceId = useWorkspaceId();
  const paths = useWorkspacePaths();
  const catalog = useQuery({
    queryKey: ["market", "catalog", workspaceId],
    queryFn: () => listMarketCatalog(workspaceId),
    enabled: enabled && workspaceId.length > 0,
  });

  if (!enabled) return null;

  return (
    <div className="flex flex-col gap-4 px-6 py-6">
      <h1 className="text-title">{t(($) => $.title)}</h1>
      {catalog.isPending ? <p className="text-body text-muted-foreground">{t(($) => $.loading)}</p> : null}
      {catalog.isError ? <p className="text-body">{t(($) => $.catalog_error)}</p> : null}
      {catalog.data && catalog.data.resources.length === 0 ? (
        <p className="text-body text-muted-foreground">{t(($) => $.empty)}</p>
      ) : null}
      {catalog.data && catalog.data.resources.length > 0 ? (
        <ul className="flex flex-col gap-2">
          {catalog.data.resources.map((resource) => (
            <li key={resource.resourceId}>
              <AppLink href={paths.marketDetail(resource.resourceId)} className="text-body">
                {resource.name}
              </AppLink>
              <span className="ml-2 text-caption text-muted-foreground">{resource.kind}</span>
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  );
}
