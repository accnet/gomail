/* ============================================
   GoMail — Teams Management (Standalone)
   ============================================ */

const state = {
  teams: [],
  selectedTeamID: null,
  selectedTeam: null
};

const token = () => localStorage.getItem("access_token") || "";
const api = (url, opts = {}) => {
  const headers = { "Content-Type": "application/json", ...opts.headers };
  if (token()) headers["Authorization"] = "Bearer " + token();
  const teamID = localStorage.getItem("active_team_id");
  if (teamID) headers["X-Team-Id"] = teamID;
  return fetch(url, { ...opts, headers }).then(async r => {
    if (!r.ok) {
      let body = { message: "Request failed" };
      try { body = await r.json(); } catch (_) { const t = await r.text().catch(() => ""); if (t) body.message = t; }
      throw new Error(body.message || "Request failed");
    }
    return r.json();
  });
};

// ── DOM Helpers ────────────────────────────────────────────────────────────
const root = document.getElementById("teams-root");
const modalOverlay = document.getElementById("modal-overlay");
const modalTitle = document.getElementById("modal-title");
const modalBody = document.getElementById("modal-body");
const modalClose = document.getElementById("modal-close");

function openModal(title, bodyHTML) {
  modalTitle.textContent = title;
  modalBody.innerHTML = bodyHTML;
  modalOverlay.classList.remove("hidden");
}
function closeModal() {
  modalOverlay.classList.add("hidden");
}
modalClose.onclick = closeModal;
modalOverlay.addEventListener("click", (e) => {
  if (e.target === modalOverlay) closeModal();
});

// ── Helpers ────────────────────────────────────────────────────────────────
function escHtml(s) {
  const d = document.createElement("div");
  d.textContent = s || "";
  return d.innerHTML;
}
function escapeHTML(str) {
  if (!str) return "";
  return str.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");
}
function parseScopes(permissions) {
  if (!permissions) return [];
  try {
    const scopes = typeof permissions === "string" ? JSON.parse(permissions) : permissions;
    return Array.isArray(scopes) ? scopes : [];
  } catch { return []; }
}

// ── Load & Render Teams ────────────────────────────────────────────────────
async function loadTeams() {
  try {
    state.teams = await api("/api/teams") || [];
  } catch (err) {
    state.teams = [];
  }
  renderTeamsList();
}

