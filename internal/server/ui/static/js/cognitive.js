// cognitive.js — Reflection feed, detail panel, filters, resolve, think, SSE
let eventSource = null;
let cognitiveFilter = '';

function loadReflections(status) {
    if (!selectedIndex) return;
    cognitiveFilter = ''; // Reset client-side filter when using status filter
    document.querySelectorAll('.cog-filter').forEach(function(b) { b.classList.remove('btn-primary'); b.classList.add('btn-secondary'); });
    highlightStatusBtn(status);

    var url = status
        ? '/vector/indexes/' + selectedIndex + '/reflections?status=' + status
        : '/vector/indexes/' + selectedIndex + '/reflections';
    fetch(url).then(function(resp) { return resp.json(); }).then(function(d) {
        var refs = d.reflections || [];
        renderReflectionList(refs);
    }).catch(function() {
        document.getElementById('reflection-list').innerHTML = "<div class='loading'>Error loading.</div>";
    });
}

function setCognitiveFilter(f) {
    if (cognitiveFilter === f) {
        // Toggle off
        cognitiveFilter = '';
        loadReflections('');
        return;
    }
    cognitiveFilter = f;
    // Fetch all (no status filter) and apply client-side filter
    document.querySelectorAll('.status-btn').forEach(function(b) { b.classList.remove('btn-primary'); b.classList.add('btn-secondary'); });
    document.querySelectorAll('.cog-filter').forEach(function(b) {
        b.classList.remove('btn-primary'); b.classList.add('btn-secondary');
    });
    event.target.classList.remove('btn-secondary');
    event.target.classList.add('btn-primary');

    fetch('/vector/indexes/' + selectedIndex + '/reflections').then(function(resp) { return resp.json(); }).then(function(d) {
        var refs = d.reflections || [];
        refs = refs.filter(function(ref) {
            var m = ref.metadata || {};
            if (f === 'action_required') return m.action_required === true;
            if (f === 'failures') return m.type === 'failure_pattern';
            if (f === 'profiles') return m.type === 'user_profile_insight';
            if (f === 'evolution') return m.type === 'knowledge_evolution';
            return true;
        });
        renderReflectionList(refs);
    }).catch(function() {
        document.getElementById('reflection-list').innerHTML = "<div class='loading'>Error loading.</div>";
    });
}

function highlightStatusBtn(status) {
    document.querySelectorAll('.status-btn').forEach(function(b) {
        b.classList.remove('btn-primary');
        b.classList.add('btn-secondary');
    });
    document.querySelectorAll('.status-btn').forEach(function(b) {
        var s = b.getAttribute('data-status') || '';
        if (s === status) {
            b.classList.remove('btn-secondary');
            b.classList.add('btn-primary');
        }
    });
}

function renderReflectionList(refs) {
    var list = document.getElementById('reflection-list');
    list.innerHTML = "";
    if (!refs.length) {
        list.innerHTML = "<div class='loading'>No reflections found.</div>";
        return;
    }
    refs.forEach(function(ref) {
        var m = ref.metadata || {};
        var type = m.type || "reflection";
        var conf = m.confidence || 0;
        var st = m.status || "";
        var ts = m._created_at || 0;

        var indicators = '';
        if (m.action_required) indicators += '<span class="badge badge-danger" style="margin-left:4px">ACTION</span>';
        if (m.severity === 'high') indicators += '<span class="badge badge-danger" style="margin-left:4px">HIGH</span>';
        if (m.severity === 'medium') indicators += '<span class="badge badge-warning" style="margin-left:4px">MED</span>';
        if (m.detector_count) indicators += '<span class="badge badge-purple" style="margin-left:4px">' + m.detector_count + ' signals</span>';
        if (m.failure_count) indicators += '<span class="badge badge-warning" style="margin-left:4px">' + m.failure_count + ' fails</span>';
        if (m.source_memory_count) indicators += '<span class="badge badge-info" style="margin-left:4px">' + m.source_memory_count + ' src</span>';
        if (st === 'resolved') indicators += '<span class="badge badge-success" style="margin-left:4px">RESOLVED</span>';

        var div = document.createElement('div');
        div.className = 'reflection-item';
        div.innerHTML = '<div>' + nodeTypeIcon(type) + ' ' + escapeHtml((m.content || "").substring(0, 55)) +
            '...</div><div class="ref-meta"><span class="badge ' + confClass(conf) + '">' +
            (conf * 100).toFixed(0) + '%</span> ' + escapeHtml(st) + ' \u00B7 ' + timeAgo(ts) + indicators + '</div>';
        div.onclick = function() {
            document.querySelectorAll('.reflection-item').forEach(function(e) { e.classList.remove('active'); });
            div.classList.add('active');
            showReflectionDetail(ref);
        };
        list.appendChild(div);
    });
}

