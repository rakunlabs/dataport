import type { CalcFn } from '@/lib/helper/calc';

// The panel beside result grids, kept open or closed, on the value or the
// calculator, and at the width the user left it, in this browser.

const key = 'dataport.valuePanel';

export type PanelView = 'value' | 'calc';

interface PanelState {
  open: boolean;
  width: number;
  view: PanelView;
  // The calculator's functions, and whether it counts numbers only.
  calcFns: CalcFn[];
  calcNumeric: boolean;
}

const defaultFns: CalcFn[] = ['count', 'distinct', 'sum', 'avg', 'min', 'max'];

function load(): PanelState {
  try {
    const v = JSON.parse(localStorage.getItem(key) ?? '{}');
    return {
      open: !!v.open,
      width: Number(v.width) || 380,
      view: v.view === 'calc' ? 'calc' : 'value',
      calcFns: Array.isArray(v.calcFns) && v.calcFns.length ? v.calcFns : defaultFns,
      calcNumeric: v.calcNumeric !== false,
    };
  } catch {
    return { open: false, width: 380, view: 'value', calcFns: defaultFns, calcNumeric: true };
  }
}

export const valuePanel = $state(load());

export function saveValuePanel() {
  try {
    localStorage.setItem(key, JSON.stringify(valuePanel));
  } catch {
    // The choice then lasts for this page only.
  }
}

export function setValuePanel(open: boolean, view?: PanelView) {
  valuePanel.open = open;
  if (view) valuePanel.view = view;
  saveValuePanel();
}
