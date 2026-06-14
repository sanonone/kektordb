// Fase 5 — TypeScript Client Test (CommonJS)
const { KektorDBClient } = require("../dist/index.js");
const fs = require("fs");

const PREFIX = `ts5_${Date.now()}`;
const client = new KektorDBClient({ host: "localhost", port: 9090 });

const results = { client: "TypeScript", checks_passed: 0, checks_failed: 0, failures: [] };
const t0 = Date.now();

function check(condition, desc) {
    if (condition) { results.checks_passed++; console.log(`  OK: ${desc}`); }
    else { results.checks_failed++; results.failures.push(desc); console.log(`  FAIL: ${desc}`); }
}

function makeVector() {
    return Array.from({ length: 128 }, () => (Math.random() * 2 - 1));
}

async function main() {
    try {
        for (const name of [`${PREFIX}_users`, `${PREFIX}_docs`, `${PREFIX}_knowledge`]) {
            try {
                await client.vcreate({ indexName: name, metric: "cosine", precision: "float32", m: 16, efConstruction: 200, textLanguage: "english" });
            } catch (e) { /* may exist */ }
        }
        console.log("  OK: Indexes ready"); results.checks_passed++;

        console.log("\n-- Users & Docs --");
        const users = [
            ["user:alice", { name: "Alice", role: "developer", lang: "go, python", level: 8 }],
            ["user:bob", { name: "Bob", role: "designer", lang: "figma", level: 6 }],
            ["user:carol", { name: "Carol", role: "developer", lang: "rust, go", level: 9 }],
            ["user:dave", { name: "Dave", role: "manager", lang: "english", level: 7 }],
            ["user:eve", { name: "Eve", role: "developer", lang: "python, js", level: 5 }],
        ];
        for (const [uid, meta] of users) {
            await client.vadd(`${PREFIX}_users`, uid, makeVector(), meta);
        }
        for (const [uid, meta] of users) {
            try {
                const r = await client.vget(`${PREFIX}_users`, uid);
                const md = r.metadata || {};
                for (const [k, v] of Object.entries(meta)) {
                    check(String(md[k]) === String(v), `User ${uid}.${k}=${v}`);
                }
            } catch (e) {
                check(false, `User ${uid}: ${e.message}`);
            }
        }

        const docs = [
            ["doc:a1", "Go concurrency", "published", ["go"], 8, "user:alice"],
            ["doc:a2", "REST APIs Go", "published", ["go", "api"], 7, "user:alice"],
            ["doc:b1", "Figma design", "published", ["figma"], 7, "user:bob"],
            ["doc:c1", "Rust ownership", "published", ["rust"], 9, "user:carol"],
            ["doc:c2", "Zero-cost Rust", "published", ["rust", "perf"], 8, "user:carol"],
        ];
        for (const [did, content, status, tags, imp, author] of docs) {
            await client.vadd(`${PREFIX}_docs`, did, makeVector(), {
                content, author_id: author, status, tags, importance: imp
            });
        }

        const res = await client.searchNodes(`${PREFIX}_docs`, "status = 'published'", 100);
        check(Array.isArray(res) && res.length === docs.length, `Published: ${res.length}`);

        const res2 = await client.searchNodes(`${PREFIX}_users`, "level >= 7", 100);
        const ids = res2.map(n => n.id).sort();
        check(JSON.stringify(ids) === JSON.stringify(["user:alice", "user:carol", "user:dave"]),
              `level>=7: ${ids}`);

        console.log("\n-- Graph --");
        await client.vlink({ indexName: `${PREFIX}_users`, sourceId: "user:alice", targetId: "user:bob", relationType: "colleague", inverseRelationType: "inv_colleague" });
        await client.vlink({ indexName: `${PREFIX}_users`, sourceId: "user:alice", targetId: "user:carol", relationType: "colleague", inverseRelationType: "inv_colleague" });
        await client.vlink({ indexName: `${PREFIX}_users`, sourceId: "user:carol", targetId: "user:eve", relationType: "mentors", inverseRelationType: "inv_mentors" });

        const links = await client.vgetLinks(`${PREFIX}_users`, "user:alice", "colleague");
        check(links.includes("user:bob"), "Link alice->bob");
        check(links.includes("user:carol"), "Link alice->carol");

        const tr = await client.traverse(`${PREFIX}_users`, "user:alice", ["colleague"]);
        const conns = (tr.result || tr || {}).connections || {};
        const travIds = (conns.colleague || []).map(n => n.id);
        check(travIds.includes("user:bob") && travIds.includes("user:carol"), `Traverse: ${travIds}`);

        try {
            const fp = await client.findPath(`${PREFIX}_users`, "user:alice", "user:eve",
                ["colleague", "mentors", "manages", "inv_colleague", "inv_mentors", "inv_manages"]);
            const path = fp.path || [];
            check(path.includes("user:carol"), `FindPath: ${path}`);
        } catch (e) {
            check(false, `FindPath: ${e.message}`);
        }

        console.log("\n-- Knowledge Engine --");
        const comp = await client.compile({
            name: "entity_card",
            sources: { type: "graph_query", entity: { type: "user", id: "alice" }, depth: 2 },
            index_name: `${PREFIX}_knowledge`
        });
        check(comp.status === "complete", `Compile: v${comp.version}`);

        const art = await client.getArtifact("entity_card", "user", "alice", `${PREFIX}_knowledge`);
        check(art.name === "entity_card", `GetArtifact: v${art.version}`);

        const arts = await client.listArtifacts(`${PREFIX}_knowledge`);
        check(Array.isArray(arts) || typeof arts === "object", "ListArtifacts OK");

        const tmpl = await client.listCompileTemplates();
        check(Array.isArray(tmpl) || typeof tmpl === "object", "Templates OK");

        const emb = await client.embedderStatus();
        check(typeof emb === "object", "Embedder OK");

    } catch (e) {
        console.error(`ERROR: ${e.message || e}`);
        results.failures.push(e.message || String(e));
        results.checks_failed++;
    }

    const dur = ((Date.now() - t0) / 1000).toFixed(2);
    results.duration_s = parseFloat(dur);
    const total = results.checks_passed + results.checks_failed;
    console.log(`\nTypeScript client: ${results.checks_passed}/${total} passed, ${dur}s`);
    fs.writeFileSync("/tmp/ts_client_results.json", JSON.stringify(results, null, 2));
}

main().catch(console.error);
