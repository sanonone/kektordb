/**
 * Type definitions for the KektorDB client.
 */

// --- Client Configuration ---

export interface KektorDBClientOptions {
  host?: string;
  port?: number;
  timeout?: number;
  apiKey?: string;
}

// --- Index Management ---

export interface CreateIndexParams {
  indexName: string;
  metric?: "cosine" | "euclidean";
  precision?: "float32" | "float16" | "int8";
  m?: number;
  efConstruction?: number;
  textLanguage?: string;
  maintenance?: Record<string, any>;
  autoLinks?: AutoLinkRule[];
  memoryConfig?: MemoryConfig;
}

export interface AutoLinkRule {
  metadata_field: string;
  relation_type: string;
  create_node?: boolean;
}

export interface MemoryConfig {
  enabled: boolean;
  half_life_days?: number;
  min_score?: number;
}

export interface IndexInfo {
  name: string;
  vector_count: number;
  metric: string;
  precision: string;
  dimension: number;
  m: number;
  ef_construction: number;
  text_language: string;
  [key: string]: any;
}

// --- Vector Data ---

export interface VectorData {
  id: string;
  vector: number[];
  metadata?: Record<string, any>;
}

export interface SearchResult {
  id: string;
  score?: number;
  metadata?: Record<string, any>;
  relations?: Record<string, string[]>;
}

// --- Graph ---

export interface LinkParams {
  indexName: string;
  sourceId: string;
  targetId: string;
  relationType: string;
  inverseRelationType?: string;
  weight?: number;
  props?: Record<string, any>;
}

export interface SubgraphNode {
  id: string;
  metadata: Record<string, any>;
}

export interface SubgraphEdge {
  source: string;
  target: string;
  relation: string;
  dir: string;
}

export interface SubgraphResult {
  root_id: string;
  nodes: SubgraphNode[];
  edges: SubgraphEdge[];
}

// --- Search ---

export interface SearchParams {
  indexName: string;
  queryVector: number[];
  k: number;
  filter?: string;
  textQuery?: string;
  alpha?: number;
  efSearch?: number;
  graphFilter?: GraphFilter;
  includeRelations?: string[];
  hydrateRelations?: boolean;
}

export interface GraphFilter {
  rootId: string;
  relations: string[];
  depth: number;
}

// --- Cognitive ---

export interface Reflection {
  id: string;
  metadata: Record<string, any>;
}

// --- Task ---

export interface TaskStatus {
  id: string;
  status: "pending" | "completed" | "failed";
  progress_message?: string;
  error?: string;
}

// --- Auth ---

export interface ApiKeyPolicy {
  id: string;
  role: string;
  namespace: string;
  created_at: string;
}
