import type { ColorRule } from '@/lib/helper/rowColors';

// Row colour rules, per table or query tab, kept in this browser.

const key = 'dataport.rowColors';

function load(): Record<string, ColorRule[]> {
  try {
    const v = JSON.parse(localStorage.getItem(key) ?? '{}');
    return v && typeof v === 'object' ? v : {};
  } catch {
    return {};
  }
}

export const rowColors = $state<{ rules: Record<string, ColorRule[]> }>({ rules: load() });

// The most tables and queries whose colours are kept; the oldest go first.
const maxScopes = 200;

function save() {
  const scopes = Object.keys(rowColors.rules);
  for (const s of scopes.slice(0, Math.max(0, scopes.length - maxScopes))) delete rowColors.rules[s];
  try {
    localStorage.setItem(key, JSON.stringify(rowColors.rules));
  } catch {
    // The colours then last for this page only.
  }
}

export function colorRules(scope: string): ColorRule[] {
  return rowColors.rules[scope] ?? [];
}

export function setColorRules(scope: string, rules: ColorRule[]) {
  // Set again, a scope moves to the end, as the most recent.
  delete rowColors.rules[scope];
  if (rules.length) rowColors.rules[scope] = rules;
  save();
}

export function addColorRule(scope: string, rule: Omit<ColorRule, 'id' | 'enabled'>) {
  // A new rule for the same column and value replaces the old one.
  const rest = colorRules(scope).filter((r) => !(r.column === rule.column && r.op === rule.op && r.value === rule.value));
  setColorRules(scope, [{ ...rule, id: Math.random().toString(36).slice(2, 10), enabled: true }, ...rest]);
}

export function clearColumnRules(scope: string, column: string) {
  setColorRules(
    scope,
    colorRules(scope).filter((r) => r.column !== column),
  );
}
