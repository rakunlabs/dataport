export interface Toast {
  id: number;
  kind: 'info' | 'error' | 'success';
  message: string;
}

export const toasts = $state<Toast[]>([]);
let next = 1;

export function toast(message: string, kind: Toast['kind'] = 'info', ms = 4000) {
  const id = next++;
  toasts.push({ id, kind, message });
  setTimeout(() => dismiss(id), ms);
}

export function dismiss(id: number) {
  const i = toasts.findIndex((t) => t.id === id);
  if (i >= 0) toasts.splice(i, 1);
}

export function errorMessage(e: unknown): string {
  if (e instanceof Error) return e.message;
  return String(e);
}
