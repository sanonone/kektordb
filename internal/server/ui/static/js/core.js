// core.js — Shared state, utilities, sidebar, tab switching, initialization
let selectedIndex = "";
let currentResults = [];

function escapeHtml(t) {
    if (!t) return "";
    return t.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");
}

function getLabel(meta, fallback) {
    if (!meta) return fallback;
    let l = meta.name || meta.filename || meta.source || fallback;
    if (l.includes('/')) l = l.split('/').pop();
    if (l.length > 20) l = l.substring(0, 20) + "...";
    return l;
}

function timeAgo(ts) {
    if (!ts) return "";
    const s = Math.floor((Date.now() - ts * 1000) / 1000);
    if (s < 60) return s + "s ago";
    if (s < 3600) return Math.floor(s / 60) + "m ago";
    if (s < 86400) return Math.floor(s / 3600) + "h ago";
    return Math.floor(s / 86400) + "d ago";
}

function nodeTypeIcon(t) {
    const m = {
        reflection: "\u{1F4A1}", consolidated_memory: "\u{1F9E0}",
        user_profile_insight: "\u{1F464}", failure_pattern: "\u274C",
        knowledge_evolution: "\u{1F4C8}", entity: "\u{1F537}",
        document: "\u{1F4C4}", working_memory: "\u{1F4DD}", agent_action: "\u26A1"
    };
    return m[t] || "\u25CF";
}

function confColor(c) {
    if (c >= 0.7) return "var(--success)";
    if (c >= 0.4) return "var(--warning)";
    return "var(--danger)";
}

function confClass(c) {
    if (c >= 0.7) return "badge-success";
    if (c >= 0.4) return "badge-warning";
    return "badge-danger";
}

// Tab switching — uses `this` instead of implicit `event` for cross-browser support
function switchTab(el, name) {
    document.querySelectorAll('.tab-content').forEach(t => t.classList.remove('active'));
    document.querySelectorAll('.tab-btn').forEach(b => b.classList.remove('active'));
    document.getElementById('tab-' + name).classList.add('active');
    el.classList.add('active');
    if (name === 'cognitive' && typeof startSSE === 'function') startSSE();
    if (name === 'admin' && selectedIndex && typeof loadAdminInfo === 'function') loadAdminInfo();
}

function closeModal() {
    document.getElementById('modal-overlay').classList.remove('show');
}

// Sidebar
async function loadIndexes() {
    try {
        const r = await fetch('/vector/indexes');
        const idx = await r.json();
        const list = document.getElementById('index-list');
        list.innerHTML = "";
        if (!idx || !idx.length) {
            list.innerHTML = "<div style='color:#64748b;padding:10px'>No indexes.</div>";
            return;
        }
        idx.forEach((x, i) => {
            const div = document.createElement('div');
            div.className = 'idx-item';
            div.innerHTML = `<span>${escapeHtml(x.name)}</span><span class="idx-badge">${x.vector_count}</span>`;
            div.onclick = function() {
                document.querySelectorAll('.idx-item').forEach(e => e.classList.remove('active'));
                div.classList.add('active');
                selectedIndex = x.name;
                if (typeof loadReflections === 'function') loadReflections('');
                if (typeof loadAdminInfo === 'function') loadAdminInfo();
                if (typeof loadAutoLinks === 'function') loadAutoLinks();
            };
            list.appendChild(div);
            if (i === 0) div.onclick();
        });
    } catch (e) {
        document.getElementById('index-list').innerHTML =
            "<div style='color:#ef4444;padding:10px'>Connection failed</div>";
    }
}

// Init
document.addEventListener('DOMContentLoaded', function() {
    loadIndexes();
    document.getElementById('modal-overlay').onclick = function(e) {
        if (e.target === this) closeModal();
    };
    document.getElementById('search-query').addEventListener('keypress', function(e) {
        if (e.key === 'Enter' && typeof performSearch === 'function') performSearch();
    });
});
