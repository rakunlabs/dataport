// The SQL editors of open tabs, so the assistant can read where the user
// is and change the text of the tab it helps with.
export interface EditorFocus {
  // The statement a run would take: the selection, or the one under the cursor.
  statement: string;
  from: number;
  to: number;
  // The cursor's line, counted from 1.
  line: number;
  selected: boolean;
}

export interface EditorHandle {
  // For a notebook: the cell the editor belongs to.
  cell?: { path: string; connection: string; sql: string };
  selection(): string;
  focus(): EditorFocus | undefined;
  insert(text: string): boolean;
  replaceRange(from: number, to: number, text: string): boolean;
}

const editors = new Map<string, EditorHandle>();

export function registerEditor(tabId: string, handle: EditorHandle) {
  editors.set(tabId, handle);
  return () => {
    if (editors.get(tabId) === handle) editors.delete(tabId);
  };
}

export function editorOf(tabId: string): EditorHandle | undefined {
  return editors.get(tabId);
}
