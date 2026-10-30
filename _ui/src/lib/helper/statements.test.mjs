import { test } from 'node:test';
import assert from 'node:assert/strict';
import { separate, statementAt, statements } from './statements.ts';

const at = (src, marker, dbType = 'postgres') => {
  const pos = src.indexOf('|');
  const text = src.slice(0, pos) + src.slice(pos + 1);
  return statementAt(statements(text, dbType), text, pos)?.text;
};

test('the statement the cursor is in', () => {
  assert.equal(at('select 1;\nsel|ect 2;\nselect 3;'), 'select 2;');
  assert.equal(at('|select 1;\nselect 2;'), 'select 1;');
  assert.equal(at('select 1|'), 'select 1');
});

test('after the semicolon on the same line', () => {
  assert.equal(at('select 1;|\nselect 2;'), 'select 1;');
  assert.equal(at('select 1;   |  -- note\nselect 2;'), 'select 1;');
});

test('blank space before a statement on its line', () => {
  assert.equal(at('select 1;\n  |  select 2;'), 'select 2;');
});

test('a blank line between statements runs nothing', () => {
  assert.equal(at('select 1;\n|\nselect 2;'), undefined);
  assert.equal(at('select 1;\n\n|'), undefined);
});

test('several statements on one line', () => {
  assert.equal(at('select 1; sel|ect 2; select 3;'), 'select 2;');
  assert.equal(at('select 1; select 2;| select 3;'), 'select 2;');
});

test('semicolons in strings and comments do not split', () => {
  assert.equal(at("select ';' as a, 1 -- ;\n, 2|;\nselect 3;"), "select ';' as a, 1 -- ;\n, 2;");
});

const texts = (sql, dbType = 'postgres', blank = true) => statements(sql, dbType, blank).map((s) => s.text);

test('without a blank line, lines never end a statement', () => {
  assert.deepEqual(texts('select * from a\nselect * from b'), ['select * from a\nselect * from b']);
  assert.deepEqual(texts('update t\nset a = 1\nwhere b = 2\ndelete from t'), ['update t\nset a = 1\nwhere b = 2\ndelete from t']);
});

test('a blank line ends a statement before one that starts a new statement', () => {
  assert.deepEqual(texts('select * from a\n\nselect * from b'), ['select * from a', 'select * from b']);
  assert.deepEqual(texts('select 1\n   \nselect 2'), ['select 1', 'select 2']);
  assert.deepEqual(texts('select 1\n-- note\n\nselect 2'), ['select 1', 'select 2']);
  assert.deepEqual(texts('select 1\n\n-- note\nselect 2'), ['select 1', 'select 2']);
  assert.deepEqual(texts('create table t (a int)\n\ninsert into t values (1)'), ['create table t (a int)', 'insert into t values (1)']);
  assert.deepEqual(texts('create view v as\n\nselect 1\n\ndrop view v'), ['create view v as\n\nselect 1', 'drop view v']);
  assert.deepEqual(texts('select 1;\n\nselect 2\n\nselect 3;'), ['select 1;', 'select 2', 'select 3;']);
});

test('blank lines inside a statement keep it whole', () => {
  for (const sql of [
    'delete from t\n\nwhere id = 1',
    'select 1\n\nunion all\n\nselect 2',
    'with x as (\n\n  select 1\n\n)\n\nselect * from x',
    'select a,\n\n  b\nfrom t',
    'select * from t where a in (\n\nselect b from c\n\n)',
    "select 'a\n\nselect b'",
    'select a /* x\n\nselect */ from t',
    'create function f() returns int as $$\nbegin\n\n  select 1;\n\nend\n$$ language plpgsql',
    'create table x as\n\nselect 1',
    'create materialized view m as\n\nwith y as (select 1)\nselect * from y',
    'create trigger tr before update on t\n\nexecute procedure f()',
    'insert into t (a)\n\nselect 1',
    'explain analyze\n\nselect 1',
    'update t\n\nset a = 1',
    'select *\nfrom unnest(array[1,2])\n\nwith ordinality as t(x, n)',
    'alter table t\n\n  drop column a,\n\n  alter column b type int',
    'merge into t using s on t.id = s.id\n\nwhen matched then\n\n  update set a = 1',
  ]) {
    assert.deepEqual(texts(sql), [sql], sql);
  }
});

test('the blank-line rule can be turned off, and never applies to SQL Server', () => {
  assert.deepEqual(texts('select 1\n\nselect 2', 'postgres', false), ['select 1\n\nselect 2']);
  assert.deepEqual(texts('declare @a int\n\nset @a = 1\n\nselect @a', 'mssql'), ['declare @a int\n\nset @a = 1\n\nselect @a']);
});

