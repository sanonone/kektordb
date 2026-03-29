// graph.js — Graph visualization with vis.js
let graphNetwork = null;

function procNode(node, nodesMap, edgesArr, simplified, isMain) {
    if (!node) return;
    const m = node.metadata || {};
    const t = m.type || "memory";
    const isEnt = t === "entity";
    const isDoc = t === "document";
    const isReflect = t.includes("reflection") || t.includes("failure") || t.includes("evolution") || t.includes("insight");
    const isChunk = !isEnt && !isDoc && !isReflect;

    if (simplified && isChunk && !isMain) return;

    if (!nodesMap.has(node.id)) {
        let grp = 'context';
        if (isDoc) grp = 'parent';
        if (isEnt) grp = 'entity';
        if (isReflect) grp = m.status === 'unresolved' ? 'reflection' : 'insight';
        if (isMain) grp = 'main';
        nodesMap.set(node.id, {
            id: node.id,
            label: getLabel(m, isEnt ? node.id : "Node"),
            group: grp,
            title: m.content || node.id
        });
    }

    if (node.connections) {
        for (const [rel, ns] of Object.entries(node.connections)) {
            if (simplified && (rel === 'next' || rel === 'prev')) continue;
            if (!ns) continue;
            ns.forEach(function(rn) {
                const rm = rn.metadata || {};
                const rt = rm.type || "";
                const isRelEnt = rt === "entity";
                const relIsChunk = !isRelEnt && rt !== "document" && !rt.includes("reflection") && !rt.includes("failure");
                if (simplified && relIsChunk) return;

                if (!nodesMap.has(rn.id)) {
                    let grp = 'context';
                    if (rel === 'parent' || rt === 'document') grp = 'parent';
                    if (isRelEnt) grp = 'entity';
                    if (rt.includes('reflection')) grp = 'reflection';
                    nodesMap.set(rn.id, {
                        id: rn.id,
                        label: getLabel(rm, rn.id),
                        group: grp,
                        title: rm.content || rn.id
                    });
                }
                edgesArr.push({ from: node.id, to: rn.id, label: rel, arrows: 'to', font: { align: 'middle' } });
            });
        }
    }
}

var graphGroups = {
    main: { color: { background: '#3b82f6', border: '#2563eb' }, size: 25 },
    parent: { color: { background: '#f59e0b', border: '#d97706' }, size: 20 },
    context: { color: { background: '#475569', border: '#334155' }, size: 12 },
    entity: { color: { background: '#8b5cf6', border: '#7c3aed' }, size: 28, shape: 'diamond' },
    reflection: { color: { background: '#ef4444', border: '#dc2626' }, size: 22, shape: 'triangle' },
    insight: { color: { background: '#06b6d4', border: '#0891b2' }, size: 22, shape: 'triangle' }
};

var graphOptions = {
    nodes: { shape: 'dot', size: 16, font: { color: '#fff', size: 12, strokeWidth: 2, strokeColor: '#000' }, borderWidth: 2 },
    groups: graphGroups,
    edges: { font: { color: '#cbd5e1', size: 9, align: 'middle', background: 'rgba(15,23,42,.7)' }, color: { color: '#64748b' }, smooth: { type: 'continuous' } },
    physics: { stabilization: false, barnesHut: { gravitationalConstant: -3000, springConstant: 0.04, springLength: 120 } },
    layout: { randomSeed: 2 }
};

function renderSearchGraph(results) {
    var c = document.getElementById('graph-view');
    if (network) { network.destroy(); network = null; }
    var nodes = new Map();
    var edges = [];
    results.forEach(function(r) { procNode(r.node, nodes, edges, false, true); });
    if (nodes.size === 0) { c.innerHTML = '<div class="loading">No graph data.</div>'; return; }
    c.innerHTML = '';
    var data = { nodes: Array.from(nodes.values()), edges: edges };
    // Force height from parent so vis.js has known dimensions
    fitContainer(c);
    network = new vis.Network(c, data, graphOptions);
    network.fit();
}

function fitContainer(c) {
    var h = c.parentElement ? c.parentElement.clientHeight : 0;
    if (h > 0) {
        c.style.height = h + 'px';
    } else {
        // Fallback: use the main area height minus toolbar
        var main = document.getElementById('main');
        if (main) {
            var toolbar = c.parentElement.querySelector('.toolbar');
            var tbH = toolbar ? toolbar.offsetHeight : 0;
            c.style.height = (main.clientHeight - tbH - 50) + 'px';
        }
    }
}

async function exploreGraph() {
    if (!selectedIndex) return alert("Select an index first.");
    var lim = parseInt(document.getElementById('explore-limit').value) || 200;
    if (lim > 1000 && !confirm("Load " + lim + " nodes? May be slow.")) return;
    var c = document.getElementById('graph-canvas');
    c.innerHTML = '<div class="loading" style="padding-top:100px">Loading graph...</div>';
    if (graphNetwork) { graphNetwork.destroy(); graphNetwork = null; }
    try {
        var r = await fetch('/ui/explore', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ index_name: selectedIndex, limit: lim })
        });
        var d = await r.json();
        if (d.error) {
            c.innerHTML = '<div style="color:var(--danger);text-align:center;margin-top:60px">' + escapeHtml(d.error) + '</div>';
            return;
        }
        var res = (d.results || []).map(function(n) { return { node: n }; });
        c.innerHTML = "";
        var nodes = new Map();
        var edges = [];
        var hideChunks = document.getElementById('hide-chunks') && document.getElementById('hide-chunks').checked;
        res.forEach(function(r) { procNode(r.node, nodes, edges, hideChunks, false); });
        if (nodes.size === 0) { c.innerHTML = '<div class="loading">No nodes found.</div>'; return; }
        var gData = { nodes: Array.from(nodes.values()), edges: edges };
        fitContainer(c);
        graphNetwork = new vis.Network(c, gData, graphOptions);
        graphNetwork.fit();
    } catch (e) {
        c.innerHTML = '<div style="color:var(--danger);text-align:center">Error: ' + escapeHtml(e.message) + '</div>';
    }
}

function refreshGraph() {
    if (currentResults.length > 0) renderSearchGraph(currentResults);
}

function loadAllRelations() {
    if (selectedIndex) exploreGraph();
}

// Keep graphs responsive on window resize
window.addEventListener('resize', function() {
    if (network) { fitContainer(document.getElementById('graph-view')); network.fit(); }
    if (graphNetwork) { fitContainer(document.getElementById('graph-canvas')); graphNetwork.fit(); }
});
