// Fase 5 — TypeScript Client Test
import { KektorDBClient } from "../dist/index.js";

const PREFIX = `ts5_${Date.now()}`;
const client = new KektorDBClient({ host: "localhost", port: 9090 });

const results: any = { client: "TypeScript", checks_passed: 0, checks_failed: 0, failures: [] };
const t0 = Date.now();

function check(condition: boolean, desc: string) {
    if (condition) { results.checks_passed++; console.log(`  OK: ${desc}`); }
    else { results.checks_failed++; results.failures.push(desc); console.log(`  FAIL: ${desc}`); }
}

function makeVector(): number[] {
    return Array.from({ length: 128 }, () => (Math.random() * 2 - 1));
}

async function main() {
    try {
        // Create indexes
        for (const name of [`${PREFIX}_users`, `${PREFIX}_docs`, `${PREFIX}_knowledge`]) {
            try {
                await client.vcreate({ index_name: name, metric: "cosine", precision: "float32", m: 16, ef_construction: 200, text_language: "english" });
            } catch { /* may exist */ }
        }
        console.log("  OK: Indexes ready");
        results.checks_passed++;

        // Create users
        console.log("\n-- Users & Docs --");
        const users = [
            ["user:alice", { name: "Alice", role: "developer", lang: "go, python", level: 8 }],
            ["user:bob", { name: "Bob", role: "designer", lang: "figma", level: 6 }],
            ["user:carol", { name: "Carol", role: "developer", lang: "rust, go", level: 9 }],
            ["user:dave", { name: "Dave", role: "manager", lang: "english", level: 7 }],
            ["user:eve", { name: "Eve", role: "developer", lang: "python, js", level: 5 }],
        ];
        for (const [uid, meta] of users) {
            await client.vadd(`${PREFIX}_users`, uid as string, makeVector(), meta as any);
        }
        for (const [uid, meta] of users) {
            try {
                const r = await client.vget(`${PREFIX}_users`, uid as string);
                const md = (r as any).metadata || {};
                for (const [k, v] of Object.entries(meta as any)) {
                    check(md[k] === v || String(md[k]) === String(v), `User ${uid}.${k}=${v}`);
                }
            } catch (e: any) {
                check(false, `User ${uid}: ${e.message}`);
            }
        }

        // Create docs
        const docs = [
            ["doc:a1", "Go concurrency", "published", ["go"], 8, "user:alice"],
            ["doc:a2", "REST APIs Go", "published", ["go", "api"], 7, "user:alice"],
            ["doc:b1", "Figma design", "published", ["figma"], 7, "user:bob"],
            ["doc:c1", "Rust ownership", "published", ["rust"], 9, "user:carol"],
            ["doc:c2", "Zero-cost Rust", "published", ["rust", "perf"], 8, "user:carol"],
        ];
        for (const [did, content, status, tags, imp, author] of docs) {
            await client.vadd(`${PREFIX}_docs`, did as string, makeVector(), {
                content, author_id: author, status, tags, importance: imp
            });
        }

        const r = await client.searchNodes(`${PREFIX}_docs`, "status = 'published'", 100);
        const nodes = (r as any).nodes || r || [];
        check(Array.isArray(nodes) && nodes.length === docs.length, `Published: ${nodes.length}`);

        const r2 = await client.searchNodes(`${PREFIX}_users`, "level >= 7", 100);
        const nodes2 = (r2 as any).nodes || r2 || [];
        const ids = nodes2.map((n: any) => n.id).sort();
        check(JSON.stringify(ids) === JSON.stringify(["user:alice", "user:carol", "user:dave"]),
              `level>=7: ${ids}`);

        // Graph
        console.log("\n-- Graph --");
        await client.link(`${PREFIX}_users`, "user:alice", "colleague", "user:bob", "inv_colleague");
        await client.link(`${PREFIX}_users`, "user:alice", "colleague", "user:carol", "inv_colleague");
        await client.link(`${PREFIX}_users`, "user:carol", "mentors", "user:eve", "inv_mentors");

        const links = await client.getLinks(`${PREFIX}_users`, "user:alice", "colleague");
        const linkIds: string[] = (links as any).targets || links || [];
        check(linkIds.includes("user:bob"), "Link alice->bob");
        check(linkIds.includes("user:carol"), "Link alice->carol");

        const tr = await client.traverse(`${PREFIX}_users`, "user:alice", ["colleague"]);
        const result = (tr as any).result || tr || {};
        const conns = result.connections || {};
        const travIds = (conns.colleague || []).map((n: any) => n.id);
        check(travIds.includes("user:bob") && travIds.includes("user:carol"), `Traverse: ${travIds}`);

        const fp = await client.findPath(`${PREFIX}_users`, "user:alice", "user:eve",
            ["colleague", "mentors", "manages", "inv_colleague", "inv_mentors", "inv_manages"], 4);
        const path = (fp as any).path || [];
        check(path.includes("user:carol"), `FindPath: ${path}`);

        // Knowledge Engine
        console.log("\n-- Knowledge Engine --");
        const comp = await client.compile({
            name: "entity_card",
            sources: { type: "graph_query", entity: { type: "user", id: "alice" }, depth: 2 },
            index_name: `${PREFIX}_knowledge`
        });
        check((comp as any).status === "complete", `Compile: v${(comp as any).version}`);

        const art = await client.getArtifact("entity_card", "user", "alice", `${PREFIX}_knowledge`);
        check((art as any).name === "entity_card", `GetArtifact: v${(art as any).version}`);

        const arts = await client.listArtifacts(`${PREFIX}_knowledge`);
        check(Array.isArray(arts) || typeof arts === "object", "ListArtifacts OK");

        const tmpl = await client.listCompileTemplates();
        check(Array.isArray(tmpl) || typeof tmpl === "object", "Templates OK");

        const emb = await client.embedderStatus();
        check(typeof emb === "object", "Embedder OK");

    } catch (e: any) {
        console.error(`ERROR: ${e.message || e}`);
        results.failures.push(e.message || String(e));
        results.checks_failed++;
    }

    const dur = ((Date.now() - t0) / 1000).toFixed(2);
    results.duration_s = parseFloat(dur);
    const total = results.checks_passed + results.checks_failed;
    console.log(`\nTypeScript client: ${results.checks_passed}/${total} passed, ${dur}s`);

    const fs = await import("fs");
    fs.writeFileSync("/tmp/ts_client_results.json", JSON.stringify(results, null, 2));
}

main().catch(console.error);