test('separate adds the missing semicolons for the server', () => {
  assert.equal(separate('select * from a -- x\n\nselect * from b\n\nselect 3;', 'postgres'), 'select * from a; -- x\n\nselect * from b;\n\nselect 3;');
  assert.equal(separate('select 1\nselect 2', 'postgres'), 'select 1\nselect 2');
  assert.equal(separate('select 1\n\nselect 2', 'postgres', false), 'select 1\n\nselect 2');
});

// The guarantee: a script written as one statement, however long and with
// blank lines anywhere, is never cut. Every line break of these is made a
// blank line, and they must still read as one statement.
test('one statement stays one, with blank lines between all its lines', () => {
  const corpus = [
    `with recent as (
  select o.id, o.customer_id, sum(i.price * i.qty) as total,
         row_number() over (partition by o.customer_id order by o.created_at desc) as rn
  from orders o
  join items i on i.order_id = o.id
  where o.created_at > now() - interval '30 days'
    and o.status in ('paid', 'shipped')
  group by o.id, o.customer_id
)
select c.name, r.total,
       case when r.total > 1000 then 'big'
            else 'small'
       end as size
from recent r
join customers c on c.id = r.customer_id
where r.rn = 1
  and exists (
    select 1
    from refunds f
    where f.order_id = r.id
  )
union all
select 'none', 0, 'small'
order by 2 desc
limit 10`,
    `insert into archive (id, payload)
select id, payload
from events
where created_at < now() - interval '1 year'
on conflict (id) do update
set payload = excluded.payload
returning id`,
    `update accounts a
set balance = a.balance - t.amount,
    updated_at = now()
from transfers t
where t.account_id = a.id
  and t.state = 'pending'
returning a.id`,
    `delete from sessions
where user_id in (
  select id
  from users
  where disabled
)`,
    `create or replace view active_orders as
with paid as (
  select *
  from orders
  where status = 'paid'
)
select p.*,
       c.name
from paid p
join customers c on c.id = p.customer_id`,
    `create table audit_log (
  id bigint generated always as identity primary key,
  action text not null,
  comment text,
  created_at timestamptz default now()
)`,
    `merge into stock s
using deliveries d
on s.item = d.item
when matched then
  update set qty = s.qty + d.qty
when not matched then
  insert (item, qty) values (d.item, d.qty)`,
    `select
  a,
  b
from t
where x in (1, 2)
  and y between 1 and 5
group by a, b
having count(*) > 1
order by a
limit 5 offset 10`,
  ];
  for (const one of corpus) {
    const spaced = one.replace(/\n/g, '\n\n');
    for (const sql of [one, spaced]) {
      const got = texts(sql);
      assert.equal(got.length, 1, `split into ${got.length}:\n${got.join('\n---\n')}`);
    }
  }
});

// The same cases as the server's TestSplit, so the editor's frame and run
// buttons match what the server runs.
test('a routine body is one statement, as on the server', () => {
  const cases = [
    ['mysql', 'create procedure p()\nbegin\n  declare x int;\n  set x = 1;\n  select x;\nend;\nselect 2', ['create procedure p()\nbegin\n  declare x int;\n  set x = 1;\n  select x;\nend;', 'select 2']],
    ['mysql', 'create procedure p()\nbegin\n  if x > 1 then\n    select 1;\n  end if;\n  case x when 1 then select 2; end case;\n  while x < 3 do set x = x + 1; end while;\nend; select 3', ['create procedure p()\nbegin\n  if x > 1 then\n    select 1;\n  end if;\n  case x when 1 then select 2; end case;\n  while x < 3 do set x = x + 1; end while;\nend;', 'select 3']],
    ['mysql', 'create trigger tr before insert on t for each row set new.a = 1; select 1', ['create trigger tr before insert on t for each row set new.a = 1;', 'select 1']],
    ['mysql', 'create procedure p()\nbegin\n  select `end`, t.end from t;\nend; select 2', ['create procedure p()\nbegin\n  select `end`, t.end from t;\nend;', 'select 2']],
    ['sqlite', 'create trigger tr after insert on t\nbegin\n  update u set n = n + 1;\n  delete from v;\nend; select 1', ['create trigger tr after insert on t\nbegin\n  update u set n = n + 1;\n  delete from v;\nend;', 'select 1']],
    ['mssql', 'create procedure p as\nbegin\n  select 1;\n  select 2;\nend; select 3', ['create procedure p as\nbegin\n  select 1;\n  select 2;\nend;', 'select 3']],
    ['postgres', 'create function f() returns int language sql\nbegin atomic\n  select 1;\nend; select 2', ['create function f() returns int language sql\nbegin atomic\n  select 1;\nend;', 'select 2']],
    ['postgres', 'begin; select 1; commit;', ['begin;', 'select 1;', 'commit;']],
    ['mysql', 'create procedure p()\nbegin\n  select 1;\nselect 2; select 3', ['create procedure p()\nbegin\n  select 1;', 'select 2;', 'select 3']],
  ];
  for (const [db, sql, want] of cases) assert.deepEqual(texts(sql, db), want, sql);
});

