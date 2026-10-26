// The value panel beside result grids, kept open or closed and at the
// width the user left it, in this browser.

const key = 'dataport.valuePanel';

function load(): { open: boolean; width: number } {
  try {
    const v = JSON.parse(localStorage.getItem(key) ?? '{}');
    return { open: !!v.open, width: Number(v.width) || 380 };
  } catch {
    return { open: false, width: 380 };
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

export function setValuePanel(open: boolean) {
  valuePanel.open = open;
  saveValuePanel();
}
