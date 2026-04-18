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
  /** Decay half-life as a duration string (e.g., "72h", "7d", "1w") */
  decay_half_life?: string;
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
  compressContext?: boolean;  // Enable safe lexical compression for LLM optimization (20-35% token reduction)
}

export interface GraphFilter {
  rootId: string;
  relations: string[];
  maxDepth: number;
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
  compressContext?: boolean;  // Enable safe lexical compression for LLM optimization (20-35% token reduction)
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
  compressContext?: boolean;  // Enable safe lexical compression for LLM optimization (20-35% token reduction)
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

// --- Epistemic Engine ---

export interface BeliefAssessmentParams {
  indexName: string;
  query?: string;      // Text to embed (alternative to queryVec)
  queryVec?: number[]; // Direct vector (alternative to query)
  limit?: number;      // Number of candidates (default 10, max 50)
}

export interface ConsensusEvidence {
  score: number;           // 0.0-1.0
  sources: number;         // Number of nodes analyzed
  vector_variance: number; // Raw variance metric
}

export interface StabilityEvidence {
  score: number;        // 0.0-1.0
  avg_age_days: number; // Average age in days
  total_access: number; // Sum of access counts
}

export interface FrictionEvidence {
  score: number;          // 0.0-1.0 (1.0 = no friction)
  contradictions: number; // Count of contradicts relations
  invalidations: number;  // Count of invalidated_by relations
}

export interface EpistemicEvidence {
  consensus: ConsensusEvidence;
  stability: StabilityEvidence;
  friction: FrictionEvidence;
}

export interface EpistemicNode {
  id: string;
  content: string;      // From metadata
  score: number;        // Cosine similarity
  created_at: number;   // Unix timestamp
  access_count: number;
  is_historical: boolean;
  contradictions: number;
  invalidations: number;
}

export interface BeliefAssessmentResult {
  confidence: number; // 0.0-1.0 aggregated score
  state: 'crystallized' | 'stable' | 'volatile' | 'contested';
  evidence: EpistemicEvidence;
  caveat?: string;    // Human-readable explanation
  nodes: EpistemicNode[];
}

export interface InvalidateMemoryParams {
  indexName: string;
  targetId: string;    // Node being invalidated (required)
  sourceId?: string;   // Node performing invalidation (optional)
  reason?: string;     // Explanation for invalidation
}

// --- Memory Evolution ---

export interface VEvolveParams {
  indexName: string;
  oldId: string;                    // ID of node to evolve (required)
  reason: string;                   // Reason for evolution (required)
  newContent?: string;              // New content text (optional)
  newVector?: number[];             // New vector embedding (optional)
  newMetadata?: Record<string, any>; // Additional metadata (optional)
}

export interface VEvolveResult {
  new_id: string;   // ID of the newly created node
  old_id: string;   // ID of the original node
  status: string;   // "evolved"
  message: string;
}

export interface MemoryEvolutionStep {
  memory_id: string;
  content?: string;
  created_at: number;
  evolves_from?: string;
  superseded_by?: string;
  evolution_reason?: string;
  is_current: boolean;
}

export interface GetMemoryEvolutionParams {
  indexName: string;
  memoryId: string;     // Starting memory ID (required)
  direction?: 'backward' | 'forward'; // "backward" (default) or "forward"
}

export interface GetMemoryEvolutionResult {
  evolution_chain: MemoryEvolutionStep[];
  total_steps: number;
}
