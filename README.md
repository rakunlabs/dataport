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
docker run -d -p 8080:8080 -v dataport:/data \
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
together. With PostgreSQL set `DATAPORT_AUTH_SECRET_KEY` on every replica,
since a generated key lives in the local data directory. There is no tool to
move an existing SQLite store to PostgreSQL yet.

Environment variables use the `DATAPORT_` prefix (a `dataport.yaml` file works
too).

| Variable | Default | Meaning |
| --- | --- | --- |
| `DATAPORT_DATA_DIR` | `./data` | SQLite file and generated secret key |
| `DATAPORT_STORE_TYPE` | `sqlite` | `sqlite` or `postgres` for the app database |
| `DATAPORT_STORE_DSN` | | Connection string of the app database, e.g. `postgres://dataport:secret@db:5432/dataport` |
| `DATAPORT_STORE_TABLE_PREFIX` | `dp_` | Prefix of the app tables, to share a database or schema |
| `DATAPORT_AUTH_SECRET_KEY` | generated | Encrypts stored connection passwords. Any text; an AES key is derived from it with argon2id. Back it up |
| `DATAPORT_AUTH_ADMIN_USERNAME` | `admin` | First admin, created when there are no users |
| `DATAPORT_AUTH_ADMIN_PASSWORD` | generated | |
| `DATAPORT_AUTH_SESSION_TTL` | `12h` | Sliding session lifetime |
| `DATAPORT_AUTH_SECURE_COOKIE` | `false` | Force the Secure cookie flag |
| `DATAPORT_AUTH_EXTERNAL_URL` | | Public origin for SSO redirect URIs, e.g. `https://dataport.example.com` |
| `DATAPORT_AUTH_DISABLE_PASSWORD_LOGIN` | `false` | Hide the password form and allow only SSO |
| `DATAPORT_QUERY_DEFAULT_MAX_ROWS` | `1000` | Rows returned per statement |
| `DATAPORT_QUERY_DEFAULT_TIMEOUT` | `30s` | Per request |
| `DATAPORT_QUERY_POOL_MAX_OPEN` | `5` | Default pool size per connection |
| `DATAPORT_QUERY_POOL_MAX_IDLE` | `5` | Default idle connections kept per pool |
| `DATAPORT_QUERY_POOL_MAX_LIFETIME` | `1h` | Default lifetime of a pooled connection |
| `DATAPORT_QUERY_POOL_MAX_IDLE_TIME` | `5m` | Default time an unused connection stays open |
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

| Database | Read-only enforcement | Grid editing |
| --- | --- | --- |
| PostgreSQL | read-only transaction | yes |
| MySQL / MariaDB | read-only transaction + statement checks | yes |
| SQL Server | statement checks, always rolled back | yes |
| ClickHouse | server setting `readonly=2` | no (no transactions) |

Integration tests run against `make env`: `go test -tags integration ./internal/engine/`.
