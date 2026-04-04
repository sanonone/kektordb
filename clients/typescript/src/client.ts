import {
  APIError,
  ConnectionError,
  KektorDBError,
  TimeoutError,
} from "./errors";
import type {
  AdaptiveRetrieveParams,
  AdaptiveRetrieveResult,
  ApiKeyPolicy,
  CreateIndexParams,
  EndSessionParams,
  EndSessionResult,
  GraphFilter,
  IndexInfo,
  KektorDBClientOptions,
  LinkParams,
  RagRetrieveParams,
  RagRetrieveResult,
  Reflection,
  SearchResult,
  SearchParams,
  StartSessionParams,
  StartSessionResult,
  SubgraphResult,
  TaskStatus,
  UserProfile,
  UserProfileItem,
  UserProfileList,
  VectorData,
} from "./types";

/**
 * Represents an asynchronous task on the KektorDB server.
 */
export class Task {
  public id: string;
  public status: string;
  public progressMessage?: string;
  public error?: string;

  private client: KektorDBClient;

  constructor(client: KektorDBClient, data: Record<string, any>) {
    this.client = client;
    this.id = data.id ?? "";
    this.status = data.status ?? "unknown";
    this.progressMessage = data.progress_message;
    this.error = data.error;
  }

  async refresh(): Promise<Task> {
    const data = await this.client.getTaskStatus(this.id);
    this.status = data.status;
    this.progressMessage = data.progress_message;
    this.error = data.error;
    return this;
  }

  async wait(interval = 10, timeout = 7200): Promise<Task> {
    const start = Date.now();
    while (Date.now() - start < timeout * 1000) {
      await this.refresh();
      if (this.status === "completed" || this.status === "failed") {
        return this;
      }
      await new Promise((r) => setTimeout(r, interval * 1000));
    }
    throw new TimeoutError(
      `Task '${this.id}' did not complete within ${timeout} seconds.`
    );
  }
}

/**
 * Official TypeScript client for KektorDB.
 */
export class KektorDBClient {
  private baseUrl: string;
  private timeout: number;
  private apiKey?: string;

  constructor(options: KektorDBClientOptions = {}) {
    const host = options.host ?? "localhost";
    const port = options.port ?? 9091;
    this.baseUrl = `http://${host}:${port}`;
    this.timeout = options.timeout ?? 30000;
    this.apiKey = options.apiKey;
  }

  // --- Internal HTTP Helper ---

  private async request<T = any>(
    method: string,
    endpoint: string,
    body?: Record<string, any>,
    timeout?: number
  ): Promise<T> {
    const headers: Record<string, string> = {
      "Content-Type": "application/json",
    };
    if (this.apiKey) {
      headers["Authorization"] = `Bearer ${this.apiKey}`;
    }

    const controller = new AbortController();
    const timer = setTimeout(
      () => controller.abort(),
      timeout ?? this.timeout
    );

    try {
      const response = await fetch(`${this.baseUrl}${endpoint}`, {
        method,
        headers,
        body: body ? JSON.stringify(body) : undefined,
        signal: controller.signal,
      });

      if (response.status === 204) {
        return {} as T;
      }

      if (!response.ok) {
        let msg = response.statusText;
        try {
          const errBody = await response.json();
          msg = errBody.error ?? errBody.message ?? msg;
        } catch {}
        throw new APIError(msg, response.status);
      }

      return (await response.json()) as T;
    } catch (e: any) {
      if (e instanceof APIError) throw e;
      if (e.name === "AbortError") {
        throw new ConnectionError(`Request to ${endpoint} timed out.`);
      }
      throw new ConnectionError(`KektorDB Connection Error: ${e.message}`);
    } finally {
      clearTimeout(timer);
    }
  }

  // --- Key-Value Store ---

  async set(key: string, value: string): Promise<void> {
    await this.request("POST", `/kv/${key}`, { value });
  }

