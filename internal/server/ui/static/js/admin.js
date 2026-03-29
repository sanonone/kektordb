// admin.js — Index management, system operations, auto-links

async function loadAdminInfo() {
    if (!selectedIndex) return;
    try {
        const r = await fetch('/vector/indexes/' + selectedIndex);
        const info = await r.json();
        document.getElementById('admin-index-info').innerHTML =
            '<div style="font-size:.85rem">' +
            '<div><strong>Name:</strong> ' + escapeHtml(info.name) + '</div>' +
            '<div><strong>Vectors:</strong> ' + info.vector_count + '</div>' +
            '<div><strong>Metric:</strong> ' + escapeHtml(info.metric) + '</div>' +
            '<div><strong>Precision:</strong> ' + escapeHtml(info.precision) + '</div>' +
            '<div><strong>Dimension:</strong> ' + info.dimension + '</div>' +
            '<div><strong>M:</strong> ' + info.m + ' \u00B7 <strong>ef:</strong> ' + info.ef_construction + '</div>' +
            '<div><strong>Language:</strong> ' + escapeHtml(info.text_language || "none") + '</div>' +
            '<div style="margin-top:8px"><button class="btn btn-danger btn-sm" onclick="deleteIndex(\'' + selectedIndex + '\')">Delete Index</button></div></div>';
    } catch (e) {
        document.getElementById('admin-index-info').innerHTML = "<div style='color:var(--danger)'>Failed to load.</div>";
    }
}

async function createIndex() {
    const name = document.getElementById('create-name').value;
    if (!name) return alert("Name required.");
    try {
        await fetch('/vector/actions/create', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                index_name: name,
                metric: document.getElementById('create-metric').value,
                precision: document.getElementById('create-prec').value,
                text_language: document.getElementById('create-lang').value || ""
            })
        });
        loadIndexes();
    } catch (e) { alert("Failed: " + e.message); }
}

async function deleteIndex(name) {
    if (!confirm('Delete index "' + name + '"?')) return;
    try {
        await fetch('/vector/indexes/' + name, { method: 'DELETE' });
        selectedIndex = "";
        loadIndexes();
    } catch (e) { alert("Failed: " + e.message); }
}

async function systemSave() {
    try { await fetch('/system/save', { method: 'POST' }); alert("Snapshot saved."); }
    catch (e) { alert("Failed: " + e.message); }
}

async function systemAOFRewrite() {
    try { await fetch('/system/aof-rewrite', { method: 'POST' }); alert("AOF rewrite started."); }
    catch (e) { alert("Failed: " + e.message); }
}

async function loadAutoLinks() {
    if (!selectedIndex) return;
    try {
        const r = await fetch('/vector/indexes/' + selectedIndex + '/auto-links');
        const d = await r.json();
        const rules = d.rules || [];
        document.getElementById('autolinks-list').innerHTML = rules.length
            ? rules.map(function(rule) {
                return '<div style="font-size:.8rem;padding:4px 0"><code>' +
                    escapeHtml(rule.metadata_field) + '</code> \u2192 <code>' +
                    escapeHtml(rule.relation_type) + '</code></div>';
            }).join('')
            : '<span style="color:var(--muted)">No rules configured.</span>';
    } catch (e) { }
}

function showAutoLinksModal() {
    document.getElementById('modal-content').innerHTML =
        '<h3>Configure Auto-Links</h3>' +
        '<div class="form-group"><label>Metadata Field</label><input type="text" id="al-field" placeholder="project_id"></div>' +
        '<div class="form-group"><label>Relation Type</label><input type="text" id="al-rel" placeholder="belongs_to"></div>' +
        '<div class="modal-actions"><button class="btn btn-secondary" onclick="closeModal()">Cancel</button>' +
        '<button class="btn btn-primary" onclick="addAutoLink()">Add Rule</button></div>';
    document.getElementById('modal-overlay').classList.add('show');
}

async function addAutoLink() {
    const field = document.getElementById('al-field').value;
    const rel = document.getElementById('al-rel').value;
    if (!field || !rel) return alert("Both fields required.");
    try {
        await fetch('/vector/indexes/' + selectedIndex + '/auto-links', {
            method: 'PUT',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ rules: [{ metadata_field: field, relation_type: rel }] })
        });
        closeModal();
        loadAutoLinks();
    } catch (e) { alert("Failed: " + e.message); }
}
