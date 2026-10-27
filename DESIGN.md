---
name: dataport
description: A multi-user web SQL workspace dressed as the database IDE its users already know.
colors:
  bg: "#ffffff"
  panel: "#f3f4f6"
  raised: "#ffffff"
  sunken: "#ebecf0"
  hover: "#e7e9ee"
  active: "#dfe3eb"
  line: "#d8dbe2"
  line-soft: "#e9ebef"
  fg: "#1f2329"
  fg-2: "#3c424c"
  muted: "#646b77"
  faint: "#6c727d"
  accent: "#2f6be6"
  accent-hover: "#285ccc"
  accent-fg: "#ffffff"
  accent-soft: "#3574f01f"
  focus: "#3574f0"
  ok: "#208a3c"
  ok-soft: "#208a3c1a"
  warn: "#b25e00"
  warn-soft: "#e5a1001f"
  danger: "#d22e3b"
  danger-soft: "#d22e3b14"
  prod: "#d22e3b"
  type: "#0b7a75"
  grid-head: "#f3f4f6"
  row-stripe: "#f8f9fa"
  row-active: "#dde8fd"
  row-new: "#e6f4ea"
  row-new-fg: "#1c7a35"
  row-deleted: "#fcebec"
  row-dirty-fg: "#a35400"
  cell-dirty: "#fff2d6"
  type-int: "#1750c7"
  type-num: "#0e6f83"
  type-text: "#1c7a35"
  type-bool: "#b2470f"
  type-time: "#7a3fc1"
  type-struct: "#b1265f"
  type-id: "#8a5a00"
  type-bin: "#b0262f"
  bg-dark: "#1e1f22"
  panel-dark: "#2b2d30"
  raised-dark: "#2b2d30"
  sunken-dark: "#191a1c"
  hover-dark: "#36383c"
  active-dark: "#3e4249"
  line-dark: "#393b40"
  line-soft-dark: "#2e3034"
  fg-dark: "#dfe1e5"
  fg-2-dark: "#bcbec4"
  muted-dark: "#9a9ea6"
  faint-dark: "#82868e"
  accent-dark: "#2f6be6"
  accent-hover-dark: "#3a77ee"
  accent-soft-dark: "#3574f033"
  focus-dark: "#548af7"
  ok-dark: "#5fb865"
  ok-soft-dark: "#5fb8651f"
  warn-dark: "#e5a54b"
  warn-soft-dark: "#e5a54b1f"
  danger-dark: "#f0717d"
  danger-soft-dark: "#e5576524"
  prod-dark: "#e55765"
  type-dark: "#4fc4bc"
  grid-head-dark: "#2b2d30"
  row-stripe-dark: "#222326"
  row-active-dark: "#2e436e"
  row-new-dark: "#1f3324"
  row-new-fg-dark: "#7fd389"
  row-deleted-dark: "#3d2427"
  row-dirty-fg-dark: "#e8b65f"
  cell-dirty-dark: "#47391d"
  type-int-dark: "#87aeff"
  type-num-dark: "#5ccfe6"
  type-text-dark: "#8fce8b"
  type-bool-dark: "#f2a46b"
  type-time-dark: "#c8a2ff"
  type-struct-dark: "#f08fbb"
  type-id-dark: "#e6c26b"
  type-bin-dark: "#f08b8b"
