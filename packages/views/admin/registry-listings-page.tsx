"use client";

import { useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Search, Upload } from "lucide-react";
import { toast } from "sonner";
import {
  REGISTRY_LISTING_STATUSES,
  adminRegistryListingsOptions,
  adminRegistrySourcesOptions,
  useAdminRegistryUpload,
  type RegistryListingStatus,
} from "@multica/core/admin";
import { paths } from "@multica/core/paths";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import { Checkbox } from "@multica/ui/components/ui/checkbox";
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
import { Textarea } from "@multica/ui/components/ui/textarea";
import { useT } from "../i18n";
import { AppLink } from "../navigation";
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
import { ListingStatusBadge, RegistryNav, useRegistryStatusLabel } from "./components/registry-shared";

const ALL = "__all__";

export function AdminRegistryListingsPage() {
  const { t } = useT("admin");
  const formatDate = useAdminDate();
  const statusLabel = useRegistryStatusLabel();
  const role = useAdminRole();
  const [draft, setDraft] = useState("");
  const [q, setQ] = useState("");
  const [sourceId, setSourceId] = useState(ALL);
  const [status, setStatus] = useState(ALL);
  const [pendingOnly, setPendingOnly] = useState(false);
  const [page, setPage] = useState(1);
  const [uploadOpen, setUploadOpen] = useState(false);

  const sources = useQuery(adminRegistrySourcesOptions());
  const listings = useQuery(
    adminRegistryListingsOptions({
      q: q || undefined,
      source_id: sourceId === ALL ? undefined : sourceId,
      status: status === ALL ? undefined : (status as RegistryListingStatus),
      pending_only: pendingOnly,
      page,
      page_size: ADMIN_PAGE_SIZE,
    }),
  );

  const sourceItems = [
    { value: ALL, label: t(($) => $.registry.filter_all_sources) },
    ...(sources.data ?? []).map((s) => ({ value: s.id, label: s.namespace })),
  ];
  const statusItems = [
    { value: ALL, label: t(($) => $.registry.filter_all_statuses) },
    ...REGISTRY_LISTING_STATUSES.map((s) => ({ value: s, label: statusLabel(s) })),
  ];

  return (
    <>
      <AdminPageHeader
        title={t(($) => $.registry.title)}
        count={listings.data?.total}
        actions={
          canOperate(role) ? (
            <Button size="sm" onClick={() => setUploadOpen(true)}>
              <Upload className="size-4" aria-hidden="true" />
              {t(($) => $.registry.upload)}
            </Button>
          ) : null
        }
      />
      <AdminPageBody>
        <RegistryNav />
        <form
          className="flex flex-wrap items-center gap-3"
          onSubmit={(e) => {
            e.preventDefault();
            setPage(1);
            setQ(draft.trim());
          }}
        >
          <div className="relative min-w-56 flex-1">
            <Search
              className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground"
              aria-hidden="true"
            />
            <Input
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              placeholder={t(($) => $.registry.search_placeholder)}
              aria-label={t(($) => $.search)}
              className="pl-8"
            />
          </div>
          <Select
            items={sourceItems}
            value={sourceId}
            onValueChange={(v) => {
              setPage(1);
              setSourceId(v ?? ALL);
            }}
          >
            <SelectTrigger className="w-44" aria-label={t(($) => $.registry.col_source)}>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {sourceItems.map((item) => (
                <SelectItem key={item.value} value={item.value}>
                  {item.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <Select
            items={statusItems}
            value={status}
            onValueChange={(v) => {
              setPage(1);
              setStatus(v ?? ALL);
            }}
          >
            <SelectTrigger className="w-40" aria-label={t(($) => $.registry.col_status)}>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {statusItems.map((item) => (
                <SelectItem key={item.value} value={item.value}>
                  {item.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <Label className="flex items-center gap-2 text-caption">
            <Checkbox
              checked={pendingOnly}
              onCheckedChange={(checked) => {
                setPage(1);
                setPendingOnly(checked === true);
              }}
            />
            {t(($) => $.registry.pending_only)}
          </Label>
          <Button type="submit" variant="outline" size="sm">
            {t(($) => $.search)}
          </Button>
        </form>

        {listings.isPending ? (
          <AdminLoading />
        ) : listings.isError ? (
          <AdminError onRetry={() => void listings.refetch()} />
        ) : listings.data.items.length === 0 ? (
          <p className="py-8 text-center text-caption text-muted-foreground">
            {t(($) => $.registry.empty)}
          </p>
        ) : (
          <>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t(($) => $.registry.col_listing)}</TableHead>
                  <TableHead>{t(($) => $.registry.col_source)}</TableHead>
                  <TableHead>{t(($) => $.registry.col_status)}</TableHead>
                  <TableHead>{t(($) => $.registry.col_latest)}</TableHead>
                  <TableHead>{t(($) => $.registry.col_updated)}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {listings.data.items.map((l) => (
                  <TableRow key={l.id}>
                    <TableCell>
                      <AppLink
                        href={paths.admin.registryListing(l.id)}
                        className="block min-w-0 hover:underline"
                      >
                        <span className="font-medium">{l.display_name || l.name}</span>
                        <span className="ml-2 font-mono text-caption text-muted-foreground">
                          {l.name}
                        </span>
                      </AppLink>
                    </TableCell>
                    <TableCell className="font-mono text-caption">{l.namespace}</TableCell>
                    <TableCell>
                      <div className="flex items-center gap-1.5">
                        <ListingStatusBadge status={l.status} />
                        {l.public ? (
                          <Badge variant="outline">{t(($) => $.registry.public)}</Badge>
                        ) : null}
                        {l.pending_count > 0 ? (
                          <Badge variant="secondary">
                            {t(($) => $.registry.pending_badge, { count: l.pending_count })}
                          </Badge>
                        ) : null}
                      </div>
                    </TableCell>
                    <TableCell className="font-mono text-caption">
                      {l.latest_version || "—"}
                    </TableCell>
                    <TableCell className="whitespace-nowrap text-muted-foreground">
                      {formatDate(l.updated_at)}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
            <AdminPagination page={listings.data} onPageChange={setPage} />
          </>
        )}
      </AdminPageBody>

      <UploadDialog open={uploadOpen} onOpenChange={setUploadOpen} />
    </>
  );
}

/**
 * Uploads one package version. The source is chosen here rather than read
 * from the archive: the registry decides which namespace a package lands in,
 * not the package itself.
 */
function UploadDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const { t } = useT("admin");
  const sources = useQuery({ ...adminRegistrySourcesOptions(), enabled: open });
  const upload = useAdminRegistryUpload();
  const fileRef = useRef<HTMLInputElement>(null);
  const [sourceId, setSourceId] = useState("");
  const [file, setFile] = useState<File | null>(null);
  const [changelog, setChangelog] = useState("");

  const uploadable = (sources.data ?? []).filter((s) => s.enabled && (s.kind === "self" || s.kind === "official"));
  const sourceItems = uploadable.map((s) => ({ value: s.id, label: `${s.namespace} · ${s.display_name}` }));
  const selected = sourceId || uploadable[0]?.id || "";

  const reset = () => {
    setSourceId("");
    setFile(null);
    setChangelog("");
    if (fileRef.current) fileRef.current.value = "";
  };

  return (
    <ReasonDialog
      open={open}
      onOpenChange={(next) => {
        onOpenChange(next);
        if (!next) reset();
      }}
      title={t(($) => $.registry.upload_dialog_title)}
      description={t(($) => $.registry.upload_dialog_description)}
      confirmLabel={t(($) => $.registry.upload)}
      canSubmit={selected.length > 0 && file !== null}
      onConfirm={async (reason) => {
        if (!file) return;
        const result = await upload.mutateAsync({
          source_id: selected,
          bundle: file,
          reason,
          changelog: changelog.trim() || undefined,
        });
        toast.success(t(($) => $.registry.uploaded_toast, { name: result.name, version: result.version }));
      }}
    >
      <div className="space-y-1.5">
        <Label htmlFor="registry-upload-source">{t(($) => $.registry.col_source)}</Label>
        {uploadable.length === 0 ? (
          <p className="text-caption text-muted-foreground">{t(($) => $.registry.upload_no_source)}</p>
        ) : (
          <Select items={sourceItems} value={selected} onValueChange={(v) => setSourceId(v ?? "")}>
            <SelectTrigger id="registry-upload-source" className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {sourceItems.map((item) => (
                <SelectItem key={item.value} value={item.value}>
                  {item.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        )}
      </div>
      <div className="space-y-1.5">
        <Label htmlFor="registry-upload-file">{t(($) => $.registry.upload_file_label)}</Label>
        <Input
          id="registry-upload-file"
          ref={fileRef}
          type="file"
          accept=".zip,application/zip"
          onChange={(e) => setFile(e.target.files?.[0] ?? null)}
        />
        <p className="text-caption text-muted-foreground">{t(($) => $.registry.upload_file_hint)}</p>
      </div>
      <div className="space-y-1.5">
        <Label htmlFor="registry-upload-changelog">{t(($) => $.registry.changelog)}</Label>
        <Textarea
          id="registry-upload-changelog"
          rows={3}
          value={changelog}
          onChange={(e) => setChangelog(e.target.value)}
          placeholder={t(($) => $.registry.changelog_placeholder)}
        />
      </div>
    </ReasonDialog>
  );
}
