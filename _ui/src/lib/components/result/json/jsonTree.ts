// Numbers that JSON.parse would round (int64 ids are common in Kafka payloads)
// keep their source text, so the viewer never shows a value that was not sent.
export class LosslessNumber {
  raw: string
  constructor(raw: string) {
    this.raw = raw
  }
}

// Tree-wide controls shared through context: `gen` bumps on every
// expand/collapse-all so each node can follow it.
export type TreeControl = { depth: number; gen: number; all: boolean }

export function parseJson(text: string): { ok: true; value: unknown } | { ok: false } {
  try {
    const value = JSON.parse(text, function (_key: string, v: unknown, ctx?: { source?: string }) {
      // ctx.source is only passed by newer engines; without it numbers parse as usual.
      if (typeof v === 'number' && ctx?.source !== undefined && String(v) !== ctx.source) {
        return new LosslessNumber(ctx.source)
      }
      return v
    } as (this: unknown, key: string, value: unknown) => unknown)
    return { ok: true, value }
  } catch {
    return { ok: false }
  }
}

export const isContainer = (v: unknown): v is object =>
  v !== null && typeof v === 'object' && !(v instanceof LosslessNumber)

// formatJson re-indents JSON text without parsing its values, so numbers keep
// every digit (JSON.parse rounds past 2^53) and strings stay byte for byte.
// indent 0 writes it compact. Text that is not JSON gives undefined.
export function formatJson(text: string, indent = 2): string | undefined {
  if (!parseJson(text).ok) return undefined
  const pad = (n: number) => (indent ? '\n' + ' '.repeat(indent * n) : '')
  let out = ''
  let depth = 0
  for (let i = 0; i < text.length; i++) {
    const c = text[i]
    if (c === '"') {
      let j = i + 1
      while (j < text.length && text[j] !== '"') j += text[j] === '\\' ? 2 : 1
      out += text.slice(i, j + 1)
      i = j
      continue
    }
    if (c === ' ' || c === '\t' || c === '\n' || c === '\r') continue
    if (c === '{' || c === '[') {
      // An empty object or array stays on one line.
      let j = i + 1
      while (j < text.length && /\s/.test(text[j])) j++
      if (text[j] === (c === '{' ? '}' : ']')) {
        out += c + text[j]
        i = j
        continue
      }
      depth++
      out += c + pad(depth)
    } else if (c === '}' || c === ']') {
      depth--
      out += pad(depth) + c
    } else if (c === ',') {
      out += ',' + pad(depth)
    } else if (c === ':') {
      out += indent ? ': ' : ':'
    } else {
      out += c
    }
  }
  return out
}
