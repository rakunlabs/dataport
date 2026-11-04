import { session } from '@/lib/stores/session.svelte';
import { i18n } from '@/lib/i18n/index.svelte';
import { activeTab, connection } from '@/lib/stores/workspace.svelte';
import { editorOf } from '@/lib/stores/editors.svelte';
import { api } from '@/lib/api/client';

// The AI assistant: a side panel next to the tabs that talks to the model
// the admin set up, through the server. The server runs the read-only
// tools as the user; SQL the model proposes for the editor waits here for
// the user to apply it.

export interface AIConversation {
  id: string;
  title: string;
  created_at: number;
  updated_at: number;
}

export interface AIStep {
  id: string;
  name: string;
  arguments?: string;
  done?: boolean;
  error?: boolean;
}

export type ProposalMode = 'replace' | 'insert' | 'statement';

export interface AIProposal {
  id: string;
  sql: string;
  mode: ProposalMode;
  // For 'statement': the part of the editor the proposal replaces, as it
  // was when the user asked.
  range?: { from: number; to: number; text: string };
  explanation?: string;
  // The tab it was proposed for, and what the user did with it.
  tabId: string;
  state: '' | 'applied' | 'dismissed';
}

// A button the model offers to open something in the workspace.
export interface AINavigate {
  id: string;
  target: 'table' | 'structure' | 'docs' | 'diagram' | 'query';
  connection: string;
  schema?: string;
  table?: string;
  sql?: string;
  label?: string;
}

// A question the model asks the user, with choices to answer it.
export interface AIQuestion {
  id: string;
  question: string;
  choices: string[];
  answered?: string;
}

export interface AIEntry {
  role: 'user' | 'assistant';
  questions?: AIQuestion[];
  text: string;
  steps: AIStep[];
  proposals: AIProposal[];
  links: AINavigate[];
  error?: string;
  // When it was written, and for answers when it was done (ms).
  at: number;
  doneAt?: number;
}

interface StoredMessage {
  seq: number;
  created_at: number;
  role: 'user' | 'assistant' | 'tool' | 'system';
  content: string;
  tool_calls?: { id: string; function: { name: string; arguments: string } }[];
  tool_name?: string;
  tool_call_id?: string;
}

const editorTool = 'set_editor_sql';
const navigateTool = 'open_in_workspace';
const askTool = 'ask_user';

function navigateOf(id: string, a: Record<string, unknown>): AINavigate {
  const s = (k: string) => (typeof a[k] === 'string' ? (a[k] as string) : undefined);
  const target = s('target');
  return {
    id,
    target: target === 'structure' || target === 'docs' || target === 'diagram' || target === 'query' ? target : 'table',
    connection: s('connection') ?? '',
    schema: s('schema'),
    table: s('table'),
    sql: s('sql'),
    label: s('label'),
  };
}
const widthKey = 'dataport.assistant.width';

export const assistant = $state({
  open: false,
  width: Number(localStorage.getItem(widthKey)) || 380,
  conversations: [] as AIConversation[],
  current: '' as string,
  entries: [] as AIEntry[],
  busy: false,
  // The conversation list shows instead of the chat.
  listing: false,
  // Folded up to its header to look at what is behind it; the header keeps
  // the state and the controls, and brings it back as it was.
  minimized: false,
});

let abort: AbortController | undefined;

export function setAssistantWidth(w: number) {
  assistant.width = w;
  try {
    localStorage.setItem(widthKey, String(w));
  } catch {
    // The width then lasts for this page only.
  }
}

export function toggleAssistant() {
  if (assistant.minimized) {
    restoreAssistant();
    return;
  }
  assistant.open = !assistant.open;
  if (assistant.open) void loadConversations();
}

export function minimizeAssistant() {
  assistant.minimized = true;
}

export function restoreAssistant() {
  assistant.minimized = false;
  assistant.open = true;
}

export function closeAssistant() {
  // Closed means nothing goes on behind it.
  stop();
  assistant.open = false;
  assistant.minimized = false;
}

export async function loadConversations() {
  try {
    assistant.conversations = await api.get<AIConversation[]>('/ai/conversations');
  } catch {
    // The list stays as it was.
  }
}

