<script lang="ts">
  // Database logos, drawn from SVG files the way DBX does
  // (public/icons/database, from DBX under Apache-2.0; see NOTICE.md there).
  // Types without a logo get a generic database mark.
  import { Database } from 'lucide-svelte';

  let { type, size = 14, dialect = '' }: { type: string; size?: number; dialect?: string } = $props();

  const files: Record<string, string> = {
    postgres: 'postgres',
    mysql: 'mysql',
    mariadb: 'mariadb',
    mssql: 'sqlserver',
    oracle: 'oracle',
    clickhouse: 'clickhouse',
    trino: 'trino',
    duckdb: 'duckdb',
    sqlite: 'sqlite',
  };

  // ODBC connections show the logo of the database their dialect names.
  const dialects: Record<string, string> = {
    db2: 'db2',
    mysql: 'mysql',
    mssql: 'sqlserver',
    postgres: 'postgres',
  };

  const file = $derived(type === 'odbc' ? dialects[dialect] : files[type]);
  // Logos keep some margin in their 1024 box; a slight scale evens them out,
  // as in DBX.
  const box = $derived(size + 2);
</script>

{#if file}
  <img
    src="./icons/database/{file}.svg"
    alt=""
    width={box}
    height={box}
    class="db-logo inline-block shrink-0 scale-[1.25] object-contain"
    class:db-logo-dark-bg={file === 'duckdb'}
    style="width:{box}px;height:{box}px"
    title={type}
    draggable="false"
    aria-hidden="true"
  />
{:else}
  <span class="inline-flex shrink-0 items-center justify-center text-muted" style="width:{box}px;height:{box}px" title={type} aria-hidden="true">
    <Database size={box - 2} />
  </span>
{/if}

<style>
  /* The DuckDB logo is a black disc; on the dark theme it gets a light
     backing so it stays visible. */
  :global(.dark) .db-logo-dark-bg {
    border-radius: 9999px;
    background: #f5f5f5;
    transform: scale(1.1);
  }
</style>
