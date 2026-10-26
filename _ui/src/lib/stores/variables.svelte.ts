// Asks for the values of placeholders before a run.

import type { Placeholder } from '@/lib/helper/variables';

interface Request {
  items: Placeholder[];
  resolve: (values: Record<string, string> | null) => void;
}

export const variablesState = $state<{ current: Request | null }>({ current: null });

// Values typed last time, so running the same query again is one Enter.
const remembered = new Map<string, string>();

export function rememberedValue(name: string): string {
  return remembered.get(name.toLowerCase()) ?? '';
}

export function askVariables(items: Placeholder[]): Promise<Record<string, string> | null> {
  return new Promise((resolve) => {
    variablesState.current?.resolve(null);
    variablesState.current = { items, resolve };
  });
}

export function answerVariables(values: Record<string, string> | null) {
  const cur = variablesState.current;
  variablesState.current = null;
  if (values) for (const [k, v] of Object.entries(values)) remembered.set(k.toLowerCase(), v);
  cur?.resolve(values);
}
