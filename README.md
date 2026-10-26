# dataport

A web SQL workspace for teams. An admin defines the databases and who may use
them; users sign in with their own account and work only on what was shared
with them. Users never see hosts or credentials and cannot add connections.

## Run

```sh
make build
DATAPORT_AUTH_ADMIN_PASSWORD=change-me ./bin/dataport
```

Open http://localhost:8080 and sign in as `admin`. Without
`DATAPORT_AUTH_ADMIN_PASSWORD` a password is generated and logged once.

Development: `make run` for the API on :8080 and `make run-ui` for the UI on
:3000. `make env` starts sample PostgreSQL and MySQL databases.

## Docker

```sh
docker run -d -p 8080:8080 -v dataport:/data -v ./databases:/files \
  -e DATAPORT_AUTH_ADMIN_PASSWORD=change-me \
  ghcr.io/rakunlabs/dataport:latest
```

`/data` holds the SQLite store and the generated secret key; keep it on a
volume and back it up. Behind a reverse proxy that rewrites `Host`, pass the
original host in `X-Forwarded-Host` and set `DATAPORT_AUTH_SECURE_COOKIE=true`
when TLS ends at the proxy.

Releases are built by GoReleaser from `v*` tags (`.github/workflows/tag.yml`);
`make snapshot` builds every archive and image locally without publishing.

## Configuration

The app database (users, connections, grants, query log) is SQLite by
default. For several replicas or managed backups use PostgreSQL:

```sh
DATAPORT_STORE_TYPE=postgres \
DATAPORT_STORE_DSN='postgres://dataport:secret@db:5432/dataport?sslmode=require' \
DATAPORT_AUTH_SECRET_KEY='…' \
dataport
```

Tables are created on start; migrations take a lock, so replicas can start
together. When the account that may create tables differs from the one the
application runs with, set `DATAPORT_STORE_MIGRATE_DSN`: migrations then run
on that connection and the application only needs to read and write the
tables. With PostgreSQL set `DATAPORT_AUTH_SECRET_KEY` on every replica,
since a generated key lives in the local data directory. There is no tool to
move an existing SQLite store to PostgreSQL yet.

Environment variables use the `DATAPORT_` prefix (a `dataport.yaml` file works
too).

| Variable | Default | Meaning |
| --- | --- | --- |
| `DATAPORT_DATA_DIR` | `./data` | SQLite file and generated secret key |
| `DATAPORT_STORE_TYPE` | `sqlite` | `sqlite` or `postgres` for the app database |
| `DATAPORT_STORE_DSN` | | PostgreSQL connection string, e.g. `postgres://dataport:secret@db:5432/dataport`; for SQLite the database file (default `<data dir>/dataport.db`) |
| `DATAPORT_STORE_MIGRATE_DSN` | | PostgreSQL only: separate connection that creates and migrates the tables; empty uses the store DSN |
| `DATAPORT_STORE_TABLE_PREFIX` | `dataport_` | Prefix of the app tables, to share a database or schema. Before this version it was `dp_`: set it to `dp_` to keep an existing store |
| `DATAPORT_AUTH_SECRET_KEY` | generated | Encrypts stored connection passwords. Any text; an AES key is derived from it with argon2id. Back it up |
| `DATAPORT_AUTH_ADMIN_USERNAME` | `admin` | First admin, created when there are no users |
| `DATAPORT_AUTH_ADMIN_PASSWORD` | generated | |
| `DATAPORT_AUTH_SESSION_TTL` | `12h` | Sliding session lifetime; the cookie ends when the browser closes |
| `DATAPORT_AUTH_REMEMBER_TTL` | `720h` | Sliding lifetime of a session signed in with *Remember me*, whose cookie survives closing the browser; `0` hides the option |
| `DATAPORT_AUTH_SECURE_COOKIE` | `false` | Force the Secure cookie flag |
| `DATAPORT_AUTH_EXTERNAL_URL` | | Public origin for SSO redirect URIs, e.g. `https://dataport.example.com` |
| `DATAPORT_AUTH_DISABLE_PASSWORD_LOGIN` | `false` | Hide the password form and allow only SSO |
| `DATAPORT_QUERY_DEFAULT_MAX_ROWS` | `200` | Rows returned per statement; users may ask for more per tab, up to the limit below |
| `DATAPORT_QUERY_MAX_ROWS_LIMIT` | `100000` | Most rows a user may ask one statement for; `0` means no limit |
| `DATAPORT_QUERY_DEFAULT_TIMEOUT` | `0s` | Time limit of a run when the connection sets none; `0s` means no limit. Users may pick one per tab and can always stop a run |
| `DATAPORT_QUERY_MAX_TIMEOUT` | `0s` | Cap on any time limit, also on runs without one; `0s` means no cap |
| `DATAPORT_QUERY_POOL_MAX_OPEN` | `5` | Default pool size per connection |
| `DATAPORT_QUERY_POOL_MAX_IDLE` | `5` | Default idle connections kept per pool |
| `DATAPORT_QUERY_POOL_MAX_LIFETIME` | `1h` | Default lifetime of a pooled connection |
| `DATAPORT_QUERY_POOL_MAX_IDLE_TIME` | `5m` | Default time an unused connection stays open |
| `DATAPORT_QUERY_FILE_DIR` | | Directory SQLite and DuckDB files are opened from; empty disables file databases |
| `DATAPORT_MCP_ENABLED` | `true` | Serve the MCP endpoint at `<base path>/mcp` |
| `DATAPORT_MCP_MAX_ROWS` | `200` | Rows returned to an agent per statement |
| `DATAPORT_MCP_MAX_CELL_CHARS` | `2000` | Longer text values are shortened for agents |
| `DATAPORT_MCP_MAX_SCHEMA_TABLES` | `100` | Tables described by one `dataport_schema_context` call |
| `DATAPORT_MCP_TOKEN_TTL` | `2160h` | Longest lifetime of an access token, and of an OAuth sign-in before the client must sign in again; `0` allows tokens that never expire |
| `DATAPORT_MCP_STATELESS` | `false` | No MCP sessions on the server, for replicas without sticky sessions; production writes are then refused |
| `DATAPORT_SERVER_PORT` | `8080` | |
| `DATAPORT_SERVER_BASE_PATH` | | Serve under a sub path such as `/dataport`. The UI uses relative paths, so it works under any base path without a rebuild |

