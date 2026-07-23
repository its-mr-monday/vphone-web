import { useQuery } from "@tanstack/react-query";
import { api } from "../api/client";

export function useSystemStatus() {
  return useQuery({
    queryKey: ["system", "status"],
    queryFn: api.systemStatus,
    refetchInterval: 5000,
  });
}

export function useSystemConfig() {
  return useQuery({
    queryKey: ["system", "config"],
    queryFn: api.systemConfig,
    staleTime: 60_000,
  });
}
