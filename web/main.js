/* ============================================
   GoMail — Main Application
   ============================================ */

let accessToken = localStorage.getItem("access_token") || "";
let refreshToken = localStorage.getItem("refresh_token") || "";
let currentView = "dashboard";
let currentUser = null;
let eventSource = null;

const state = {
  domains: [],
  inboxes: [],
  emails: [],
  emailPagination: null,
  users: [],
  dashboard: null,
  selectedEmailID: null,
  selectedInboxID: null,
  emailUnreadOnly: false,
  emailPage: 1
};

// --- DOM References ---
const $ = (id) => document.getElementById(id);

const els = {
  appShell: $("app-shell"),
  pageContent: $("page-content"),
  breadcrumbSection: $("breadcrumb-section"),
  breadcrumbTitle: $("breadcrumb-title"),
  sidebarMx: $("sidebar-mx"),
  sidebarCollapse: $("sidebar-collapse"),
  sidebarToggleMobile: $("sidebar-toggle-mobile"),
  sidebar: $("sidebar"),
  themeToggle: $("theme-toggle"),
  accountTrigger: $("account-trigger"),
  accountDropdown: $("account-dropdown"),
  accountAvatar: $("account-avatar"),
  accountName: $("account-name"),
  dropdownEmail: $("dropdown-email"),
  dropdownSettings: $("dropdown-settings"),
  dropdownChangepw: $("dropdown-changepw"),
  dropdownLogout: $("dropdown-logout"),
  modalOverlay: $("modal-overlay"),
  modalTitle: $("modal-title"),
  modalBody: $("modal-body"),
  modalClose: $("modal-close")
};

const viewMeta = {
  dashboard: { section: "Overview", title: "Dashboard" },
  email: { section: "Messaging", title: "Email" },
  domains: { section: "Infrastructure", title: "Domains" },
  users: { section: "Admin", title: "Users" },
  settings: { section: "Account", title: "Settings" }
};

const defaultView = "dashboard";

// --- API Helper ---
const api = async (path, options = {}) => {
  const { refresh, ...fetchOptions } = options;
  const res = await fetch(`/api${path}`, {
    ...fetchOptions,
    headers: {
      "Content-Type": "application/json",
      ...(accessToken ? { Authorization: `Bearer ${accessToken}` } : {}),
      ...(fetchOptions.headers || {})
    }
  });
  if (res.status === 401 && refresh !== false && refreshToken) {
    const refreshed = await refreshSession();
    if (refreshed) {
      return api(path, { ...options, refresh: false });
    }
  }
  if (!res.ok) {
    let body = { message: "Request failed" };
    try {
      body = await res.json();
    } catch (_) {
      const text = await res.text().catch(() => "");
      if (text) body.message = text;
    }
    throw new Error(body.message || "Request failed");
  }
  return res.json();
};

async function refreshSession() {
  try {
    const res = await fetch("/api/auth/refresh", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ refresh_token: refreshToken })
    });
    if (!res.ok) return false;
    const data = await res.json();
    accessToken = data.access_token;
    refreshToken = data.refresh_token;
    localStorage.setItem("access_token", accessToken);
    localStorage.setItem("refresh_token", refreshToken);
    eventSource?.close();
    eventSource = null;
    connectEvents();
    return true;
  } catch (_) {
    return false;
  }
}

// --- Theme ---
function setTheme() {
  const theme = localStorage.getItem("theme") || "light";
  document.documentElement.setAttribute("data-theme", theme);
}

function toggleTheme() {
  const current = document.documentElement.getAttribute("data-theme") || "light";
  const next = current === "dark" ? "light" : "dark";
  localStorage.setItem("theme", next);
  setTheme();
}

// --- Navigation ---
function setView(view) {
  currentView = view;
  const meta = viewMeta[view];
  els.breadcrumbSection.textContent = meta.section;
  els.breadcrumbTitle.textContent = meta.title;
  document.querySelectorAll(".nav-item").forEach((btn) => {
    btn.classList.toggle("active", btn.dataset.view === view);
  });
  // Close mobile sidebar
  els.sidebar.classList.remove("open");
}

function normalizeView(view) {
  return viewMeta[view] ? view : defaultView;
}

function viewFromURL() {
  const hashView = window.location.hash.replace(/^#\/?/, "").trim();
  return normalizeView(hashView || defaultView);
}

function setViewURL(view, replace = false) {
  const nextHash = `#/${normalizeView(view)}`;
  if (window.location.hash === nextHash) return false;
  if (replace) {
    history.replaceState(null, "", nextHash);
  } else {
    window.location.hash = nextHash;
  }
  return true;
}

async function renderView(view, options = {}) {
  const nextView = normalizeView(view);
  if (options.updateURL !== false) {
    const changed = setViewURL(nextView, options.replaceURL);
    if (changed && !options.replaceURL) return;
  }
  if (nextView === "dashboard") await renderDashboard();
  else if (nextView === "domains") await renderDomains();
  else if (nextView === "email") await renderEmail();
  else if (nextView === "users") await renderUsers();
  else if (nextView === "settings") renderSettings();
}

// --- Utilities ---
function bytes(n) {
  if (!n) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let value = n;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit += 1;
  }
  return `${value.toFixed(value >= 10 || unit === 0 ? 0 : 1)} ${units[unit]}`;
}

function gb(bytesValue) {
  return Math.round((Number(bytesValue || 0) / 1024 / 1024 / 1024) * 10) / 10;
}

function storagePercent(user) {
  if (!user.max_storage_bytes) return 0;
  return Math.min(100, Math.round((Number(user.storage_used_bytes || 0) / Number(user.max_storage_bytes)) * 100));
}

function emailItems(payload) {
  return Array.isArray(payload) ? payload : (payload?.items || []);
}