function renderTeamsList() {
  state.selectedTeamID = null;
  state.selectedTeam = null;

  if (state.teams.length === 0) {
    root.innerHTML = `
      <div class="page-header"><h1>Teams</h1></div>
      <div class="teams-empty">
        <div class="teams-empty-icon">
          <svg width="32" height="32" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><path d="M8 14s1.5 2 4 2 4-2 4-2"/><line x1="9" y1="9" x2="9.01" y2="9"/><line x1="15" y1="9" x2="15.01" y2="9"/></svg>
        </div>
        <p class="teams-empty-title">No teams yet</p>
        <p class="teams-empty-desc">Teams let you collaborate with others by sharing domains, API keys, and websites. Create your first team to get started.</p>
        <button id="teams-create-btn-empty" class="btn btn-primary">+ Create Your First Team</button>
      </div>
    `;
    document.getElementById("teams-create-btn-empty").addEventListener("click", showCreateTeamModal);
    return;
  }

  const totalMembers = state.teams.reduce((s, t) => s + (t.member_count || 0), 0);
  const ownedTeams = state.teams.filter(t => t.role === 'owner').length;
  const activeTeamID = localStorage.getItem("active_team_id") || "";

  root.innerHTML = `
    <div class="page-header">
      <h1>Teams</h1>
      <button id="teams-create-btn" class="btn btn-primary">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round"><line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/></svg>
        New Team
      </button>
    </div>

    <div class="team-summary">
      <div class="team-summary-item">
        <div class="team-summary-icon members">
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M16 21v-2a4 4 0 00-4-4H6a4 4 0 00-4 4v2"/><circle cx="9" cy="7" r="4"/></svg>
        </div>
        <div class="team-summary-info">
          <span class="team-summary-value">${state.teams.length}</span>
          <span class="team-summary-label">Teams</span>
        </div>
      </div>
      <div class="team-summary-item">
        <div class="team-summary-icon members">
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M17 21v-2a4 4 0 00-4-4H5a4 4 0 00-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M23 21v-2a4 4 0 00-3-3.87"/><path d="M16 3.13a4 4 0 010 7.75"/></svg>
        </div>
        <div class="team-summary-info">
          <span class="team-summary-value">${totalMembers}</span>
          <span class="team-summary-label">Total Members</span>
        </div>
      </div>
      <div class="team-summary-item">
        <div class="team-summary-icon admins">
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="11" width="18" height="11" rx="2" ry="2"/><path d="M7 11V7a5 5 0 0110 0v4"/></svg>
        </div>
        <div class="team-summary-info">
          <span class="team-summary-value">${ownedTeams}</span>
          <span class="team-summary-label">Owned</span>
        </div>
      </div>
    </div>

    <div class="card-list-header"><h3>All Teams</h3></div>
    <div class="card-list">
      ${state.teams.map(t => {
        const initials = (t.name || '?').substring(0, 2).toUpperCase();
        const isActive = t.id === activeTeamID;
        return `
          <div class="team-card">
            <div class="team-card-left">
              <div class="team-card-icon ${t.role === 'owner' ? '' : 'personal'}">
                <span>${escHtml(initials)}</span>
              </div>
              <div class="team-card-info">
                <div class="team-card-name">${escHtml(t.name)}</div>
                <div class="team-card-meta">
                  <span class="member-role-badge ${t.role}">${t.role}</span>
                  <span class="team-card-meta-item">
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M17 21v-2a4 4 0 00-4-4H5a4 4 0 00-4 4v2"/><circle cx="9" cy="7" r="4"/></svg>
                    ${t.member_count} member${t.member_count !== 1 ? 's' : ''}
                  </span>
                  ${isActive ? '<span class="team-card-status">Active</span>' : ''}
                </div>
              </div>
            </div>
            <div class="team-card-actions">
              ${!isActive ? `<button class="btn btn-secondary btn-sm switch-team-btn" data-id="${t.id}">Switch</button>` : ''}
              <button class="btn btn-primary btn-sm manage-team-btn" data-id="${t.id}">Manage</button>
            </div>
          </div>
        `;
      }).join("")}
    </div>
  `;

  document.getElementById("teams-create-btn").addEventListener("click", showCreateTeamModal);

  root.querySelectorAll(".switch-team-btn").forEach(btn => {
    btn.addEventListener("click", () => {
      localStorage.setItem("active_team_id", btn.dataset.id);
      loadTeams();
    });
  });

  root.querySelectorAll(".manage-team-btn").forEach(btn => {
    btn.addEventListener("click", () => {
      const team = state.teams.find(t => t.id === btn.dataset.id);
      if (!team) return;
      renderTeamDetail(btn.dataset.id, team);
    });
  });
}

// ── Team Detail View ───────────────────────────────────────────────────────
function renderTeamDetail(teamID, team) {
  state.selectedTeamID = teamID;
  state.selectedTeam = team;
  const isOwner = team.role === 'owner';
  const isAdmin = team.role === 'owner' || team.role === 'admin';

  root.innerHTML = `
    <div class="team-detail">
      <button class="team-detail-back" id="back-to-teams">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="15 18 9 12 15 6"/></svg>
        Back to Teams
      </button>

      <div class="team-detail-header">
        <div class="team-detail-header-info">
          <h2>${escHtml(team.name)}</h2>
          <div class="team-detail-header-meta">
            <span class="member-role-badge ${team.role}">${team.role}</span>
            <span style="font-size:13px;color:var(--color-text-tertiary)">${team.member_count} member${team.member_count !== 1 ? 's' : ''}</span>
          </div>
        </div>
        ${isOwner ? `
        <div class="team-detail-header-actions">
          <button id="rename-team-btn" class="btn btn-secondary btn-sm">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M11 4H4a2 2 0 00-2 2v14a2 2 0 002 2h14a2 2 0 002-2v-7"/><path d="M18.5 2.5a2.121 2.121 0 013 3L12 15l-4 1 1-4 9.5-9.5z"/></svg>
            Rename
          </button>
          <button id="delete-team-btn" class="btn btn-danger btn-sm">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="3 6 5 6 21 6"/><path d="M19 6v14a2 2 0 01-2 2H7a2 2 0 01-2-2V6m3 0V4a2 2 0 012-2h4a2 2 0 012 2v2"/></svg>
            Delete
          </button>
        </div>
        ` : ''}
      </div>

      <div class="team-section">
        <div class="team-section-header">
          <h3>Members</h3>
          ${isAdmin ? `<button id="invite-member-btn" class="btn btn-primary btn-sm">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round"><line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/></svg>
            Invite
          </button>` : ''}
        </div>
        <div id="members-container" class="member-list">
          <div class="member-row"><span style="color:var(--color-text-tertiary)">Loading members...</span></div>
        </div>
      </div>

      ${isAdmin ? `
      <div class="team-section">
        <div class="team-section-header"><h3>Pending Invites</h3></div>
        <div id="invites-container" class="member-list">
          <div class="invite-row"><span style="color:var(--color-text-tertiary)">Loading invites...</span></div>
        </div>
      </div>
      ` : ''}
    </div>
  `;

  document.getElementById("back-to-teams").addEventListener("click", loadTeams);
  if (isOwner) {
    document.getElementById("rename-team-btn").addEventListener("click", () => showRenameTeamModal(teamID, team));
    document.getElementById("delete-team-btn").addEventListener("click", () => showDeleteTeamModal(teamID, team));
  }
  const inviteBtn = document.getElementById("invite-member-btn");
  if (inviteBtn) inviteBtn.addEventListener("click", () => showInviteModal(teamID, team));

  loadMembers(teamID, team);
  if (isAdmin) loadInvites(teamID, team);
}