typography:
  headline:
    fontFamily: "'Inter Variable', ui-sans-serif, system-ui, -apple-system, 'Segoe UI', Roboto, sans-serif"
    fontSize: "16px"
    fontWeight: 600
    lineHeight: "22px"
    letterSpacing: "-0.01em"
  title:
    fontFamily: "'Inter Variable', ui-sans-serif, system-ui, -apple-system, 'Segoe UI', Roboto, sans-serif"
    fontSize: "14px"
    fontWeight: 600
    lineHeight: "20px"
  body:
    fontFamily: "'Inter Variable', ui-sans-serif, system-ui, -apple-system, 'Segoe UI', Roboto, sans-serif"
    fontSize: "13px"
    fontWeight: 400
    lineHeight: "18px"
    fontFeature: "'cv11', 'ss01'"
  label:
    fontFamily: "'Inter Variable', ui-sans-serif, system-ui, -apple-system, 'Segoe UI', Roboto, sans-serif"
    fontSize: "12px"
    fontWeight: 500
    lineHeight: "16px"
  caption:
    fontFamily: "'Inter Variable', ui-sans-serif, system-ui, -apple-system, 'Segoe UI', Roboto, sans-serif"
    fontSize: "11px"
    fontWeight: 500
    lineHeight: "14px"
  mono:
    fontFamily: "'JetBrains Mono Variable', ui-monospace, SFMono-Regular, Menlo, Consolas, monospace"
    fontSize: "12px"
    fontWeight: 400
    lineHeight: "16px"
    fontFeature: "'liga' 0"
rounded:
  sm: "3px"
  md: "4px"
  lg: "6px"
spacing:
  xs: "4px"
  sm: "8px"
  md: "12px"
  lg: "16px"
  control: "26px"
  field: "28px"
  row: "26px"
  toolbar: "36px"
  topbar: "38px"
components:
  button:
    backgroundColor: "transparent"
    textColor: "{colors.fg-2}"
    typography: "{typography.body}"
    rounded: "{rounded.md}"
    padding: "0 8px"
    height: "26px"
  button-hover:
    backgroundColor: "{colors.hover}"
    textColor: "{colors.fg}"
  button-outline:
    backgroundColor: "{colors.raised}"
    textColor: "{colors.fg}"
    rounded: "{rounded.md}"
    padding: "0 10px"
    height: "26px"
  button-primary:
    backgroundColor: "{colors.accent}"
    textColor: "{colors.accent-fg}"
    rounded: "{rounded.md}"
    padding: "0 10px"
    height: "26px"
  button-primary-hover:
    backgroundColor: "{colors.accent-hover}"
  button-danger:
    backgroundColor: "{colors.prod}"
    textColor: "#ffffff"
    rounded: "{rounded.md}"
    padding: "0 10px"
    height: "26px"
  icon-button:
    backgroundColor: "transparent"
    textColor: "{colors.muted}"
    rounded: "{rounded.md}"
    size: "26px"
  icon-button-pressed:
    backgroundColor: "{colors.active}"
    textColor: "{colors.fg}"
  input:
    backgroundColor: "{colors.bg}"
    textColor: "{colors.fg}"
    typography: "{typography.body}"
    rounded: "{rounded.md}"
    padding: "0 8px"
    height: "28px"
  chip:
    backgroundColor: "transparent"
    textColor: "{colors.fg-2}"
    typography: "{typography.caption}"
    rounded: "{rounded.sm}"
    padding: "0 6px"
    height: "20px"
  pill-prod:
    backgroundColor: "{colors.danger-soft}"
    textColor: "{colors.danger}"
    typography: "{typography.caption}"
    rounded: "{rounded.sm}"
    padding: "0 6px"
    height: "18px"
  pill-rw:
    backgroundColor: "{colors.warn-soft}"
    textColor: "{colors.warn}"
    rounded: "{rounded.sm}"
    padding: "0 6px"
    height: "18px"
  pill-ro:
    backgroundColor: "{colors.sunken}"
    textColor: "{colors.muted}"
    rounded: "{rounded.sm}"
    padding: "0 6px"
    height: "18px"
  pill-own:
    backgroundColor: "{colors.ok-soft}"
    textColor: "{colors.ok}"
    rounded: "{rounded.sm}"
    padding: "0 6px"
    height: "18px"
  card:
    backgroundColor: "{colors.raised}"
    rounded: "{rounded.lg}"
  popover:
    backgroundColor: "{colors.raised}"
    rounded: "{rounded.lg}"
    padding: "4px 0"
  menu-item:
    backgroundColor: "transparent"
    textColor: "{colors.fg}"
    rounded: "{rounded.md}"
    padding: "0 8px"
    height: "28px"
  menu-item-hover:
    backgroundColor: "{colors.accent}"
    textColor: "{colors.accent-fg}"
  segmented:
    backgroundColor: "{colors.sunken}"
    rounded: "{rounded.md}"
    padding: "2px"
    height: "26px"
  segmented-selected:
    backgroundColor: "{colors.raised}"
    textColor: "{colors.fg}"
    rounded: "{rounded.sm}"
  toolbar:
    backgroundColor: "{colors.panel}"
    padding: "0 8px"
    height: "36px"
  kbd:
    textColor: "{colors.muted}"
    typography: "{typography.mono}"
    rounded: "{rounded.sm}"
    height: "16px"
  table-header:
    backgroundColor: "{colors.panel}"
    textColor: "{colors.muted}"
    typography: "{typography.label}"
    padding: "0 12px"
    height: "32px"
  table-row:
    typography: "{typography.body}"
    padding: "0 12px"
    height: "36px"