  async get(key: string): Promise<string> {
    const data = await this.request("GET", `/kv/${key}`);
    return data.value;
  }

  async delete(key: string): Promise<void> {
    await this.request("DELETE", `/kv/${key}`);
  }

  // --- Index Management ---

  async vcreate(params: CreateIndexParams): Promise<void> {
    const payload: Record<string, any> = {
      index_name: params.indexName,
      metric: params.metric ?? "cosine",
      precision: params.precision ?? "float32",
    };
    if (params.m) payload.m = params.m;
    if (params.efConstruction) payload.ef_construction = params.efConstruction;
    if (params.textLanguage) payload.text_language = params.textLanguage;
    if (params.maintenance) payload.maintenance = params.maintenance;
    if (params.autoLinks) payload.auto_links = params.autoLinks;
    if (params.memoryConfig) payload.memory_config = params.memoryConfig;
    await this.request("POST", "/vector/actions/create", payload);
  }

  async listIndexes(): Promise<IndexInfo[]> {
    return this.request("GET", "/vector/indexes");
  }

  async getIndexInfo(indexName: string): Promise<IndexInfo> {
    return this.request("GET", `/vector/indexes/${indexName}`);
  }

  async deleteIndex(indexName: string): Promise<void> {
    await this.request("DELETE", `/vector/indexes/${indexName}`);
  }

  async vcompress(indexName: string, precision: string): Promise<Task> {
    const data = await this.request("POST", "/vector/actions/compress", {
      index_name: indexName,
      precision,
    });
    return new Task(this, data);
  }

  async vupdateConfig(
    indexName: string,
    config: Record<string, any>
  ): Promise<void> {
    await this.request("POST", `/vector/indexes/${indexName}/config`, config);
  }

  // --- Vector Operations ---

  async vadd(
    indexName: string,
    id: string,
    vector: number[] | null,
    metadata?: Record<string, any>,
    pinned = false
  ): Promise<void> {
    const payload: Record<string, any> = {
      index_name: indexName,
      id,
      vector: vector ?? [],
    };
    if (metadata) payload.metadata = metadata;
    if (pinned) {
      payload.metadata = { ...(payload.metadata ?? {}), _pinned: true };
    }
    await this.request("POST", "/vector/actions/add", payload);
  }

  async vaddBatch(
    indexName: string,
    vectors: VectorData[]
  ): Promise<{ vectors_added: number }> {
    return this.request("POST", "/vector/actions/add-batch", {
      index_name: indexName,
      vectors,
    });
  }

  async vimport(
    indexName: string,
    vectors: VectorData[],
    batchSize = 10000
  ): Promise<void> {
    for (let i = 0; i < vectors.length; i += batchSize) {
      const batch = vectors.slice(i, i + batchSize);
      await this.request("POST", "/vector/actions/import", {
        index_name: indexName,
        vectors: batch,
      });
    }
    await this.request("POST", "/vector/actions/import/commit", {
      index_name: indexName,
    });
  }

  async vdelete(indexName: string, id: string): Promise<void> {
    await this.request("POST", "/vector/actions/delete_vector", {
      index_name: indexName,
      id,
    });
  }

  async vget(indexName: string, id: string): Promise<VectorData> {
    return this.request("GET", `/vector/indexes/${indexName}/vectors/${id}`);
  }

  async vgetMany(indexName: string, ids: string[]): Promise<VectorData[]> {
    return this.request("POST", "/vector/actions/get-vectors", {
      index_name: indexName,
      ids,
    });
  }

