// A single app-wide confirmation dialog for writes the server holds back,
// shown with the exact SQL that will run (as DBX does): any change on a
// production database, and UPDATE or DELETE without WHERE on any database.

interface Request {
  connection: string;
  sql: string;
  risk: string;
  // Set when the connection is production; false when only the statements
  // without WHERE asked. Undefined is read as production, for callers that
  // ask before the server does.
  production?: boolean;
  // The statements that change every row of their table.
  unbounded?: string[];
  resolve: (ok: boolean) => void;
}

export const confirmState = $state<{ current: Request | null }>({ current: null });

export function confirmProduction(req: Omit<Request, 'resolve'>): Promise<boolean> {
  return new Promise((resolve) => {
    confirmState.current?.resolve(false);
    confirmState.current = { ...req, resolve };
  });
}

// confirmAsked shows what a 409 confirmation_required answer asked for.
export function confirmAsked(connection: string, sql: string, data: Record<string, unknown> | undefined): Promise<boolean> {
  return confirmProduction({
    connection,
    sql,
    risk: String(data?.risk ?? 'write'),
    production: data?.production === undefined ? true : !!data.production,
    unbounded: Array.isArray(data?.unbounded) ? (data.unbounded as string[]) : [],
  });
}

export function answer(ok: boolean) {
  const cur = confirmState.current;
  confirmState.current = null;
  cur?.resolve(ok);
}
