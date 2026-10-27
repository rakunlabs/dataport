export interface DbObject {
  name: string;
  kind: 'table' | 'view' | 'materialized_view' | 'foreign_table';
}

export interface Routine {
  name: string;
  kind: 'procedure' | 'function' | 'package' | 'sequence';
  signature?: string;
}

export interface ColumnInfo {
  name: string;
  type: string;
  nullable: boolean;
  default: string;
  primary_key: boolean;
  // What the column keeps beyond type and default, such as AUTO_INCREMENT.
  extra?: string;
}

export interface IndexInfo {
  name: string;
  columns: string[];
  unique: boolean;
  primary: boolean;
  method?: string;
}

export interface ForeignKey {
  name: string;
  columns: string[];
  ref_schema: string;
  ref_table: string;
  ref_columns: string[];
  on_update?: string;
  on_delete?: string;
}

export interface TriggerInfo {
  name: string;
  timing: string;
  event: string;
  enabled: boolean;
  definition: string;
}

export interface TableStructure {
  columns: ColumnInfo[];
  indexes: IndexInfo[];
  foreign_keys: ForeignKey[];
  ddl: string;
  comment: string;
  comments: Record<string, string>;
  triggers: TriggerInfo[];
}

export interface DiagramTable {
  name: string;
  kind: string;
  columns: ColumnInfo[];
  foreign_keys: ForeignKey[];
}

export interface Diagram {
  tables: DiagramTable[];
  truncated: boolean;
}
