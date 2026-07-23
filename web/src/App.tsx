import { BrowserRouter, Route, Routes } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { Sidebar } from "./components/layout/Sidebar";
import { DashboardPage } from "./pages/DashboardPage";
import { VMPage } from "./pages/VMPage";
import { SettingsPage } from "./pages/SettingsPage";
import { IPSWPage } from "./pages/IPSWPage";
import { CreateVMPage } from "./pages/CreateVMPage";
import { UsersPage } from "./pages/UsersPage";
import { NodesPage } from "./pages/NodesPage";
import { LoginPage } from "./pages/LoginPage";
import { AuthProvider, useAuth } from "./hooks/useAuth";
import { ThemeProvider } from "./hooks/useTheme";

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: 1,
      refetchOnWindowFocus: false,
    },
  },
});

function Shell() {
  const { loading, enabled, user } = useAuth();

  if (loading) {
    return (
      <div className="flex h-screen w-screen items-center justify-center bg-base font-mono text-sm text-fg-dim">
        loading…
      </div>
    );
  }
  if (enabled && !user) {
    return <LoginPage />;
  }

  return (
    <BrowserRouter>
      <div className="flex h-screen w-screen overflow-hidden bg-base text-fg">
        <Sidebar />
        <main className="min-w-0 flex-1 overflow-hidden">
          <Routes>
            <Route path="/" element={<DashboardPage />} />
            <Route path="/create" element={<CreateVMPage />} />
            <Route path="/vms/:id" element={<VMPage />} />
            <Route path="/ipsws" element={<IPSWPage />} />
            <Route path="/nodes" element={<NodesPage />} />
            <Route path="/users" element={<UsersPage />} />
            <Route path="/settings" element={<SettingsPage />} />
          </Routes>
        </main>
      </div>
    </BrowserRouter>
  );
}

export default function App() {
  return (
    <ThemeProvider>
      <QueryClientProvider client={queryClient}>
        <AuthProvider>
          <Shell />
        </AuthProvider>
      </QueryClientProvider>
    </ThemeProvider>
  );
}