function relative(iso) {
  if (!iso) return "Never";
  const date = new Date(iso);
  const now = new Date();
  const diff = now - date;
  const mins = Math.floor(diff / 60000);
  const hours = Math.floor(diff / 3600000);
  const days = Math.floor(diff / 86400000);

  if (mins < 1) return "Just now";
  if (mins < 60) return `${mins}m ago`;
  if (hours < 24) return `${hours}h ago`;
  if (days < 7) return `${days}d ago`;
  return `${date.toLocaleDateString()} ${date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}`;
}

function badge(status) {
  const cls = {
    verified: "badge-verified",
    verified_warning: "badge-verified_warning",
    pending: "badge-pending",
    failed: "badge-failed",
    flagged: "badge-flagged",
    infected: "badge-infected",
    clean: "badge-clean",
    active: "badge-clean",
    disabled: "badge-failed",
    "Super Admin": "badge-verified",
    Admin: "badge-pending",
    User: "badge-default"
  };
  return `<span class="badge ${cls[status] || "badge-default"}">${status}</span>`;
}

function roleLabel(user) {
  if (user?.is_super_admin) return "Super Admin";
  if (user?.is_admin) return "Admin";
  return "User";
}

function initials(value) {
  const source = (value || "?").replace(/<[^>]+>/g, "").trim();
  const parts = source.split(/\s+/).filter(Boolean).slice(0, 2);
  if (!parts.length) return "?";
  return parts.map((part) => part[0].toUpperCase()).join("");
}

function escapeHTML(value) {
  if (!value) return "";
  return String(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#039;");
}

function flash(node, message, ok) {
  node.textContent = message;
  node.className = `form-message ${ok ? "success" : "error"}`;
  node.classList.remove("hidden");
}

// --- Modal ---
function openModal(title, bodyHTML) {
  els.modalTitle.textContent = title;
  els.modalBody.innerHTML = bodyHTML;
  els.modalOverlay.classList.remove("hidden");
}

function closeModal() {
  els.modalOverlay.classList.add("hidden");
}

els.modalClose.onclick = closeModal;
els.modalOverlay.addEventListener("click", (e) => {
  if (e.target === els.modalOverlay) closeModal();
});

// --- Account Dropdown ---
function toggleDropdown(show) {
  els.accountDropdown.classList.toggle("hidden", !show);
}

document.addEventListener("click", (e) => {
  if (!els.accountTrigger.contains(e.target) && !els.accountDropdown.contains(e.target)) {
    els.accountDropdown.classList.add("hidden");
  }
});

els.accountTrigger.onclick = (e) => {
  e.stopPropagation();
  toggleDropdown(els.accountDropdown.classList.contains("hidden"));
};

// --- Session ---
async function bootstrapSession() {
  if (!accessToken) {
    window.location.href = "/app/login.html";
    return;
  }
  try {
    currentUser = await api("/me");
    updateAccountUI();
    connectEvents();
    await renderView(viewFromURL(), { updateURL: true, replaceURL: !window.location.hash });
  } catch (_) {
    logout();
  }
}

function updateAccountUI() {
  if (!currentUser) return;
  const init = initials(currentUser.name || currentUser.email);
  els.accountAvatar.textContent = init;
  els.accountName.textContent = currentUser.name || currentUser.email;
  els.dropdownEmail.textContent = currentUser.email;
  document.querySelectorAll(".admin-only").forEach((node) => {
    node.classList.toggle("hidden", !currentUser.is_super_admin);
  });
}

// --- Logout ---
function logout() {
  const tokenToRevoke = refreshToken;
  if (tokenToRevoke) {
    fetch("/api/auth/logout", {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        ...(accessToken ? { Authorization: `Bearer ${accessToken}` } : {})
      },
      body: JSON.stringify({ refresh_token: tokenToRevoke })
    }).catch(() => {});
  }
  clearSession();
}

function clearSession() {
  accessToken = "";
  refreshToken = "";
  currentUser = null;
  localStorage.removeItem("access_token");
  localStorage.removeItem("refresh_token");
  eventSource?.close();
  eventSource = null;
  window.location.href = "/app/login.html";
}

els.dropdownLogout.onclick = () => logout();

els.dropdownSettings.onclick = async () => {
  els.accountDropdown.classList.add("hidden");
  await renderView("settings");
};

els.dropdownChangepw.onclick = () => {
  els.accountDropdown.classList.add("hidden");
  openChangePasswordModal();
};

function openChangePasswordModal() {
  openModal("Change Password", `
    <form id="changePasswordForm">
      <div class="form-group">
        <label for="oldPassword">Current password</label>
        <input id="oldPassword" name="old_password" type="password" autocomplete="current-password" required />
      </div>
      <div class="form-group">
        <label for="newPassword">New password</label>
        <input id="newPassword" name="new_password" type="password" minlength="8" autocomplete="new-password" required />
      </div>
      <div class="form-group">
        <label for="confirmPassword">Confirm new password</label>
        <input id="confirmPassword" name="confirm_password" type="password" minlength="8" autocomplete="new-password" required />
      </div>
      <button type="submit" class="btn btn-primary btn-full">Update Password</button>
      <p id="changePasswordMessage" class="form-message hidden"></p>
    </form>
  `);
  document.getElementById("changePasswordForm").onsubmit = submitChangePasswordForm;
  setTimeout(() => document.getElementById("oldPassword")?.focus(), 0);
}

async function submitChangePasswordForm(event) {
  event.preventDefault();
  const form = event.currentTarget;
  const message = document.getElementById("changePasswordMessage");
  const oldPassword = form.elements.old_password.value;
  const newPassword = form.elements.new_password.value;
  const confirmPassword = form.elements.confirm_password.value;

  if (newPassword.length < 8) {
    flash(message, "New password must be at least 8 characters.", false);
    return;
  }
  if (newPassword !== confirmPassword) {
    flash(message, "Password confirmation does not match.", false);
    return;
  }
  if (oldPassword === newPassword) {
    flash(message, "New password must be different from current password.", false);
    return;
  }

  try {
    await api("/auth/change-password", {
      method: "POST",
      body: JSON.stringify({
        old_password: oldPassword,
        new_password: newPassword
      })
    });
    form.reset();
    flash(message, "Password updated. Please sign in again.", true);
    setTimeout(clearSession, 900);
  } catch (error) {
    flash(message, error.message, false);
  }
}

