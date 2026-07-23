import { BrowserRouter, Route, Routes } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { Sidebar } from "./components/layout/Sidebar";
import { DashboardPage } from "./pages/DashboardPage";
import { VMPage } from "./pages/VMPage";
import { SettingsPage } from "./pages/SettingsPage";
import { IPSWPage } from "./pages/IPSWPage";
import { CreateVMPage } from "./pages/CreateVMPage";

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: 1,
      refetchOnWindowFocus: false,
    },
  },
});

export default function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <div className="flex h-screen w-screen overflow-hidden bg-base text-fg">
          <Sidebar />
          <main className="min-w-0 flex-1 overflow-hidden">
            <Routes>
              <Route path="/" element={<DashboardPage />} />
              <Route path="/create" element={<CreateVMPage />} />
              <Route path="/vms/:id" element={<VMPage />} />
              <Route path="/ipsws" element={<IPSWPage />} />
              <Route path="/settings" element={<SettingsPage />} />
            </Routes>
          </main>
        </div>
      </BrowserRouter>
    </QueryClientProvider>
  );
}