function showReflectionDetail(ref) {
    var d = document.getElementById('reflection-detail');
    var m = ref.metadata || {};
    var conf = m.confidence || 0;
    var type = m.type || "reflection";
    var st = m.status || "";

    var actionBtn = "";
    if (st === 'unresolved' || st === 'insight') {
        actionBtn = '<div style="margin-top:16px"><button class="btn btn-primary btn-sm" onclick="resolveReflection(\'' + ref.id + '\')">Resolve</button></div>';
    }

    // Status-specific display
    var statusHtml = '';
    if (st === 'resolved') {
        statusHtml = '<div style="background:rgba(16,185,129,.1);border:1px solid rgba(16,185,129,.3);border-radius:6px;padding:12px;margin-top:12px">' +
            '<div style="font-weight:700;color:var(--success)">\u2713 Resolved</div>';
        if (m.resolution) statusHtml += '<div style="margin-top:4px;font-size:.85rem">' + escapeHtml(m.resolution) + '</div>';
        if (m._updated_at) statusHtml += '<div style="margin-top:4px;font-size:.75rem;color:var(--muted)">Resolved: ' + timeAgo(m._updated_at) + '</div>';
        statusHtml += '</div>';
    }

    var extraFields = '';
    if (m.action_required) extraFields += '<div><strong>Action Required:</strong> <span class="badge badge-danger">YES</span></div>';
    if (m.suggested_resolution) extraFields += '<div style="margin-top:8px"><strong>Suggested:</strong> ' + escapeHtml(m.suggested_resolution) + '</div>';
    if (m.severity) extraFields += '<div><strong>Severity:</strong> <span class="badge badge-' + (m.severity === 'high' ? 'danger' : m.severity === 'medium' ? 'warning' : 'success') + '">' + m.severity.toUpperCase() + '</span></div>';
    if (m.root_cause) extraFields += '<div style="margin-top:8px"><strong>Root Cause:</strong> ' + escapeHtml(m.root_cause) + '</div>';
    if (m.pattern) extraFields += '<div><strong>Pattern:</strong> ' + escapeHtml(m.pattern) + '</div>';
    if (m.failure_count) extraFields += '<div><strong>Failures:</strong> ' + m.failure_count + '</div>';
    if (m.competency_level) extraFields += '<div><strong>Competency:</strong> ' + escapeHtml(m.competency_level) + '</div>';
    if (m.timeline) extraFields += '<div style="margin-top:8px"><strong>Timeline:</strong><br>' + escapeHtml(m.timeline) + '</div>';
    if (m.topic) extraFields += '<div><strong>Topic:</strong> ' + escapeHtml(m.topic) + '</div>';
    if (m.source_memory_count) extraFields += '<div><strong>Source Memories:</strong> ' + m.source_memory_count + '</div>';

    var prefsHtml = '';
    if (m.preferences && typeof m.preferences === 'object') {
        prefsHtml = '<div style="margin-top:8px"><strong>User Preferences:</strong><div style="padding-left:12px">';
        for (var k in m.preferences) {
            prefsHtml += '<div><code>' + escapeHtml(k) + '</code>: ' + escapeHtml(String(m.preferences[k])) + '</div>';
        }
        prefsHtml += '</div></div>';
    }

    var gapsHtml = '';
    if (m.knowledge_gaps && Array.isArray(m.knowledge_gaps)) {
        gapsHtml = '<div style="margin-top:8px"><strong>Knowledge Gaps:</strong><ul style="padding-left:20px;margin:4px 0">';
        m.knowledge_gaps.forEach(function(gap) { gapsHtml += '<li>' + escapeHtml(String(gap)) + '</li>'; });
        gapsHtml += '</ul></div>';
    }

    var detHtml = '';
    if (m.detector_types && Array.isArray(m.detector_types)) {
        detHtml = '<div style="margin-top:8px"><strong>Detectors:</strong> ';
        m.detector_types.forEach(function(dt) {
            detHtml += '<span class="badge badge-purple" style="margin-right:4px">' + escapeHtml(String(dt)) + '</span>';
        });
        detHtml += '</div>';
    }

    d.innerHTML = '<div class="card">' +
        '<div class="card-header"><span>' + nodeTypeIcon(type) + ' ' + escapeHtml(type) + '</span><span class="badge ' + confClass(conf) + '">' + (conf * 100).toFixed(0) + '%</span></div>' +
        '<div class="card-body" style="font-size:.9rem">' + escapeHtml(m.content || "") + '</div>' +
        statusHtml +
        '<div style="margin-top:12px;font-size:.8rem">' +
        '<div><strong>Status:</strong> <span class="badge badge-info">' + escapeHtml(st) + '</span></div>' +
        extraFields + prefsHtml + gapsHtml + detHtml +
        '<div style="margin-top:8px;font-size:.75rem;color:var(--muted)">ID: ' + escapeHtml(ref.id) + ' \u00B7 Created: ' + timeAgo(m._created_at) + '</div>' +
        '</div>' + actionBtn +
        '<div class="conf-bar"><div class="conf-fill" style="width:' + (conf * 100) + '%;background:' + confColor(conf) + '"></div></div>' +
        '</div>';
}

async function resolveReflection(id) {
    var res = prompt("Enter your resolution:");
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
        setTimeout(function() { loadReflections(''); }, 3000);
    } catch (e) { alert("Failed: " + e.message); }
}

function startSSE() {
    if (eventSource) eventSource.close();
    try {
        eventSource = new EventSource('/events/stream');
        var dot = document.getElementById('sse-dot');
        var status = document.getElementById('sse-status');
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