// ── Load Members ───────────────────────────────────────────────────────────
async function loadMembers(teamID, team) {
  const container = document.getElementById("members-container");
  if (!container) return;
  const isAdmin = team.role === 'owner' || team.role === 'admin';

  try {
    const members = await api(`/api/teams/${teamID}/members`);
    if (!members || members.length === 0) {
      container.innerHTML = '<div class="member-row"><span style="color:var(--color-text-tertiary)">No members found</span></div>';
      return;
    }

    container.innerHTML = members.map(m => {
      const initials = (m.user_name || m.user_email || '?').substring(0, 2).toUpperCase();
      const scopes = parseScopes(m.permissions);
      return `
        <div class="member-row">
          <div class="member-avatar ${m.role}">${escHtml(initials)}</div>
          <div class="member-info">
            <span class="member-name">${escHtml(m.user_name || m.user_email)}</span>
            <span class="member-email">${escHtml(m.user_email)}</span>
          </div>
          <span class="member-role-badge ${m.role}">${m.role}</span>
          ${m.role !== 'owner' ? `
          <div class="member-scopes">
            ${scopes.length === 0
              ? '<span class="scope-tag scope-tag-all">All scopes</span>'
              : scopes.map(s => `<span class="scope-tag">${escHtml(s)}</span>`).join('')}
          </div>
          ` : '<span style="font-size:12px;color:var(--color-text-tertiary)">Full access</span>'}
          ${isAdmin && m.role !== 'owner' ? `
          <div class="member-actions">
            <button class="btn btn-secondary btn-xs edit-member-btn" data-user="${m.user_id}" data-role="${m.role}" data-scopes="${escHtml(JSON.stringify(scopes))}">Edit</button>
            <button class="btn btn-danger btn-xs remove-member-btn" data-user="${m.user_id}" data-name="${escHtml(m.user_name || m.user_email)}">Remove</button>
          </div>
          ` : ''}
        </div>
      `;
    }).join("");

    container.querySelectorAll(".edit-member-btn").forEach(btn => {
      btn.addEventListener("click", () => {
        const scopes = btn.dataset.scopes ? JSON.parse(btn.dataset.scopes) : [];
        showEditMemberModal(teamID, team, btn.dataset.user, btn.dataset.role, scopes);
      });
    });
    container.querySelectorAll(".remove-member-btn").forEach(btn => {
      btn.addEventListener("click", () => {
        showConfirmModal("Remove Member",
          `Are you sure you want to remove <strong>${escHtml(btn.dataset.name)}</strong> from the team?`,
          "Remove", "btn-danger",
          () => api(`/api/teams/${teamID}/members/${btn.dataset.user}`, { method: "DELETE" }).then(() => renderTeamDetail(teamID, team))
        );
      });
    });
  } catch (err) {
    container.innerHTML = `<div class="member-row"><span style="color:var(--color-danger)">Failed: ${escapeHTML(err.message)}</span></div>`;
  }
}