  async vsearch(params: SearchParams): Promise<SearchResult[]> {
    const payload: Record<string, any> = {
      index_name: params.indexName,
      k: params.k,
      query_vector: params.queryVector,
    };
    if (params.filter) payload.filter = params.filter;
    if (params.textQuery) payload.text_query = params.textQuery;
    if (params.graphFilter) {
      payload.graph_filter = {
        root_id: params.graphFilter.rootId,
        relations: params.graphFilter.relations,
        depth: params.graphFilter.depth,
      };
    }
    if (params.efSearch) payload.ef_search = params.efSearch;
    if (params.alpha !== undefined) payload.alpha = params.alpha;
    if (params.includeRelations) payload.include_relations = params.includeRelations;
    if (params.hydrateRelations) payload.hydrate_relations = true;

    const data = await this.request("POST", "/vector/actions/search", payload);
    return data.results ?? [];
  }

  async vsearchWithScores(
    indexName: string,
    queryVector: number[],
    k: number
  ): Promise<SearchResult[]> {
    const data = await this.request("POST", "/vector/actions/search-with-scores", {
      index_name: indexName,
      k,
      query_vector: queryVector,
    });
    return data.results ?? [];
  }

  async vreinforce(indexName: string, ids: string[]): Promise<void> {
    await this.request("POST", "/vector/actions/reinforce", {
      index_name: indexName,
      ids,
    });
  }

  async vexport(
    indexName: string,
    limit = 1000,
    offset = 0
  ): Promise<{ data: VectorData[]; hasMore: boolean; nextOffset: number }> {
    return this.request(
      "GET",
      `/vector/indexes/${indexName}/export?limit=${limit}&offset=${offset}`
    );
  }

  // --- Graph Operations ---

  async vlink(params: LinkParams): Promise<void> {
    const payload: Record<string, any> = {
      index_name: params.indexName,
      source_id: params.sourceId,
      target_id: params.targetId,
      relation_type: params.relationType,
    };
    if (params.inverseRelationType) payload.inverse_relation_type = params.inverseRelationType;
    if (params.weight !== undefined) payload.weight = params.weight;
    if (params.props) payload.props = params.props;
    await this.request("POST", "/graph/actions/link", payload);
  }

  async vunlink(
    indexName: string,
    sourceId: string,
    targetId: string,
    relationType: string
  ): Promise<void> {
    await this.request("POST", "/graph/actions/unlink", {
      index_name: indexName,
      source_id: sourceId,
      target_id: targetId,
      relation_type: relationType,
    });
  }

  async vgetLinks(
    indexName: string,
    sourceId: string,
    relationType: string
  ): Promise<string[]> {
    const data = await this.request("POST", "/graph/actions/get-links", {
      index_name: indexName,
      source_id: sourceId,
      relation_type: relationType,
    });
    return data.targets ?? [];
  }

  async getIncoming(
    indexName: string,
    targetId: string,
    relationType: string
  ): Promise<string[]> {
    const data = await this.request("POST", "/graph/actions/get-incoming", {
      index_name: indexName,
      target_id: targetId,
      relation_type: relationType,
    });
    return data.sources ?? [];
  }

  async vgetConnections(
    indexName: string,
    sourceId: string,
    relationType: string
  ): Promise<VectorData[]> {
    const data = await this.request("POST", "/graph/actions/get-connections", {
      index_name: indexName,
      source_id: sourceId,
      relation_type: relationType,
    });
    return data.results ?? [];
  }

  async traverse(
    indexName: string,
    sourceId: string,
    paths: string[]
  ): Promise<any> {
    return this.request("POST", "/graph/actions/traverse", {
      index_name: indexName,
      source_id: sourceId,
      paths,
    });
  }

  async extractSubgraph(
    indexName: string,
    rootId: string,
    relations: string[],
    maxDepth = 2,
    atTime = 0
  ): Promise<SubgraphResult> {
    return this.request("POST", "/graph/actions/extract-subgraph", {
      index_name: indexName,
      root_id: rootId,
      relations,
      max_depth: maxDepth,
      at_time: atTime,
    });
  }

  async findPath(
    indexName: string,
    source: string,
    target: string,
    relations?: string[],
    atTime = 0
  ): Promise<any> {
    return this.request("POST", "/graph/actions/find-path", {
      index_name: indexName,
      source_id: source,
      target_id: target,
      relations: relations ?? ["related_to", "mentions", "parent", "child"],
      at_time: atTime,
    });
  }

