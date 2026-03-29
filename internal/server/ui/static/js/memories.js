// memories.js — Search, list rendering, add memory
let network = null;

function switchView(v) {
    var lv = document.getElementById('list-view');
    var gv = document.getElementById('graph-view');
    if (v === 'list') {
        lv.style.display = 'block';
        lv.style.flex = '1';
        gv.style.display = 'none';
        gv.style.flex = 'none';
    } else {
        lv.style.display = 'none';
        lv.style.flex = 'none';
        gv.style.display = 'block';
        gv.style.flex = '1';
    }
    document.getElementById('btn-list').className = 'view-btn' + (v === 'list' ? ' active' : '');
    document.getElementById('btn-graph').className = 'view-btn' + (v === 'graph' ? ' active' : '');
    if (v === 'graph' && currentResults.length > 0) renderSearchGraph(currentResults);
}

async function performSearch() {
    if (!selectedIndex) return alert("Select an index first.");
    const q = document.getElementById('search-query').value;
    const k = parseInt(document.getElementById('search-k').value) || 10;
    const lv = document.getElementById('list-view');
    lv.innerHTML = "<div class='loading'>Searching...</div>";
    try {
        const r = await fetch('/ui/search', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                index_name: selectedIndex,
                query: q,
                k: k,
                include_relations: ["prev", "next", "parent", "child", "mentions", "related_to", "contradicts", "suggests_link", "focus_shifted"],
                hydrate: true
            })
        });
        const d = await r.json();
        if (d.error) {
            lv.innerHTML = '<div style="color:var(--danger);margin-top:40px;text-align:center">' + escapeHtml(d.error) + '</div>';
            return;
        }
        currentResults = d.results || [];
        renderList(currentResults);
        if (document.getElementById('graph-view').style.display === 'block') renderSearchGraph(currentResults);
    } catch (e) {
        lv.innerHTML = '<div style="color:var(--danger);text-align:center">Error: ' + escapeHtml(e.message) + '</div>';
    }
}

function renderList(results) {
    const area = document.getElementById('list-view');
    area.innerHTML = "";
    if (!results.length) {
        area.innerHTML = "<div class='loading'>No results.</div>";
        return;
    }
    results.forEach(function(r) {
        const n = r.node;
        const m = n.metadata || {};
        let content = m.content || m.text || "No text content";
        if (content.length > 500) content = content.substring(0, 500) + "...";
        const type = m.type || "memory";
        const sim = (r.score * 100).toFixed(1);

        let conns = "";
        if (n.connections) {
            conns += "<div style='margin-top:12px;padding-top:10px;border-top:1px dashed var(--border)'>";
            for (const [rel, nodes] of Object.entries(n.connections)) {
                if (!nodes || !nodes.length) continue;
                const badges = nodes.map(function(x) {
                    const sm = x.metadata || {};
                    let lb = sm.name || sm.filename || sm.content || x.id;
                    if (lb.length > 25) lb = lb.substring(0, 25) + "...";
                    return '<span class="conn-tag" title="' + escapeHtml(sm.content || x.id) + '">' + escapeHtml(lb) + '</span>';
                }).join("");
                conns += '<div class="conn-row"><div class="conn-label">' + rel + '</div><div class="conn-badges">' + badges + '</div></div>';
            }
            conns += "</div>";
        }

        const div = document.createElement('div');
        div.className = 'card';
        div.innerHTML = '<div class="card-header"><span>' + nodeTypeIcon(type) + ' <strong>' + escapeHtml(n.id) +
            '</strong></span><span class="score">' + sim + '%</span></div><div class="card-body">' +
            escapeHtml(content) + '</div>' + conns;
        area.appendChild(div);
    });
}

function showAddMemoryModal() {
    const content = document.getElementById('modal-content');
    content.innerHTML = '<h3>Add Memory</h3>' +
        '<div class="form-group"><label>ID</label><input type="text" id="add-id" placeholder="mem_1"></div>' +
        '<div class="form-group"><label>Content</label><textarea id="add-content" placeholder="Memory text..."></textarea></div>' +
        '<div class="form-group"><label>Tags (comma-separated)</label><input type="text" id="add-tags" placeholder="tag1, tag2"></div>' +
        '<div class="form-group"><label>Pinned</label><select id="add-pinned"><option value="false">No</option><option value="true">Yes</option></select></div>' +
        '<div class="modal-actions"><button class="btn btn-secondary" onclick="closeModal()">Cancel</button>' +
        '<button class="btn btn-primary" onclick="addMemory()">Add</button></div>';
    document.getElementById('modal-overlay').classList.add('show');
}

async function addMemory() {
    const id = document.getElementById('add-id').value;
    const content = document.getElementById('add-content').value;
    if (!id || !content) return alert("ID and content required.");
    const tags = document.getElementById('add-tags').value.split(',').map(t => t.trim()).filter(Boolean);
    const pinned = document.getElementById('add-pinned').value === 'true';
    try {
        await fetch('/vector/actions/add', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                index_name: selectedIndex,
                id: id,
                vector: [],
                metadata: { content: content, type: "memory", tags: tags.length ? tags : undefined, _pinned: pinned || undefined }
            })
        });
        closeModal();
        performSearch();
    } catch (e) { alert("Failed: " + e.message); }
}