// Quoting and comments as the server reads them: no ";" may be added inside
// a string or comment, and no statement is cut there.
test('strings and comments of each database hide blank lines', () => {
  for (const [db, sql] of [
    ['postgres', "update t set note = E'don\\'t\n\nDelete me' where id = 1"],
    ['postgres', 'update t set note = $x1$line\n\nupdate here$x1$ where id = 1'],
    ['oracle', "update t set note = q'[it's\n\nupdate later]' where id = 1"],
    ['postgres', 'update t set a = 1\n/* old: /* note */\n\nupdate t set b = 2 */\nwhere id = 3'],
    ['sqlite', 'select [a;b] from t'],
    ['mssql', 'select [a]];b] from t'],
    ['mysql', "select 'a\\';b' from t"],
  ]) {
    assert.deepEqual(texts(sql, db), [sql], `${db}: ${sql}`);
    assert.equal(separate(sql, db), sql, `${db}: ${sql}`);
  }
});

test('words that carry a statement on after a blank line', () => {
  for (const [db, sql] of [
    ['postgres', 'with s as (select 1 a)\ninsert into t select a from s\non conflict (a) do update\n\nset a = excluded.a'],
    ['postgres', 'explain analyze\ninsert into t select 1\non conflict (a) do update\n\nset a = 2'],
    ['mysql', 'create table t2 (id int primary key)\n\nselect id from t1'],
    ['postgres', 'alter table m\n\nattach partition p for values in (1)'],
    ['mysql', 'select * from orders\n\nuse index (ix) where id = 1'],
  ]) {
    assert.deepEqual(texts(sql, db), [sql], `${db}: ${sql}`);
  }
  // Elsewhere a CREATE TABLE with its columns ends at a blank line.
  assert.deepEqual(texts('create table t (a int)\n\nselect * from t'), ['create table t (a int)', 'select * from t']);
  // A backslash does not escape in a MySQL name: `a\` ends there.
  assert.deepEqual(texts('select `a\\`; select 2', 'mysql'), ['select `a\\`;', 'select 2']);
});

test('the cursor on a comment after a statement stays with it', () => {
  const at = (src) => {
    const pos = src.indexOf('|');
    const text = src.slice(0, pos) + src.slice(pos + 1);
    return statementAt(statements(text, 'postgres'), text, pos)?.text;
  };
  assert.equal(at('delete from a where id = 1; -- cleanup|\n\ndelete from b;'), 'delete from a where id = 1;');
  assert.equal(at('delete from a where id = 1; -- cleanup\n\n|\n\ndelete from b;'), undefined);
  assert.equal(at('delete from a where id = 1;\n-- next|\ndelete from b;'), undefined);
  assert.equal(at('delete from a where id = 1;\n-- next\ndelete| from b;'), '-- next\ndelete from b;');
});

test('separate is linear', () => {
  const big = Array.from({ length: 50000 }, (_, i) => `select ${i} from t`).join('\n\n');
  const t = performance.now();
  separate(big, 'postgres');
  assert.ok(performance.now() - t < 3000, `separate took ${performance.now() - t} ms`);
});

test('a routine body ends at its END, and only routines have one', () => {
  for (const [db, sql, want] of [
    ['sqlite', 'create index ix on shifts (begin);\ndelete from shifts;\nselect id as end from shifts;', 3],
    ['mysql', 'create table t (id int, begin int);\ncreate procedure p() begin select 1; select 2; end;', 2],
    ['mysql', 'create procedure p() begin select case when a then 1 end, case when b then 2 end from t; select 3; end; select 4', 2],
    ['mysql', 'create procedure p() begin drop table if exists t; select if(a, 1, 2) from t; end; select 4', 2],
    ['mysql', 'create procedure p() begin lbl: for i in 1..3 do select i; end for lbl; end; select 4', 2],
    ['mssql', 'create procedure p as begin if 1 = 1 select 1; else select 2; begin tran; commit; end; select 4', 2],
    ['mysql', 'create procedure p()\nbegin\n  select 1;\nend\n\nselect 2', 2],
  ]) {
    assert.equal(texts(sql, db).length, want, `${db}: ${sql} -> ${JSON.stringify(texts(sql, db))}`);
  }
});
