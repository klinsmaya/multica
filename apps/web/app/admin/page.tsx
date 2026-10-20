import { redirect } from "next/navigation";
import { paths } from "@multica/core/paths";

export default function AdminIndexRoute() {
  redirect(paths.admin.overview());
}
