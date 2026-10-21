"use client";

import { use } from "react";
import { AdminWorkspaceDetailPage } from "@multica/views/admin";

export default function AdminWorkspaceRoute({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);
  return <AdminWorkspaceDetailPage workspaceId={id} />;
}