export function newConversation() {
  stop();
  assistant.current = '';
  assistant.entries = [];
  assistant.listing = false;
}

// openConversation shows a stored conversation. Tool answers are folded
// into the assistant message that asked for them.
export async function openConversation(id: string) {
  stop();
  const out = await api.get<{ conversation: AIConversation; messages: StoredMessage[] }>(`/ai/conversations/${encodeURIComponent(id)}`);
  // What each tool call was answered with, by call id.
  const answers = new Map<string, string>();
  for (const m of out.messages) if (m.role === 'tool' && m.tool_call_id) answers.set(m.tool_call_id, m.content);

  const entries: AIEntry[] = [];
  let last: AIEntry | undefined;
  for (const m of out.messages) {
    if (m.role === 'user') {
      last = undefined;
      entries.push({ role: 'user', text: m.content, steps: [], proposals: [], links: [], at: m.created_at });
    } else if (m.role === 'assistant') {
      if (!last) {
        last = { role: 'assistant', text: '', steps: [], proposals: [], links: [], at: m.created_at };
        entries.push(last);
      }
      last.doneAt = m.created_at;
      if (m.content) last.text += (last.text ? '\n\n' : '') + m.content;
      for (const tc of m.tool_calls ?? []) {
        const answer = answers.get(tc.id) ?? '';
        if (tc.function.name === askTool) {
          const a = parseArgs(tc.function.arguments);
          (last.questions ??= []).push({ id: tc.id, question: String(a.question ?? ''), choices: Array.isArray(a.choices) ? a.choices.map(String) : [] });
        } else if (tc.function.name === navigateTool) {
          last.links.push(navigateOf(tc.id, parseArgs(tc.function.arguments)));
        } else if (tc.function.name === editorTool) {
          const args = parseArgs(tc.function.arguments);
          last.proposals.push({
            id: tc.id,
            sql: String(args.sql ?? ''),
            mode: proposalMode(String(args.mode ?? '')),
            explanation: args.explanation as string | undefined,
            tabId: '',
            state: answer.startsWith('applied') ? 'applied' : answer.startsWith('dismissed') ? 'dismissed' : '',
          });
        } else {
          last.steps.push({ id: tc.id, name: tc.function.name, arguments: tc.function.arguments, done: true, error: answer.startsWith('error: ') });
        }
      }
    }
  }
  // A question is answered by the user message after it.
  for (let i = 0; i < entries.length; i++) {
    const next = entries.slice(i + 1).find((e) => e.role === 'user');
    for (const q of entries[i].questions ?? []) q.answered = next ? next.text : undefined;
  }
  assistant.current = out.conversation.id;
  assistant.entries = entries;
  assistant.listing = false;
}

export async function deleteConversation(id: string) {
  await api.del(`/ai/conversations/${encodeURIComponent(id)}`);
  if (assistant.current === id) newConversation();
  await loadConversations();
}

export async function deleteAllConversations() {
  await api.del('/ai/conversations');
  newConversation();
  await loadConversations();
}

export function stop() {
  abort?.abort();
  abort = undefined;
  assistant.busy = false;
}

function parseArgs(raw: string): Record<string, unknown> {
  try {
    return JSON.parse(raw || '{}') as Record<string, unknown>;
  } catch {
    return {};
  }
}

// A small view of the result the user looks at: its columns and first
// rows, so the model can talk about it without running it again.
function resultOf(tab: NonNullable<ReturnType<typeof activeTab>>) {
  const r = tab.result?.results?.[tab.activeResult];
  if (!r || r.pending) return undefined;
  const rows = (r.rows ?? []).slice(0, 20).map((row) => row.map((v) => (typeof v === 'string' && v.length > 200 ? v.slice(0, 200) + '…' : v)));
  return {
    statement: r.statement,
    kind: r.kind,
    columns: (r.columns ?? []).map((c) => `${c.name} ${c.type}`.trim()),
    rows,
    row_count: r.rows?.length ?? 0,
    rows_affected: r.rows_affected,
    truncated: r.truncated,
    duration_ms: r.duration_ms,
    error: r.error ?? '',
    selected: selectedCells(tab, r),
  };
}