// --- Theme Toggle ---
els.themeToggle.onclick = toggleTheme;

// --- Sidebar ---
els.sidebarCollapse.onclick = () => {
  els.sidebar.classList.toggle("collapsed");
};

els.sidebarToggleMobile.onclick = () => {
  els.sidebar.classList.toggle("open");
};

// --- Navigation ---
document.querySelectorAll("[data-view]").forEach((button) => {
  button.onclick = async () => {
    await renderView(button.dataset.view);
  };
});

window.addEventListener("hashchange", async () => {
  if (!currentUser) return;
  await renderView(viewFromURL(), { updateURL: false });
});

// --- SSE ---
function connectEvents() {
  if (eventSource || !accessToken) return;
  eventSource = new EventSource(`/api/events/stream?token=${encodeURIComponent(accessToken)}`);
  eventSource.addEventListener("mail.received", async () => {
    if (currentView === "email") await renderEmail();
    if (currentView === "dashboard") await renderDashboard();
  });
  eventSource.onerror = () => {
    eventSource?.close();
    eventSource = null;
    setTimeout(connectEvents, 1500);
  };
}

// =============================================
// VIEWS
// =============================================

// --- Dashboard ---
async function renderDashboard() {
  setView("dashboard");
  const [dashboard, domains, inboxes, emailsPayload] = await Promise.all([
    api("/dashboard"),
    api("/domains"),
    api("/inboxes"),
    api("/emails?page=1&page_size=100")
  ]);
  state.dashboard = dashboard;
  state.domains = domains;
  state.inboxes = inboxes;
  state.emails = emailItems(emailsPayload);

  const activeDomains = domains.filter((d) => d.status === "verified").length;
  const warningDomains = domains.filter((d) => d.warning_status).length;
  const activeInboxes = inboxes.filter((i) => i.is_active).length;

  els.pageContent.innerHTML = `
    <div class="dashboard-hero">
      <p class="dashboard-hero-label">Control Room</p>
      <h2 class="dashboard-hero-title">Operational overview</h2>
    </div>

    <div class="stats-grid stats-3" style="margin-bottom:20px">
      <div class="stat-card">
        <p class="stat-label">Mail Today</p>
        <p class="stat-value">${dashboard.mail_today}</p>
      </div>
      <div class="stat-card">
        <p class="stat-label">Storage Used</p>
        <p class="stat-value">${bytes(dashboard.storage_used_bytes)}</p>
      </div>
      <div class="stat-card">
        <p class="stat-label">Active Inboxes</p>
        <p class="stat-value">${dashboard.active_inboxes}</p>
      </div>
    </div>

    <div class="grid-2 grid-2-wide">
      <div class="card">
        <div class="card-header">
          <h3>Domain Posture</h3>
        </div>
        <div class="card-body">
          <div class="stats-grid stats-3">
            <div class="stat-card" style="border:none;padding:12px;background:var(--color-surface-hover)">
              <p class="stat-label">Total</p>
              <p class="stat-value stat-value-sm">${domains.length}</p>
            </div>
            <div class="stat-card" style="border:none;padding:12px;background:var(--color-surface-hover)">
              <p class="stat-label">Verified</p>
              <p class="stat-value stat-value-sm">${activeDomains}</p>
            </div>
            <div class="stat-card" style="border:none;padding:12px;background:var(--color-surface-hover)">
              <p class="stat-label">Warnings</p>
              <p class="stat-value stat-value-sm">${warningDomains}</p>
            </div>
          </div>
        </div>
      </div>

      <div class="card">
        <div class="card-header">
          <h3>Recent Intake</h3>
        </div>
        <div class="card-body" style="padding:12px 20px">
          ${state.emails.length ? state.emails.slice(0, 5).map((mail) => `
            <div class="info-row" style="cursor:default">
              <div style="min-width:0;flex:1">
                <p style="font-size:13px;font-weight:500;color:var(--color-text);white-space:nowrap;overflow:hidden;text-overflow:ellipsis">${escapeHTML(mail.subject || "(no subject)")}</p>
                <p style="font-size:12px;color:var(--color-text-tertiary);margin-top:2px">${escapeHTML(mail.from_address || "Unknown")} &middot; ${relative(mail.received_at)}</p>
              </div>
            </div>
          `).join("") : `
            <div class="empty-state" style="padding:24px">
              <p style="font-size:13px;color:var(--color-text-tertiary)">No mail received yet.</p>
            </div>
          `}
        </div>
      </div>
    </div>

    <div class="grid-2 grid-2-equal" style="margin-top:20px">
      <div class="card">
        <div class="card-header">
          <h3>Account</h3>
        </div>
        <div class="card-body">
          <div class="info-row">
            <span class="info-row-label">Email</span>
            <span class="info-row-value">${escapeHTML(currentUser.email)}</span>
          </div>
          <div class="info-row">
            <span class="info-row-label">Role</span>
            <span class="info-row-value">${currentUser.is_admin ? "Admin" : "User"}</span>
          </div>
          <div class="info-row">
            <span class="info-row-label">Storage Quota</span>
            <span class="info-row-value">${bytes(currentUser.max_storage_bytes)}</span>
          </div>
        </div>
      </div>

      <div class="card">
        <div class="card-header">
          <h3>Infrastructure</h3>
        </div>
        <div class="card-body">
          <div class="info-row">
            <span class="info-row-label">App URL</span>
            <span class="info-row-value" style="font-size:12px">${window.location.origin}</span>
          </div>
          <div class="info-row">
            <span class="info-row-label">MX Target</span>
            <span class="info-row-value" style="font-size:12px">${state.domains[0]?.mx_target || "Configured on server"}</span>
          </div>
        </div>
      </div>
    </div>
  `;
}

