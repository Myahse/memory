import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { StrictMode, type ReactNode } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter, Navigate, Route, Routes, useLocation } from "react-router-dom";
import { AuthProvider, useAuth } from "./auth/AuthProvider";
import { Layout } from "./components/Layout";
import { Toaster } from "./components/Toast";
import "./index.css";
import { ApiError } from "./lib/api";
import { applyTheme, getTheme } from "./lib/theme";
import { AskPage } from "./pages/Ask";
import { Login, Register, Welcome } from "./pages/Auth";
import { Home } from "./pages/Home";
import { MemoryDetail } from "./pages/MemoryDetail";
import { SearchPage } from "./pages/Search";
import { Settings } from "./pages/Settings";
import { Shared } from "./pages/Shared";
import { Timeline } from "./pages/Timeline";

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 15_000,
      retry: (count, err) => !(err instanceof ApiError && err.status >= 400 && err.status < 500) && count < 2,
    },
  },
});

matchMedia("(prefers-color-scheme: dark)").addEventListener("change", () => getTheme() === "system" && applyTheme("system"));

function RequireAuth({ children }: { children: ReactNode }) {
  const { session, loading } = useAuth();
  const loc = useLocation();
  if (loading)
    return (
      <div className="flex min-h-screen items-center justify-center">
        <div className="flex size-14 animate-pulse items-center justify-center rounded-2xl bg-accent text-2xl font-bold text-white">M</div>
      </div>
    );
  if (!session) return <Navigate to="/welcome" replace state={{ from: loc.pathname }} />;
  return <>{children}</>;
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <AuthProvider>
        <BrowserRouter>
          <Routes>
            <Route path="/welcome" element={<Welcome />} />
            <Route path="/login" element={<Login />} />
            <Route path="/register" element={<Register />} />
            <Route path="/shared/:token" element={<Shared />} />
            <Route
              element={
                <RequireAuth>
                  <Layout />
                </RequireAuth>
              }
            >
              <Route index element={<Home />} />
              <Route path="search" element={<SearchPage />} />
              <Route path="ask" element={<AskPage />} />
              <Route path="ask/:id" element={<AskPage />} />
              <Route path="memory/:id" element={<MemoryDetail />} />
              <Route path="timeline" element={<Timeline />} />
              <Route path="settings" element={<Settings />} />
            </Route>
            <Route path="*" element={<Navigate to="/" replace />} />
          </Routes>
        </BrowserRouter>
        <Toaster />
      </AuthProvider>
    </QueryClientProvider>
  </StrictMode>,
);
