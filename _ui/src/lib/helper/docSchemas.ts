// Schema patterns as the server reads them (Go's path.Match, without regard
// to case): * matches any run of characters, ? one character, [a-z] and
// [^0-9] a class, and \ escapes the next character.

function toRegExp(pattern: string): RegExp | null {
  let re = '';
  for (let i = 0; i < pattern.length; i++) {
    const c = pattern[i];
    if (c === '*') re += '[^/]*';
    else if (c === '?') re += '[^/]';
    else if (c === '\\') {
      i++;
      if (i >= pattern.length) return null;
      re += pattern[i].replace(/[.*+?^${}()|[\]\\/-]/g, '\\$&');
    } else if (c === '[') {
      const end = pattern.indexOf(']', i + 2);
      if (end < 0) return null;
      let body = pattern.slice(i + 1, end);
      const negate = body.startsWith('^');
      if (negate) body = body.slice(1);
      re += '[' + (negate ? '^' : '') + body.replace(/[\]\\]/g, '\\$&') + ']';
      i = end;
    } else re += c.replace(/[.*+?^${}()|[\]\\/-]/g, '\\$&');
  }
  try {
    return new RegExp('^' + re + '$', 'i');
  } catch {
    return null;
  }
}

export function matchesAny(patterns: string[], schema: string): boolean {
  return patterns.some((p) => toRegExp(p)?.test(schema) ?? false);
}

// schemaChosen applies the include patterns (empty: every schema), then the
// exclude patterns.
export function schemaChosen(include: string[], exclude: string[], schema: string): boolean {
  if (include.length && !matchesAny(include, schema)) return false;
  return !matchesAny(exclude, schema);
}
