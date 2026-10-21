"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ArrowLeft, Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";
import {
  REGISTRY_LISTING_STATUSES,
  adminRegistryConsumersOptions,
  adminRegistryListingOptions,
  useAdminRegistryReviewVersion,
  useAdminRegistrySetListingStatus,
  useAdminRegistrySetTargeting,
  type RegistryListingDetail,
  type RegistryListingStatus,
  type RegistryReviewDecision,
  type RegistryTargetInput,
  type RegistryVersion,
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
import { useT } from "../i18n";
import { AppLink } from "../navigation";
import { canOperate, useAdminRole } from "./admin-shell";
import {
  AdminError,
  AdminLoading,
  AdminPageBody,
  AdminPageHeader,
  ReasonDialog,
  useAdminDate,
} from "./components/shared";
import {
  ListingStatusBadge,
  ReviewStatusBadge,
  useRegistryStatusLabel,
} from "./components/registry-shared";

export function AdminRegistryListingPage({ listingId }: { listingId: string }) {
  const { t } = useT("admin");
  const listing = useQuery(adminRegistryListingOptions(listingId));

  const back = (
    <Button
      variant="ghost"
      size="icon-sm"
      render={<AppLink href={paths.admin.registry()} />}
      aria-label={t(($) => $.registry.title)}
    >
      <ArrowLeft aria-hidden="true" />
    </Button>
  );

  if (listing.isPending) {
    return (
      <>
        <AdminPageHeader title={t(($) => $.registry.detail_title)} actions={back} />
        <AdminLoading />
      </>
    );
  }
  if (listing.isError) {
    return (
      <>
        <AdminPageHeader title={t(($) => $.registry.detail_title)} actions={back} />
        <AdminError onRetry={() => void listing.refetch()} />
      </>
    );
  }
  return <ListingDetail listing={listing.data} back={back} />;
}

function ListingDetail({ listing, back }: { listing: RegistryListingDetail; back: React.ReactNode }) {
  const { t } = useT("admin");
  const formatDate = useAdminDate();
  const role = useAdminRole();
  const operate = canOperate(role);
  const [statusOpen, setStatusOpen] = useState(false);
  const [targetingOpen, setTargetingOpen] = useState(false);
  const [review, setReview] = useState<{ version: RegistryVersion; decision: RegistryReviewDecision } | null>(null);

  return (
    <>
      <AdminPageHeader
        title={listing.display_name || listing.name}
        actions={
          <>
            {operate ? (
              <>
                <Button variant="outline" size="sm" onClick={() => setTargetingOpen(true)}>
                  {t(($) => $.registry.targeting.edit)}
                </Button>
                <Button variant="outline" size="sm" onClick={() => setStatusOpen(true)}>
                  {t(($) => $.registry.change_status)}
                </Button>
              </>
            ) : null}
            {back}
          </>
        }
      />
      <AdminPageBody>
        <section className="space-y-2">
          <div className="flex flex-wrap items-center gap-2">
            <code className="font-mono text-caption">{listing.namespace}/{listing.name}</code>
            <ListingStatusBadge status={listing.status} />
            {listing.public ? <Badge variant="outline">{t(($) => $.registry.public)}</Badge> : null}
            {!listing.source_enabled ? (
              <Badge variant="destructive">{t(($) => $.registry.source_disabled)}</Badge>
            ) : null}
            {listing.tags.map((tag) => (
              <Badge key={tag} variant="secondary">{tag}</Badge>
            ))}
          </div>
          {listing.description ? (
            <p className="max-w-2xl text-caption">{listing.description}</p>
          ) : null}
          <p className="text-caption text-muted-foreground">
            {t(($) => $.registry.installations_total, { count: listing.installations_total })}
          </p>
        </section>

        <section className="space-y-2">
          <h2 className="text-body font-medium">{t(($) => $.registry.targeting.title)}</h2>
          {listing.targeting.length === 0 ? (
            <p className="text-caption text-muted-foreground">
              {listing.public
                ? t(($) => $.registry.targeting.empty_public)
                : t(($) => $.registry.targeting.empty)}
            </p>
          ) : (
            <ul className="space-y-1 text-caption">
              {listing.targeting.map((target, index) => (
                <li key={index} className="flex flex-wrap items-center gap-2">
                  <span className="font-medium">{target.consumer_name || target.consumer}</span>
                  <span className="text-muted-foreground">
                    {target.tenant_id
                      ? t(($) => $.registry.targeting.tenant, {
                          tenant: target.tenant_label || target.tenant_id,
                        })
                      : target.tenant_group
                        ? t(($) => $.registry.targeting.group, { group: target.tenant_group })
                        : t(($) => $.registry.targeting.all_tenants)}
                  </span>
                </li>
              ))}
            </ul>
          )}
        </section>

        <section className="space-y-2">
          <h2 className="text-body font-medium">{t(($) => $.registry.versions.title)}</h2>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t(($) => $.registry.versions.col_version)}</TableHead>
                <TableHead>{t(($) => $.registry.versions.col_review)}</TableHead>
                <TableHead>{t(($) => $.registry.versions.col_contents)}</TableHead>
                <TableHead>{t(($) => $.registry.versions.col_installations)}</TableHead>
                <TableHead>{t(($) => $.registry.versions.col_uploaded)}</TableHead>
                {operate ? <TableHead /> : null}
              </TableRow>
            </TableHeader>
            <TableBody>
              {listing.versions.map((v) => (
                <TableRow key={v.id}>
                  <TableCell>
                    <code className="font-mono">{v.version}</code>
                    <p className="font-mono text-caption text-muted-foreground" title={v.sha256}>
                      {v.sha256.slice(0, 12)}
                    </p>
                    {v.changelog ? (
                      <p className="max-w-md text-caption text-muted-foreground">{v.changelog}</p>
                    ) : null}
                  </TableCell>
                  <TableCell>
                    <ReviewStatusBadge status={v.review_status} />
                    {v.review_reason ? (
                      <p className="max-w-xs text-caption text-muted-foreground">{v.review_reason}</p>
                    ) : null}
                  </TableCell>
                  <TableCell className="text-caption">
                    <VersionContents version={v} />
                  </TableCell>
                  <TableCell className="text-caption tabular-nums">
                    {v.installations.length === 0
                      ? "—"
                      : v.installations.map((i) => `${i.consumer}: ${i.count}`).join(" · ")}
                  </TableCell>
                  <TableCell className="whitespace-nowrap text-muted-foreground">
                    {formatDate(v.created_at)}
                  </TableCell>
                  {operate ? (
                    <TableCell className="text-right whitespace-nowrap">
                      {v.review_status === "pending" || v.review_status === "rejected" ? (
                        <Button variant="ghost" size="xs" onClick={() => setReview({ version: v, decision: "approved" })}>
                          {t(($) => $.registry.review.approve)}
                        </Button>
                      ) : null}
                      {v.review_status === "pending" ? (
                        <Button variant="ghost" size="xs" onClick={() => setReview({ version: v, decision: "rejected" })}>
                          {t(($) => $.registry.review.reject)}
                        </Button>
                      ) : null}
                      {v.review_status === "approved" ? (
                        <Button variant="ghost" size="xs" onClick={() => setReview({ version: v, decision: "revoked" })}>
                          {t(($) => $.registry.review.revoke)}
                        </Button>
                      ) : null}
                    </TableCell>
                  ) : null}
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </section>
      </AdminPageBody>

      <StatusDialog listing={listing} open={statusOpen} onOpenChange={setStatusOpen} />
      <TargetingDialog listing={listing} open={targetingOpen} onOpenChange={setTargetingOpen} />
      <ReviewDialog review={review} onClose={() => setReview(null)} />
    </>
  );
}

/** What a reviewer is approving: the capabilities, not only the name. */
function VersionContents({ version }: { version: RegistryVersion }) {
  const { t } = useT("admin");
  const parts: string[] = [];
  if (version.skills.length > 0) {
    parts.push(t(($) => $.registry.versions.skills, { count: version.skills.length, names: version.skills.join(", ") }));
  }
  if (version.agents.length > 0) {
    parts.push(t(($) => $.registry.versions.agents, { count: version.agents.length, names: version.agents.join(", ") }));
  }
  for (const server of version.mcp_servers) {
    parts.push(t(($) => $.registry.versions.mcp_server, { name: server.name, url: server.url }));
  }
  if (parts.length === 0) return <span className="text-muted-foreground">—</span>;
  return (
    <ul className="space-y-0.5">
      {parts.map((part) => (
        <li key={part}>{part}</li>
      ))}
    </ul>
  );
}

function StatusDialog({
  listing,
  open,
  onOpenChange,
}: {
  listing: RegistryListingDetail;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const { t } = useT("admin");
  const statusLabel = useRegistryStatusLabel();
  const setStatus = useAdminRegistrySetListingStatus();
  const [status, setStatusValue] = useState<RegistryListingStatus>(listing.status);
  const [isPublic, setIsPublic] = useState(listing.public);
  const items = REGISTRY_LISTING_STATUSES.map((s) => ({ value: s, label: statusLabel(s) }));
  const hasApproved = listing.versions.some((v) => v.review_status === "approved");

  return (
    <ReasonDialog
      open={open}
      onOpenChange={(next) => {
        onOpenChange(next);
        if (next) {
          setStatusValue(listing.status);
          setIsPublic(listing.public);
        }
      }}
      title={t(($) => $.registry.status_dialog_title)}
      description={t(($) => $.registry.status_dialog_description)}
      confirmLabel={t(($) => $.registry.change_status)}
      onConfirm={async (reason) => {
        await setStatus.mutateAsync({ listingId: listing.id, status, public: isPublic, reason });
        toast.success(t(($) => $.registry.status_saved_toast));
      }}
    >
      <div className="space-y-1.5">
        <Label htmlFor="registry-status">{t(($) => $.registry.col_status)}</Label>
        <Select items={items} value={status} onValueChange={(v) => setStatusValue(v as RegistryListingStatus)}>
          <SelectTrigger id="registry-status" className="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {items.map((item) => (
              <SelectItem key={item.value} value={item.value}>
                {item.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        {status === "published" && !hasApproved ? (
          <p className="text-caption text-destructive">{t(($) => $.registry.publish_needs_approved)}</p>
        ) : null}
      </div>
      <Label className="flex items-start gap-2 text-caption">
        <Checkbox checked={isPublic} onCheckedChange={(checked) => setIsPublic(checked === true)} />
        <span>
          {t(($) => $.registry.public_label)}
          <span className="block text-muted-foreground">{t(($) => $.registry.public_hint)}</span>
        </span>
      </Label>
    </ReasonDialog>
  );
}

const ANY_TENANT = "any";

type TargetDraft = { consumer: string; scope: "any" | "tenant" | "group"; value: string };

function toDrafts(listing: RegistryListingDetail): TargetDraft[] {
  return listing.targeting.map((target) => ({
    consumer: target.consumer,
    scope: target.tenant_id ? "tenant" : target.tenant_group ? "group" : ANY_TENANT,
    value: target.tenant_id || target.tenant_group,
  }));
}

/**
 * Replaces the whole targeting set. Rows are the complete picture, not a
 * delete: removing a row and saving is what withdraws visibility.
 */
function TargetingDialog({
  listing,
  open,
  onOpenChange,
}: {
  listing: RegistryListingDetail;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const { t } = useT("admin");
  const consumers = useQuery({ ...adminRegistryConsumersOptions(), enabled: open });
  const setTargeting = useAdminRegistrySetTargeting();
  const [drafts, setDrafts] = useState<TargetDraft[]>(() => toDrafts(listing));

  const consumerItems = (consumers.data ?? [])
    .filter((c) => c.enabled)
    .map((c) => ({ value: c.key, label: c.name }));
  const scopeItems = [
    { value: ANY_TENANT, label: t(($) => $.registry.targeting.scope_any) },
    { value: "tenant", label: t(($) => $.registry.targeting.scope_tenant) },
    { value: "group", label: t(($) => $.registry.targeting.scope_group) },
  ];
  const update = (index: number, patch: Partial<TargetDraft>) =>
    setDrafts((current) => current.map((d, i) => (i === index ? { ...d, ...patch } : d)));
  const valid = drafts.every((d) => d.consumer && (d.scope === ANY_TENANT || d.value.trim()));

  return (
    <ReasonDialog
      open={open}
      onOpenChange={(next) => {
        onOpenChange(next);
        if (next) setDrafts(toDrafts(listing));
      }}
      title={t(($) => $.registry.targeting.dialog_title)}
      description={t(($) => $.registry.targeting.dialog_description)}
      confirmLabel={t(($) => $.registry.targeting.save)}
      canSubmit={valid}
      onConfirm={async (reason) => {
        const targets: RegistryTargetInput[] = drafts.map((d) => ({
          consumer: d.consumer,
          tenant_id: d.scope === "tenant" ? d.value.trim() : undefined,
          tenant_group: d.scope === "group" ? d.value.trim() : undefined,
        }));
        await setTargeting.mutateAsync({ listingId: listing.id, targets, reason });
        toast.success(t(($) => $.registry.targeting.saved_toast));
      }}
    >
      <div className="space-y-2">
        {drafts.map((draft, index) => (
          <div key={index} className="flex flex-wrap items-center gap-2">
            <Select
              items={consumerItems}
              value={draft.consumer}
              onValueChange={(v) => update(index, { consumer: v ?? "" })}
            >
              <SelectTrigger className="w-40" aria-label={t(($) => $.registry.targeting.consumer)}>
                <SelectValue placeholder={t(($) => $.registry.targeting.consumer)} />
              </SelectTrigger>
              <SelectContent>
                {consumerItems.map((item) => (
                  <SelectItem key={item.value} value={item.value}>
                    {item.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Select
              items={scopeItems}
              value={draft.scope}
              onValueChange={(v) => update(index, { scope: (v ?? ANY_TENANT) as TargetDraft["scope"], value: "" })}
            >
              <SelectTrigger className="w-36" aria-label={t(($) => $.registry.targeting.scope)}>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {scopeItems.map((item) => (
                  <SelectItem key={item.value} value={item.value}>
                    {item.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            {draft.scope !== ANY_TENANT ? (
              <Input
                className="w-56 font-mono"
                value={draft.value}
                aria-label={
                  draft.scope === "tenant"
                    ? t(($) => $.registry.targeting.scope_tenant)
                    : t(($) => $.registry.targeting.scope_group)
                }
                placeholder={draft.scope === "tenant" ? t(($) => $.registry.targeting.tenant_placeholder) : ""}
                onChange={(e) => update(index, { value: e.target.value })}
              />
            ) : null}
            <Button
              variant="ghost"
              size="icon-sm"
              aria-label={t(($) => $.registry.targeting.remove)}
              onClick={() => setDrafts((current) => current.filter((_, i) => i !== index))}
            >
              <Trash2 aria-hidden="true" />
            </Button>
          </div>
        ))}
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() =>
            setDrafts((current) => [...current, { consumer: consumerItems[0]?.value ?? "", scope: ANY_TENANT, value: "" }])
          }
        >
          <Plus className="size-4" aria-hidden="true" />
          {t(($) => $.registry.targeting.add)}
        </Button>
      </div>
    </ReasonDialog>
  );
}

function ReviewDialog({
  review,
  onClose,
}: {
  review: { version: RegistryVersion; decision: RegistryReviewDecision } | null;
  onClose: () => void;
}) {
  const { t } = useT("admin");
  const reviewVersion = useAdminRegistryReviewVersion();
  const decision = review?.decision ?? "approved";
  const label =
    decision === "approved"
      ? t(($) => $.registry.review.approve)
      : decision === "rejected"
        ? t(($) => $.registry.review.reject)
        : t(($) => $.registry.review.revoke);
  const description =
    decision === "approved"
      ? t(($) => $.registry.review.approve_description, { version: review?.version.version ?? "" })
      : decision === "rejected"
        ? t(($) => $.registry.review.reject_description, { version: review?.version.version ?? "" })
        : t(($) => $.registry.review.revoke_description, { version: review?.version.version ?? "" });

  return (
    <ReasonDialog
      open={review !== null}
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
      destructive={decision !== "approved"}
      title={t(($) => $.registry.review.dialog_title, { version: review?.version.version ?? "" })}
      description={description}
      confirmLabel={label}
      onConfirm={async (reason) => {
        if (!review) return;
        await reviewVersion.mutateAsync({ versionId: review.version.id, decision, reason });
        toast.success(t(($) => $.registry.review.saved_toast));
      }}
    />
  );
}