// --- Domains ---
async function renderDomains() {
  setView("domains");
  state.domains = await api("/domains");
  const mxTarget = state.domains[0]?.mx_target || "Configured on server";
  els.sidebarMx.textContent = mxTarget;

  els.pageContent.innerHTML = `
    <div class="card" style="margin-bottom:20px">
      <div class="card-header">
        <h3>Claimed Domains</h3>
        <div style="display:flex;gap:8px">
          <button id="refreshDomainsBtn" class="btn btn-secondary btn-sm">Refresh</button>
          <button id="addDomainBtn" class="btn btn-primary btn-sm">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/></svg>
            Add Domain
          </button>
        </div>
      </div>
      <div class="card-body" style="padding:0">
        <div class="table-container">
          <table>
            <thead>
              <tr>
                <th>Domain</th>
                <th>MX</th>
                <th>Status</th>
                <th>Last Verified</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              ${state.domains.length ? state.domains.map((domain) => `
                <tr>
                  <td>
                    <p style="font-weight:500">${escapeHTML(domain.name)}</p>
                    ${domain.verification_error ? `<p style="font-size:12px;color:var(--color-danger);margin-top:2px">${escapeHTML(domain.verification_error)}</p>` : ""}
                  </td>
                  <td style="font-size:13px;color:var(--color-text-secondary)">${escapeHTML(domain.mx_target)}</td>
                  <td>${badge(domain.warning_status || domain.status)}</td>
                  <td style="font-size:13px;color:var(--color-text-secondary)">${relative(domain.last_verified_at)}</td>
                  <td>
                    <div style="display:flex;gap:4px">
                      <button data-domain-verify="${domain.id}" class="icon-btn" title="Verify">
                        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="20 6 9 17 4 12"/></svg>
                      </button>
                      <button data-domain-delete="${domain.id}" class="icon-btn" title="Delete" style="color:var(--color-danger)">
                        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="3 6 5 6 21 6"/><path d="M19 6v14a2 2 0 01-2 2H7a2 2 0 01-2-2V6m3 0V4a2 2 0 012-2h4a2 2 0 012 2v2"/></svg>
                      </button>
                    </div>
                  </td>
                </tr>
              `).join("") : `
                <tr>
                  <td colspan="5">
                    <div class="empty-state">
                      <div class="empty-state-icon">
                        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="10"/><line x1="2" y1="12" x2="22" y2="12"/><path d="M12 2a15.3 15.3 0 014 10 15.3 15.3 0 01-4 10 15.3 15.3 0 01-4-10 15.3 15.3 0 014-10z"/></svg>
                      </div>
                      <p class="empty-state-title">No domains yet</p>
                      <p class="empty-state-desc">Add a domain to start receiving mail.</p>
                    </div>
                  </td>
                </tr>
              `}
            </tbody>
          </table>
        </div>
      </div>
    </div>

    <div class="card">
      <div class="card-header">
        <h3>MX Instruction</h3>
      </div>
      <div class="card-body">
        <div class="mx-instruction">
          <p class="mx-instruction-label">Configure DNS</p>
          <span class="mx-instruction-value">MX 10 ${escapeHTML(mxTarget)}</span>
          <p class="mx-instruction-desc">Point your domain's MX record to the target above. Once the record propagates, use Verify to activate the domain.</p>
        </div>
      </div>
    </div>
  `;

  document.getElementById("refreshDomainsBtn").onclick = renderDomains;
  document.getElementById("addDomainBtn").onclick = () => {
    openModal("Add Domain", `
      <form id="domainForm">
        <div class="form-group">
          <label for="domainName">Domain name</label>
          <input id="domainName" name="name" placeholder="example.org" />
        </div>
        <button type="submit" class="btn btn-primary btn-full">Create Domain</button>
        <p id="domainFormMessage" class="form-message hidden"></p>
      </form>
    `);
    document.getElementById("domainForm").onsubmit = submitDomainForm;
  };
  document.querySelectorAll("[data-domain-verify]").forEach((button) => {
    button.onclick = async () => {
      try {
        await api(`/domains/${button.dataset.domainVerify}/verify`, { method: "POST" });
        await renderDomains();
      } catch (error) {
        alert(error.message);
      }
    };
  });
  document.querySelectorAll("[data-domain-delete]").forEach((button) => {
    button.onclick = async () => {
      const domainId = button.dataset.domainDelete;
      const domain = state.domains.find((d) => d.id === domainId);
      if (!confirm(`Delete domain "${domain?.name || domainId}"? This will also remove all associated inboxes and emails.`)) return;
      try {
        await api(`/domains/${domainId}`, { method: "DELETE" });
        await renderDomains();
      } catch (error) {
        alert(error.message);
      }
    };
  });
}

async function submitDomainForm(event) {
  event.preventDefault();
  const form = event.currentTarget;
  const message = document.getElementById("domainFormMessage");
  const name = form.elements.name.value.trim();

  // Client-side validation
  if (!name) {
    flash(message, "Domain name is required.", false);
    return;
  }
  if (!/^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?)*\.[a-zA-Z]{2,}$/.test(name)) {
    flash(message, "Invalid domain format. Use e.g. example.com", false);
    return;
  }
  if (name.length > 253) {
    flash(message, "Domain name is too long (max 253 characters).", false);
    return;
  }

  try {
    await api("/domains", { method: "POST", body: JSON.stringify({ name }) });
    closeModal();
    await renderDomains();
  } catch (error) {
    flash(message, error.message, false);
  }
}