// selectedCells is what the user selected in the grid, as a small table:
// the selected columns of the selected rows, at most 50 rows.
function selectedCells(tab: NonNullable<ReturnType<typeof activeTab>>, r: { columns?: { name: string }[]; rows?: unknown[][] }) {
  const sel = tab.gridSelection;
  if (!sel?.rows.length || !r.rows || !r.columns) return undefined;
  const cols = sel.cols.length ? sel.cols : r.columns.map((_, i) => i);
  // A single cell or a few: worth telling; the whole result is already sent.
  if (sel.rows.length === r.rows.length && cols.length === r.columns.length) return undefined;
  const cut = (v: unknown) => (typeof v === 'string' && v.length > 300 ? v.slice(0, 300) + '…' : v);
  return {
    row_numbers: sel.rows.slice(0, 50).map((i) => i + 1),
    columns: cols.map((i) => r.columns![i]?.name ?? ''),
    rows: sel.rows.slice(0, 50).map((i) => cols.map((c) => cut(r.rows![i]?.[c]))),
    total_rows: sel.rows.length,
  };
}

// The tab the assistant helps with, as the model sees it.
function context(tabId: string) {
  const tab = activeTab();
  const c = tab ? connection(tab.connectionId) : undefined;
  const handle = tabId ? editorOf(tabId) : undefined;
  const focus = handle?.focus();
  const editable = !!tab && (tab.kind === 'query' || tab.kind === 'notebook');
  // In a notebook the editor is the cell the user works in.
  const cell = handle?.cell;
  return {
    cell_path: cell?.path ?? '',
    cell_connection: cell?.connection ?? '',
    connection: c?.name ?? '',
    db_type: c?.db_type ?? '',
    database: c?.database ?? '',
    read_only: !!c?.read_only,
    production: !!c?.production,
    schema: tab?.schema ?? '',
    table: tab?.table ?? '',
    tab_kind: tab?.kind ?? '',
    tab_title: tab?.title ?? '',
    sql: cell ? cell.sql : editable && tab!.kind === 'query' ? tab!.sql : '',
    selection: focus?.selected ? focus.statement : '',
    statement: focus && !focus.selected ? focus.statement : '',
    cursor_line: focus?.line ?? 0,
    where: tab?.kind === 'table' ? tab.where : '',
    order_by: tab?.kind === 'table' ? tab.orderBy : '',
    tx_open: !!tab?.txOpen,
    tx_statements: tab?.txStatements ?? 0,
    error: tab?.error ?? '',
    result: tab ? resultOf(tab) : undefined,
    locale: i18n.locale,
    time_zone: Intl.DateTimeFormat().resolvedOptions().timeZone,
    utc_offset: -new Date().getTimezoneOffset(),
  };
}

// The answers to editor proposals of the last turn, for the server to
// pass to the model.
function editorResults() {
  const out: Record<string, string> = {};
  for (const e of assistant.entries) {
    for (const p of e.proposals) {
      if (p.state === 'applied') out[p.id] = 'applied';
      else if (p.state === 'dismissed') out[p.id] = 'dismissed by the user';
    }
  }
  return out;
}

// canRetry tells whether the last question can be answered again.
export function canRetry() {
  return !assistant.busy && assistant.entries.some((e) => e.role === 'user');
}

// retry answers the last question again, with the tab as it is now. The
// answer before is dropped, here and on the server.
export function retry() {
  if (!canRetry()) return;
  let i = assistant.entries.length - 1;
  while (i >= 0 && assistant.entries[i].role !== 'user') i--;
  const question = assistant.entries[i].text;
  assistant.entries.splice(i);
  // The server drops its last question only when it is this one; before the
  // conversation was saved there is nothing to drop.
  return send(question, !!assistant.current);
}

