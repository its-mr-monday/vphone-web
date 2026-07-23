import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api/client";

export function useSnapshots(vmID: string | undefined) {
  return useQuery({
    queryKey: ["snapshots", vmID],
    queryFn: () => api.listSnapshots(vmID!),
    enabled: !!vmID,
    refetchInterval: 4000,
  });
}

export function useCreateSnapshot(vmID: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (name: string) => api.createSnapshot(vmID, name),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["snapshots", vmID] });
      qc.invalidateQueries({ queryKey: ["jobs"] });
    },
  });
}

export function useRestoreSnapshot(vmID: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (name: string) => api.restoreSnapshot(vmID, name),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["jobs"] }),
  });
}

export function useDeleteSnapshot(vmID: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (name: string) => api.deleteSnapshot(vmID, name),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["snapshots", vmID] }),
  });
}