// --- Email ---
async function renderEmail() {
  setView("email");
  const query = new URLSearchParams({
    page: String(state.emailPage),
    page_size: "25"
  });
  if (state.emailUnreadOnly) query.set("unread", "true");
  if (state.selectedInboxID) query.set("inbox_id", state.selectedInboxID);
  const [inboxes, emailsPayload, domains] = await Promise.all([
    api("/inboxes"),
    api(`/emails?${query.toString()}`),
    api("/domains")
  ]);
  state.inboxes = inboxes;
  state.emails = emailItems(emailsPayload);
  state.emailPagination = Array.isArray(emailsPayload) ? null : emailsPayload.pagination;
  state.domains = domains;

  // Determine selected inbox
  state.selectedInboxID = inboxes.find((i) => i.id === state.selectedInboxID)?.id ||
    state.emails.find((m) => m.id === state.selectedEmailID)?.inbox_id ||
    inboxes[0]?.id || null;

  const filteredEmails = state.selectedInboxID
    ? state.emails.filter((m) => m.inbox_id === state.selectedInboxID)
    : state.emails;

  state.selectedEmailID = filteredEmails.find((m) => m.id === state.selectedEmailID)?.id ||
    filteredEmails[0]?.id || null;

  const selectedInbox = inboxes.find((i) => i.id === state.selectedInboxID);
  const unreadCount = emails.filter((m) => !m.is_read).length;

  els.pageContent.innerHTML = `
    <div class="email-layout">
      <!-- Column 1: Inboxes -->
      <div class="email-panel">
        <div class="email-panel-header">
          <h3>Mailboxes</h3>
          <p class="sub">${inboxes.length} addresses &middot; ${unreadCount} unread</p>
        </div>
        <div class="email-panel-body">
          <button id="addInboxBtn" class="btn btn-primary btn-sm btn-full" style="margin-bottom:12px;padding:10px">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/></svg>
            New Address
          </button>

          ${inboxes.length ? inboxes.map((inbox) => `
            <div class="inbox-item ${state.selectedInboxID === inbox.id ? "active" : ""}" data-mailbox-id="${inbox.id}">
              <span class="inbox-address">${escapeHTML(inbox.address)}</span>
              <span class="inbox-status">${inbox.is_active ? "Active" : "Off"}</span>
            </div>
          `).join("") : `
            <div class="empty-state" style="padding:24px">
              <p class="empty-state-title">No mailboxes</p>
              <p class="empty-state-desc">Create an inbox to start receiving mail.</p>
            </div>
          `}
        </div>
      </div>

      <!-- Column 2: Email List -->
      <div class="email-panel">
        <div class="email-panel-header">
          <h3>${selectedInbox ? escapeHTML(selectedInbox.address) : "All Mail"}</h3>
          <p class="sub">${state.emailPagination?.total ?? filteredEmails.length} messages</p>
        </div>
        <div class="email-panel-body">
          <div style="display:flex;gap:8px;align-items:center;padding:8px 8px 12px">
            <button id="toggleUnreadBtn" class="btn btn-secondary btn-xs">${state.emailUnreadOnly ? "Unread only" : "All mail"}</button>
            <div style="margin-left:auto;display:flex;gap:6px;align-items:center">
              <button id="prevEmailPageBtn" class="btn btn-secondary btn-xs" ${state.emailPagination?.has_prev ? "" : "disabled"}>Prev</button>
              <span style="font-size:12px;color:var(--color-text-tertiary)">Page ${state.emailPagination?.page || 1}/${state.emailPagination?.total_pages || 1}</span>
              <button id="nextEmailPageBtn" class="btn btn-secondary btn-xs" ${state.emailPagination?.has_next ? "" : "disabled"}>Next</button>
            </div>
          </div>
          ${filteredEmails.length ? filteredEmails.map((mail) => `
            <button class="email-item ${state.selectedEmailID === mail.id ? "active" : ""}" data-email-id="${mail.id}">
              <div class="email-item-row">
                <div class="email-avatar">${initials(mail.from_address)}</div>
                <div class="email-item-content">
                  <div class="email-item-top">
                    <span class="email-item-subject">${escapeHTML(mail.subject || "(no subject)")}</span>
                    <span class="email-item-time">${relative(mail.received_at)}</span>
                  </div>
                  <div class="email-item-from">${escapeHTML(mail.from_address || "Unknown sender")}</div>
                  <div class="email-item-snippet">${escapeHTML(mail.snippet || "")}</div>
                </div>
              </div>
            </button>
          `).join("") : `
            <div class="empty-state">
              <div class="empty-state-icon">
                <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M4 4h16c1.1 0 2 .9 2 2v12c0 1.1-.9 2-2 2H4c-1.1 0-2-.9-2-2V6c0-1.1.9-2 2-2z"/><polyline points="22,6 12,13 2,6"/></svg>
              </div>
              <p class="empty-state-title">No messages</p>
              <p class="empty-state-desc">Waiting for inbound mail to arrive.</p>
            </div>
          `}
        </div>
      </div>

      <!-- Column 3: Email Detail -->
      <div class="email-panel">
        <div id="emailDetailContainer">
          ${state.selectedEmailID
            ? `<div class="empty-state" style="padding:24px"><p style="font-size:13px;color:var(--color-text-tertiary)">Loading message...</p></div>`
            : `<div class="empty-state">
                <div class="empty-state-icon">
                  <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M4 4h16c1.1 0 2 .9 2 2v12c0 1.1-.9 2-2 2H4c-1.1 0-2-.9-2-2V6c0-1.1.9-2 2-2z"/><polyline points="22,6 12,13 2,6"/></svg>
                </div>
                <p class="empty-state-title">Select a message</p>
                <p class="empty-state-desc">Choose an email to view its contents.</p>
              </div>`
          }
        </div>
      </div>
    </div>
  `;

  // Bind events
  document.getElementById("addInboxBtn").onclick = () => {
    openModal("New Address", `
      <form id="inboxForm">
        <div class="form-group">
          <label for="inboxLocalPart">Local part</label>
          <input id="inboxLocalPart" name="local_part" placeholder="hello" />
        </div>
        <div class="form-group">
          <label>Domain</label>
          <input id="inboxDomain" name="domain_id" type="hidden" />
          <div class="domain-dropdown">
            <button id="domainDropdownTrigger" type="button" class="domain-dropdown-trigger">
              <span id="domainDropdownLabel">Select domain</span>
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="6 9 12 15 18 9"/></svg>
            </button>
            <div id="domainDropdownMenu" class="domain-dropdown-menu hidden">
            ${domains.length ? domains.map((domain) => {
              const canUse = domain.status === "verified";
              return `
                <button type="button" class="domain-dropdown-item ${canUse ? "" : "disabled"}" data-domain-id="${domain.id}" data-domain-name="${escapeHTML(domain.name)}" ${canUse ? "" : "disabled"}>
                  <span class="domain-dropdown-name">${escapeHTML(domain.name)}</span>
                  ${badge(domain.warning_status || domain.status)}
                </button>
              `;
            }).join("") : `
              <div class="domain-dropdown-empty">
                Add and verify a domain before creating an address.
              </div>
            `}
            </div>
          </div>
        </div>
        <button type="submit" class="btn btn-primary btn-full">Create Address</button>
        <p id="inboxFormMessage" class="form-message hidden"></p>
      </form>
  `);
    document.getElementById("inboxForm").onsubmit = submitInboxForm;
    const trigger = document.getElementById("domainDropdownTrigger");
    const menu = document.getElementById("domainDropdownMenu");
    const input = document.getElementById("inboxDomain");
    const label = document.getElementById("domainDropdownLabel");
    trigger.onclick = () => menu.classList.toggle("hidden");
    document.querySelectorAll("[data-domain-id]").forEach((item) => {
      item.onclick = () => {
        input.value = item.dataset.domainId;
        label.textContent = item.dataset.domainName;
        menu.classList.add("hidden");
      };
    });
  };

  document.getElementById("toggleUnreadBtn").onclick = async () => {
    state.emailUnreadOnly = !state.emailUnreadOnly;
    state.emailPage = 1;
    await renderEmail();
  };
  document.getElementById("prevEmailPageBtn").onclick = async () => {
    if (!state.emailPagination?.has_prev) return;
    state.emailPage -= 1;
    await renderEmail();
  };
  document.getElementById("nextEmailPageBtn").onclick = async () => {
    if (!state.emailPagination?.has_next) return;
    state.emailPage += 1;
    await renderEmail();
  };

  document.querySelectorAll("[data-mailbox-id]").forEach((button) => {
    button.onclick = async () => {
      state.selectedInboxID = button.dataset.mailboxId;
      state.emailPage = 1;
      state.selectedEmailID = filteredEmails.find((m) => m.inbox_id === state.selectedInboxID)?.id ||
        state.emails.find((m) => m.inbox_id === state.selectedInboxID)?.id || null;
      await renderEmail();
    };
  });

  document.querySelectorAll("[data-email-id]").forEach((button) => {
    button.onclick = async () => {
      state.selectedEmailID = button.dataset.emailId;
      await renderEmail();
    };
  });

  if (state.selectedEmailID) {
    await renderEmailDetail(state.selectedEmailID);
  }
}

