import { useState } from "react";
import { Smartphone, LogIn } from "lucide-react";
import { api, ApiError } from "../api/client";
import { useAuth } from "../hooks/useAuth";
import { Button } from "../components/ui/Button";

/**
 * LoginPage is shown when access control is enabled and no user is signed in.
 * It performs local username/password login; external providers (OIDC/SAML) will
 * add redirect buttons here as they land.
 */
export function LoginPage() {
  const { refresh } = useAuth();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setBusy(true);
    try {
      await api.login(username.trim(), password);
      await refresh();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "login failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="flex h-screen w-screen items-center justify-center bg-base">
      <form onSubmit={submit} className="w-[360px] rounded-md border border-border bg-surface p-6 shadow-2xl">
        <div className="mb-6 flex items-center gap-2">
          <Smartphone className="h-6 w-6 text-accent" />
          <div className="leading-tight">
            <div className="font-mono text-sm font-semibold text-fg">vphone</div>
            <div className="font-mono text-[10px] uppercase tracking-widest text-fg-dim">web console</div>
          </div>
        </div>

        <label className="mb-3 block">
          <span className="mb-1.5 block font-mono text-[10px] uppercase tracking-widest text-fg-dim">Username</span>
          <input value={username} onChange={(e) => setUsername(e.target.value)} autoFocus className="login-input" />
        </label>
        <label className="mb-4 block">
          <span className="mb-1.5 block font-mono text-[10px] uppercase tracking-widest text-fg-dim">Password</span>
          <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} className="login-input" />
        </label>

        {error && (
          <div className="mb-3 rounded-sm border border-error/40 bg-error/10 px-3 py-2 font-mono text-xs text-error">
            {error}
          </div>
        )}

        <Button type="submit" variant="primary" icon={<LogIn className="h-3.5 w-3.5" />} disabled={busy || !username || !password} className="w-full justify-center">
          {busy ? "Signing in…" : "Sign in"}
        </Button>

        <style>{`
          .login-input { width:100%; background:var(--color-base); border:1px solid var(--color-border); border-radius:2px; padding:0.5rem 0.7rem; font-family:var(--font-mono); font-size:0.8rem; color:var(--color-fg); outline:none; }
          .login-input:focus { border-color:var(--color-accent); }
        `}</style>
      </form>
    </div>
  );
}