export async function send(message: string, again = false) {
  const text = message.trim();
  if (!text || assistant.busy) return;
  // A message answers the questions still open.
  for (const e of assistant.entries) for (const q of e.questions ?? []) q.answered ??= text;
  const tabId = activeTab()?.id ?? '';
  const ctx = context(tabId);
  // The statement the user was on, to replace it if the model proposes so.
  const focus = tabId ? editorOf(tabId)?.focus() : undefined;
  const range = focus?.statement ? { from: focus.from, to: focus.to, text: focus.statement } : undefined;
  const body = { conversation_id: assistant.current, message: text, context: ctx, editor_results: editorResults(), retry: again };

  assistant.entries.push({ role: 'user', text, steps: [], proposals: [], links: [], at: Date.now() });
  assistant.entries.push({ role: 'assistant', text: '', steps: [], proposals: [], links: [], at: Date.now() });
  const answer = assistant.entries[assistant.entries.length - 1];
  assistant.busy = true;
  abort = new AbortController();
  const signal = abort.signal;

  try {
    const resp = await fetch('./api/v1/ai/chat', {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
      signal,
    });
    if (!resp.ok || !resp.body) {
      const data = await resp.json().catch(() => ({}));
      throw new Error((data as { message?: string }).message || resp.statusText);
    }

    const reader = resp.body.getReader();
    const decoder = new TextDecoder();
    let buf = '';
    for (;;) {
      const { value, done } = await reader.read();
      if (done) break;
      buf += decoder.decode(value, { stream: true });
      let nl: number;
      while ((nl = buf.indexOf('\n')) >= 0) {
        const line = buf.slice(0, nl).trim();
        buf = buf.slice(nl + 1);
        if (line) handle(answer, JSON.parse(line), tabId, range);
      }
    }
  } catch (e) {
    if (!signal.aborted) answer.error = e instanceof Error ? e.message : String(e);
  } finally {
    answer.doneAt ??= Date.now();
    if (abort?.signal === signal) {
      abort = undefined;
      assistant.busy = false;
    }
    void loadConversations();
  }
}

type Event =
  | { type: 'conversation'; conversation: AIConversation; at?: number }
  | { type: 'navigate'; id: string; target: Record<string, unknown> }
  | { type: 'question'; id: string; question: string; choices?: string[] }
  | { type: 'text'; text: string }
  | { type: 'tool'; id: string; name: string; arguments: string }
  | { type: 'tool_done'; id: string; error: boolean }
  | { type: 'editor'; id: string; sql: string; mode: string; explanation?: string }
  | { type: 'error'; error: string }
  | { type: 'done'; at?: number };

function proposalMode(m: string): ProposalMode {
  return m === 'insert' || m === 'statement' ? m : 'replace';
}

function handle(answer: AIEntry, e: Event, tabId: string, range?: AIProposal['range']) {
  switch (e.type) {
    case 'conversation':
      assistant.current = e.conversation.id;
      break;
    case 'question':
      (answer.questions ??= []).push({ id: e.id, question: e.question, choices: e.choices ?? [] });
      break;
    case 'navigate':
      answer.links.push(navigateOf(e.id, e.target ?? {}));
      break;
    case 'done':
      answer.doneAt = e.at ?? Date.now();
      break;
    case 'text':
      answer.text += e.text;
      break;
    case 'tool':
      answer.steps.push({ id: e.id, name: e.name, arguments: e.arguments });
      break;
    case 'tool_done': {
      const s = answer.steps.find((x) => x.id === e.id);
      if (s) {
        s.done = true;
        s.error = e.error;
      }
      break;
    }
    case 'editor':
      answer.proposals.push({ id: e.id, sql: e.sql, mode: proposalMode(e.mode), range: e.mode === 'statement' ? range : undefined, explanation: e.explanation, tabId, state: '' });
      break;
    case 'error':
      answer.error = e.error;
      break;
  }
}

export const assistantAvailable = () => !!session.user?.ai;

// ask opens the assistant on a new conversation and sends a question about
// the open tab, for the "fix with assistant" and "ask about" buttons.
export function ask(question: string, fresh = true) {
  if (assistant.busy) stop();
  if (fresh) newConversation();
  assistant.open = true;
  assistant.minimized = false;
  void loadConversations();
  return send(question);
}