---

# Design System: dataport

## Overview

**Creative North Star: "The House IDE"**

dataport looks like the database IDE its users already live in (DataGrip, DBX), played straight: graphite and paper-grey panels, hairline splits, one blue for selection and the primary action, and colour otherwise reserved for state. It is a tool for people who open it many times a day for hours, so every surface is dense, fixed-size and quiet. The content (tables, SQL, rows, errors) carries the visual weight; the chrome recedes.

Both themes are first-class. The theme follows the system by default (`prefers-color-scheme`), with an explicit System / Light / Dark segmented switch in the user menu; the choice is applied as a `.dark` class on the root before first paint. Every colour is a semantic token defined twice in one stylesheet (light in the theme block, dark in the `.dark` override), and components use only the semantic names, so no component knows which theme it is in. The frontmatter lists each light token and its `-dark` counterpart.

Safety is legible without being loud. Production, read-write, read-only and own-account states use one small pill vocabulary everywhere they matter (connection tree, toolbar, admin tables), and a production tab swaps its blue active underline for production red. Workspace and admin share exactly the same buttons, fields, chips and tables.

**Key Characteristics:**
- Flat panels divided by 1px hairlines; shadows only on things that float.
- One control height (26px) across buttons, icon buttons, segmented controls; fields at 28px.
- Fixed product type scale from 11px to 16px; 13px body.
- Inter for UI, JetBrains Mono for SQL, identifiers, hosts and cell data.
- Colour means state: accent blue, production red, read-write amber, own-account green, column-type hues in grid headers.
- Light and dark defined as paired semantic tokens; theme follows the system.

## Colors

A neutral graphite (dark) or cool paper-grey (light) IDE palette with a single blue accent and a disciplined state vocabulary.

### Primary
- **Selection Blue** (`accent`, same in both themes): the primary action (Save, New connection, Sign in), menu-item hover, the active tab underline, checked checkboxes and radios, the user avatar. Hover deepens in light (`accent-hover`) and lifts in dark (`accent-hover-dark`). `accent-soft` tints selected toggle chips, the admin-role and unique-index pills, and the column badge in WHERE suggestions; text selection is accent at 32%.
- **Focus Blue** (`focus` / `focus-dark`): the 2px focus-visible outline and the input focus border plus 25% ring. Slightly brighter in dark so it reads on graphite.

### Secondary
- **Production Red** (`prod` / `prod-dark`): production pill text, the production tab underline, the destructive Save button on production connections (`btn-danger`). `danger` / `danger-soft` cover errors, error toasts and the inline login error.
- **Read-Write Amber** (`warn` / `warn-soft`): the read-write pill, pending-edit dot on tabs and the pending-changes count.
- **Own-Account Green** (`ok` / `ok-soft`): the own-account pill (user signs in with their own database account), success toasts.
- **Query Teal** (`type`): the query-tab icon when active.
- **Object Hues** (`obj-schema`, `obj-table`, `obj-view`, `obj-matview`, `obj-procedure`, `obj-function`, `obj-package`, `obj-sequence`), after DBX: icon colours of database objects in the connection tree and object lists (schemas amber, tables green, views dusty rose, materialized views indigo, procedures blue, functions amber, packages cyan, sequences teal). The active table tab's icon uses `obj-table`. Icons only, never text or fills.

