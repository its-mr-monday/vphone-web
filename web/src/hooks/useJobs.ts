import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, type Job } from "../api/client";

const active = (jobs: Job[] | undefined) =>
  jobs?.some((j) => j.status === "RUNNING" || j.status === "PENDING");

/** Jobs list, optionally scoped to a VM. Polls faster while work is active. */
export function useJobs(vmID?: string) {
  return useQuery({
    queryKey: ["jobs", vmID ?? "all"],
    queryFn: () => api.listJobs(vmID),
    refetchInterval: (q) => (active(q.state.data as Job[] | undefined) ? 1500 : 5000),
  });
}

export function useJob(id: string | undefined) {
  return useQuery({
    queryKey: ["job", id],
    queryFn: () => api.getJob(id!),
    enabled: !!id,
    refetchInterval: (q) => {
      const j = q.state.data as Job | undefined;
      return j && (j.status === "RUNNING" || j.status === "PENDING") ? 1500 : false;
    },
  });
}

export function useCancelJob() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.cancelJob(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["jobs"] }),
  });
}
