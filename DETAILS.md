# dataport — details

Everything about running and using dataport. The [README](README.md) has the
short version.

- [Run](#run) · [Docker](#docker) · [Configuration](#configuration)
- [Replicas and cluster](#replicas-and-cluster)
- [How access works](#how-access-works) · [Workspace](#workspace)
- [Notebooks and SQL endpoints](#notebooks-and-sql-endpoints)
- [Single sign-on](#single-sign-on) · [Supported databases](#supported-databases) · [MCP](#mcp)

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

`/data` holds the SQLite store; keep it on a volume and back it up. Set
`DATAPORT_AUTH_SECRET_KEY` too, and back it up with the store. Behind a reverse proxy that rewrites `Host`, pass the
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
tables. With PostgreSQL set the same `DATAPORT_AUTH_SECRET_KEY` on every
replica. There is no tool to
move an existing SQLite store to PostgreSQL yet.

Configuration is read with [chu](https://github.com/rakunlabs/chu): the
defaults below, then a file, then environment variables, each overriding the
one before. The file is `dataport.yaml` (`.toml`, `.yml` and `.json` work
too) in the current folder or `/etc`, or the path in `CONFIG_FILE`; the
[README](README.md#configuration) has a full example. Environment variables
use the `DATAPORT_` prefix and the key path joined with `_`, so `auth.secret_key`
is `DATAPORT_AUTH_SECRET_KEY`. `.env` and `.env.local` in the current folder
are read too. Secret backends (Vault, Consul, AWS Secrets Manager and SSM,
GCP Secret Manager and Parameter Manager, Azure Key Vault) are built in and
turn on with their own environment variables; `CONFIG_SET_<NAME>=false`
turns one off.

| Variable | Default | Meaning |
| --- | --- | --- |
| `DATAPORT_DATA_DIR` | `./data` | SQLite file |
| `DATAPORT_STORE_TYPE` | `sqlite` | `sqlite` or `postgres` for the app database |
| `DATAPORT_STORE_DSN` | | PostgreSQL connection string, e.g. `postgres://dataport:secret@db:5432/dataport`; for SQLite the database file (default `<data dir>/dataport.db`) |
| `DATAPORT_STORE_MIGRATE_DSN` | | PostgreSQL only: separate connection that creates and migrates the tables; empty uses the store DSN |
| `DATAPORT_STORE_TABLE_PREFIX` | `dataport_` | Prefix of the app tables, to share a database or schema. Before this version it was `dp_`: set it to `dp_` to keep an existing store |
| `DATAPORT_AUTH_SECRET_KEY` | | Encrypts stored connection passwords. Any text; an AES key is derived from it with argon2id. Empty is used as an empty key (a warning is logged), so set one before saving passwords; changing it later makes saved ones unreadable. Back it up |
| `DATAPORT_AUTH_ADMIN_USERNAME` | `admin` | First admin, created when there are no users |
| `DATAPORT_AUTH_ADMIN_PASSWORD` | generated | |
| `DATAPORT_AUTH_SECURE_COOKIE` | `false` | Force the Secure cookie flag |
| `DATAPORT_AUTH_EXTERNAL_URL` | | Public origin for SSO redirect URIs, e.g. `https://dataport.example.com` |
| `DATAPORT_AUTH_DISABLE_PASSWORD_LOGIN` | `false` | Hide the password form and allow only SSO |
| `DATAPORT_QUERY_FILE_DIR` | | Directory SQLite and DuckDB files are opened from; empty disables file databases |
| `DATAPORT_SERVER_PORT` | `8080` | |
| `DATAPORT_SERVER_BASE_PATH` | | Serve under a sub path such as `/dataport`. The UI uses relative paths, so it works under any base path without a rebuild |

Query, pool, notebook, connection and MCP settings are not configured:
admins set them under *Settings → Server limits*, and these defaults apply
until they do.
Most apply at once; turning MCP on or off and stateless mode apply after a
restart.

| Setting | Default | Meaning |
| --- | --- | --- |
| Queries: rows per statement | `100` | Rows returned per statement; users may ask for more per tab, up to the limit below |
| Queries: most rows a user may ask for | `100000` | Most rows a user may ask one statement for; `0` means no limit |
| Queries: default time limit | `0` | Time limit of a run when the connection sets none; `0` means no limit. Users may pick one per tab and can always stop a run |
| Queries: time limit cap | `0` | Cap on any time limit, also on runs without one; `0` means no cap |
| Pools: open connections per pool | `5` | Default pool size per connection |
| Pools: idle connections kept per pool | `5` | Default idle connections kept per pool |
| Pools: connection lifetime | `1h` | Default lifetime of a pooled connection |
| Pools: unused connection stays open | `5m` | Default time an unused connection stays open |
| Pools: close unused pools after | `10m` | An idle pool to a database is closed after this time |
| Notebooks: simultaneous runs | `8` | Maximum simultaneous notebook runs per instance, foreground and background combined; excess requests return 429 |
| Notebooks: longest run | `30m` | Whole-notebook deadline, also for background runs; cell and server query timeouts still apply |
| Notebooks: result budget per run | `16 MiB` | Total result budget of a notebook, including dependency results; SQL cells return at most 10,000 rows per statement and obey lower server limits |
| Connections: users may create their own | off | Users add their own connections under *Settings → My connections*; only the owner uses one. Turned off, they are kept but hidden and refused |
| Connections: also database files | off | Also let users open database files (SQLite, DuckDB) from the file directory for their own connections |
| MCP: serve the endpoint | on | Serve the MCP endpoint at `<base path>/mcp` |
| MCP: rows per statement | `200` | Rows returned to an agent per statement |
| MCP: longest text value | `2000` | Longer text values are shortened for agents |
| MCP: tables per schema overview | `100` | Tables described by one `dataport_schema_context` call |
| MCP: longest token lifetime | `90 days` | Longest lifetime of an access token, and of an OAuth sign-in before the client must sign in again; `0` allows tokens that never expire |
| MCP: stateless | off | No MCP sessions on the server, for replicas without sticky sessions; production writes are then refused |

## Replicas and cluster

Several dataport replicas can serve one installation. They share the
PostgreSQL store and the secret key; with the cluster turned on they also
share the target databases: each pool to a database is opened on **one
replica**, its owner, and the others forward their work there over QUIC
([alan](https://github.com/rakunlabs/alan)) and hand the result back. A
database sees one pool per connection however many replicas run, and open
manual transactions, cancels and the process list work from any replica.

```sh
DATAPORT_STORE_TYPE=postgres
DATAPORT_STORE_DSN='postgres://dataport:secret@db:5432/dataport'
DATAPORT_AUTH_SECRET_KEY='…'                 # the same on every replica
DATAPORT_CLUSTER_ENABLED=true
DATAPORT_CLUSTER_DNS_ADDR=dataport-headless.default.svc.cluster.local
DATAPORT_CLUSTER_REPLICAS=3
DATAPORT_CLUSTER_SECURITY_KEY='…'            # admits only replicas with this key
```

| Variable | Default | Meaning |
| --- | --- | --- |
| `DATAPORT_CLUSTER_ENABLED` | `false` | Join the other replicas. Needs the PostgreSQL store |
| `DATAPORT_CLUSTER_DNS_ADDR` | | A name resolving to every replica, such as a Kubernetes headless service |
| `DATAPORT_CLUSTER_PORT` | `7946` | UDP port of the cluster, the same on every replica |
| `DATAPORT_CLUSTER_BIND_ADDR` | `0.0.0.0` | Address the cluster listens on |
| `DATAPORT_CLUSTER_REPLICAS` | `0` | Expected replicas, this one included. Owning a pool needs a majority; `0` turns the quorum off |
| `DATAPORT_CLUSTER_SECURITY_KEY` | | Pre-shared key; without it any host reaching the port may join (a warning is logged) |
| `DATAPORT_CLUSTER_REFRESH_INTERVAL` | `30s` | How often the DNS name is resolved again to find new replicas |
| `DATAPORT_CLUSTER_NAME` | host name | Name of this replica in *Settings → Cluster* |

How it works:

- The first replica that needs a pool takes a cluster lock on it and opens it.
  The others ask who holds it and forward queries, metadata reads, edits,
  imports, data generation, documentation scans and process lists there.
  Exports stream back as they are written; transfers run on the owner of the
  target and read the source rows from the owner of the source.
- Requests carry the connection as the forwarding replica resolved it, after
  its access checks; the password travels encrypted with the shared secret
  key, inside the TLS 1.3 connection between replicas.
- When a pool closes (idle timeout, *Disconnect*, an edited connection) its
  owner gives the lock up, so the next use may open it elsewhere. When an
  owner stops, its locks are released and another replica takes the pool
  over on the next request; requests that were running on it fail with
  "try again", and its open manual transactions are rolled back by the
  database.
- Without a quorum a replica opens the pool itself rather than refusing work.
- *Settings → Cluster* lists the replicas with their address, version and
  uptime, the quorum and key status, and the pools each one holds with the
  sessions in use, running queries and open transactions. *Release* closes a
  pool on its owner and gives up its ownership.

`make build-cluster` and `make env PROFILE=cluster` start three replicas on
:8081, :8082 and :8083 sharing the sample PostgreSQL as their store (sign in
as `admin` / `change-me-now`).

## How access works

- Sign-in lifetimes are set by admins under **Settings → Sign-in**. A sign-in
  without *Remember me* ends 24 hours after signing in and its cookie ends
  with the browser. A remembered one stays while used: each visit extends it
  by 7 days, up to 30 days after signing in. Changes apply to open sessions
  from their next use.
- Connections are granted to users or groups, read-only or read-write.
  Read-write needs both the connection and a grant to allow it.
- Each run is one transaction. Read-only runs use a read-only transaction and
  only reading statements; everything else is rolled back. A failing statement
  rolls back the whole run. `BEGIN`/`COMMIT` in scripts are refused.
- A query tab can switch to a **manual transaction** (the *TX* button): its
  runs then share one transaction until the user presses *Commit* or
  *Rollback*. A failing statement keeps the transaction open; stopping a run,
  closing the tab or 30 minutes without use rolls it back. The choice is
  remembered for new tabs and survives a reload, and the open transaction
  too while the server holds it. It holds a pooled connection on the replica
  that owns the pool, so at most the pool size minus one can be open per
  connection.
  Commits and rollbacks are written to the query log.
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
- **Explain** (`Ctrl+Shift+E`, or a statement's gutter menu) shows the
  execution plan of the selection or the statement under the cursor, as a
  result of its own. A dialog offers the options the database has: on
  PostgreSQL `ANALYZE`, `VERBOSE`, `COSTS`, `BUFFERS`, `TIMING`,
  `SETTINGS`, `WAL`, `SUMMARY` and text or JSON; on MySQL and MariaDB
  analyze and JSON; on Trino analyze and verbose; DuckDB analyze; ClickHouse
  verbose actions; SQL Server the estimated `SHOWPLAN` as text or XML;
  Oracle `DBMS_XPLAN`; SQLite `EXPLAIN QUERY PLAN`. A JSON plan of
  PostgreSQL shows as a tree with times, actual and estimated rows and the
  share of each step, the heaviest marked; text plans highlight costs,
  times and full scans. The choices are remembered.
  Analyze runs the statement and always rolls it back. On a read-only grant
  or a database without transactions only reading statements may be
  analyzed; on production an analyzed write asks for confirmation, as it
  holds locks and fires triggers while it runs. Plans are written to the
  query log.
- **Count all** in the result footer counts every row the query returns,
  not only the ones shown, as DBeaver's *Calculate total row count*. It runs
  `SELECT COUNT(*) FROM (<query>)` read-only, can be stopped while it runs,
  and a table tab keeps the count over pages while its filter stays.

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

*Refresh now* rebuilds the snapshot. Scheduled refreshes use one server-wide
cron schedule, powered by [hardloop](https://github.com/rakunlabs/hardloop),
that admins edit under *Settings → Documentation*: a *Scheduled refresh*
switch turns it on or off (keeping the expression), and *Refresh all now*
rebuilds every saved snapshot at once, with progress and an estimated finish
time. Users keep seeing the previous document until its new one is ready.
The schedule defaults to midnight
(`0 0 * * *`, server time) and rebuilds every saved snapshot, each with its
owner's current access. Use
`@hourly`, or e.g. `CRON_TZ=Europe/Istanbul 0 3 * * *` for 03:00 Istanbul
time daily. The minimum interval is one minute. Day-of-month and weekday
restrictions use AND, not traditional cron's OR. The UI shows the snapshot
date, next run, refresh-in-progress and failure status. MCP outputs also
state the snapshot date and that it is not live.

A run missed while the server was down happens once at startup, not once per
missed occurrence. A schedule saved on one replica reaches the others within
a minute. Jobs run
sequentially per instance; persisted, expiring leases prevent manual and
scheduled refreshes from overlapping across replicas, with fenced writes.
On failure the last successful document stays available with a warning;
no partial replacement is published. A metadata scan is capped at 90 seconds
and 300 tables. All replicas need the same application store and encryption
key. Protect store backups: documentation contains schema details, comments
and definitions, though no table rows or connection passwords.

**Who may build documentation**

Under *Settings → Documentation → Who may build documentation* (or per group,
in the group dialog's *May use* boxes), admins can limit building and
refreshing documentation to admins and some groups; by default every user
may. Users outside those groups read, without refreshing it, the newest
snapshot someone built with the same connection and database account. On a
connection where everyone uses a shared account, that is one snapshot for
all of them. On an own-account connection each user has their own account,
so nobody else's snapshot is shown, and the schema stays unbuilt until an
admin lets the user build. The schedule no longer refreshes snapshots of
users who may not build. The same group boxes also choose who may use
notebooks while notebooks are limited to groups.

**Pushing documentation to a search index**

Under *Settings → Documentation → Push to a search index*, admins can copy
saved documentation into a `push` collection of a search service such as
[krabby](https://github.com/rytsh/krabby). Agents then find tables there with
the service's full-text and vector search. Each table or view becomes one
Markdown item (columns, keys, ready JOIN clauses, indexes, triggers, a view's
definition, design notes and related tables), and each schema gets an
overview item. Item ids are `dataport/<connection id>/<schema>/<table>` and
`dataport/<connection id>/<schema>/_schema`; the connection name is set as
the item's team so searches can be filtered by it.

The admin picks each connection, the user whose access documents it and,
optionally, its schemas (the default schema otherwise). Personal connections
cannot be chosen. The push reads that user's saved snapshot, building it
first if there is none. It runs after every scheduled or manual *Refresh all*;
*Push now* sends every item again. Otherwise only items whose content
changed are sent, and items that are no longer documented are pruned. If any
connection fails, nothing is pruned and that connection's items stay as they
were. The collection is created when missing.

The protocol is krabby's push source API, so any service that answers these
requests can be the target:

```text
GET    {url}/api/v1/sources/{collection}              -> {"source":{"type":"push","page_count":n}}, 404 when missing
POST   {url}/api/v1/sources                           {"name","type":"push","description"}
POST   {url}/api/v1/sources/{collection}/items        {"items":[{id,title,content_type,content,teams,updated_at}]}, at most 20
POST   {url}/api/v1/sources/{collection}/items/prune  {"keep":[ids]} -> {"removed":n}
DELETE {url}/api/v1/sources/{collection}/items?id=    when nothing is kept
```

An optional token is sent as `Authorization: Bearer`, for the service or a
proxy in front of it; it is stored encrypted. Such services often have no
access control of their own, so everything pushed can be read by anyone who
reaches the service.

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
- *Generate test data* (on a table) fills it with made-up rows: names,
  emails, addresses, numbers, dates and more, guessed per column from its
  name and type and changeable before the run, with a live preview. Foreign
  keys pick existing values of the referenced table; identity, computed and
  defaulted columns are left to the database. A column can also take a list,
  a constant, a sequence or a mugo template such as
  `{{ .row.first_name | lower }}@example.com` (`fake "email"`, `randInt` and
  `oneOf` make values). A seed makes the same rows again. A data language
  (any of faker.js's, such as Türkçe or 日本語) turns names, places, phone
  numbers, companies and text into that language; the browser makes those
  values with faker.js, so this is in the UI only. Agents have the
  same tool as `dataport_generate_rows`, in English.

Tools that write need a read-write grant and follow the production rules.

## Notebooks and SQL endpoints

Open **Notebooks** in the top bar, then **New notebook**, or use **Create
notebook** in a query tab. Notebooks are stored on the server (SQLite or
PostgreSQL), separately from saved queries. Each starts private.

### Cells and dependencies

Each cell has its own connection, description, unique path, SQL, row limit
and timeout. Cells can be reordered with the up/down controls, duplicated,
collapsed or disabled. **Show output** controls the returned result; it does
not stop dependent cells from using it. The existing result grid and CSV/JSON
export work on notebook results too.

Save before running. **Run all** uses enabled cells; **Run cell** runs that
cell and its dependencies. Dependencies are cell paths under **Cell
settings**. They run in a stable topological order, once per run, including
nested dependencies. Missing, cyclic and disabled dependencies are refused.
Dependency cells must be read-only SQL cells, not write or transfer cells.
Only the final row-producing statement of a dependency supplies its value.
Results are capped; a dependency can therefore see a partial result when its
query exceeds the row limit. Use narrow queries rather than relying on an
unbounded dependency result.

Cells are read-only by default. **Allow writes** is an explicit opt-in and
still requires a writable database grant. **Each cell is a separate
transaction**, not a transaction for the whole notebook: if a later cell
fails or is cancelled, earlier committed writes remain. Databases without
transactions retain their usual engine limitations. A failed run is never
automatically replayed.

### Parameters and safe SQL templates

Declare inputs under **Parameters** with a name, type (`string`, `number`,
`boolean`, `json`), required flag and optional JSON default. Test input is a
JSON object. Unknown inputs and wrong types are refused. Defaults are JSON:
write `"active"`, not `active`, for a string. Local drafts and test input are
kept in the signed-in user's browser storage; do not use them for secrets.

Enable **SQL template** on a cell to use Go template control flow and bound
values. For example, a cell with path `customers`:

```sql
SELECT id, name FROM customers
WHERE status = {{ param .data.status }};
```

A second cell depending on `customers` can use:

```sql
SELECT * FROM orders
WHERE customer_id = {{ param (index .cells.customers 0).id }};
```

Do **not** put quotes around `{{ param ... }}`. It becomes a native driver
placeholder (`?`, `$1`, `@p1`, etc.), and the value is bound separately, never
interpolated into SQL. Parameterized cells must contain one statement.
Identifiers (table, schema and column names) must be static SQL; dynamic
`ident` interpolation is deliberately unavailable, including inside quoted
SQL or comments. `if`, `with`, and a single level of `range` over an array
or object are supported. Output actions must end with `param`. Rendering is
cancellation-aware and capped at 10,000 iterations; integer ranges are
refused. Inputs are bounded to 10,000 values and 20 levels of nesting; SQL
and rendered SQL are limited to 1 MiB.

#### mugo functions

Templates use [mugo](https://rytsh.github.io/mugo/functions/reference.html)
functions, as Saz does, limited to those that cannot reach the server and
whose output stays proportional to their input. Shape a value, then bind it:

```sql
SELECT * FROM customers
WHERE name = {{ .data.name | trim | upper | param }}
  AND id = {{ param (cast.ToInt .data.id) }}
  AND tags = {{ param (.data.tags | sortAlpha | join ",") }};
```

Available:

- **strings:** `trim`, `trimAll`, `trimPrefix`, `trimSuffix`, `upper`,
  `lower`, `title`, `untitle`, `substr`, `trunc`, `abbrev`, `abbrevboth`,
  `nospace`, `initials`, `swapcase`, `snakecase`, `camelcase`, `kebabcase`,
  `contains`, `hasPrefix`, `hasSuffix`, `quote`, `squote`, `cat`, `plural`,
  `toString`, `splitList`, `join`, `sortAlpha`, `replace`, `regexMatch`,
  `regexFind`, `regexFindAll`, `regexSplit`;
- **numbers:** `atoi`, `int`, `int64`, `float64`, `add`, `add1`, `sub`,
  `mul`, `div`, `mod`, `max`, `min`, `addf`, `add1f`, `subf`, `mulf`, `divf`,
  `maxf`, `minf`, `round`, `floor`, `ceil`;
- **defaults and logic:** `default`, `empty`, `coalesce`, `ternary`, `all`,
  `any`, `compact`;
- **data:** `list`, `dict`, `get`, `hasKey`, `keys`, `values`, `pick`,
  `omit`, `dig`, `first`, `last`, `rest`, `initial`, `reverse`, `has`;
- **JSON and encoding:** `toJson`, `toRawJson`, `fromJson`, `b64enc`,
  `b64dec`, `b32enc`, `b32dec`, `sha1sum`, `sha256sum`, `sha512sum`,
  `adler32sum`;
- **dates:** `now`, `date`, `dateInZone`, `toDate`, `mustToDate`,
  `unixEpoch`, `duration`, `durationRound`, `dateModify`, `ago`, `uuidv4`;
- **mugo namespaces:** `cast.ToBool`, `ToFloat64`, `ToInt`, `ToInt64`,
  `ToString`, `ToSlice`, `ToStringSlice`, `ToStringMap`, `ToIntSlice`,
  `ToDuration`, `StringToDate`; `codec.JsonDecode`, `JsonEncode`,
  `ByteToString`, `StringToByte`; `crypto.Base64`, `Base64Decode`, `MD5`,
  `SHA1`, `SHA256`, `FNV32a`, `HMAC`, `JwtParseUnverified`; `time.Now`,
  `RFC3339`, `Format`, `UTC`, `AddDuration`, `Duration`, `AddDate`;
- **Go built-ins:** `index`, `len`, `slice`, `eq`, `ne`, `lt`, `le`, `gt`,
  `ge`, `and`, `or`, `not`.

Not available: functions that read the environment, files or network or
run commands (`env`, `expandenv`, `getHostByName`, `exec`, `file`, `os`,
`execTemplate`); functions whose output a small input can blow up (`repeat`,
`until`, `untilStep`, `seq`, `indent`, `wrap`, `printf`, `randBytes`, key
and certificate generation, `bcrypt`); and mugo's `math` namespace, whose
arbitrary-precision decimals turn `"1e999999999"` into a billion digits; use
the integer and float functions above instead. Namespaces work only as
`namespace.Method`, not as values. Missing keys are errors: for optional
inputs use `get .data "key" | default "x"` or declare a parameter default.
`param` binds strings, numbers, booleans, dates and byte strings; exact
decimals stay text, so the database converts them without rounding.

**Preview writes** runs read-only cells and renders write cells without
executing them. Production SQL writes in the workspace first show this
preview. Approval is bound to the connection, exact SQL and parameter values;
dependencies are read again when the real run starts, and changed values
require another review. Preview reads and actual reads are not a shared
database snapshot. Inspect the partial-success warning before manually
retrying a run.

### Sharing and publishing

The owner or an administrator can share with users/groups:

- **View**: read the notebook;
- **View and run**: also execute it;
- **View, run and edit**: also save changes.

Only the owner or an administrator can change sharing, delete or publish.
Notebook sharing never grants database access. All source and target grants
are checked before running, and again before each cell. Own-account
connections use the caller's saved database identity, not the author's.
Saving uses optimistic version checks so two editors cannot silently
overwrite one another. Running or previewing also requires the version
currently displayed: a newer saved draft causes a reload-required error,
never silent execution of unseen SQL.

Under **Endpoint**, publish a saved version. The endpoint runs an immutable
snapshot of that document; saving a draft does not change it. Publish again
to replace the snapshot, or **Unpublish** to disable it. A published path
cannot be renamed until it is unpublished.

Published endpoints require a personal access token as a bearer token;
browser session cookies alone do not work. Create a token under **Settings →
MCP access**. The token's owner needs notebook execution permission and all
the database grants. A single-connection token cannot run a plan using
another connection. Tokens are rechecked on each request and between cells.
GET runs are read-only. POST may execute non-production writes only with a
write-capable token and writable grants. **Production writes are refused by
published endpoints**, since no interactive confirmation is available.

```sh
curl -X POST 'https://dataport.example.com/api/v1/run/customer-report' \
  -H 'Authorization: Bearer dpt_…' \
  -H 'Content-Type: application/json' \
  --data '{"status":"active"}'

# Run one published cell and its dependencies:
curl 'https://dataport.example.com/api/v1/run/customer-report/customers?status=active' \
  -H 'Authorization: Bearer dpt_…'
```

These paths are relative to the configured server base path. GET input uses
query parameters; non-string parameters use JSON syntax. POST input is the
JSON object itself, not a wrapper containing `data`. Responses contain a run
ID, status, duration, and per-cell typed engine results for cells with output
enabled. A failed run returns its partial results with HTTP 422; an
unconfirmed workspace production write uses HTTP 409.

### Background work and history

**Run in background** starts a saved notebook and returns a run ID without
waiting for completion. **Run history** shows cell status/timings and lets
the initiating user or an administrator stop a run. History is private to
the caller, except that administrators can inspect all runs of a notebook
they can open. It records metadata, not input values or result rows;
**background results cannot be downloaded later**. Use a foreground run for
results or a transfer cell for a persistent destination.

Runs maintain a heartbeat in the shared store. Cancellation requests work
across replicas through the store, normally within one second. Abandoned
workers are marked failed after their two-minute lease expires; terminal
states cannot be overwritten by a late worker. Runs are not automatically
resumed after a crash. Completed history is kept for 14 days. All replicas
need the same store and secret key. A cancellation stops further work; it
does not undo already committed cells.

### Transfer cells

Enable **Transfer SQL result** to stream a cell's read-only source query into
an existing table on another connection, without materializing the whole
dataset in the browser. Source query row limits do not restrict a transfer.
Choose the target, schema, table and batch size (1–500, further limited by
driver parameter limits). Optional **Empty target first** uses `DELETE`
inside the target transaction.

Mappings select/rename columns, preserve values or convert to
string/number/date, enforce nullable handling, decode ISO-8859-1 bytes, and
apply a value template. Value templates use the same mugo functions as SQL
templates, with the source value at `.`, for example `{{ . | trim | upper }}`;
they print the value directly, but cannot loop or call templates. No mappings
keeps all columns.
The source account may be read-only; the target needs a writable grant.
The target must support transactions, must not be production, and must use
a distinct connection. A target failure rolls back that cell's target
transaction, including its optional DELETE. It does not roll back earlier
notebook cells.

**Skip insert errors containing** is optional and supported on PostgreSQL,
MySQL and SQLite targets. A matching failed batch is rolled back to a
savepoint and retried row by row, so valid neighbours are retained. An
unmatched error or any savepoint-control failure aborts the transfer. The
result reports inserted and skipped rows. Production transfers remain in
the existing connection transfer tool, rather than accepting a blanket
notebook approval.

### Import/export and Saz compatibility

**Export notebook** downloads its definition as JSON, without credentials,
results, sharing grants or tokens. **Import JSON / Saz** creates a new,
unpublished local draft from a dataport document or a Saz note JSON object
(`name`, `path`, `content.cells`); the Saz API's `data` wrapper is accepted
too. Source/target database names are matched to
dataport connections where possible; review every connection before saving.

Cell order, paths, descriptions, enabled/output/template flags,
dependencies, limits and compatible transfer configuration are carried over.
Saz source-scan conversions (`map_type.column`) are refused with an explicit
manual-migration error rather than being collapsed into a different destination
conversion. Unknown encodings and the old skip-all-errors mode are also
refused, never silently substituted. Saz templates printed values straight
into the SQL text (`'{{ .data.status }}'`); dataport refuses that, so rewrite
each value as a bound parameter without quotes, for example
`{{ .data.status | param }}`. The supported mugo functions keep working inside
that pipeline; the server-reaching ones listed above do not. Inputs used
through `.data` must be declared in Parameters. Imported notebooks do not
inherit the old shared-token model.

The new UI labels are translated in English and Turkish; the other existing
locales currently use English for the notebook namespace.

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
it. Its base image (`ghcr.io/rakunlabs/dock/db/multi`) brings unixODBC and the
ODBC drivers, registered in `/etc/odbcinst.ini` as `DB2`,
`ODBC Driver 18 for SQL Server`, `PostgreSQL Unicode` and `MariaDB Unicode`
(use them as `Driver={DB2};…`). Ingres is not included, as Actian's license
does not allow redistribution; to add it, build your own image on top of
dataport's as described in
[dock/images/db](https://github.com/rakunlabs/dock/tree/main/images/db#adding-ingres). Mount SQLite and DuckDB files under `/files`. Releases also
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

`dataport_explain` returns the plan of one statement with its estimated
costs; with `analyze` the statement runs and is rolled back, which counts as
a write for the agent's access when the statement writes.

### Agent access

Each user decides under **Settings → MCP access → Agent access** which of
their connections agents may use: drag a connection between *Available to
agents* and *Hidden from agents*, and choose per connection whether agents
may **read**, **write** (change data) or also change the **schema** (DDL).
Connections shared later start hidden or read-only, as the user chooses.
The choice is one per user, read on every call, and applies at once to all
of the user's tokens and signed-in clients. The user's own grant, read-only
tokens and production confirmation remain upper limits. Until a user saves a
choice, agents reach every connection as far as the grant allows.

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
  access tokens and rotating refresh tokens that live as long as the
  longest token lifetime in *Settings → Server limits*.
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

**Notebooks over MCP.** An agent can manage the user's notebooks with the
same rules as the workspace:

- `dataport_list_notebooks`, `dataport_get_notebook`: the notebooks the user
  owns or that are shared with them, and a notebook's cells (connection
  names, SQL, flags, dependencies, transfer settings), parameters and version;
- `dataport_run_notebook`: run the saved draft, or with `published` the
  published snapshot, or one `cell` with its dependencies, with `inputs` for
  its parameters; `preview` renders write cells without running them, and
  `background` returns a run id at once;
- `dataport_notebook_runs`, `dataport_cancel_notebook_run`: follow and stop
  the caller's runs (administrators see every run);
- `dataport_save_notebook`: create a notebook, or replace a draft the user may
  edit; updates need the current `version`, so a concurrent change is never
  overwritten, and cells name connections the user was granted;
- `dataport_publish_notebook`, `dataport_share_notebook`,
  `dataport_delete_notebook`: owner (or administrator) only. Shares name users
  and groups; the list is replaced as a whole.

Read-only tokens get only the reading and running tools, and their runs
refuse write and transfer cells. A token or endpoint for one connection only
sees notebooks whose cells (draft and published) all stay on that connection.
Writes on production connections are shown to the user through the client
(MCP elicitation): dataport previews the run, asks with every rendered
statement and its bound values, and runs only those exact statements;
dependency values that change in between are refused rather than run.
Clients that cannot ask are refused, though they may still preview. Notebook
statements go to the query log with source `mcp`; run history records the
source too.

Integration tests run against `make env`: `go test -tags integration ./internal/engine/`.