## How access works

- Connections are granted to users or groups, read-only or read-write.
  Read-write needs both the connection and a grant to allow it.
- Each run is one transaction. Read-only runs use a read-only transaction and
  only reading statements; everything else is rolled back. A failing statement
  rolls back the whole run. `BEGIN`/`COMMIT` in scripts are refused.
- The account configured on the connection is the final limit: give it only
  the privileges users should have.
- A connection can instead let **each user sign in with their own database
  account**. Users enter their username and password the first time they open
  it; dataport checks them against the database and stores them encrypted for
  that user only. Each user then runs on their own pool, so the database's own
  privileges apply per person. Users change or forget their account from the
  key icon in the sidebar.
- Each connection has its own pool. The admin sets its limits (max open, max
  idle, lifetime, idle time) under "Connection pool"; empty fields use the
  server defaults. On own-account connections each user has a separate pool
  and may change these limits for themselves under "Details" when signing in;
  on shared-account connections they cannot.
- Every statement is written to the query log.

- Connections marked **production** ask the user to confirm every write after
  showing the exact SQL; the server refuses unconfirmed writes.
- Table tabs edit rows in place: changes stay pending until saved, can be
  previewed as SQL, and are applied in one transaction that fails as a whole
  if any update or delete does not hit exactly one row. Editing needs a
  primary key; tables without one only accept new rows.

## Workspace

The interface speaks English, German, Spanish, French, Dutch, Polish,
Russian, Turkish, Japanese, Korean and Chinese (Simplified and Traditional);
it follows the browser and can be changed from the
user menu or the sign-in page. Translations live in `_ui/src/lib/i18n`, one
file per language typed against `en.ts`, so a missing key fails the build.

The workspace follows DBX and DBeaver: a connection tree on the left, tabs on
the right. Administration (connections, users, groups, single sign-on, query
log) lives in the **Settings** tab (gear icon), next to personal settings;
non-admins only see the personal part.

**Query tabs**

- `Ctrl+Enter` runs the selection, or the whole editor, and replaces the
  current result; `Ctrl+\` adds a new result tab instead.
  `Ctrl+Shift+Enter` runs only the statement under the cursor, and each
  statement ending with `;` has a run button in the gutter.
- Variables are filled in in the browser before a run, so the query log
  sees the final SQL: `@set x = 1` then `@x`, `${x}`, `#{x}`, and optionally
  `:x`. Undeclared names are asked for. Settings → Editor turns the
  syntaxes on or off and shows a live preview.
- Editor settings (font, line height, tab size, vim mode, run buttons…) are
  kept in the browser.

**Table tabs**

- Rows are filtered with a WHERE and ORDER BY bar that completes column
  names; `Ctrl+Enter` or `F5` reloads.
- Cells are edited in place and rows added with *Add row*; changes stay
  pending until saved (see above).
- The value panel on the right shows the selected cell; JSON shows as a
  tree that folds with +/−, keeps large integers exact and can be edited
  as text.
- *Structure* edits columns, indexes, foreign keys, triggers and comments.
  The changes become ALTER statements for the database, shown for review
  before they run. SQLite only adds, renames and drops columns; Trino and
  ODBC are read-only. *DDL* shows the CREATE statement; on PostgreSQL also
  the owner, comments and triggers, as pg_dump writes them.
