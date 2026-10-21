"use client";

import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { RefreshCw } from "lucide-react";
import { toast } from "sonner";
import {
  adminConnectorHealthOptions,
  adminConnectorServicesOptions,
  adminConnectorUsersOptions,
  useAdminSetConnectorPolicy,
  useAdminSetConnectorServiceEnabled,
  type AdminConnectorService,
} from "@multica/core/admin";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@multica/ui/components/ui/select";
import { Switch } from "@multica/ui/components/ui/switch";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@multica/ui/components/ui/table";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { useT } from "../i18n";
import { canOperate, useAdminRole } from "./admin-shell";
import {
  ADMIN_PAGE_SIZE,
  AdminError,
  AdminLoading,
  AdminPageBody,
  AdminPageHeader,
  AdminPagination,
  ReasonDialog,
  useAdminDate,
} from "./components/shared";

const ALL_SERVICES = "__all__";

function splitLines(value: string): string[] {
  return value
    .split(/[\n,]/)
    .map((s) => s.trim())
    .filter(Boolean);
}

function HealthSection() {
  const { t } = useT("admin");
  const formatDate = useAdminDate();
  const health = useQuery(adminConnectorHealthOptions());

  return (
    <section className="space-y-2">
      <div className="flex items-center justify-between gap-2">
        <h2 className="text-body font-medium">
          {t(($) => $.connectors.health.title)}
        </h2>
        <Button
          variant="outline"
          size="sm"
          disabled={health.isFetching}
          onClick={() => void health.refetch()}
        >
          <RefreshCw
            className={health.isFetching ? "size-4 animate-spin" : "size-4"}
            aria-hidden="true"
          />
          {t(($) => $.connectors.health.check_again)}
        </Button>
      </div>
      {health.isPending ? (
        <AdminLoading />
      ) : health.isError ? (
        <AdminError onRetry={() => void health.refetch()} />
      ) : !health.data.configured || !health.data.health ? (
        <div className="rounded-lg border p-4">
          <Badge variant="outline">
            {t(($) => $.connectors.health.not_configured)}
          </Badge>
          <p className="mt-2 text-caption text-muted-foreground">
            {t(($) => $.connectors.health.not_configured_help)}
          </p>
        </div>
      ) : (
        <div className="rounded-lg border p-4">
          <div className="flex flex-wrap items-center gap-2">
            {health.data.health.reachable ? (
              <Badge>{t(($) => $.connectors.health.reachable)}</Badge>
            ) : (
              <Badge variant="destructive">
                {t(($) => $.connectors.health.unreachable)}
              </Badge>
            )}
            <span className="truncate font-mono text-caption text-muted-foreground">
              {health.data.health.base_url}
            </span>
          </div>
          {health.data.health.error ? (
            <p role="alert" className="mt-2 break-all font-mono text-caption text-destructive">
              {health.data.health.error}
            </p>
          ) : null}
          <dl className="mt-3 grid grid-cols-2 gap-3 md:grid-cols-4">
            <div>
              <dt className="text-caption text-muted-foreground">
                {t(($) => $.connectors.health.latency)}
              </dt>
              <dd className="text-body font-medium tabular-nums">
                {t(($) => $.connectors.health.latency_value, {
                  ms: health.data.health.latency_ms,
                })}
              </dd>
            </div>
            <div>
              <dt className="text-caption text-muted-foreground">
                {t(($) => $.connectors.health.providers)}
              </dt>
              <dd className="text-body font-medium tabular-nums">
                {health.data.health.providers_total}
              </dd>
            </div>
            <div>
              <dt className="text-caption text-muted-foreground">
                {t(($) => $.connectors.health.oauth_configured)}
              </dt>
              <dd className="text-body font-medium tabular-nums">
                {health.data.health.oauth_configured}
              </dd>
            </div>
            <div>
              <dt className="text-caption text-muted-foreground">
                {t(($) => $.connectors.health.checked_at)}
              </dt>
              <dd className="text-body font-medium">
                {formatDate(health.data.health.checked_at)}
              </dd>
            </div>
          </dl>
        </div>
      )}
    </section>
  );
}

