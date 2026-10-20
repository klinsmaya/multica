"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Search } from "lucide-react";
import { adminWorkspacesOptions } from "@multica/core/admin";
import { paths } from "@multica/core/paths";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@multica/ui/components/ui/table";
import { useT } from "../i18n";
import { AppLink } from "../navigation";
import {
  ADMIN_PAGE_SIZE,
  AdminError,
  AdminLoading,
  AdminPageBody,
  AdminPageHeader,
  AdminPagination,
  useAdminDate,
} from "./components/shared";

export function AdminWorkspacesPage() {
  const { t } = useT("admin");
  const formatDate = useAdminDate();
  const [draft, setDraft] = useState("");
  const [q, setQ] = useState("");
  const [page, setPage] = useState(1);

  const workspaces = useQuery(
    adminWorkspacesOptions({ q: q || undefined, page, page_size: ADMIN_PAGE_SIZE }),
  );

  return (
    <>
      <AdminPageHeader
        title={t(($) => $.workspaces.title)}
        count={workspaces.data?.total}
      />
      <AdminPageBody>
        <form
          className="flex items-center gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            setPage(1);
            setQ(draft.trim());
          }}
        >
          <Input
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            placeholder={t(($) => $.search_placeholder)}
            className="w-64"
          />
          <Button type="submit" variant="outline" size="sm">
            <Search className="size-4" aria-hidden="true" />
            {t(($) => $.search)}
          </Button>
        </form>

        {workspaces.isPending ? (
          <AdminLoading />
        ) : workspaces.isError ? (
          <AdminError onRetry={() => void workspaces.refetch()} />
        ) : workspaces.data.items.length === 0 ? (
          <p className="py-8 text-center text-caption text-muted-foreground">
            {t(($) => $.empty)}
          </p>
        ) : (
          <>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t(($) => $.workspaces.col_workspace)}</TableHead>
                  <TableHead className="text-right">
                    {t(($) => $.workspaces.col_members)}
                  </TableHead>
                  <TableHead className="text-right">
                    {t(($) => $.workspaces.col_agents)}
                  </TableHead>
                  <TableHead>{t(($) => $.workspaces.col_created)}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {workspaces.data.items.map((w) => (
                  <TableRow key={w.id}>
                    <TableCell>
                      <AppLink
                        href={paths.admin.workspace(w.id)}
                        className="block min-w-0 hover:underline"
                      >
                        <span className="block truncate font-medium">{w.name}</span>
                        <span className="block truncate font-mono text-caption text-muted-foreground">
                          {w.slug}
                        </span>
                      </AppLink>
                    </TableCell>
                    <TableCell className="text-right tabular-nums">
                      {w.member_count}
                    </TableCell>
                    <TableCell className="text-right tabular-nums">
                      {w.agent_count}
                    </TableCell>
                    <TableCell className="whitespace-nowrap text-muted-foreground">
                      {formatDate(w.created_at)}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
            <AdminPagination page={workspaces.data} onPageChange={setPage} />
          </>
        )}
      </AdminPageBody>
    </>
  );
}
