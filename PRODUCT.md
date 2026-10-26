# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users

Technical teams: developers, data analysts and support engineers who write
queries and inspect tables many times a day, often for hours. An admin
(platform or DBA team) defines the target databases and grants them to users
or groups. Desktop first; narrow screens only need to stay readable.

## Product Purpose

A multi-user web SQL workspace. People sign in with their own dataport
account (password or SSO) and work only on the databases shared with them.
They never see hosts or shared credentials and cannot add connections.
Success: a user opens a granted database, finds a table, reads or edits rows
and runs queries without asking anyone for credentials, and an admin can tell
who ran what.

## Positioning

Desktop clients (DBX, DBeaver, TablePlus) give each person the keys. dataport
keeps the keys on the server: access is granted, read-only by default,
audited per statement, and production databases ask for confirmation with
the exact SQL before any write. A connection can also make each user sign in
with their own database account.

## Operating Context

- Workspace: connection tree (connection > schema > table > columns), tabs
  for queries and tables, CodeMirror SQL editor, virtual result grid with
  inline editing, history and saved queries.
- Table tabs: Data / Structure / DDL, WHERE and ORDER BY with suggestions,
  pending edits with SQL preview, copy rows as TSV/CSV/JSON/Markdown/SQL.
- Admin: connections, users, groups, SSO providers, query log.
- Databases: PostgreSQL, MySQL/MariaDB, SQL Server, Oracle, ClickHouse, Trino,
  SQLite and DuckDB files, and anything with an ODBC driver.
- MCP endpoint: agents work as the user of a personal access token.
- UI languages: English and Turkish; theme follows the system (light/dark).

## Capabilities and Constraints

- Svelte 5 + Tailwind 4 SPA with hash routing, embedded in a Go binary.
- Every run is one transaction; read-only grants are enforced server side.
- Production connections: the server refuses unconfirmed writes.
- No mobile-specific workflows.

## Brand Commitments

The name "dataport" stays. Logo and visual identity are open.

## Evidence on Hand

No customers, metrics or testimonials. Demo data in the local compose
databases (`shop`: customers, orders) is synthetic.

## Product Principles

1. The tool disappears into the work: dense, fast, keyboard friendly.
2. Safety is visible, not noisy: read-only, production and own-account states
   are always legible where the user acts.
3. Never hide what will run: SQL previews before writes, exact errors after.
4. Same vocabulary everywhere: one button, field, chip and table language
   across workspace and admin.