- *Export* downloads CSV or JSON or copies CSV, TSV, JSON, Markdown or
  INSERT statements of the rows shown.

**The tree**

Right-click menus open tables, show their DDL, write SELECT/INSERT/UPDATE
templates, and under *More* truncate, empty (`DELETE`) or drop a table.
Objects of a schema are grouped by kind (tables, views, procedures,
functions, sequences…); routines and sequences open with their source, and
a schema opens as an ER diagram. Every statement a menu writes is
shown for review (run, open in editor, cancel) before it runs.

**Documentation** (right-click a schema or table → *Documentation*)

A schema report in the manner of [SchemaSpy](https://schemaspy.org), built
in and working on every database: an overview with counts and the largest
and most referenced tables; every table and column with types, defaults,
keys and comments; the relationships; estimated row counts from the
database's statistics; and a page per table with what it references, what
references it, its indexes and a view's definition. It reads only the
catalog, as the signed-in user, never table data.

It also lists SchemaSpy's anomalies: tables without a primary key, foreign
keys without an index, implied relationships (`customer_id` next to
`customers.id` with no foreign key), tables with no relationships, single
column tables, nullable unique columns, defaults of the text `'null'`,
numbered columns (`phone1`, `phone2`) and duplicate indexes. Where one
statement fixes it (a missing index, an implied foreign key, a duplicate
index) *Write the fix* opens it in an editor. *Download HTML* saves the
whole report, diagram included, as one file to share.

The first read saves a documentation snapshot in the application store;
subsequent UI and MCP reads use it rather than scanning the target database
again. Snapshots (including triggers) are isolated by user, connection and
schema, survive restarts, and are invalidated when the connection or saved
database identity changes. Current access is checked on every read and job.

*Refresh now* rebuilds the snapshot. *Auto-refresh* saves a personal cron
schedule for this schema, powered by
[hardloop](https://github.com/rakunlabs/hardloop). Leave it blank to turn it
off, use `@hourly`, or e.g. `CRON_TZ=Europe/Istanbul 0 3 * * *` for 03:00
Istanbul time daily. The minimum interval is one minute; without `CRON_TZ`
the server's time zone applies. Day-of-month and weekday restrictions use
AND, not traditional cron's OR. The UI shows the snapshot date, next run,
refresh-in-progress and failure status. MCP outputs also state the snapshot
date and that it is not live.

Hardloop checks due schedules each minute and once at startup. A missed
schedule runs once on recovery, not once per missed occurrence. Jobs run
sequentially per instance; persisted, expiring leases prevent manual and
scheduled refreshes from overlapping across replicas, with fenced writes.
On failure the last successful document stays available with a warning;
no partial replacement is published. A metadata scan is capped at 90 seconds
and 300 tables. All replicas need the same application store and encryption
key. Protect store backups: documentation contains schema details, comments
and definitions, though no table rows or connection passwords.

**Connection tools** (right-click a connection)

- *Disconnect* closes the connection's pool; shared pools only drop their
  idle connections.
- *Processes* lists the sessions of the database and kills one: PostgreSQL,
  MySQL/MariaDB, SQL Server, Oracle, ClickHouse and Trino.
- *Users & privileges* lists the database's accounts and their grants, and
  creates, changes or drops them.
- *Export* streams tables from the server as CSV, JSON or SQL (structure
  and/or data), several tables as a zip; the row limit does not apply.
- *Import* loads a CSV or JSON file into a new or existing table, in batches
  that each run in one transaction.
- *Data transfer* copies tables to another connection, also across database
  types, mapping column types; each table is one transaction on the target.
  Indexes and foreign keys are not copied.

Tools that write need a read-write grant and follow the production rules.

## Single sign-on

Admins add OpenID Connect / OAuth2 providers under Settings → Single
sign-on (Keycloak, Entra ID, Google, Okta, Dex…). By default only existing
users can sign in: an identity is linked to the user with the same verified
email, or an admin-created link. Optionally users are created on first
sign-in, restricted to email domains, and the admin role and groups are
synced from a claim such as `realm_access.roles` on every sign-in.

## Supported databases

New connections start with a database picker (search, categories, tiles or
list), after DBX's. Each database then asks only for what it needs: host and
port, a file, or an ODBC connection string.

| Database | Read-only enforcement | Grid editing | Build |
| --- | --- | --- | --- |
| PostgreSQL (and CockroachDB, YugabyteDB, Timescale…) | read-only transaction | yes | both |
| MySQL / MariaDB (and TiDB, PlanetScale…) | read-only transaction + statement checks | yes | both |
| SQL Server / Azure SQL | statement checks, always rolled back | yes | both |
| Oracle (go-ora, no Instant Client) | `SET TRANSACTION READ ONLY`, no database links | yes | both |
| ClickHouse | server setting `readonly=2` | no (no transactions) | both |
| Trino / Presto | read-only transaction; writes run in autocommit | no | both |
| SQLite (file) | `PRAGMA query_only`, `mode=ro` for read-only connections; no `ATTACH` | yes | both |
| DuckDB (file) | `access_mode=read_only`; external access off and configuration locked | yes | `dataport-cgo` |
| ODBC (Ingres, DB2, Informix, Teradata, HANA…) | statement checks, always rolled back | yes | `dataport-cgo` |

The container image runs the cgo build, so every database above works in
it. Its base image (`ghcr.io/rytsh/dock/db/multi`) brings unixODBC and the
ODBC drivers; mount SQLite and DuckDB files under `/files`. Releases also
ship binaries, as dbq does: `dataport` is pure Go and runs anywhere without
ODBC and DuckDB; `dataport-cgo` (linux/amd64) has them and needs unixODBC
(`libodbc`) at run time. `make build-cgo` builds it locally and
`make build-container` builds the image. Drivers missing from a build show in
the picker as unavailable.

- **Oracle**: the database field is the service name; set the SID option to
  connect by SID instead. Scripts may hold PL/SQL blocks; end each with a
  line holding only `/`, as in SQL*Plus.
- **Trino**: the database field is the catalog. A username is required;
  a password needs TLS.
- **SQLite and DuckDB** open files on the dataport server, only inside
  `DATAPORT_QUERY_FILE_DIR`. Paths outside it, symlinks leading out of it and
  the data directory are refused; the file must exist.
- **ODBC** takes a connection string (`DSN=…` from `odbc.ini`, or
  `Driver=…;Server=…`). The username and password are added as `UID` and
  `PWD`, so users may sign in with their own account. The catalog dialect
  (`ingres`, `db2`, `mysql`, `mssql`, `postgres`, or `information_schema`)
  selects how schemas, tables and columns are read.

## MCP

AI agents (Claude Code, Cursor, VS Code…) can use dataport through the Model
Context Protocol (streamable HTTP). Settings → *MCP access* shows the
address and the client setup.

**Endpoints**

- `<base path>/mcp` serves every connection shared with the user; tools take
  a connection name.
- `<base path>/mcp/<connection>` serves one connection, such as
  `/mcp/orders-db`. The agent sees nothing else and needs no connection
  name, so one client can hold a server per database.

**Signing in**

- *Browser sign-in (OAuth)*: add the server without a token,
  `claude mcp add --transport http dataport https://dataport.example.com/mcp`.
  The first time, the client opens dataport in the browser; the user signs
  in (password or SSO), picks the databases and read-only or read-write,
  and approves. *Remember this client* approves it again later without
  asking, until its access is revoked. The client renews its access by itself. dataport is the
  OAuth 2.1 server: dynamic client registration, PKCE (S256), one-hour
  access tokens and rotating refresh tokens that live as long as
  `DATAPORT_MCP_TOKEN_TTL`.
- *Access tokens*: for clients without browser sign-in, scripts and CI,
  create a `dpt_…` token (optionally for one connection) and send it as a
  bearer token:

```sh
claude mcp add --transport http dataport https://dataport.example.com/mcp \
  --header "Authorization: Bearer dpt_…"
```

Both kinds show under *Your tokens* and can be revoked there. Behind a
reverse proxy set `DATAPORT_AUTH_EXTERNAL_URL`, so the OAuth addresses
dataport announces are the public ones.

An agent acts as its user and gets nothing more:

- only the connections granted to the user (and, for a token or endpoint
  of one connection, only that one), with the same read-only and
  own-account rules; host and credentials are never shown;
- a token can be read-only, which hides the write tool and keeps every run
  read-only even where the user may write;
- writes on production connections ask the user to confirm the exact SQL
  through the client (MCP elicitation); clients that cannot ask are refused;
- every statement goes to the query log with source `mcp`, filterable under
  Settings → Query log;
- revoking a token or disabling the user stops it at once.

Tools: `dataport_list_connections`, `dataport_list_schemas`,
`dataport_list_tables`, `dataport_describe_table`, `dataport_schema_context`
(one line per table, cheap on tokens), `dataport_table_docs` (everything
code needs about given tables: columns with comments and defaults, ready
`JOIN` clauses both ways, unique keys for upserts, triggers, a view's
definition, row estimates and the columns of related tables),
`dataport_schema_report` (the documentation's map and anomalies of a whole
schema), `dataport_query` (read-only, rolled back) and `dataport_execute`
(only for tokens that may write).

Integration tests run against `make env`: `go test -tags integration ./internal/engine/`.
