// A small Markdown renderer for the assistant's answers: fenced code,
// inline code, bold, italic, links, headings and lists. Text is escaped
// first, so the model cannot inject HTML.

export interface MdBlock {
  kind: 'code' | 'html';
  lang?: string;
  text: string;
}

const esc = (s: string) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');

function inline(s: string): string {
  let out = esc(s);
  out = out.replace(/`([^`]+)`/g, '<code>$1</code>');
  out = out.replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>');
  out = out.replace(/(^|[^*])\*([^*\s][^*]*)\*/g, '$1<em>$2</em>');
  out = out.replace(/\[([^\]]+)\]\((https?:\/\/[^\s)]+)\)/g, '<a href="$2" target="_blank" rel="noopener noreferrer">$1</a>');
  return out;
}

function prose(text: string): string {
  const lines = text.split('\n');
  const out: string[] = [];
  let list: 'ul' | 'ol' | '' = '';
  let para: string[] = [];

  const flushPara = () => {
    if (para.length) out.push(`<p>${para.map(inline).join('<br>')}</p>`);
    para = [];
  };
  const closeList = () => {
    if (list) out.push(`</${list}>`);
    list = '';
  };

  for (const line of lines) {
    const h = /^(#{1,4})\s+(.*)$/.exec(line);
    const ul = /^\s*[-*]\s+(.*)$/.exec(line);
    const ol = /^\s*\d+[.)]\s+(.*)$/.exec(line);
    if (h) {
      flushPara();
      closeList();
      out.push(`<h4>${inline(h[2])}</h4>`);
    } else if (ul || ol) {
      flushPara();
      const want = ul ? 'ul' : 'ol';
      if (list !== want) {
        closeList();
        out.push(`<${want}>`);
        list = want;
      }
      out.push(`<li>${inline((ul ?? ol)![1])}</li>`);
    } else if (!line.trim()) {
      flushPara();
      closeList();
    } else {
      closeList();
      para.push(line);
    }
  }
  flushPara();
  closeList();
  return out.join('');
}

// markdown splits text into code blocks and rendered prose.
export function markdown(text: string): MdBlock[] {
  const blocks: MdBlock[] = [];
  const re = /```([\w+-]*)[^\n]*\n([\s\S]*?)(?:```|$)/g;
  let at = 0;
  let m: RegExpExecArray | null;
  while ((m = re.exec(text))) {
    if (m.index > at) blocks.push({ kind: 'html', text: prose(text.slice(at, m.index)) });
    blocks.push({ kind: 'code', lang: m[1] || '', text: m[2].replace(/\n$/, '') });
    at = re.lastIndex;
  }
  if (at < text.length) blocks.push({ kind: 'html', text: prose(text.slice(at)) });
  return blocks.filter((b) => b.kind === 'code' || b.text);
}
