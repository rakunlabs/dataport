// Personal preferences kept in the browser, per user of this browser.

export type FontFamily = 'jetbrains' | 'system' | 'menlo' | 'consolas' | 'courier';

// Editor fonts: the bundled JetBrains Mono, then fonts most systems have.
export const fontFamilies: { value: FontFamily; label: string; css: string }[] = [
  { value: 'jetbrains', label: 'JetBrains Mono', css: "'JetBrains Mono Variable', ui-monospace, monospace" },
  { value: 'system', label: 'System monospace', css: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace' },
  { value: 'menlo', label: 'Menlo / Monaco', css: 'Menlo, Monaco, ui-monospace, monospace' },
  { value: 'consolas', label: 'Consolas', css: "Consolas, 'Cascadia Mono', ui-monospace, monospace" },
  { value: 'courier', label: 'Courier New', css: "'Courier New', Courier, monospace" },
];

// Placeholder syntaxes that are filled in before a run.
export interface VariableSyntaxes {
  // @set name = value; declarations, used as ${name} or @name.
  atSet: boolean;
  // ${name}
  shell: boolean;
  // :name
  named: boolean;
  // #{name}
  mybatis: boolean;
}

export interface Prefs {
  editorFontSize: number;
  editorFontFamily: FontFamily;
  // Line height as a multiple of the font size.
  lineHeight: number;
  tabSize: number;
  lineNumbers: boolean;
  wrap: boolean;
  // Outline the statement under the cursor when the script has several.
  highlightStatement: boolean;
  closeBrackets: boolean;
  vim: boolean;
  // Show the run buttons next to each statement of the SQL editor.
  statementRun: boolean;
  // A blank line ends a statement that has no ";", as in DBeaver.
  blankLineSplits: boolean;
  // Results of a statement run from its gutter button open in a new tab.
  resultInNewTab: boolean;
  // Fill in placeholders before a run.
  variables: boolean;
  variableSyntaxes: VariableSyntaxes;
  // New query tabs start with a manual transaction, as last chosen.
  manualTx: boolean;
}

const key = 'dataport.prefs';

export const defaultPrefs: Prefs = {
  editorFontSize: 13,
  editorFontFamily: 'jetbrains',
  lineHeight: 1.55,
  tabSize: 2,
  lineNumbers: true,
  wrap: false,
  highlightStatement: true,
  closeBrackets: true,
  vim: false,
  statementRun: true,
  blankLineSplits: true,
  resultInNewTab: false,
  variables: true,
  variableSyntaxes: { atSet: true, shell: true, named: false, mybatis: true },
  manualTx: false,
};

function load(): Prefs {
  try {
    const stored = JSON.parse(localStorage.getItem(key) ?? '{}');
    return { ...defaultPrefs, ...stored, variableSyntaxes: { ...defaultPrefs.variableSyntaxes, ...stored.variableSyntaxes } };
  } catch {
    return structuredClone(defaultPrefs);
  }
}

export const prefs = $state<Prefs>(load());

export function savePrefs() {
  try {
    localStorage.setItem(key, JSON.stringify(prefs));
  } catch {
    // The choice then lasts for this page only.
  }
}

export function resetPrefs() {
  Object.assign(prefs, structuredClone(defaultPrefs));
  savePrefs();
}

export function fontCSS(f: FontFamily): string {
  return (fontFamilies.find((x) => x.value === f) ?? fontFamilies[0]).css;
}
