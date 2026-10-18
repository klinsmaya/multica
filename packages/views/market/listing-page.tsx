"use client";

import type { ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { useWorkspaceId } from "@multica/core/hooks";
import { marketListingOptions } from "@multica/core/market";
import { useWorkspacePaths } from "@multica/core/paths";
import type { MarketListing } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../i18n";
import { BreadcrumbHeader } from "../layout/breadcrumb-header";
import { PAGE_GUTTER, PAGE_RAIL } from "../layout/page-header";
import { AppLink } from "../navigation";
import { MarketError, MarketMessage, SourceBadge } from "./market-parts";

export function ListingPage({ listingId }: { listingId: string }) {
  const { t } = useT("market");
  const wsId = useWorkspaceId();
  const p = useWorkspacePaths();
  const { data: listing, error, refetch } = useQuery(marketListingOptions(wsId, listingId));

  let body;
  if (error) {
    body = <MarketError error={error} onRetry={() => void refetch()} />;
  } else if (listing === undefined) {
    body = (
      <div className="space-y-3">
        <Skeleton className="h-6 w-56" />
        <Skeleton className="h-4 w-full" />
        <Skeleton className="h-24 w-full" />
      </div>
    );
  } else if (listing === null) {
    body = <MarketMessage>{t(($) => $.detail.unreadable)}</MarketMessage>;
  } else {
    body = <ListingDetail listing={listing} />;
  }

  return (
    <div className="flex h-full min-h-0 flex-col">
      <BreadcrumbHeader
        segments={[{ href: p.market(), label: t(($) => $.detail.back) }]}
        leaf={<span className="truncate">{listing?.listing.name ?? listingId}</span>}
        actions={
          listing ? (
            <Button
              size="sm"
              render={<AppLink href={p.marketInstall(listingId, listing.version)} />}
              nativeButton={false}
            >
              {t(($) => $.detail.check_install)}
            </Button>
          ) : null
        }
      />
      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className={cn(PAGE_RAIL, PAGE_GUTTER, "max-w-3xl space-y-6 py-6")}>{body}</div>
      </div>
    </div>
  );
}

function ListingDetail({ listing }: { listing: MarketListing }) {
  const { t } = useT("market");
  const { requirements: req } = listing;
  return (
    <>
      <div className="space-y-1">
        <div className="flex flex-wrap items-center gap-2">
          <h1 className="text-title font-semibold">{listing.listing.name}</h1>
          <SourceBadge source={listing.source} />
        </div>
        <p className="text-caption text-muted-foreground">
          {[listing.listing.publisher, t(($) => $.detail.version, { version: listing.version })]
            .filter(Boolean)
            .join(" · ")}
        </p>
        {listing.listing.summary ? <p className="text-body">{listing.listing.summary}</p> : null}
      </div>
      {listing.description ? (
        <p className="whitespace-pre-wrap text-body text-muted-foreground">{listing.description}</p>
      ) : null}
      <section className="space-y-3">
        <h2 className="text-body font-medium">{t(($) => $.detail.requirements)}</h2>
        <dl className="space-y-2">
          <Requirement label={t(($) => $.detail.model)}>
            {req.model.models.length > 0
              ? req.model.models.join(", ")
              : t(($) => $.detail.any_model, { provider: req.model.provider })}
          </Requirement>
          {req.tools.length > 0 ? (
            <Requirement label={t(($) => $.detail.tools)}>{req.tools.map((x) => x.name).join(", ")}</Requirement>
          ) : null}
          {req.connections.length > 0 ? (
            <Requirement label={t(($) => $.detail.connections)}>
              {req.connections.map((x) => x.toolkit).join(", ")}
            </Requirement>
          ) : null}
          {req.knowledge.length > 0 ? (
            <Requirement label={t(($) => $.detail.knowledge)}>
              {req.knowledge.map((x) => x.name).join(", ")}
            </Requirement>
          ) : null}
          {req.unsupported.length > 0 ? (
            <Requirement label={t(($) => $.detail.unsupported)}>
              {req.unsupported.map((x) => (x.name ? `${x.kind}: ${x.name}` : x.kind)).join(", ")}
            </Requirement>
          ) : null}
        </dl>
      </section>
    </>
  );
}

function Requirement({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex gap-4">
      <dt className="w-32 shrink-0 text-caption text-muted-foreground">{label}</dt>
      <dd className="min-w-0 flex-1 break-words text-body">{children}</dd>
    </div>
  );
}
