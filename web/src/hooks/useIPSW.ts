import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, type IPSW, type IPSWKind } from "../api/client";

const downloading = (items: IPSW[] | undefined) =>
  items?.some((i) => i.status === "DOWNLOADING");

export function useIPSWs() {
  return useQuery({
    queryKey: ["ipsws"],
    queryFn: api.listIPSWs,
    refetchInterval: (q) => (downloading(q.state.data as IPSW[] | undefined) ? 2000 : 8000),
  });
}

export function useRegisterIPSW() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: { file_path: string; version?: string; build?: string; device?: string; kind?: IPSWKind }) =>
      api.registerIPSW(body),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["ipsws"] }),
  });
}

export function useDownloadIPSW() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ url, kind }: { url: string; kind: IPSWKind }) => api.downloadIPSW(url, kind),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["ipsws"] }),
  });
}

export function useDeleteIPSW() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.deleteIPSW(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["ipsws"] }),
  });
}
