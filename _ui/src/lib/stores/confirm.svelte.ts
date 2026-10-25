// A single app-wide confirmation dialog for writes on production databases,
// shown with the exact SQL that will run (as DBX does).

interface Request {
  connection: string;
  sql: string;
  risk: string;
  resolve: (ok: boolean) => void;
}

export const confirmState = $state<{ current: Request | null }>({ current: null });

export function confirmProduction(req: Omit<Request, 'resolve'>): Promise<boolean> {
  return new Promise((resolve) => {
    confirmState.current?.resolve(false);
    confirmState.current = { ...req, resolve };
  });
}

export function answer(ok: boolean) {
  const cur = confirmState.current;
  confirmState.current = null;
  cur?.resolve(ok);
}
