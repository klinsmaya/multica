import { AdminShell } from "@multica/views/admin";

/**
 * `/admin/*` — the platform operations console, served from the same origin
 * as the app so it shares the session cookie. `admin` is a reserved slug, so
 * this never shadows a workspace. AdminShell redirects signed-out visitors to
 * /login and shows a no-access page unless /api/admin/me grants a role.
 */
export default function AdminLayout({ children }: { children: React.ReactNode }) {
  return <AdminShell>{children}</AdminShell>;
}