// ── Load Invites ───────────────────────────────────────────────────────────
async function loadInvites(teamID, team) {
  const container = document.getElementById("invites-container");
  if (!container) return;

  try {
    const invites = await api(`/api/teams/${teamID}/invites`);
    if (!invites || invites.length === 0) {
      container.innerHTML = '<div class="invite-row"><span style="color:var(--color-text-tertiary)">No pending invites</span></div>';
      return;
    }

    container.innerHTML = invites.map(inv => {
      const expDate = new Date(inv.expires_at);
      const isExpiring = expDate - new Date() < 24 * 60 * 60 * 1000;
      return `
        <div class="invite-row">
          <div class="invite-icon">
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M4 4h16c1.1 0 2 .9 2 2v12c0 1.1-.9 2-2 2H4c-1.1 0-2-.9-2-2V6c0-1.1.9-2 2-2z"/><polyline points="22,6 12,13 2,6"/></svg>
          </div>
          <div class="invite-info">
            <span class="invite-email">${escHtml(inv.email)}</span>
            <span class="invite-meta">Role: ${inv.role}</span>
          </div>
          <span class="invite-expiry ${isExpiring ? 'expiring' : ''}">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><polyline points="12 6 12 12 16 14"/></svg>
            ${isExpiring ? 'Expires soon: ' : 'Expires: '}${expDate.toLocaleDateString()}
          </span>
          <div class="invite-actions">
            <button class="btn btn-danger btn-xs cancel-invite-btn" data-id="${inv.id}" data-email="${escHtml(inv.email)}">Cancel</button>
          </div>
        </div>
      `;
    }).join("");

    container.querySelectorAll(".cancel-invite-btn").forEach(btn => {
      btn.addEventListener("click", () => {
        showConfirmModal("Cancel Invite",
          `Cancel the invitation for <strong>${btn.dataset.email}</strong>?`,
          "Cancel Invite", "btn-danger",
          () => api(`/api/teams/${teamID}/invites/${btn.dataset.id}`, { method: "DELETE" }).then(() => renderTeamDetail(teamID, team))
        );
      });
    });
  } catch (err) {
    container.innerHTML = `<div class="invite-row"><span style="color:var(--color-danger)">Failed: ${escapeHTML(err.message)}</span></div>`;
  }
}

// ── Modals ─────────────────────────────────────────────────────────────────

function showCreateTeamModal() {
  openModal("Create Team", `
    <div class="modal-field">
      <label>Team Name</label>
      <input id="modal-team-name" type="text" placeholder="e.g. Engineering, Marketing..." autofocus />
      <span class="modal-field-hint">Give your team a clear, descriptive name</span>
    </div>
    <div class="modal-actions">
      <button class="btn btn-ghost modal-close-btn">Cancel</button>
      <button id="modal-create-submit" class="btn btn-primary">Create Team</button>
    </div>
  `);

  const input = document.getElementById("modal-team-name");
  input.addEventListener("keydown", (e) => { if (e.key === "Enter") submit(); });
  document.getElementById("modal-create-submit").addEventListener("click", submit);
  document.querySelector(".modal-close-btn").addEventListener("click", closeModal);

  function submit() {
    const name = input.value.trim();
    if (!name) { input.focus(); return; }
    const btn = document.getElementById("modal-create-submit");
    btn.disabled = true; btn.textContent = "Creating...";
    api("/api/teams", { method: "POST", body: JSON.stringify({ name }) })
      .then(() => { closeModal(); loadTeams(); })
      .catch(err => { alert(err.message); btn.disabled = false; btn.textContent = "Create Team"; });
  }
}

function showRenameTeamModal(teamID, team) {
  openModal("Rename Team", `
    <div class="modal-field">
      <label>Team Name</label>
      <input id="modal-rename-input" type="text" value="${escHtml(team.name)}" autofocus />
    </div>
    <div class="modal-actions">
      <button class="btn btn-ghost modal-close-btn">Cancel</button>
      <button id="modal-rename-submit" class="btn btn-primary">Save Changes</button>
    </div>
  `);

  const input = document.getElementById("modal-rename-input");
  input.addEventListener("keydown", (e) => { if (e.key === "Enter") submit(); });
  document.getElementById("modal-rename-submit").addEventListener("click", submit);
  document.querySelector(".modal-close-btn").addEventListener("click", closeModal);

  function submit() {
    const name = input.value.trim();
    if (!name) return;
    const btn = document.getElementById("modal-rename-submit");
    btn.disabled = true;
    api(`/api/teams/${teamID}`, { method: "PATCH", body: JSON.stringify({ name }) })
      .then(() => { closeModal(); loadTeams(); })
      .catch(err => { alert(err.message); btn.disabled = false; });
  }
}