async function renderEmailDetail(emailID) {
  state.selectedEmailID = emailID;
  const email = await api(`/emails/${emailID}`);
  const container = document.getElementById("emailDetailContainer");

  container.innerHTML = `
    <div class="email-detail">
      <div class="email-detail-header">
        <div class="email-detail-sender">
          <div class="email-detail-avatar">${initials(email.from_address)}</div>
          <div class="email-detail-meta">
            <h3 class="email-detail-subject">${escapeHTML(email.subject || "(no subject)")}</h3>
            <p class="email-detail-from">From: <strong>${escapeHTML(email.from_address || "Unknown sender")}</strong></p>
            <p class="email-detail-to">To: <strong>${escapeHTML(email.to_address || "-")}</strong></p>
          </div>
          <div class="email-detail-time">${relative(email.received_at)}</div>
        </div>
      </div>

      <div class="email-detail-body">
        <!-- HTML Body -->
        <div class="email-detail-section">
          <p class="email-detail-section-title">Message</p>
          <div class="email-detail-html">${email.html_body_sanitized || "<p style='color:var(--color-text-tertiary)'>No HTML content</p>"}</div>
        </div>

        <!-- Plain Text -->
        ${email.text_body ? `
        <div class="email-detail-section">
          <p class="email-detail-section-title">Plain Text</p>
          <div class="email-detail-plain">${escapeHTML(email.text_body)}</div>
        </div>
        ` : ""}

        <!-- Attachments -->
        <div class="email-detail-section">
          <p class="email-detail-section-title">Attachments (${email.attachments?.length || 0})</p>
          ${email.attachments?.length ? email.attachments.map((att) => `
            <div class="attachment-item">
              <div class="attachment-info">
                <p class="attachment-name">${escapeHTML(att.filename)}</p>
                <p class="attachment-meta">${bytes(att.size_bytes)} &middot; ${att.content_type || "unknown"}</p>
              </div>
              <div class="attachment-actions">
                ${badge(att.scan_status)}
                ${att.is_blocked && !att.admin_override_download
                  ? `<span class="attachment-blocked">Blocked</span>
                     ${currentUser?.is_admin ? `<button data-override-attachment="${att.id}" data-override-email="${email.id}" class="btn btn-secondary btn-xs">Override</button>` : ""}`
                  : `<button data-download-email="${email.id}" data-download-attachment="${att.id}" class="btn btn-secondary btn-xs">Download</button>`
                }
              </div>
            </div>
          `).join("") : `
            <p style="font-size:13px;color:var(--color-text-tertiary)">No attachments.</p>
          `}
        </div>

        <!-- Auth Results -->
        <div class="email-detail-section">
          <p class="email-detail-section-title">Authentication</p>
          <div class="email-detail-auth">${escapeHTML(JSON.stringify(email.auth_results_json || {}, null, 2))}</div>
        </div>
      </div>
    </div>
  `;

  document.querySelectorAll("[data-download-email]").forEach((button) => {
    button.onclick = async () => {
      if (refreshToken) await refreshSession();
      const token = encodeURIComponent(accessToken);
      window.open(`/api/emails/${button.dataset.downloadEmail}/attachments/${button.dataset.downloadAttachment}/download?token=${token}`, "_blank");
    };
  });
  document.querySelectorAll("[data-override-attachment]").forEach((button) => {
    button.onclick = async () => {
      if (!confirm("Allow this blocked attachment to be downloaded?")) return;
      try {
        await api(`/admin/attachments/${button.dataset.overrideAttachment}/override`, { method: "PATCH" });
        await renderEmailDetail(button.dataset.overrideEmail);
      } catch (error) {
        alert(error.message);
      }
    };
  });
}

