"use client";

import type { RegistryListingStatus, RegistryReviewStatus } from "@multica/core/admin";
import { paths } from "@multica/core/paths";
import { Badge } from "@multica/ui/components/ui/badge";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n";
import { AppLink, useNavigation } from "../../navigation";

const REGISTRY_TABS = [
  { key: "listings", href: paths.admin.registry() },
  { key: "sources", href: paths.admin.registrySources() },
  { key: "consumers", href: paths.admin.registryConsumers() },
] as const;

/** Second-level navigation shared by every marketplace page. */
export function RegistryNav() {
  const { t } = useT("admin");
  const nav = useNavigation();
  return (
    <nav className="flex gap-1" aria-label={t(($) => $.registry.title)}>
      {REGISTRY_TABS.map(({ key, href }) => {
        const active =
          key === "listings"
            ? nav.pathname === href || nav.pathname.startsWith(`${href}/listings`)
            : nav.pathname.startsWith(href);
        return (
          <AppLink
            key={key}
            href={href}
            aria-current={active ? "page" : undefined}
            className={cn(
              "rounded-md px-2 py-1 text-caption transition-colors",
              active
                ? "bg-muted font-medium text-foreground"
                : "text-muted-foreground hover:bg-muted/60 hover:text-foreground",
            )}
          >
            {t(($) => $.registry.tabs[key])}
          </AppLink>
        );
      })}
    </nav>
  );
}

export function useRegistryStatusLabel() {
  const { t } = useT("admin");
  return (status: RegistryListingStatus | string): string => {
    switch (status) {
      case "draft":
        return t(($) => $.registry.status.draft);
      case "published":
        return t(($) => $.registry.status.published);
      case "disabled":
        return t(($) => $.registry.status.disabled);
      default:
        return status;
    }
  };
}

export function useRegistryReviewLabel() {
  const { t } = useT("admin");
  return (status: RegistryReviewStatus | string): string => {
    switch (status) {
      case "pending":
        return t(($) => $.registry.review.pending);
      case "approved":
        return t(($) => $.registry.review.approved);
      case "rejected":
        return t(($) => $.registry.review.rejected);
      case "revoked":
        return t(($) => $.registry.review.revoked);
      default:
        return status;
    }
  };
}

export function ListingStatusBadge({ status }: { status: RegistryListingStatus }) {
  const label = useRegistryStatusLabel();
  return (
    <Badge variant={status === "published" ? "default" : status === "disabled" ? "destructive" : "secondary"}>
      {label(status)}
    </Badge>
  );
}

export function ReviewStatusBadge({ status }: { status: RegistryReviewStatus }) {
  const label = useRegistryReviewLabel();
  const variant =
    status === "approved" ? "default" : status === "pending" ? "secondary" : "destructive";
  return <Badge variant={variant}>{label(status)}</Badge>;
}
