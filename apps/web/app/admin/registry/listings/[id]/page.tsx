"use client";

import { use } from "react";
import { AdminRegistryListingPage } from "@multica/views/admin";

export default function AdminRegistryListingRoute({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);
  return <AdminRegistryListingPage listingId={id} />;
}