function showDeleteTeamModal(teamID, team) {
  openModal("Delete Team", `
    <div class="modal-warning">
      <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M10.29 3.86L1.82 18a2 2 0 001.71 3h16.94a2 2 0 001.71-3L13.71 3.86a2 2 0 00-3.42 0z"/><line x1="12" y1="9" x2="12" y2="13"/><line x1="12" y1="17" x2="12.01" y2="17"/></svg>
      <span>This will permanently delete <strong>${escHtml(team.name)}</strong> and remove all members. Resources (domains, API keys, websites) will be kept for audit purposes.</span>
    </div>
    <div class="modal-field">
      <label>Type the team name to confirm: <strong>${escHtml(team.name)}</strong></label>
      <input id="modal-delete-confirm" type="text" placeholder="${escHtml(team.name)}" autofocus />
    </div>
    <div class="modal-actions">
      <button class="btn btn-ghost modal-close-btn">Cancel</button>
      <button id="modal-delete-submit" class="btn btn-danger" disabled>Delete Team</button>
    </div>
  `);

  const input = document.getElementById("modal-delete-confirm");
  const btn = document.getElementById("modal-delete-submit");
  input.addEventListener("input", () => { btn.disabled = input.value.trim() !== team.name; });
  input.addEventListener("keydown", (e) => { if (e.key === "Enter" && !btn.disabled) submit(); });
  btn.addEventListener("click", submit);
  document.querySelector(".modal-close-btn").addEventListener("click", closeModal);

  function submit() {
    if (input.value.trim() !== team.name) return;
    btn.disabled = true; btn.textContent = "Deleting...";
    api(`/api/teams/${teamID}`, { method: "DELETE" })
      .then(() => { closeModal(); localStorage.removeItem("active_team_id"); loadTeams(); })
      .catch(err => { alert(err.message); btn.disabled = false; btn.textContent = "Delete Team"; });
  }
}

function showInviteModal(teamID, team) {
  const scopeOptions = ["email:access", "email:manage", "apikey:read", "apikey:create", "apikey:manage", "website:read", "website:deploy", "website:manage", "domain:manage", "member:manage"];
  const selectedScopes = new Set(["email:access", "apikey:read", "website:read"]);

  openModal("Invite Member", `
    <div class="modal-field">
      <label>Email Address</label>
      <input id="modal-invite-email" type="email" placeholder="colleague@example.com" autofocus />
    </div>
    <div class="modal-field">
      <label>Permission Scopes</label>
      <div class="scope-list">
        ${scopeOptions.map(s => `
          <div class="scope-item ${selectedScopes.has(s) ? 'selected' : ''}" data-scope="${s}">
            <div class="scope-checkbox"></div>
            <span>${s}</span>
          </div>
        `).join("")}
      </div>
      <span class="modal-field-hint">Select permissions for this member invite.</span>
    </div>
    <div id="modal-invite-result"></div>
    <div class="modal-actions">
      <button class="btn btn-ghost modal-close-btn">Cancel</button>
      <button id="modal-invite-submit" class="btn btn-primary">Send Invite</button>
    </div>
  `);

  document.querySelectorAll(".scope-item").forEach(item => {
    item.addEventListener("click", () => {
      item.classList.toggle("selected");
      const scope = item.dataset.scope;
      if (item.classList.contains("selected")) selectedScopes.add(scope);
      else selectedScopes.delete(scope);
    });
  });

  const emailInput = document.getElementById("modal-invite-email");
  const submitBtn = document.getElementById("modal-invite-submit");
  const resultDiv = document.getElementById("modal-invite-result");

  emailInput.addEventListener("keydown", (e) => { if (e.key === "Enter") submit(); });
  submitBtn.addEventListener("click", submit);
  document.querySelector(".modal-close-btn").addEventListener("click", closeModal);

  function submit() {
    const email = emailInput.value.trim();
    if (!email) { emailInput.focus(); return; }
    const scopes = [...selectedScopes];

    submitBtn.disabled = true; submitBtn.textContent = "Sending...";
    api(`/api/teams/${teamID}/invites`, {
      method: "POST",
      body: JSON.stringify({ email, role: "member", scopes }),
    }).then(result => {
      if (result.token) {
        const link = `${location.origin}/app/join.html?token=${result.token}`;
        resultDiv.innerHTML = `
          <div class="invite-token-box">
            <div class="invite-token-label">Invite Link Created</div>
            <div class="invite-token-field">
              <input type="text" value="${escHtml(link)}" readonly id="invite-link-input" />
              <button class="btn btn-primary btn-sm" id="copy-invite-link">Copy</button>
            </div>
          </div>
        `;
        document.getElementById("copy-invite-link").addEventListener("click", () => {
          navigator.clipboard.writeText(link).then(() => {
            document.getElementById("copy-invite-link").textContent = "Copied!";
          });
        });
        submitBtn.textContent = "Send Another"; submitBtn.disabled = false;
      }
      renderTeamDetail(teamID, team);
    }).catch(err => { alert(err.message); submitBtn.disabled = false; submitBtn.textContent = "Send Invite"; });
  }
}

