"use client";

import { use } from "react";
import { AdminUserDetailPage } from "@multica/views/admin";

export default function AdminUserRoute({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);
  return <AdminUserDetailPage userId={id} />;
}