function ServicesSection({ operator }: { operator: boolean }) {
  const { t } = useT("admin");
  const services = useQuery(adminConnectorServicesOptions());
  const setEnabled = useAdminSetConnectorServiceEnabled();
  const setPolicy = useAdminSetConnectorPolicy();

  const [filter, setFilter] = useState("");
  const [toggleTarget, setToggleTarget] = useState<AdminConnectorService | null>(null);
  const [policyTarget, setPolicyTarget] = useState<AdminConnectorService | null>(null);
  const [toolsDraft, setToolsDraft] = useState("");
  const [usersDraft, setUsersDraft] = useState("");

  const items = useMemo(() => {
    const all = services.data?.items ?? [];
    const q = filter.trim().toLowerCase();
    if (!q) return all;
    return all.filter(
      (s) => s.service.includes(q) || s.name.toLowerCase().includes(q),
    );
  }, [services.data, filter]);

  const openPolicy = (s: AdminConnectorService) => {
    setToolsDraft(s.allowed_tools.join("\n"));
    setUsersDraft(s.allowed_users.map((u) => u.email || u.user_id).join("\n"));
    setPolicyTarget(s);
  };

  return (
    <section className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 className="text-body font-medium">
          {t(($) => $.connectors.services.title)}
          {services.data && services.data.items.length > 0 ? (
            <span className="ml-2 font-mono text-caption tabular-nums text-muted-foreground">
              {services.data.items.length}
            </span>
          ) : null}
        </h2>
        <Input
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          placeholder={t(($) => $.connectors.services.search_placeholder)}
          aria-label={t(($) => $.connectors.services.search_placeholder)}
          className="w-56"
        />
      </div>
      {services.data?.catalog_error ? (
        <p role="alert" className="text-caption text-destructive">
          {t(($) => $.connectors.services.catalog_error, {
            error: services.data.catalog_error,
          })}
        </p>
      ) : null}

      {services.isPending ? (
        <AdminLoading />
      ) : services.isError ? (
        <AdminError onRetry={() => void services.refetch()} />
      ) : items.length === 0 ? (
        <p className="py-8 text-center text-caption text-muted-foreground">
          {services.data.items.length === 0
            ? t(($) => $.connectors.services.empty)
            : t(($) => $.empty)}
        </p>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t(($) => $.connectors.services.col_service)}</TableHead>
              <TableHead>{t(($) => $.connectors.services.col_auth)}</TableHead>
              <TableHead>{t(($) => $.connectors.services.col_connections)}</TableHead>
              <TableHead>{t(($) => $.connectors.services.col_users)}</TableHead>
              <TableHead>{t(($) => $.connectors.services.col_tools)}</TableHead>
              <TableHead>{t(($) => $.connectors.services.col_published)}</TableHead>
              {operator ? <TableHead /> : null}
            </TableRow>
          </TableHeader>
          <TableBody>
            {items.map((s) => (
              <TableRow key={s.service}>
                <TableCell>
                  <span className="block truncate font-medium">{s.name || s.service}</span>
                  <span className="block truncate font-mono text-caption text-muted-foreground">
                    {s.service}
                  </span>
                  {!s.available ? (
                    <Badge variant="outline" className="mt-1">
                      {t(($) => $.connectors.services.unavailable)}
                    </Badge>
                  ) : null}
                </TableCell>
                <TableCell className="whitespace-nowrap text-muted-foreground">
                  {s.auth_types.join(", ")}
                </TableCell>
                <TableCell className="whitespace-nowrap tabular-nums">
                  {t(($) => $.connectors.services.connections_summary, {
                    active: s.connections.active,
                    users: s.connections.users,
                  })}
                </TableCell>
                <TableCell className="whitespace-nowrap">
                  {s.allowed_users.length === 0
                    ? t(($) => $.connectors.services.everyone)
                    : t(($) => $.connectors.services.users_count, {
                        count: s.allowed_users.length,
                      })}
                </TableCell>
                <TableCell className="whitespace-nowrap">
                  {s.allowed_tools.length === 0
                    ? t(($) => $.connectors.services.all_tools)
                    : t(($) => $.connectors.services.tools_count, {
                        count: s.allowed_tools.length,
                      })}
                </TableCell>
                <TableCell>
                  <Switch
                    checked={s.enabled}
                    // A service the instance no longer offers can only be
                    // unpublished; the server rejects publishing it.
                    disabled={!operator || (!s.available && !s.enabled)}
                    aria-label={t(($) => $.connectors.services.publish_label, {
                      name: s.name || s.service,
                    })}
                    onCheckedChange={() => setToggleTarget(s)}
                  />
                </TableCell>
                {operator ? (
                  <TableCell className="text-right">
                    <Button variant="ghost" size="xs" onClick={() => openPolicy(s)}>
                      {t(($) => $.connectors.services.edit_policy)}
                    </Button>
                  </TableCell>
                ) : null}
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}

      <ReasonDialog
        open={toggleTarget !== null}
        onOpenChange={(open) => {
          if (!open) setToggleTarget(null);
        }}
        destructive={toggleTarget?.enabled === true}
        title={
          toggleTarget?.enabled
            ? t(($) => $.connectors.services.disable_dialog_title, {
                name: toggleTarget?.name || toggleTarget?.service || "",
              })
            : t(($) => $.connectors.services.enable_dialog_title, {
                name: toggleTarget?.name || toggleTarget?.service || "",
              })
        }
        description={
          toggleTarget?.enabled
            ? t(($) => $.connectors.services.disable_dialog_description, {
                name: toggleTarget?.name || toggleTarget?.service || "",
              })
            : t(($) => $.connectors.services.enable_dialog_description, {
                name: toggleTarget?.name || toggleTarget?.service || "",
              })
        }
        confirmLabel={
          toggleTarget?.enabled
            ? t(($) => $.connectors.services.unpublish)
            : t(($) => $.connectors.services.publish)
        }
        onConfirm={async (reason) => {
          if (!toggleTarget) return;
          const enabled = !toggleTarget.enabled;
          await setEnabled.mutateAsync({ service: toggleTarget.service, enabled, reason });
          toast.success(
            enabled
              ? t(($) => $.connectors.services.published_toast)
              : t(($) => $.connectors.services.unpublished_toast),
          );
        }}
      />

      <ReasonDialog
        open={policyTarget !== null}
        onOpenChange={(open) => {
          if (!open) setPolicyTarget(null);
        }}
        title={t(($) => $.connectors.services.policy_dialog_title, {
          name: policyTarget?.name || policyTarget?.service || "",
        })}
        description={t(($) => $.connectors.services.policy_dialog_description)}
        confirmLabel={t(($) => $.connectors.services.save)}
        onConfirm={async (reason) => {
          if (!policyTarget) return;
          await setPolicy.mutateAsync({
            service: policyTarget.service,
            allowed_tools: splitLines(toolsDraft),
            allowed_user_emails: splitLines(usersDraft),
            reason,
          });
          toast.success(t(($) => $.connectors.services.policy_saved_toast));
        }}
      >
        <div className="space-y-1.5">
          <Label htmlFor="connector-tools">
            {t(($) => $.connectors.services.tools_label)}
          </Label>
          <Textarea
            id="connector-tools"
            value={toolsDraft}
            onChange={(e) => setToolsDraft(e.target.value)}
            placeholder={t(($) => $.connectors.services.tools_placeholder)}
            rows={4}
            className="font-mono"
          />
          <p className="text-caption text-muted-foreground">
            {t(($) => $.connectors.services.tools_help, {
              service: policyTarget?.service ?? "",
            })}
          </p>
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="connector-users">
            {t(($) => $.connectors.services.users_label)}
          </Label>
          <Textarea
            id="connector-users"
            value={usersDraft}
            onChange={(e) => setUsersDraft(e.target.value)}
            placeholder={t(($) => $.connectors.services.users_placeholder)}
            rows={4}
          />
        </div>
      </ReasonDialog>
    </section>
  );
}

function UsersSection() {
  const { t } = useT("admin");
  const formatDate = useAdminDate();
  const services = useQuery(adminConnectorServicesOptions());
  const [service, setService] = useState(ALL_SERVICES);
  const [draft, setDraft] = useState("");
  const [q, setQ] = useState("");
  const [page, setPage] = useState(1);

  const users = useQuery(
    adminConnectorUsersOptions({
      service: service === ALL_SERVICES ? undefined : service,
      q: q || undefined,
      page,
      page_size: ADMIN_PAGE_SIZE,
    }),
  );

  const serviceItems = useMemo(
    () => [
      { value: ALL_SERVICES, label: t(($) => $.connectors.users.all_services) },
      ...(services.data?.items ?? []).map((s) => ({
        value: s.service,
        label: s.name || s.service,
      })),
    ],
    [services.data, t],
  );

  return (
    <section className="space-y-3">
      <h2 className="text-body font-medium">
        {t(($) => $.connectors.users.title)}
        {users.data && users.data.total > 0 ? (
          <span className="ml-2 font-mono text-caption tabular-nums text-muted-foreground">
            {users.data.total}
          </span>
        ) : null}
      </h2>
      <form
        className="flex flex-wrap items-center gap-2"
        onSubmit={(e) => {
          e.preventDefault();
          setPage(1);
          setQ(draft.trim());
        }}
      >
        <Select
          items={serviceItems}
          value={service}
          onValueChange={(v) => {
            setPage(1);
            setService(v ?? ALL_SERVICES);
          }}
        >
          <SelectTrigger
            className="w-48"
            aria-label={t(($) => $.connectors.users.all_services)}
          >
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {serviceItems.map((item) => (
              <SelectItem key={item.value} value={item.value}>
                {item.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Input
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          placeholder={t(($) => $.search_placeholder)}
          className="w-64"
        />
        <Button type="submit" variant="outline" size="sm">
          {t(($) => $.search)}
        </Button>
      </form>

      {users.isPending ? (
        <AdminLoading />
      ) : users.isError ? (
        <AdminError onRetry={() => void users.refetch()} />
      ) : users.data.items.length === 0 ? (
        <p className="py-8 text-center text-caption text-muted-foreground">
          {t(($) => $.connectors.users.empty)}
        </p>
      ) : (
        <>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t(($) => $.connectors.users.col_user)}</TableHead>
                <TableHead>{t(($) => $.connectors.users.col_services)}</TableHead>
                <TableHead className="text-right">
                  {t(($) => $.connectors.users.col_active)}
                </TableHead>
                <TableHead className="text-right">
                  {t(($) => $.connectors.users.col_reauth)}
                </TableHead>
                <TableHead className="text-right">
                  {t(($) => $.connectors.users.col_revoked)}
                </TableHead>
                <TableHead>{t(($) => $.connectors.users.col_last_connected)}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {users.data.items.map((u) => (
                <TableRow key={u.user_id}>
                  <TableCell>
                    <span className="block truncate font-medium">{u.name || u.email}</span>
                    <span className="block truncate text-caption text-muted-foreground">
                      {u.email}
                    </span>
                    {u.disabled_at ? (
                      <Badge variant="destructive" className="mt-1">
                        {t(($) => $.users.status_disabled)}
                      </Badge>
                    ) : null}
                  </TableCell>
                  <TableCell className="font-mono text-caption">
                    {u.services.join(", ")}
                  </TableCell>
                  <TableCell className="text-right tabular-nums">
                    {u.connections.active}
                  </TableCell>
                  <TableCell className="text-right tabular-nums">
                    {u.connections.reauth_required}
                  </TableCell>
                  <TableCell className="text-right tabular-nums">
                    {u.connections.revoked}
                  </TableCell>
                  <TableCell className="whitespace-nowrap text-muted-foreground">
                    {formatDate(u.last_connected_at)}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
          <AdminPagination page={users.data} onPageChange={setPage} />
        </>
      )}
    </section>
  );
}

/**
 * open-connector operations: whether the instance answers, which services
 * are published to users (with per-service tool and user allowlists), and
 * who holds connections. Every write collects a reason and is audited; a
 * viewer sees everything but the switches and policy buttons.
 */
export function AdminConnectorsPage() {
  const { t } = useT("admin");
  const operator = canOperate(useAdminRole());

  return (
    <>
      <AdminPageHeader title={t(($) => $.connectors.title)} />
      <AdminPageBody>
        <HealthSection />
        <ServicesSection operator={operator} />
        <UsersSection />
      </AdminPageBody>
    </>
  );
}