  async getEdges(
    indexName: string,
    sourceId: string,
    relationType: string,
    atTime = 0
  ): Promise<any[]> {
    const data = await this.request("POST", "/graph/actions/get-edges", {
      index_name: indexName,
      source_id: sourceId,
      relation_type: relationType,
      at_time: atTime,
    });
    return data.edges ?? [];
  }

  async setNodeProperties(
    indexName: string,
    nodeId: string,
    properties: Record<string, any>
  ): Promise<void> {
    await this.request("POST", "/graph/actions/set-node-properties", {
      index_name: indexName,
      node_id: nodeId,
      properties,
    });
  }

  async getNodeProperties(
    indexName: string,
    nodeId: string
  ): Promise<Record<string, any>> {
    const data = await this.request("POST", "/graph/actions/get-node-properties", {
      index_name: indexName,
      node_id: nodeId,
    });
    return data.properties ?? {};
  }

  async searchNodes(
    indexName: string,
    propertyFilter: string,
    limit = 10
  ): Promise<any[]> {
    const data = await this.request("POST", "/graph/actions/search-nodes", {
      index_name: indexName,
      property_filter: propertyFilter,
      limit,
    });
    return data.nodes ?? [];
  }

  async getAllRelations(
    indexName: string,
    nodeId: string
  ): Promise<Record<string, string[]>> {
    const data = await this.request("POST", "/graph/actions/get-all-relations", {
      index_name: indexName,
      node_id: nodeId,
    });
    return data.relations ?? {};
  }

  async getAllIncoming(
    indexName: string,
    nodeId: string
  ): Promise<Record<string, string[]>> {
    const data = await this.request("POST", "/graph/actions/get-all-incoming", {
      index_name: indexName,
      node_id: nodeId,
    });
    return data.relations ?? {};
  }

  async setAutoLinks(
    indexName: string,
    rules: Record<string, any>[]
  ): Promise<void> {
    await this.request("PUT", `/vector/indexes/${indexName}/auto-links`, {
      rules,
    });
  }

  async getAutoLinks(indexName: string): Promise<Record<string, any>[]> {
    const data = await this.request(
      "GET",
      `/vector/indexes/${indexName}/auto-links`
    );
    return data.rules ?? [];
  }

  // --- Cognitive Engine ---

  async getReflections(
    indexName: string,
    status?: string
  ): Promise<Reflection[]> {
    let endpoint = `/vector/indexes/${indexName}/reflections`;
    if (status) endpoint += `?status=${status}`;
    const data = await this.request("GET", endpoint);
    return data.reflections ?? [];
  }

  async resolveReflection(
    indexName: string,
    reflectionId: string,
    resolution: string,
    discardId?: string
  ): Promise<void> {
    const payload: Record<string, any> = { resolution };
    if (discardId) payload.discard_id = discardId;
    await this.request(
      "POST",
      `/vector/indexes/${indexName}/reflections/${reflectionId}/resolve`,
      payload
    );
  }

  async think(indexName: string): Promise<void> {
    await this.request(
      "POST",
      `/vector/indexes/${indexName}/cognitive/think`
    );
  }

  // --- RAG ---

  async ragRetrieve(
    pipelineName: string,
    query: string,
    k = 5,
    includeProvenance = false
  ): Promise<RagRetrieveResult | any> {
    const payload = {
      pipeline_name: pipelineName,
      query,
      k,
      include_provenance: includeProvenance,
    };
    return this.request("POST", "/rag/retrieve", payload);
  }

  async adaptiveRetrieve(
    params: AdaptiveRetrieveParams
  ): Promise<AdaptiveRetrieveResult> {
    const payload = {
      pipeline_name: params.pipelineName,
      query: params.query,
      k: params.k ?? 5,
      max_tokens: params.maxTokens ?? 4096,
      strategy: params.strategy ?? "graph",
      expansion_depth: params.expansionDepth ?? 2,
      include_provenance: params.includeProvenance ?? false,
    };
    return this.request("POST", "/rag/retrieve-adaptive", payload);
  }

