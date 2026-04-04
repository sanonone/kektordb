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

// --- Session Management ---

export interface StartSessionParams {
  indexName: string;
  context?: string;
  agentId?: string;
  userId?: string;
  sessionId?: string;
}

export interface StartSessionResult {
  session_id: string;
  status: string;
  message: string;
}

export interface EndSessionParams {
  indexName?: string;
}

export interface EndSessionResult {
  session_id: string;
  status: string;
  message: string;
}

// --- User Profiles ---

export interface UserProfile {
  user_id: string;
  communication_style?: string;
  language?: string;
  expertise_areas?: string[];
  dislikes?: string[];
  response_length?: string;
  confidence: number;
  last_updated: number;
  profile_data?: string;
}

export interface UserProfileList {
  profiles: UserProfileItem[];
  count: number;
}

export interface UserProfileItem {
  user_id: string;
  communication_style?: string;
  confidence: number;
  last_updated: number;
}

// --- Provenance / Source Attribution ---

export interface GraphPathNode {
  id: string;
  type: 'chunk' | 'document' | 'entity';
  label: string;
}

export interface GraphPathEdge {
  source: string;
  target: string;
  relation: string;
}

export interface GraphPath {
  nodes: GraphPathNode[];
  edges: GraphPathEdge[];
  formatted: string;
}

export interface SourceAttribution {
  chunk_id: string;
  document_id: string;
  source_file: string;
  filename: string;
  chunk_index: number;
  page_number?: number;
  content: string;
  relevance: number;
  graph_depth: number;
  graph_path?: GraphPath;
  verified: boolean;
}

// --- Adaptive Retrieval ---

export interface AdaptiveRetrieveParams {
  pipelineName: string;
  query: string;
  k?: number;
  maxTokens?: number;
  strategy?: 'greedy' | 'density' | 'graph';
  expansionDepth?: number;
  includeProvenance?: boolean;
}

export interface AdaptiveRetrieveResult {
  context_text: string;
  chunks_used: number;
  total_tokens: number;
  documents_used: number;
  sources?: SourceAttribution[];
  provenance: boolean;
  expansion_stats: {
    seed_chunks: number;
    expanded_chunks: number;
    total_evaluated: number;
  };
}

// --- Enhanced RAG ---

export interface RagRetrieveParams {
  pipelineName: string;
  query: string;
  k?: number;
  includeProvenance?: boolean;
}

export interface RagRetrieveResult {
  results?: string[];  // Legacy compatibility
  response: string;
  sources: SourceAttribution[];
  confidence: number;
  total_tokens: number;
  provenance: boolean;
}

// --- Cognitive Session ---

export interface CognitiveSessionOptions {
  userId?: string;
  metadata?: Record<string, any>;
  initialContext?: Array<{ role: string; content: string }>;
  autoEnd?: boolean;
  ttl?: number;  // milliseconds
}
