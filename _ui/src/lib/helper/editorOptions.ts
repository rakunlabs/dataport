// Display options of the SQL editor, from the user's preferences.

import { fontCSS, type Prefs } from '@/lib/stores/prefs.svelte';

export interface EditorOptions {
  fontSize: number;
  fontFamily: string;
  lineHeight: number;
  tabSize: number;
  lineNumbers: boolean;
  wrap: boolean;
  highlightStatement: boolean;
  closeBrackets: boolean;
  vim: boolean;
  // Mark ${name}, #{name} and @set in the text.
  placeholders: boolean;
}

export const defaultOptions: EditorOptions = {
  fontSize: 13,
  fontFamily: "'JetBrains Mono Variable', ui-monospace, monospace",
  lineHeight: 1.55,
  tabSize: 2,
  lineNumbers: true,
  wrap: false,
  highlightStatement: true,
  closeBrackets: true,
  vim: false,
  placeholders: false,
};

export function editorOptions(p: Prefs): EditorOptions {
  return {
    fontSize: p.editorFontSize,
    fontFamily: fontCSS(p.editorFontFamily),
    lineHeight: p.lineHeight,
    tabSize: p.tabSize,
    lineNumbers: p.lineNumbers,
    wrap: p.wrap,
    highlightStatement: p.highlightStatement,
    closeBrackets: p.closeBrackets,
    vim: p.vim,
    placeholders: p.variables,
  };
}
