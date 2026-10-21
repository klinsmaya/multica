"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Plus } from "lucide-react";
import { toast } from "sonner";
import {
  REGISTRY_SOURCE_KINDS,
  adminRegistrySourcesOptions,
  useAdminRegistryCreateSource,
  useAdminRegistrySetSourceEnabled,
  type RegistrySource,
  type RegistrySourceKind,
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
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@multica/ui/components/ui/table";
import { useT } from "../i18n";
import { useAdminRole } from "./admin-shell";
import {
  AdminError,
  AdminLoading,
  AdminPageBody,
  AdminPageHeader,
  ReasonDialog,
  useAdminDate,
} from "./components/shared";
import { RegistryNav } from "./components/registry-shared";

export function useSourceKindLabel() {
  const { t } = useT("admin");
  return (kind: RegistrySourceKind | string): string => {
    switch (kind) {
      case "official":
        return t(($) => $.registry.sources.kind_official);
      case "self":
        return t(($) => $.registry.sources.kind_self);
      case "third_party":
        return t(($) => $.registry.sources.kind_third_party);
      case "skills":
        return t(($) => $.registry.sources.kind_skills);
      default:
        return kind;
    }
  };
}

export function AdminRegistrySourcesPage() {
  const { t } = useT("admin");
  const formatDate = useAdminDate();
  const kindLabel = useSourceKindLabel();
  const role = useAdminRole();
  const sources = useQuery(adminRegistrySourcesOptions());
  const createSource = useAdminRegistryCreateSource();
  const setEnabled = useAdminRegistrySetSourceEnabled();

  const [addOpen, setAddOpen] = useState(false);
  const [namespace, setNamespace] = useState("");
  const [kind, setKind] = useState<RegistrySourceKind>("self");
  const [displayName, setDisplayName] = useState("");
  const [url, setUrl] = useState("");
  const [toggleTarget, setToggleTarget] = useState<RegistrySource | null>(null);

  const isSuper = role === "super_admin";
  const kindItems = REGISTRY_SOURCE_KINDS.map((k) => ({ value: k, label: kindLabel(k) }));

  return (
    <>
      <AdminPageHeader
        title={t(($) => $.registry.sources.title)}
        count={sources.data?.length}
        actions={
          isSuper ? (
            <Button size="sm" onClick={() => setAddOpen(true)}>
              <Plus className="size-4" aria-hidden="true" />
              {t(($) => $.registry.sources.add)}
            </Button>
          ) : null
        }
      />
      <AdminPageBody>
        <RegistryNav />
        {sources.isPending ? (
          <AdminLoading />
        ) : sources.isError ? (
          <AdminError onRetry={() => void sources.refetch()} />
        ) : sources.data.length === 0 ? (
          <p className="py-8 text-center text-caption text-muted-foreground">
            {t(($) => $.registry.sources.empty)}
          </p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t(($) => $.registry.sources.col_namespace)}</TableHead>
                <TableHead>{t(($) => $.registry.sources.col_kind)}</TableHead>
                <TableHead>{t(($) => $.registry.sources.col_packages)}</TableHead>
                <TableHead>{t(($) => $.registry.sources.col_created)}</TableHead>
                {isSuper ? <TableHead /> : null}
              </TableRow>
            </TableHeader>
            <TableBody>
              {sources.data.map((s) => (
                <TableRow key={s.id}>
                  <TableCell>
                    <span className="font-mono font-medium">{s.namespace}</span>
                    <span className="ml-2 text-caption text-muted-foreground">{s.display_name}</span>
                    {!s.enabled ? (
                      <Badge variant="destructive" className="ml-2">
                        {t(($) => $.registry.sources.disabled)}
                      </Badge>
                    ) : null}
                    {s.url ? (
                      <p className="truncate text-caption text-muted-foreground">{s.url}</p>
                    ) : null}
                  </TableCell>
                  <TableCell>{kindLabel(s.kind)}</TableCell>
                  <TableCell className="tabular-nums">{s.package_count}</TableCell>
                  <TableCell className="whitespace-nowrap text-muted-foreground">
                    {formatDate(s.created_at)}
                  </TableCell>
                  {isSuper ? (
                    <TableCell className="text-right">
                      <Button variant="ghost" size="xs" onClick={() => setToggleTarget(s)}>
                        {s.enabled
                          ? t(($) => $.registry.sources.disable)
                          : t(($) => $.registry.sources.enable)}
                      </Button>
                    </TableCell>
                  ) : null}
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </AdminPageBody>

      <ReasonDialog
        open={addOpen}
        onOpenChange={(open) => {
          setAddOpen(open);
          if (!open) {
            setNamespace("");
            setKind("self");
            setDisplayName("");
            setUrl("");
          }
        }}
        title={t(($) => $.registry.sources.add_dialog_title)}
        description={t(($) => $.registry.sources.add_dialog_description)}
        confirmLabel={t(($) => $.registry.sources.add)}
        canSubmit={namespace.trim().length > 0 && displayName.trim().length > 0}
        onConfirm={async (reason) => {
          await createSource.mutateAsync({
            namespace: namespace.trim(),
            kind,
            display_name: displayName.trim(),
            url: url.trim() || undefined,
            reason,
          });
          toast.success(t(($) => $.registry.sources.added_toast));
        }}
      >
        <div className="space-y-1.5">
          <Label htmlFor="registry-source-namespace">{t(($) => $.registry.sources.col_namespace)}</Label>
          <Input
            id="registry-source-namespace"
            value={namespace}
            // eslint-disable-next-line no-restricted-syntax -- example namespace, a technical value
            placeholder="@self"
            onChange={(e) => setNamespace(e.target.value)}
          />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="registry-source-name">{t(($) => $.registry.sources.display_name_label)}</Label>
          <Input
            id="registry-source-name"
            value={displayName}
            onChange={(e) => setDisplayName(e.target.value)}
          />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="registry-source-kind">{t(($) => $.registry.sources.col_kind)}</Label>
          <Select items={kindItems} value={kind} onValueChange={(v) => setKind(v as RegistrySourceKind)}>
            <SelectTrigger id="registry-source-kind" className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {kindItems.map((item) => (
                <SelectItem key={item.value} value={item.value}>
                  {item.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <p className="text-caption text-muted-foreground">{t(($) => $.registry.sources.kind_hint)}</p>
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="registry-source-url">{t(($) => $.registry.sources.url_label)}</Label>
          <Input
            id="registry-source-url"
            type="url"
            value={url}
            placeholder="https://"
            onChange={(e) => setUrl(e.target.value)}
          />
        </div>
      </ReasonDialog>

      <ReasonDialog
        open={toggleTarget !== null}
        onOpenChange={(open) => {
          if (!open) setToggleTarget(null);
        }}
        destructive={toggleTarget?.enabled === true}
        title={
          toggleTarget?.enabled
            ? t(($) => $.registry.sources.disable_dialog_title)
            : t(($) => $.registry.sources.enable_dialog_title)
        }
        description={
          toggleTarget?.enabled
            ? t(($) => $.registry.sources.disable_dialog_description, {
                namespace: toggleTarget?.namespace ?? "",
              })
            : t(($) => $.registry.sources.enable_dialog_description, {
                namespace: toggleTarget?.namespace ?? "",
              })
        }
        confirmLabel={
          toggleTarget?.enabled
            ? t(($) => $.registry.sources.disable)
            : t(($) => $.registry.sources.enable)
        }
        onConfirm={async (reason) => {
          if (!toggleTarget) return;
          await setEnabled.mutateAsync({
            sourceId: toggleTarget.id,
            enabled: !toggleTarget.enabled,
            reason,
          });
          toast.success(t(($) => $.registry.sources.saved_toast));
        }}
      />
    </>
  );
}
