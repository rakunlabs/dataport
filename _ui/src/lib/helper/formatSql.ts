// Formats SQL for the editor with sql-formatter, in the dialect of the
// connection. The library is loaded on first use. Placeholders such as
// ${name}, #{name}, :name and @name are kept as written.
//
// Each statement is formatted on its own and put back with what separated
// it, as the editor and the server read the script: a ";" stays a ";", a
// statement that ended at a blank line keeps the blank line after it, an
// Oracle "/" line stays on its own line, and @set declarations and comments
// between statements are kept as written. So formatting never joins or
// splits statements.

import type { SqlLanguage } from 'sql-formatter';
import { statements } from '@/lib/helper/statements';

const languages: Record<string, SqlLanguage> = {
  postgres: 'postgresql',
  mysql: 'mysql',
  mariadb: 'mariadb',
  mssql: 'transactsql',
  oracle: 'plsql',
  sqlite: 'sqlite',
  clickhouse: 'clickhouse',
  duckdb: 'duckdb',
  trino: 'trino',
  odbc: 'sql',
};

export function formatLanguage(dbType: string): SqlLanguage {
  return languages[dbType] ?? 'sql';
}

export interface FormatSettings {
  tabWidth: number;
  // A blank line ends a statement (the editor's setting), so statements
  // keep one between them.
  blankLines?: boolean;
}

// A statement that is only @set declarations is filled in on the client,
// not SQL: it is kept as written.
const declaration = /^\s*@set\s/i;

export async function formatSql(src: string, dbType: string, o: FormatSettings): Promise<string> {
  const { format } = await import('sql-formatter');
  const language = formatLanguage(dbType);
  const run = (part: string) =>
    format(part, {
      language,
      tabWidth: o.tabWidth,
      // ClickHouse names are case sensitive and many of them (events, name,
      // type) are keywords to the formatter, which would upper-case them.
      keywordCase: dbType === 'clickhouse' ? 'preserve' : 'upper',
      paramTypes: {
        custom: [
          { regex: String.raw`\$\{[^}\n]*\}` },
          { regex: String.raw`#\{[^}\n]*\}` },
          // :name but not a :: cast, and @name but not @@version.
          { regex: String.raw`(?<![:\w]):[A-Za-z_]\w*` },
          { regex: String.raw`(?<![@\w])@[A-Za-z_]\w*` },
        ],
      },
    });

  const list = statements(src, dbType, o.blankLines ?? true);
  let out = '';
  let at = 0;
  for (const s of list) {
    // What came before the statement (comments, blank lines, "/" lines)
    // stays; leading blank lines of the script are dropped. Statements that
    // shared a line go on lines of their own.
    let gap = at === 0 ? src.slice(0, s.from).replace(/^\s*\n/, '') : src.slice(at, s.from);
    if (at > 0 && !gap.includes('\n')) gap = gap.replace(/^[ \t]*/, '\n');
    out += gap;
    if (declaration.test(s.text)) {
      out += s.text;
    } else {
      // The ";" is put back as written, after the formatted statement.
      const semi = s.text.endsWith(';');
      const body = semi ? s.text.slice(0, -1) : s.text;
      out += run(body).replace(/;\s*$/, '') + (semi ? ';' : '');
    }
    at = s.to;
  }
  out += src.slice(at);
  return out;
}
