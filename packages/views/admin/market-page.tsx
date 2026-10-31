"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Search } from "lucide-react";
import { adminMarketOptions } from "@multica/core/admin";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { useT } from "../i18n";
import {
  ADMIN_PAGE_SIZE,
  AdminError,
  AdminLoading,
  AdminPageBody,
  AdminPageHeader,
  AdminPagination,
} from "./components/shared";

export function AdminMarketPage() {
  const { t } = useT("admin");
  const [draft, setDraft] = useState("");
  const [q, setQ] = useState("");
  const [page, setPage] = useState(1);
  const listings = useQuery(adminMarketOptions({ q: q || undefined, page, page_size: ADMIN_PAGE_SIZE }));

  return (
    <>
      <AdminPageHeader title={t(($) => $.market.title)} count={listings.data?.total} />
      <AdminPageBody>
        <form className="flex items-center gap-2" onSubmit={(e) => { e.preventDefault(); setPage(1); setQ(draft.trim()); }}>
          <Input value={draft} onChange={(e) => setDraft(e.target.value)} placeholder={t(($) => $.market.search_placeholder)} className="w-64" />
          <Button type="submit" variant="outline" size="sm"><Search className="size-4" aria-hidden="true" />{t(($) => $.search)}</Button>
        </form>
        {listings.isPending ? <AdminLoading /> : listings.isError ? <AdminError onRetry={() => void listings.refetch()} /> : (
          <>
            <div className="space-y-2">
              {listings.data.items.map((item) => (
                <article key={item.id} className="rounded-lg border p-4">
                  <div className="flex items-center justify-between gap-3">
                    <div className="min-w-0"><h2 className="truncate font-medium">{item.name}</h2><p className="text-caption text-muted-foreground">{item.id} · {item.publisher}</p></div>
                    <span className="shrink-0 font-mono text-caption">v{item.latest_version}</span>
                  </div>
                  {item.summary ? <p className="mt-2 text-body text-muted-foreground">{item.summary}</p> : null}
                </article>
              ))}
            </div>
            {listings.data.items.length === 0 ? <p className="py-8 text-center text-caption text-muted-foreground">{t(($) => $.empty)}</p> : null}
            <AdminPagination page={listings.data} onPageChange={setPage} />
          </>
        )}
      </AdminPageBody>
    </>
  );
}
