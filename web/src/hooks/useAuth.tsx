import { createContext, useContext, useEffect, useState, type ReactNode } from "react";
import { api, ApiError, type AuthStatus, type AuthUser } from "../api/client";

interface AuthState {
  loading: boolean;
  enabled: boolean;
  user: AuthUser | null;
  isAdmin: boolean;
  refresh: () => Promise<void>;
  logout: () => Promise<void>;
}

const AuthContext = createContext<AuthState>({
  loading: true,
  enabled: false,
  user: null,
  isAdmin: true, // auth disabled → everyone is effectively admin
  refresh: async () => {},
  logout: async () => {},
});

export function AuthProvider({ children }: { children: ReactNode }) {
  const [status, setStatus] = useState<AuthStatus | null>(null);
  const [loading, setLoading] = useState(true);

  const refresh = async () => {
    try {
      setStatus(await api.me());
    } catch (e) {
      // 401 when auth is enabled and not logged in.
      if (e instanceof ApiError && e.status === 401) {
        setStatus({ enabled: true, user: null });
      } else {
        setStatus({ enabled: false });
      }
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    refresh();
  }, []);

  const logout = async () => {
    await api.logout().catch(() => {});
    await refresh();
  };

  const enabled = status?.enabled ?? false;
  const user = status?.user ?? null;
  const isAdmin = !enabled || user?.role === "vphone-admin";

  return (
    <AuthContext.Provider value={{ loading, enabled, user, isAdmin, refresh, logout }}>
      {children}
    </AuthContext.Provider>
  );
}

export function useAuth() {
  return useContext(AuthContext);
}
