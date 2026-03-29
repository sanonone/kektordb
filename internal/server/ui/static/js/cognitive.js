// cognitive.js — Reflection feed, resolve, think, SSE
let eventSource = null;

async function loadReflections(status) {
    if (!selectedIndex) return;
    const url = status
        ? '/vector/indexes/' + selectedIndex + '/reflections?status=' + status
        : '/vector/indexes/' + selectedIndex + '/reflections';
    try {
        const r = await fetch(url);
        const d = await r.json();
        const refs = d.reflections || [];
        const list = document.getElementById('reflection-list');
        list.innerHTML = "";
        if (!refs.length) {
            list.innerHTML = "<div class='loading'>No reflections found.</div>";
            return;
        }
        refs.forEach(function(ref) {
            const m = ref.metadata || {};
            const type = m.type || "reflection";
            const conf = m.confidence || 0;
            const st = m.status || "";
            const ts = m._created_at || 0;
            const div = document.createElement('div');
            div.className = 'reflection-item';
            div.innerHTML = '<div>' + nodeTypeIcon(type) + ' ' + escapeHtml((m.content || "").substring(0, 60)) +
                '...</div><div class="ref-meta"><span class="badge ' + confClass(conf) + '">' +
                (conf * 100).toFixed(0) + '%</span> ' + escapeHtml(st) + ' \u00B7 ' + timeAgo(ts) + '</div>';
            div.onclick = function() {
                document.querySelectorAll('.reflection-item').forEach(function(e) { e.classList.remove('active'); });
                div.classList.add('active');
                showReflectionDetail(ref);
            };
            list.appendChild(div);
        });
    } catch (e) {
        document.getElementById('reflection-list').innerHTML = "<div class='loading'>Error loading.</div>";
    }
}

function showReflectionDetail(ref) {
    const d = document.getElementById('reflection-detail');
    const m = ref.metadata || {};
    const conf = m.confidence || 0;

    let actionBtn = "";
    if (m.status === 'unresolved') {
        actionBtn = '<div style="margin-top:16px"><button class="btn btn-primary btn-sm" onclick="resolveReflection(\'' + ref.id + '\')">Resolve</button></div>';
    }

    let extraFields = '';
    if (m.action_required) extraFields += '<div><strong>Action Required:</strong> <span class="badge badge-danger">YES</span></div>';
    if (m.suggested_resolution) extraFields += '<div style="margin-top:8px"><strong>Suggested:</strong> ' + escapeHtml(m.suggested_resolution) + '</div>';
    if (m.severity) extraFields += '<div><strong>Severity:</strong> <span class="badge badge-' + (m.severity === 'high' ? 'danger' : m.severity === 'medium' ? 'warning' : 'success') + '">' + m.severity.toUpperCase() + '</span></div>';
    if (m.root_cause) extraFields += '<div style="margin-top:8px"><strong>Root Cause:</strong> ' + escapeHtml(m.root_cause) + '</div>';
    if (m.competency_level) extraFields += '<div><strong>Competency:</strong> ' + escapeHtml(m.competency_level) + '</div>';
    if (m.timeline) extraFields += '<div style="margin-top:8px"><strong>Timeline:</strong><br>' + escapeHtml(m.timeline) + '</div>';
    if (m.pattern) extraFields += '<div><strong>Pattern:</strong> ' + escapeHtml(m.pattern) + '</div>';
    if (m.failure_count) extraFields += '<div><strong>Failures:</strong> ' + m.failure_count + '</div>';

    d.innerHTML = '<div class="card">' +
        '<div class="card-header"><span>' + nodeTypeIcon(m.type || "reflection") + ' ' + escapeHtml(m.type || "reflection") + '</span><span class="badge ' + confClass(conf) + '">' + (conf * 100).toFixed(0) + '%</span></div>' +
        '<div class="card-body" style="font-size:.9rem">' + escapeHtml(m.content || "") + '</div>' +
        '<div style="margin-top:12px;font-size:.8rem">' +
        '<div><strong>Status:</strong> <span class="badge badge-info">' + escapeHtml(m.status || "-") + '</span></div>' +
        extraFields +
        '<div style="margin-top:8px;font-size:.75rem;color:var(--muted)">ID: ' + escapeHtml(ref.id) + ' \u00B7 Created: ' + timeAgo(m._created_at) + '</div>' +
        '</div>' + actionBtn +
        '<div class="conf-bar"><div class="conf-fill" style="width:' + (conf * 100) + '%;background:' + confColor(conf) + '"></div></div>' +
        '</div>';
}

async function resolveReflection(id) {
    const res = prompt("Enter your resolution:");
    if (!res) return;
    try {
        await fetch('/vector/indexes/' + selectedIndex + '/reflections/' + id + '/resolve', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ resolution: res })
        });
        loadReflections('');
    } catch (e) { alert("Failed: " + e.message); }
}

async function triggerThink() {
    if (!selectedIndex) return;
    try {
        await fetch('/vector/indexes/' + selectedIndex + '/cognitive/think', { method: 'POST' });
        setTimeout(function() { loadReflections(''); }, 2000);
    } catch (e) { alert("Failed: " + e.message); }
}

function startSSE() {
    if (eventSource) eventSource.close();
    try {
        eventSource = new EventSource('/events/stream');
        const dot = document.getElementById('sse-dot');
        const status = document.getElementById('sse-status');
        if (dot) dot.style.display = 'inline-block';
        if (status) status.textContent = 'Live feed active';

        eventSource.addEventListener('vector.update', function() {
            if (document.getElementById('tab-cognitive') && document.getElementById('tab-cognitive').classList.contains('active')) {
                loadReflections('');
            }
        });

        eventSource.onerror = function() {
            if (dot) dot.style.display = 'none';
            if (status) status.textContent = 'SSE disconnected';
            setTimeout(function() { startSSE(); }, 5000);
        };
    } catch (e) {
        console.warn("SSE not available:", e);
    }
}