function showEditMemberModal(teamID, team, userID, currentRole, currentScopes) {
  const scopeOptions = ["email:access", "email:manage", "apikey:read", "apikey:create", "apikey:manage", "website:read", "website:deploy", "website:manage", "domain:manage", "member:manage"];

  openModal("Edit Member", `
    <div class="modal-field">
      <label>Role</label>
      <div class="role-selector">
        <div class="role-option ${currentRole === 'member' ? 'selected' : ''}" data-role="member">
          <div class="role-option-radio"></div>
          <div><div style="font-weight:600">Member</div><div style="font-size:11px;color:var(--color-text-tertiary)">Scoped access</div></div>
        </div>
        <div class="role-option ${currentRole === 'admin' ? 'selected' : ''}" data-role="admin">
          <div class="role-option-radio"></div>
          <div><div style="font-weight:600">Admin</div><div style="font-size:11px;color:var(--color-text-tertiary)">Full management</div></div>
        </div>
      </div>
    </div>
    <div class="modal-field">
      <label>Permission Scopes</label>
      <div class="scope-list">
        ${scopeOptions.map(s => `
          <div class="scope-item ${currentScopes.includes(s) ? 'selected' : ''}" data-scope="${s}">
            <div class="scope-checkbox"></div>
            <span>${s}</span>
          </div>
        `).join("")}
      </div>
      <span class="modal-field-hint">Select specific permissions. Deselect all for default scopes.</span>
    </div>
    <div class="modal-actions">
      <button class="btn btn-ghost modal-close-btn">Cancel</button>
      <button id="modal-edit-submit" class="btn btn-primary">Save Changes</button>
    </div>
  `);

  let selectedRole = currentRole;
  const selectedScopes = new Set(currentScopes);

  document.querySelectorAll(".role-option").forEach(opt => {
    opt.addEventListener("click", () => {
      document.querySelectorAll(".role-option").forEach(o => o.classList.remove("selected"));
      opt.classList.add("selected");
      selectedRole = opt.dataset.role;
    });
  });
  document.querySelectorAll(".scope-item").forEach(item => {
    item.addEventListener("click", () => {
      item.classList.toggle("selected");
      const scope = item.dataset.scope;
      if (item.classList.contains("selected")) selectedScopes.add(scope);
      else selectedScopes.delete(scope);
    });
  });

  document.getElementById("modal-edit-submit").addEventListener("click", () => {
    const btn = document.getElementById("modal-edit-submit");
    btn.disabled = true; btn.textContent = "Saving...";
    api(`/api/teams/${teamID}/members/${userID}`, {
      method: "PATCH",
      body: JSON.stringify({ role: selectedRole, scopes: [...selectedScopes] }),
    }).then(() => { closeModal(); renderTeamDetail(teamID, team); })
      .catch(err => { alert(err.message); btn.disabled = false; btn.textContent = "Save Changes"; });
  });

  document.querySelector(".modal-close-btn").addEventListener("click", closeModal);
}

function showConfirmModal(title, message, confirmText, confirmClass, onConfirm) {
  openModal(title, `
    <p style="font-size:14px;color:var(--color-text-secondary);line-height:1.6;margin-bottom:8px">${message}</p>
    <div class="modal-actions">
      <button class="btn btn-ghost modal-close-btn">Cancel</button>
      <button id="modal-confirm-btn" class="btn ${confirmClass}">${confirmText}</button>
    </div>
  `);

  document.getElementById("modal-confirm-btn").addEventListener("click", () => {
    const btn = document.getElementById("modal-confirm-btn");
    btn.disabled = true;
    onConfirm().then(() => closeModal()).catch(err => {
      alert(err.message);
      btn.disabled = false;
    });
  });
  document.querySelector(".modal-close-btn").addEventListener("click", closeModal);
}

// ── Init ───────────────────────────────────────────────────────────────────
document.addEventListener("DOMContentLoaded", loadTeams);
