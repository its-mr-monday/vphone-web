import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { UserPlus, Trash2, ShieldCheck, User as UserIcon, Ban, CheckCircle2 } from "lucide-react";
import { api, ApiError, type AuthUser, type UserRole } from "../api/client";
import { useAuth } from "../hooks/useAuth";
import { Button } from "../components/ui/Button";

/**
 * UsersPage is the admin-only access-control management view: list users, create
 * local accounts, change roles, disable/enable, and delete. Visible only when
 * auth is enabled and the current user is an admin.
 */
export function UsersPage() {
  const { enabled, isAdmin, user: me } = useAuth();
  const qc = useQueryClient();
  const { data: users, error: listErr } = useQuery({
    queryKey: ["users"],
    queryFn: api.listUsers,
    enabled: enabled && isAdmin,
  });

  const [name, setName] = useState("");
  const [pw, setPw] = useState("");
  const [role, setRole] = useState<UserRole>("vphone-user");
  const [error, setError] = useState<string | null>(null);

  const invalidate = () => qc.invalidateQueries({ queryKey: ["users"] });
  const create = useMutation({ mutationFn: () => api.createUser({ username: name.trim(), password: pw, role }), onSuccess: invalidate });
  const update = useMutation({
    mutationFn: (v: { id: string; body: Parameters<typeof api.updateUser>[1] }) => api.updateUser(v.id, v.body),
    onSuccess: invalidate,
  });
  const del = useMutation({ mutationFn: (id: string) => api.deleteUser(id), onSuccess: invalidate });

  if (!enabled) {
    return (
      <Centered>
        Access control is disabled. Enable it in <code className="mx-1 text-fg-muted">config.toml</code> under{" "}
        <code className="mx-1 text-fg-muted">[auth]</code> to manage users.
      </Centered>
    );
  }
  if (!isAdmin) return <Centered>Admin access required.</Centered>;

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    try {
      await create.mutateAsync();
      setName("");
      setPw("");
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "failed to create user");
    }
  }

  return (
    <div className="flex h-full flex-col">
      <div className="border-b border-border px-6 py-4">
        <h1 className="font-sans text-lg font-semibold text-fg">Users & Access</h1>
        <p className="font-mono text-xs text-fg-dim">local accounts, roles, and access control</p>
      </div>

      <div className="border-b border-border p-6">
        <form onSubmit={submit} className="flex flex-wrap items-end gap-3">
          <Field label="Username">
            <input value={name} onChange={(e) => setName(e.target.value)} className="u-input" placeholder="jdoe" />
          </Field>
          <Field label="Password">
            <input type="password" value={pw} onChange={(e) => setPw(e.target.value)} className="u-input" placeholder="min 6 chars" />
          </Field>
          <Field label="Role">
            <select value={role} onChange={(e) => setRole(e.target.value as UserRole)} className="u-input">
              <option value="vphone-user">vphone-user</option>
              <option value="vphone-admin">vphone-admin</option>
            </select>
          </Field>
          <Button type="submit" variant="primary" icon={<UserPlus className="h-3.5 w-3.5" />} disabled={create.isPending || !name.trim() || pw.length < 6}>
            Add user
          </Button>
        </form>
        {error && <p className="mt-2 font-mono text-xs text-error">{error}</p>}
      </div>

      <div className="flex-1 overflow-y-auto p-6">
        <div className="rounded-md border border-border bg-surface">
          {listErr ? (
            <p className="p-4 font-mono text-xs text-error">failed to load users</p>
          ) : (
            <table className="w-full">
              <thead>
                <tr className="border-b border-border">
                  {["User", "Role", "Provider", "Status", ""].map((h) => (
                    <th key={h} className="px-4 py-2 text-left font-mono text-[10px] uppercase tracking-widest text-fg-dim">{h}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {users?.map((u) => (
                  <UserRow
                    key={u.id}
                    u={u}
                    isSelf={me?.id === u.id}
                    onRole={(r) => update.mutate({ id: u.id, body: { role: r } })}
                    onDisabled={(d) => update.mutate({ id: u.id, body: { disabled: d } })}
                    onDelete={() => {
                      if (confirm(`Delete user "${u.username}"?`)) del.mutate(u.id);
                    }}
                  />
                ))}
              </tbody>
            </table>
          )}
        </div>
      </div>

      <style>{`
        .u-input { background:var(--color-base); border:1px solid var(--color-border); border-radius:2px; padding:0.4rem 0.6rem; font-family:var(--font-mono); font-size:0.75rem; color:var(--color-fg); outline:none; }
        .u-input:focus { border-color:var(--color-accent); }
      `}</style>
    </div>
  );
}

function UserRow({
  u,
  isSelf,
  onRole,
  onDisabled,
  onDelete,
}: {
  u: AuthUser;
  isSelf: boolean;
  onRole: (r: UserRole) => void;
  onDisabled: (d: boolean) => void;
  onDelete: () => void;
}) {
  return (
    <tr className="border-b border-border/40">
      <td className="px-4 py-2.5 font-mono text-xs text-fg">
        {u.username} {isSelf && <span className="text-fg-dim">(you)</span>}
      </td>
      <td className="px-4 py-2.5">
        <span className="inline-flex items-center gap-1.5 font-mono text-[11px]">
          {u.role === "vphone-admin" ? <ShieldCheck className="h-3.5 w-3.5 text-accent" /> : <UserIcon className="h-3.5 w-3.5 text-fg-dim" />}
          {u.role}
        </span>
      </td>
      <td className="px-4 py-2.5 font-mono text-[11px] text-fg-dim">{u.provider}</td>
      <td className="px-4 py-2.5">
        {u.disabled ? (
          <span className="font-mono text-[11px] text-error">disabled</span>
        ) : (
          <span className="font-mono text-[11px] text-success">active</span>
        )}
      </td>
      <td className="px-4 py-2.5">
        <div className="flex items-center justify-end gap-1.5">
          {u.provider === "local" && (
            <button
              onClick={() => onRole(u.role === "vphone-admin" ? "vphone-user" : "vphone-admin")}
              className="rounded-sm border border-border px-2 py-1 font-mono text-[10px] text-fg-muted hover:border-accent hover:text-accent"
              title="Toggle role"
            >
              {u.role === "vphone-admin" ? "demote" : "promote"}
            </button>
          )}
          {!isSelf && (
            <>
              <button onClick={() => onDisabled(!u.disabled)} className="text-fg-dim hover:text-warn" title={u.disabled ? "Enable" : "Disable"}>
                {u.disabled ? <CheckCircle2 className="h-3.5 w-3.5" /> : <Ban className="h-3.5 w-3.5" />}
              </button>
              <button onClick={onDelete} className="text-fg-dim hover:text-error" title="Delete">
                <Trash2 className="h-3.5 w-3.5" />
              </button>
            </>
          )}
        </div>
      </td>
    </tr>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label className="block">
      <span className="mb-1.5 block font-mono text-[10px] uppercase tracking-widest text-fg-dim">{label}</span>
      {children}
    </label>
  );
}

function Centered({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex h-full items-center justify-center px-6 text-center font-mono text-sm text-fg-dim">
      <div className="max-w-md">{children}</div>
    </div>
  );
}
