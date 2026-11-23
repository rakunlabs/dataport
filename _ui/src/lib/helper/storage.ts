// localStorage throws when the browser blocks storage, such as with cookies
// turned off. These read nothing and write nothing then, so a choice lasts
// for the page only instead of breaking it.

export function storedGet(key: string): string | null {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}

export function storedSet(key: string, value: string | null) {
  try {
    if (value === null) localStorage.removeItem(key);
    else localStorage.setItem(key, value);
  } catch {
    // Storage is blocked or full.
  }
}
