"use client";

import { useQuery } from "@tanstack/react-query";
import { useWorkspaceId } from "@multica/core/hooks";
import { marketPreflightOptions } from "@multica/core/market";
import { useWorkspacePaths } from "@multica/core/paths";
import { Button } from "@multica/ui/components/ui/button";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../i18n";
import { BreadcrumbHeader } from "../layout/breadcrumb-header";
import { PAGE_GUTTER, PAGE_RAIL } from "../layout/page-header";
import { AppLink } from "../navigation";
import { MarketError, MarketMessage } from "./market-parts";
import { PreflightChecklist } from "./preflight-checklist";
import { useInstallParams } from "./use-install-params";

export function InstallPage({ listingId }: { listingId: string }) {
  const { t } = useT("market");
  const wsId = useWorkspaceId();
  const p = useWorkspacePaths();
  const { input, chooseRuntime, chooseModel } = useInstallParams(listingId);
  const { data: report, error, isFetching, refetch } = useQuery(marketPreflightOptions(wsId, input));
  const recheck = () => void refetch();

  let body;
  if (!input) {
    body = (
      <MarketMessage
        action={
          <Button
            size="sm"
            variant="outline"
            render={<AppLink href={p.marketListing(listingId)} />}
            nativeButton={false}
          >
            {t(($) => $.install.back)}
          </Button>
        }
      >
        {t(($) => $.install.missing_version)}
      </MarketMessage>
    );
  } else if (error) {
    body = <MarketError error={error} onRetry={recheck} />;
  } else if (!report) {
    body = (
      <div className="space-y-3">
        <Skeleton className="h-10 w-full" />
        <Skeleton className="h-24 w-full" />
        <Skeleton className="h-16 w-full" />
      </div>
    );
  } else if (report.status === "unreadable") {
    body = (
      <MarketMessage
        action={
          <Button type="button" variant="outline" size="sm" onClick={recheck}>
            {t(($) => $.install.recheck)}
          </Button>
        }
      >
        {t(($) => $.install.unreadable)}
      </MarketMessage>
    );
  } else {
    body = (
      <PreflightChecklist
        report={report}
        onChooseRuntime={chooseRuntime}
        onChooseModel={chooseModel}
        onRecheck={recheck}
        rechecking={isFetching}
      />
    );
  }

  return (
    <div className="flex h-full min-h-0 flex-col">
      <BreadcrumbHeader
        segments={[
          { href: p.market(), label: t(($) => $.detail.back) },
          { href: p.marketListing(listingId), label: listingId, className: "min-w-0 max-w-72 truncate" },
        ]}
        leaf={<span className="truncate">{t(($) => $.install.title)}</span>}
      />
      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className={cn(PAGE_RAIL, PAGE_GUTTER, "max-w-3xl space-y-6 py-6")}>{body}</div>
      </div>
    </div>
  );
}
