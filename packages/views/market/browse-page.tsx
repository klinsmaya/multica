"use client";

import { useState } from "react";
import { Store } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { useWorkspaceId } from "@multica/core/hooks";
import { marketListingsOptions } from "@multica/core/market";
import { useWorkspacePaths } from "@multica/core/paths";
import type { MarketListingSummary } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { cn } from "@multica/ui/lib/utils";
import { useDebouncedValue } from "../common/use-debounced-value";
import { useT } from "../i18n";
import { CollectionPageHeader } from "../layout/collection-page";
import { PAGE_GUTTER, PAGE_RAIL } from "../layout/page-header";
import { AppLink } from "../navigation";
import { MarketError, MarketMessage, SourceBadge } from "./market-parts";

export function BrowsePage() {
  const { t } = useT("market");
  const wsId = useWorkspaceId();
  const [text, setText] = useState("");
  const [page, setPage] = useState(1);
  const q = useDebouncedValue(text.trim(), 300);
  const { data, error, refetch } = useQuery(
    marketListingsOptions(wsId, { q: q || undefined, page }),
  );

  let body;
  if (error) {
    body = <MarketError error={error} onRetry={() => void refetch()} />;
  } else if (!data) {
    body = <ListingSkeleton />;
  } else if (data.items.length === 0) {
    body = <MarketMessage>{q ? t(($) => $.browse.no_match) : t(($) => $.browse.empty)}</MarketMessage>;
  } else {
    const hasNext = data.page * data.page_size < data.total;
    body = (
      <>
        <ul className="divide-y rounded-lg border">
          {data.items.map((item) => (
            <ListingRow key={item.id} item={item} />
          ))}
        </ul>
        {page > 1 || hasNext ? (
          <div className="flex justify-end gap-2">
            <Button type="button" variant="outline" size="sm" disabled={page <= 1} onClick={() => setPage(page - 1)}>
              {t(($) => $.browse.previous)}
            </Button>
            <Button type="button" variant="outline" size="sm" disabled={!hasNext} onClick={() => setPage(page + 1)}>
              {t(($) => $.browse.next)}
            </Button>
          </div>
        ) : null}
      </>
    );
  }

  return (
    <div className="flex h-full min-h-0 flex-col">
      <CollectionPageHeader
        icon={Store}
        title={t(($) => $.page.title)}
        count={data?.total}
        actions={data ? <SourceBadge source={data.source} /> : null}
      />
      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className={cn(PAGE_RAIL, PAGE_GUTTER, "space-y-4 py-4")}>
          <Input
            type="search"
            value={text}
            onChange={(event) => {
              setText(event.target.value);
              setPage(1);
            }}
            placeholder={t(($) => $.browse.search_placeholder)}
            aria-label={t(($) => $.browse.search_label)}
            className="max-w-sm"
          />
          {body}
        </div>
      </div>
    </div>
  );
}

function ListingRow({ item }: { item: MarketListingSummary }) {
  const p = useWorkspacePaths();
  return (
    <li>
      <AppLink
        href={p.marketListing(item.id)}
        newTabTitle={item.name}
        className="flex items-start gap-3 px-4 py-3 transition-colors hover:bg-accent/50"
      >
        <Store aria-hidden="true" className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
        <div className="min-w-0 flex-1">
          <div className="flex min-w-0 items-baseline gap-2">
            <span className="truncate text-body font-medium">{item.name}</span>
            {item.publisher ? (
              <span className="shrink-0 truncate text-caption text-muted-foreground">{item.publisher}</span>
            ) : null}
          </div>
          {item.summary ? (
            <p className="mt-0.5 line-clamp-2 text-caption text-muted-foreground">{item.summary}</p>
          ) : null}
        </div>
        <span className="shrink-0 font-mono text-caption text-muted-foreground">{item.latest_version}</span>
      </AppLink>
    </li>
  );
}

function ListingSkeleton() {
  return (
    <div className="space-y-2">
      {[0, 1, 2].map((i) => (
        <Skeleton key={i} className="h-14 w-full" />
      ))}
    </div>
  );
}