async function submitInboxForm(event) {
  event.preventDefault();
  const form = event.currentTarget;
  const message = document.getElementById("inboxFormMessage");
  const localPart = form.elements.local_part.value.trim();
  const domainID = form.elements.domain_id?.value || "";

  // Client-side validation
  if (!localPart) {
    flash(message, "Local part is required.", false);
    return;
  }
  if (!domainID) {
    flash(message, "Please select a domain.", false);
    return;
  }
  if (localPart.length > 64) {
    flash(message, "Local part is too long (max 64 characters).", false);
    return;
  }
  if (!/^[a-zA-Z0-9][a-zA-Z0-9._+-]*[a-zA-Z0-9]$|^[a-zA-Z0-9]$/.test(localPart)) {
    flash(message, "Invalid local part. Only letters, numbers, and . _ + - are allowed.", false);
    return;
  }

  try {
    await api("/inboxes", {
      method: "POST",
      body: JSON.stringify({
        local_part: localPart,
        domain_id: domainID
      })
    });
    closeModal();
    await renderEmail();
  } catch (error) {
    flash(message, error.message, false);
  }
}

// --- Users ---
async function renderUsers() {
  setView("users");
  if (!currentUser?.is_super_admin) {
    els.pageContent.innerHTML = `
      <div class="empty-state">
        <div class="empty-state-icon">
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="3" y="11" width="18" height="11" rx="2"/><path d="M7 11V7a5 5 0 0110 0v4"/></svg>
        </div>
        <p class="empty-state-title">Super admin required</p>
        <p class="empty-state-desc">This area is restricted to super admin accounts.</p>
      </div>
    `;
    return;
  }

  state.users = await api("/admin/users");
  const activeUsers = state.users.filter((user) => user.is_active).length;
  const adminUsers = state.users.filter((user) => user.is_admin).length;
  const storageUsed = state.users.reduce((sum, user) => sum + Number(user.storage_used_bytes || 0), 0);

  els.pageContent.innerHTML = `
    <div class="stats-grid stats-3" style="margin-bottom:20px">
      <div class="stat-card">
        <p class="stat-label">Total Users</p>
        <p class="stat-value">${state.users.length}</p>
      </div>
      <div class="stat-card">
        <p class="stat-label">Active Users</p>
        <p class="stat-value">${activeUsers}</p>
      </div>
      <div class="stat-card">
        <p class="stat-label">Storage Used</p>
        <p class="stat-value">${bytes(storageUsed)}</p>
      </div>
    </div>

    <div class="card">
      <div class="card-header">
        <div>
          <h3>User Management</h3>
          <p class="sub">${adminUsers} admin accounts</p>
        </div>
        <button id="refreshUsersBtn" class="btn btn-secondary btn-sm">Refresh</button>
      </div>
      <div class="card-body" style="padding:0">
        <div class="table-container">
          <table>
            <thead>
              <tr>
                <th>User</th>
                <th>Role</th>
                <th>Status</th>
                <th>Domains</th>
                <th>Inboxes</th>
                <th>Message</th>
                <th>Storage</th>
                <th>Created</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              ${state.users.length ? state.users.map((user) => `
                <tr>
                  <td>
                    <p style="font-weight:600">${escapeHTML(user.email)}</p>
                    ${user.name ? `<p style="font-size:12px;color:var(--color-text-tertiary);margin-top:2px">${escapeHTML(user.name)}</p>` : ""}
                  </td>
                  <td>${badge(roleLabel(user))}</td>
                  <td>${badge(user.is_active ? "active" : "disabled")}</td>
                  <td style="color:var(--color-text-secondary)">${user.max_domains}</td>
                  <td style="color:var(--color-text-secondary)">${user.max_inboxes}</td>
                  <td style="color:var(--color-text-secondary)">${user.max_message_size_mb} MB</td>
                  <td>
                    <div style="min-width:160px">
                      <div class="quota-meter">
                        <span style="width:${storagePercent(user)}%"></span>
                      </div>
                      <p style="font-size:12px;color:var(--color-text-tertiary);margin-top:4px">${bytes(user.storage_used_bytes)} / ${bytes(user.max_storage_bytes)}</p>
                    </div>
                  </td>
                  <td style="font-size:13px;color:var(--color-text-secondary)">${relative(user.created_at)}</td>
                  <td>
                    <div style="display:flex;gap:4px">
                      <button data-user-quota="${user.id}" class="icon-btn" title="Edit quotas">
                        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 20h9"/><path d="M16.5 3.5a2.12 2.12 0 013 3L7 19l-4 1 1-4Z"/></svg>
                      </button>
                      ${user.id === currentUser.id ? "" : `
                        <button data-user-status="${user.id}" data-user-active="${user.is_active}" class="icon-btn" title="${user.is_active ? "Disable user" : "Enable user"}" style="color:${user.is_active ? "var(--color-danger)" : "var(--color-success)"}">
                          ${user.is_active
                            ? `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><line x1="4.93" y1="4.93" x2="19.07" y2="19.07"/></svg>`
                            : `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="20 6 9 17 4 12"/></svg>`
                          }
                        </button>
                        <button data-user-delete="${user.id}" class="icon-btn" title="Delete user and data" style="color:var(--color-danger)">
                          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="3 6 5 6 21 6"/><path d="M19 6v14a2 2 0 01-2 2H7a2 2 0 01-2-2V6"/><path d="M8 6V4a2 2 0 012-2h4a2 2 0 012 2v2"/></svg>
                        </button>
                      `}
                    </div>
                  </td>
                </tr>
              `).join("") : `
                <tr>
                  <td colspan="9">
                    <div class="empty-state">
                      <p class="empty-state-title">No users found</p>
                    </div>
                  </td>
                </tr>
              `}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  `;

  document.getElementById("refreshUsersBtn").onclick = renderUsers;
  document.querySelectorAll("[data-user-status]").forEach((button) => {
    button.onclick = async () => {
      const userID = button.dataset.userStatus;
      const isActive = button.dataset.userActive === "true";
      const user = state.users.find((row) => row.id === userID);
      if (!confirm(`${isActive ? "Disable" : "Enable"} ${user?.email || "this user"}?`)) return;
      try {
        await api(`/admin/users/${userID}/status`, {
          method: "PATCH",
          body: JSON.stringify({ is_active: !isActive })
        });
        await renderUsers();
      } catch (error) {
        alert(error.message);
      }
    };
  });
  document.querySelectorAll("[data-user-quota]").forEach((button) => {
    button.onclick = () => openUserQuotaModal(button.dataset.userQuota);
  });
  document.querySelectorAll("[data-user-delete]").forEach((button) => {
    button.onclick = async () => {
      const userID = button.dataset.userDelete;
      const user = state.users.find((row) => row.id === userID);
      const email = user?.email || "this user";
      if (!confirm(`Delete ${email} and all domains, inboxes, emails, attachments, and sessions for this user? This cannot be undone.`)) return;
      try {
        await api(`/admin/users/${userID}`, { method: "DELETE" });
        await renderUsers();
      } catch (error) {
        alert(error.message);
      }
    };
  });
}

