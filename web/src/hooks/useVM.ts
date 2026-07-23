// TanStack Query hooks for VM state and lifecycle mutations.
import {
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { api, type CreateVMRequest, type VM } from "../api/client";

const KEYS = {
  all: ["vms"] as const,
  detail: (id: string) => ["vms", id] as const,
};

/** Poll the VM list. Faster refetch while any VM is in a transient state. */
export function useVMs() {
  return useQuery({
    queryKey: KEYS.all,
    queryFn: api.listVMs,
    refetchInterval: (query) => {
      const vms = query.state.data as VM[] | undefined;
      const transient = vms?.some((v) =>
        ["BOOTING", "STOPPING", "CREATING", "RESTORING", "INSTALLING_CFW"].includes(
          v.status,
        ),
      );
      return transient ? 1000 : 4000;
    },
  });
}

export function useVM(id: string | undefined) {
  return useQuery({
    queryKey: KEYS.detail(id ?? ""),
    queryFn: () => api.getVM(id!),
    enabled: !!id,
    refetchInterval: (query) => {
      const vm = query.state.data as VM | undefined;
      if (!vm) return 4000;
      return ["BOOTING", "STOPPING"].includes(vm.status) ? 1000 : 4000;
    },
  });
}

export function useCreateVM() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: CreateVMRequest) => api.createVM(body),
    onSuccess: () => qc.invalidateQueries({ queryKey: KEYS.all }),
  });
}

export function useImportVM() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: Parameters<typeof api.importVM>[0]) => api.importVM(body),
    onSuccess: () => qc.invalidateQueries({ queryKey: KEYS.all }),
  });
}

export function useDeleteVM() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.deleteVM(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: KEYS.all }),
  });
}

export function useBootVM() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.bootVM(id),
    onSuccess: (_data, id) => {
      qc.invalidateQueries({ queryKey: KEYS.all });
      qc.invalidateQueries({ queryKey: KEYS.detail(id) });
    },
  });
}

export function useStopVM() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.stopVM(id),
    onSuccess: (_data, id) => {
      qc.invalidateQueries({ queryKey: KEYS.all });
      qc.invalidateQueries({ queryKey: KEYS.detail(id) });
    },
  });
}

export function useUpdateVM() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, body }: { id: string; body: Parameters<typeof api.updateVM>[1] }) =>
      api.updateVM(id, body),
    onSuccess: (_data, { id }) => {
      qc.invalidateQueries({ queryKey: KEYS.all });
      qc.invalidateQueries({ queryKey: KEYS.detail(id) });
    },
  });
}
