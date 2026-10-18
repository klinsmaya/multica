"use client";

import { useQuery } from "@tanstack/react-query";
import { useFeatureEnabled } from "@multica/core/config";
import { MARKET_CATALOG_V1_FLAG } from "@multica/core/feature-flags";
import { useWorkspaceId } from "@multica/core/hooks";
import { getMarketResource } from "@multica/core/market";
import { InstallWizard } from "./install-wizard";
import { useT } from "../i18n";

function normalizeResourceId(resourceId: string): string {
  if (!resourceId.includes("%")) return resourceId;
  try {
    return decodeURIComponent(resourceId);
  } catch {
    return resourceId;
  }
}

export function MarketDetailPage({ resourceId }: { resourceId: string }) {
  const id = normalizeResourceId(resourceId);
  const { t } = useT("market");
  const enabled = useFeatureEnabled(MARKET_CATALOG_V1_FLAG, false);
  const workspaceId = useWorkspaceId();
  const resource = useQuery({
    queryKey: ["market", "resource", workspaceId, id],
    queryFn: () => getMarketResource(workspaceId, id),
    enabled: enabled && workspaceId.length > 0 && id.length > 0,
  });

  if (!enabled) return null;

  return (
    <div className="flex flex-col gap-4 px-6 py-6">
      {resource.isPending ? <p className="text-body text-muted-foreground">{t(($) => $.loading)}</p> : null}
      {resource.isError ? <p className="text-body">{t(($) => $.resource_error)}</p> : null}
      {resource.data && resource.data.resourceId.length === 0 ? (
        <p className="text-body">{t(($) => $.unavailable)}</p>
      ) : null}
      {resource.data && resource.data.resourceId.length > 0 ? (
        <>
          <h1 className="text-title">{resource.data.name}</h1>
          <p className="text-caption text-muted-foreground">{resource.data.kind}</p>
          {resource.data.description ? (
            <p className="text-body">{resource.data.description}</p>
          ) : null}
          <InstallWizard workspaceId={workspaceId} resource={resource.data} />
        </>
      ) : null}
    </div>
  );
}