function openUserQuotaModal(userID) {
  const user = state.users.find((row) => row.id === userID);
  if (!user) return;

  openModal("Edit User Quotas", `
    <form id="userQuotaForm">
      <div class="form-group">
        <label for="quotaEmail">User</label>
        <input id="quotaEmail" value="${escapeHTML(user.email)}" disabled />
      </div>
      <div class="grid-2 grid-2-equal" style="gap:12px">
        <div class="form-group">
          <label for="maxDomains">Max domains</label>
          <input id="maxDomains" name="max_domains" type="number" min="0" step="1" value="${user.max_domains}" />
        </div>
        <div class="form-group">
          <label for="maxInboxes">Max inboxes</label>
          <input id="maxInboxes" name="max_inboxes" type="number" min="0" step="1" value="${user.max_inboxes}" />
        </div>
      </div>
      <div class="grid-2 grid-2-equal" style="gap:12px">
        <div class="form-group">
          <label for="maxMessageSizeMB">Max message MB</label>
          <input id="maxMessageSizeMB" name="max_message_size_mb" type="number" min="1" step="1" value="${user.max_message_size_mb}" />
        </div>
        <div class="form-group">
          <label for="maxAttachmentSizeMB">Max attachment MB</label>
          <input id="maxAttachmentSizeMB" name="max_attachment_size_mb" type="number" min="1" step="1" value="${user.max_attachment_size_mb}" />
        </div>
      </div>
      <div class="form-group">
        <label for="maxStorageGB">Max storage GB</label>
        <input id="maxStorageGB" name="max_storage_gb" type="number" min="1" step="0.1" value="${gb(user.max_storage_bytes)}" />
      </div>
      <button type="submit" class="btn btn-primary btn-full">Save Quotas</button>
      <p id="userQuotaFormMessage" class="form-message hidden"></p>
    </form>
  `);

  document.getElementById("userQuotaForm").onsubmit = async (event) => {
    event.preventDefault();
    const form = event.currentTarget;
    const message = document.getElementById("userQuotaFormMessage");
    const maxStorageGB = Number(form.elements.max_storage_gb.value);
    const payload = {
      max_domains: Number(form.elements.max_domains.value),
      max_inboxes: Number(form.elements.max_inboxes.value),
      max_message_size_mb: Number(form.elements.max_message_size_mb.value),
      max_attachment_size_mb: Number(form.elements.max_attachment_size_mb.value),
      max_storage_bytes: Math.round(maxStorageGB * 1024 * 1024 * 1024)
    };

    if (
      payload.max_domains < 0 ||
      payload.max_inboxes < 0 ||
      payload.max_message_size_mb < 1 ||
      payload.max_attachment_size_mb < 1 ||
      payload.max_storage_bytes < 1 ||
      Object.values(payload).some((value) => !Number.isFinite(value))
    ) {
      flash(message, "Quota values must be valid numbers. Size and storage quotas must be at least 1.", false);
      return;
    }

    try {
      await api(`/admin/users/${userID}/quotas`, {
        method: "PATCH",
        body: JSON.stringify(payload)
      });
      closeModal();
      await renderUsers();
    } catch (error) {
      flash(message, error.message, false);
    }
  };
}

// --- Settings ---
function renderSettings() {
  setView("settings");

  els.pageContent.innerHTML = `
    <div class="grid-2 grid-2-equal">
      <div class="card">
        <div class="card-header">
          <h3>Session</h3>
        </div>
        <div class="card-body">
          <div class="info-row">
            <span class="info-row-label">Signed in as</span>
            <span class="info-row-value">${escapeHTML(currentUser?.email || "-")}</span>
          </div>
          <div class="info-row">
            <span class="info-row-label">Role</span>
            <span class="info-row-value">${currentUser?.is_admin ? "Admin" : "User"}</span>
          </div>
          <div class="info-row">
            <span class="info-row-label">Name</span>
            <span class="info-row-value">${escapeHTML(currentUser?.name || "-")}</span>
          </div>
        </div>
      </div>

      <div class="card">
        <div class="card-header">
          <h3>Runtime</h3>
        </div>
        <div class="card-body">
          <div class="info-row">
            <span class="info-row-label">Frontend</span>
            <span class="info-row-value">Vanilla SPA</span>
          </div>
          <div class="info-row">
            <span class="info-row-label">API Origin</span>
            <span class="info-row-value" style="font-size:12px">${window.location.origin}</span>
          </div>
          <div class="info-row">
            <span class="info-row-label">Theme</span>
            <span class="info-row-value">${(document.documentElement.getAttribute("data-theme") || "light")}</span>
          </div>
        </div>
      </div>
    </div>
  `;
}

// --- Init ---
setTheme();
bootstrapSession();