### Tertiary
- **Column Type Hues** (`type-int`, `type-num`, `type-text`, `type-bool`, `type-time`, `type-struct`, `type-id`, `type-bin`, each with a `-dark` pair): small mono type hints next to column names in grid headers and structure views, and the matching value colour where relevant (e.g. booleans). They are labels, never fills.
- **Grid State Fills** (`row-active`, also the selected tree item and admin nav item; `row-new` / `row-new-fg`, `row-deleted`, `row-dirty-fg`, `cell-dirty`, `row-stripe`, `grid-head`): selection, inserted, deleted and edited-but-unsaved rows and cells in the data grid.

### Neutral
- **Work Surface** (`bg`): the editor, the grid and inputs; the brightest (light) or darkest (dark) plane, where the work happens.
- **Chrome Panel** (`panel`): top bar, sidebar, tab strip, toolbars, status bar, table headers, modal footers, and the page body.
- **Raised** (`raised`): cards, popovers, modals, toasts, outline buttons, the selected segment.
- **Sunken** (`sunken`): segmented-control track and the read-only pill.
- **Hover / Active** (`hover`, `active`): row and button hover; pressed toggles and the active panel button.
- **Hairlines** (`line`, `line-soft`): `line` for structural splits and control borders, `line-soft` for row dividers and menu separators.
- **Ink ladder** (`fg`, `fg-2`, `muted`, `faint`): primary text, secondary text and quiet buttons, captions and table headers, placeholders and chevrons.

### Named Rules
**The One Blue Rule.** Blue marks selection, focus and the single primary action of a view. It is never decoration and never a second button's colour.

**The Colour-Is-State Rule.** Red, amber, grey and green appear only as the production, read-write, read-only and own-account states (and their error/warning/success equivalents). A coloured element always means something.

**The Paired Token Rule.** Any new colour is added as a semantic token in both the light theme block and the `.dark` override at once. Components never hard-code a hex or branch on theme.

## Typography

**Display Font:** none; the product has no display tier.
**Body Font:** Inter Variable (with ui-sans-serif, system-ui, -apple-system, Segoe UI, Roboto)
**Label/Mono Font:** JetBrains Mono Variable (with ui-monospace, SFMono-Regular, Menlo, Consolas)

**Character:** A neutral, slightly tightened Inter (`cv11`, `ss01` enabled) for chrome, and JetBrains Mono with ligatures off for everything a database would print: SQL, schema-qualified names, hosts, cell values, the generated SELECT in the status bar.

### Hierarchy
- **Headline** (600, 16px, 22px): the product name on the login page; the only text above 14px.
- **Title** (600, 14px, 20px, or 600 at 13px): login heading, modal titles, admin page titles. Admin page titles sit inline with their description rather than above it.
- **Body** (400, 13px, 18px): the default for the whole app: tree items, buttons, menu items, table cells.
- **Label** (500, 12px, 16px): field labels, table headers, sidebar section heading, status bar, hints (at 400 in `muted`).
- **Caption** (500, 11px, 14px): pills and chips.
- **Mono** (400, 12px–13px): SQL editor, WHERE/ORDER BY bar, grid cells, column type hints, `kbd` (10px).

### Named Rules
**The Fixed Scale Rule.** Sizes come only from the five-step scale (11/12/13/14/16px). No fluid type, no hero sizes; this is product UI.

**The Data-Is-Mono Rule.** Anything the database produced or will execute is set in JetBrains Mono with ligatures disabled and tabular figures for numbers; anything the app says about it is Inter.

## Layout

A desktop IDE frame that fills the viewport and never scrolls as a page (`body` overflow hidden; panes scroll internally).