  // --- Session Management ---

  async startSession(params: StartSessionParams): Promise<StartSessionResult> {
    const payload: Record<string, any> = {
      index_name: params.indexName,
    };
    if (params.context) payload.context = params.context;
    if (params.agentId) payload.agent_id = params.agentId;
    if (params.userId) payload.user_id = params.userId;
    if (params.sessionId) payload.session_id = params.sessionId;
    return this.request("POST", "/sessions", payload);
  }

  async endSession(
    sessionId: string,
    params: EndSessionParams
  ): Promise<EndSessionResult> {
    const payload: Record<string, any> = {
      index_name: params.indexName,
    };
    return this.request("POST", `/sessions/${sessionId}/end`, payload);
  }

  // --- User Profiles ---

  async getUserProfile(userId: string, indexName: string): Promise<UserProfile> {
    return this.request("GET", `/users/${userId}/profile?index_name=${indexName}`);
  }

  async listUserProfiles(indexName: string): Promise<UserProfileList> {
    return this.request("GET", `/users?index_name=${indexName}`);
  }

  // --- System ---

  async aofRewrite(): Promise<Task> {
    const data = await this.request("POST", "/system/aof-rewrite");
    return new Task(this, data);
  }

  async save(): Promise<void> {
    await this.request("POST", "/system/save");
  }

  async getTaskStatus(taskId: string): Promise<TaskStatus> {
    return this.request("GET", `/system/tasks/${taskId}`);
  }

  // --- Auth ---

  async createApiKey(role: string, namespace?: string): Promise<{ token: string }> {
    const payload: Record<string, any> = { role };
    if (namespace) payload.namespace = namespace;
    return this.request("POST", "/auth/keys", payload);
  }

  async listApiKeys(): Promise<ApiKeyPolicy[]> {
    return this.request("GET", "/auth/keys");
  }

  async revokeApiKey(keyId: string): Promise<void> {
    await this.request("DELETE", `/auth/keys/${keyId}`);
  }

  // --- Static Utility Methods ---

  /**
   * Formats source attributions for display.
   */
  static formatSources(sources: any[]): string {
    if (!sources || sources.length === 0) {
      return "No sources available";
    }

    return sources
      .map((src, i) => {
        const parts: string[] = [];
        parts.push(`[${i + 1}] ${src.filename || "unknown"} (relevance: ${src.relevance?.toFixed(2) ?? "N/A"})`);
        if (src.graph_path?.formatted) {
          parts.push(`    Path: ${src.graph_path.formatted}`);
        }
        const content = src.content || "";
        parts.push(`    Content: ${content.length > 100 ? content.slice(0, 100) + "..." : content}`);
        return parts.join("\n");
      })
      .join("\n");
  }

  /**
   * Filters sources based on criteria.
   */
  static filterSources(
    sources: any[],
    options: { minRelevance?: number; maxDepth?: number; verifiedOnly?: boolean } = {}
  ): any[] {
    return sources.filter((src) => {
      if (options.minRelevance !== undefined && src.relevance < options.minRelevance) {
        return false;
      }
      if (options.maxDepth !== undefined && src.graph_depth > options.maxDepth) {
        return false;
      }
      if (options.verifiedOnly && !src.verified) {
        return false;
      }
      return true;
    });
  }

  /**
   * Groups sources by their parent document.
   */
  static groupSourcesByDocument(sources: any[]): Record<string, any[]> {
    const groups: Record<string, any[]> = {};
    for (const src of sources) {
      const docId = src.document_id || "unknown";
      if (!groups[docId]) {
        groups[docId] = [];
      }
      groups[docId].push(src);
    }
    return groups;
  }
}
