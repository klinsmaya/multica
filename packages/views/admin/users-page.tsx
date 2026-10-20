"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Search } from "lucide-react";
import { adminUsersOptions } from "@multica/core/admin";
import { paths } from "@multica/core/paths";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import { Checkbox } from "@multica/ui/components/ui/checkbox";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
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

export function AdminUsersPage() {
  const { t } = useT("admin");
  const formatDate = useAdminDate();
  const [draft, setDraft] = useState("");
  const [q, setQ] = useState("");
  const [disabledOnly, setDisabledOnly] = useState(false);
  const [page, setPage] = useState(1);

  const users = useQuery(
    adminUsersOptions({
      q: q || undefined,
      disabled_only: disabledOnly,
      page,
      page_size: ADMIN_PAGE_SIZE,
    }),
  );

  return (
    <>
      <AdminPageHeader
        title={t(($) => $.users.title)}
        count={users.data?.total}
      />
      <AdminPageBody>
        <form
          className="flex flex-wrap items-center gap-3"
          onSubmit={(e) => {
            e.preventDefault();
            setPage(1);
            setQ(draft.trim());
          }}
        >
          <div className="flex items-center gap-2">
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
          </div>
          <Label className="gap-2 font-normal">
            <Checkbox
              checked={disabledOnly}
              onCheckedChange={(checked) => {
                setPage(1);
                setDisabledOnly(checked === true);
              }}
            />
            {t(($) => $.users.disabled_only)}
          </Label>
        </form>

        {users.isPending ? (
          <AdminLoading />
        ) : users.isError ? (
          <AdminError onRetry={() => void users.refetch()} />
        ) : users.data.items.length === 0 ? (
          <p className="py-8 text-center text-caption text-muted-foreground">
            {t(($) => $.empty)}
          </p>
        ) : (
          <>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t(($) => $.users.col_user)}</TableHead>
                  <TableHead>{t(($) => $.users.col_status)}</TableHead>
                  <TableHead className="text-right">
                    {t(($) => $.users.col_workspaces)}
                  </TableHead>
                  <TableHead>{t(($) => $.users.col_created)}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {users.data.items.map((u) => (
                  <TableRow key={u.id}>
                    <TableCell>
                      <AppLink
                        href={paths.admin.user(u.id)}
                        className="block min-w-0 hover:underline"
                      >
                        <span className="block truncate font-medium">
                          {u.name || u.email}
                        </span>
                        <span className="block truncate text-caption text-muted-foreground">
                          {u.email}
                        </span>
                      </AppLink>
                    </TableCell>
                    <TableCell>
                      {u.disabled_at ? (
                        <Badge variant="destructive">
                          {t(($) => $.users.status_disabled)}
                        </Badge>
                      ) : (
                        <Badge variant="outline">
                          {t(($) => $.users.status_active)}
                        </Badge>
                      )}
                    </TableCell>
                    <TableCell className="text-right tabular-nums">
                      {u.workspace_count}
                    </TableCell>
                    <TableCell className="whitespace-nowrap text-muted-foreground">
                      {formatDate(u.created_at)}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
            <AdminPagination page={users.data} onPageChange={setPage} />
          </>
        )}
      </AdminPageBody>
    </>
  );
}
