// A dialog that shows SQL from a tree action before it runs, so a drop or a
// truncate is never one click away. The user may run it, open it in an
// editor to change it, or cancel.

export interface Review {
  title: string;
  connection: string;
  sql: string;
  danger: boolean;
  // Asks the user for a value placed in the SQL, such as a new name.
  input?: { label: string; value: string; build: (v: string) => string };
  // noEdit hides "open in editor", for SQL holding a password: the tab
  // would keep it in the browser.
  noEdit?: boolean;
  resolve: (answer: 'run' | 'edit' | 'cancel', sql: string) => void;
}

export const reviewState = $state<{ current: Review | null }>({ current: null });

export function reviewSQL(r: Omit<Review, 'resolve'>): Promise<{ answer: 'run' | 'edit' | 'cancel'; sql: string }> {
  return new Promise((resolve) => {
    reviewState.current?.resolve('cancel', '');
    reviewState.current = { ...r, resolve: (answer, sql) => resolve({ answer, sql }) };
  });
}

export function answerReview(answer: 'run' | 'edit' | 'cancel', sql = '') {
  const cur = reviewState.current;
  reviewState.current = null;
  cur?.resolve(answer, sql);
}