- **Workspace frame:** 38px top bar (mark and product name, New query; History, Saved, Admin, user menu on the right) over a resizable connection-tree sidebar (default 264px, 200–560px, persisted) and the editor area. The editor area stacks a 34px tab strip, a 36px per-tab toolbar, the WHERE / ORDER BY bar, the grid, and a 28px status bar (rows, time against budget, SQL, export, page size, paging).
- **Admin frame:** the same top bar, a 208px left nav on `panel`, and full-width tables on `bg` headed by a single row holding title, description and the primary action at the right.
- **Login:** a centred 340px column on `panel`, no card.
- **Density:** grid rows 26px with a 28px header; tree rows 24px; admin table rows 36px with a 32px header; menu items and fields 28px.
- **Spacing rhythm:** a 4px base. Toolbars use 4px gaps and 8px side padding; table cells 12px horizontal padding; modal bodies 16px.
- **Alignment:** the primary action is right-aligned in its toolbar or header row, after the pending-state readout.
- **Narrow screens** stay readable through truncation and horizontal scroll; there is no mobile layout.

## Elevation & Depth

The resting system is flat. Depth comes from tonal planes (`bg` work surface against `panel` chrome, `sunken` tracks, `raised` floating surfaces) separated by 1px hairlines. Shadows exist only for surfaces that float over the work, and each theme has its own tuned pair; in dark they add a faint white 1px rim so the edge reads on graphite.

### Shadow Vocabulary
- **Pop** (light `0 6px 16px -4px rgb(15 20 30 / 0.18), 0 1px 3px rgb(15 20 30 / 0.12)`; dark `0 8px 22px -6px rgb(0 0 0 / 0.6), 0 0 0 1px rgb(255 255 255 / 0.04)`): popovers, menus, autocomplete, toasts.
- **Dialog** (light `0 18px 48px -12px rgb(15 20 30 / 0.35), 0 2px 6px rgb(15 20 30 / 0.12)`; dark `0 24px 60px -12px rgb(0 0 0 / 0.7), 0 0 0 1px rgb(255 255 255 / 0.05)`): modals and the expanded cell editor, over a 45% black scrim.
- **Segment lift** (`0 1px 2px rgb(0 0 0 / 0.12)`): the selected segment of a segmented control only.

### Named Rules
**The Float-Only Shadow Rule.** A shadow means "this is above the work and will go away". Panels, cards, toolbars and tables never cast one.

## Shapes

Small, consistent corners and hairline borders. Controls use 4px (`md`), tiny labels (pills, chips, kbd, checkboxes, segments) use 3px (`sm`), and floating or framed containers (cards, popovers, modals, toasts) use 6px (`lg`). Radios and the avatar are the only circles. Structural panes are square and split by 1px `line` borders; rows are split by `line-soft`. Icons are simple 1.5px-stroke Lucide outlines at 12–15px.

## Components

### Buttons
Quiet by default, one height everywhere.
- **Shape:** gently squared (4px), 26px tall, 6px icon gap, 13px text.
- **Default (ghost):** transparent, `fg-2` text; hover fills `hover` and lifts text to `fg`. Used for toolbar and top-bar actions.
- **Outline:** `raised` fill with a `line` border, `fg` text; secondary actions and SSO buttons on login.
- **Primary:** `accent` fill, `accent-fg` medium-weight text, `accent-hover` on hover. One per view.
- **Danger:** `prod` fill, white text, brightens 10% on hover; replaces Primary for writes on production connections.
- **Icon button:** 26px square, `muted` icon; `active` fill when pressed (`aria-pressed`).
- **States:** 100ms colour transitions; disabled at 40% opacity with no pointer events; 2px `focus` outline inset on keyboard focus.

### Chips
- **Chip:** 20px, 3px corners, `line` border, transparent fill, 11px `fg-2` text; neutral labels such as group grants and the table name.
- **State pills:** 18px, 3px corners, 11px medium text on a soft tint: production (`danger-soft` / `danger`), read-write (`warn-soft` / `warn`), read-only (`sunken` / `muted`, with a lock), own account (`ok-soft` / `ok`, with a key). An unsigned own-account connection shows a dashed green outline pill as its sign-in prompt.

