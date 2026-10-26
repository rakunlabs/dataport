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
| `DATAPORT_AUTH_SESSION_TTL` | `12h` | Sliding session lifetime |
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
| `DATAPORT_MCP_TOKEN_TTL` | `2160h` | Longest lifetime of an access token; `0` allows tokens that never expire |
| `DATAPORT_MCP_STATELESS` | `false` | No MCP sessions on the server, for replicas without sticky sessions; production writes are then refused |
| `DATAPORT_SERVER_PORT` | `8080` | |
| `DATAPORT_SERVER_BASE_PATH` | | Serve under a sub path such as `/dataport` |

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

## Single sign-on

Admins add OpenID Connect / OAuth2 providers under Administration → Single
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
Context Protocol at `<base path>/mcp` (streamable HTTP). Each user creates
personal access tokens from the user menu → *MCP access*, which also shows
the client setup:

```sh
claude mcp add --transport http dataport https://dataport.example.com/mcp \
  --header "Authorization: Bearer dpt_…"
```

An agent acts as the token's user and gets nothing more:

- only the connections granted to the user, with the same read-only and
  own-account rules; host and credentials are never shown;
- a token can be made read-only, which hides the write tool and keeps every
  run read-only even where the user may write;
- writes on production connections ask the user to confirm the exact SQL
  through the client (MCP elicitation); clients that cannot ask are refused;
- every statement goes to the query log with source `mcp`, filterable under
  Administration → Query log;
- revoking a token or disabling the user stops it at once.

Tools: `dataport_list_connections`, `dataport_list_schemas`,
`dataport_list_tables`, `dataport_describe_table`, `dataport_schema_context`
(one line per table, cheap on tokens), `dataport_query` (read-only, rolled
back) and `dataport_execute` (only for tokens that may write).

Integration tests run against `make env`: `go test -tags integration ./internal/engine/`.