### Cards / Containers
- **Corner Style:** 6px.
- **Background:** `raised`.
- **Shadow Strategy:** none at rest (see Elevation); popovers and modals take Pop / Dialog.
- **Border:** 1px `line`.
- **Internal Padding:** popovers 4px vertical with inset 28px menu items; modals have a 44px header with a bottom hairline, 16px body, and a `panel` footer with right-aligned actions.

### Inputs / Fields
- **Style:** 28px, `bg` fill, 1px `line` border, 4px corners, 13px text, `faint` placeholder, blue caret. The sidebar filter is a 26px, 12px-text variant with a leading search icon.
- **Hover:** border darkens toward `fg`.
- **Focus:** border turns `focus` with a 2px `focus` ring at 25%.
- **Checkbox / radio:** custom 14px boxes, `accent` fill when checked, 40% when disabled.
- **Labels:** 12px medium `fg-2`, 4px above the field; hints 12px `muted`.

### Disclosure (Details)
Rarely changed settings sit in a `<details>` box under the main fields: 1px `line` border, 4px corners, a 13px medium summary with a `faint` chevron that turns 90° when open (`.disclosure-icon`), and a 12px `muted` status beside it ("administrator defaults" / "your settings"). It opens by itself when it holds non-default values. Used for the connection pool on the connection form and on the database sign-in dialog. Empty number fields mean "inherit"; their placeholder shows the inherited value.

### Navigation
- **Top bar:** 38px `panel` strip with bottom hairline; ghost buttons; pressed panel toggles take `active`; a `toolbar-sep` before the user menu.
- **Connection tree:** 24px rows, chevrons in `faint`, database badges, state pills inline at the right; hover reveals New query / Refresh icon buttons in place of the pills; the selected table row fills `row-active`.
- **Tab strip:** 34px, tabs 128–288px wide split by `line-soft`; the active tab gets a 2px underline (accent, or `prod` on production) and a coloured icon; an amber dot marks pending edits and turns into a close button on hover.
- **Admin nav:** 208px `panel` column of ghost rows; the current page fills `row-active` with medium text.
- **Menus:** 28px items; hover fills full accent with `accent-fg` text, the IDE convention.

### Segmented Control
The Data / Structure / DDL switch and the theme switch: a 26px `sunken` track with 2px padding; segments are 12px `muted` text, the selected one `raised` with medium `fg` text and the segment-lift shadow.

### Data Grid
The signature surface: a virtualised grid with 26px rows and a sticky 28px `grid-head` header showing column name plus a mono type hint in its type hue. Cells are mono; numbers right-aligned with tabular figures. Selection fills `row-active` with a `focus` outline on the active cell; edited cells fill `cell-dirty`, inserted rows `row-new` with italic `row-new-fg` DEFAULT placeholders, deleted rows `row-deleted`. The row-number gutter takes the row's state colour.

### Toolbar and Status Bar
36px `panel` toolbars with a bottom hairline and 16px `toolbar-sep` dividers; the 28px status bar mirrors it with a top hairline and 12px `muted` text, mono for the executed SQL.

## Do's and Don'ts

### Do:
- **Do** use only semantic colour tokens, and add every new one to both the light theme block and the `.dark` override together.
- **Do** keep buttons, icon buttons and segmented controls at 26px and fields at 28px.
- **Do** keep one primary (blue) action per view, right-aligned after the pending-state readout; swap it for the danger button when the write targets production.
- **Do** express connection state with the four pills (production, read-write, read-only, own account) wherever the user acts.
- **Do** set SQL, identifiers, hosts and cell data in JetBrains Mono with ligatures off.
- **Do** separate panes with 1px hairlines and tonal planes, and reserve shadows for popovers, toasts and dialogs.
- **Do** respect reduced motion; transitions are 100ms colour changes and collapse to 0ms under `prefers-reduced-motion`.

### Don't:
- **Don't** use the accent for decoration, secondary buttons or headings.
- **Don't** introduce colours outside the state vocabulary; a coloured element must mean a state.
- **Don't** add soft shadowed cards, airy rows or large page headers; that is the generic admin-panel look this app replaced.
- **Don't** go above 16px type or use fluid sizes.
- **Don't** hard-code hex values or branch on theme inside components.
