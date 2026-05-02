

/* ============================================
   GoMail — Main Application
   ============================================ */

let accessToken = localStorage.getItem("access_token") || "";
let refreshToken = localStorage.getItem("refresh_token") || "";
let currentView = "dashboard";
let currentUser = null;
let eventSource = null;
let eventReconnectTimer = null;
let shouldReconnectEvents = true;
let eventReconnectAttempts = 0;

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
  emailPage: 1,
  websites: [],
  websiteQuota: null,
  apiKeys: [],
  smtpSettings: null
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
  websites: { section: "Hosting", title: "Websites" },
  "api-keys": { section: "Relay", title: "API Keys" },
  users: { section: "Admin", title: "Users" },
  settings: { section: "Account", title: "Settings" }
};

const defaultView = "dashboard";

// --- API Helper ---
const api = async (path, options = {}) => {
  const { refresh, ...fetchOptions } = options;
  const headers = {
    "Content-Type": "application/json",
    ...(accessToken ? { Authorization: `Bearer ${accessToken}` } : {}),
    ...(fetchOptions.headers || {})
  };
  if (fetchOptions.body instanceof FormData) {
    delete headers["Content-Type"];
  }
  const res = await fetch(`/api${path}`, {
    ...fetchOptions,
    headers
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
    disconnectEvents({ reconnect: false });
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
  const base = view.split("/")[0];
  const meta = viewMeta[base] || viewMeta[view];
  if (meta) {
    els.breadcrumbSection.textContent = meta.section;
    els.breadcrumbTitle.textContent = meta.title;
  } else {
    els.breadcrumbSection.textContent = "";
    els.breadcrumbTitle.textContent = view;
  }
  document.querySelectorAll(".nav-item").forEach((btn) => {
    btn.classList.toggle("active", btn.dataset.view === base);
  });
  els.sidebar.classList.remove("open");
}

function normalizeView(view) {
  const base = view.split("/")[0];
  return viewMeta[base] ? view : (viewMeta[view] ? view : defaultView);
}

function viewFromURL() {
  const hashView = window.location.hash.replace(/^#\/?/, "").trim();
  return normalizeView(hashView || defaultView);
}

function setViewURL(view, replace = false) {
  const base = view.split("/")[0];
  const nextHash = `#/${viewMeta[base] ? view : base}`;
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
  const base = nextView.split("/")[0];
  if (options.updateURL !== false) {
    const changed = setViewURL(nextView, options.replaceURL);
    if (changed && !options.replaceURL) return;
  }
  if (base === "dashboard") await renderDashboard();
  else if (base === "domains") await renderDomains();
  else if (base === "email") await renderEmail();
  else if (base === "websites") {
    const id = nextView.split("/")[1];
    if (id) await renderWebsiteSettings(id);
    else await renderWebsites();
  }
  else if (base === "api-keys") await renderApiKeys();
  else if (base === "users") await renderUsers();
  else if (base === "settings") renderSettings();
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

function dateTime(iso) {
  if (!iso) return "Never";
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return "-";
  return `${date.toLocaleDateString()} ${date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}`;
}

function statusIcon(status) {
  const icons = {
    verified: `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="var(--color-success)" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="M22 11.08V12a10 10 0 1 1-5.93-9.14"/><polyline points="22 4 12 14.01 9 11.01"/></svg>`,
    pending: `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="var(--color-primary)" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><polyline points="12 6 12 12 16 14"/></svg>`,
    failed: `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="var(--color-danger)" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><line x1="15" y1="9" x2="9" y2="15"/><line x1="9" y1="9" x2="15" y2="15"/></svg>`,
    warning: `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="var(--color-warning)" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z"/><line x1="12" y1="9" x2="12" y2="13"/><line x1="12" y1="17" x2="12.01" y2="17"/></svg>`,
    verified_warning: `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="var(--color-warning)" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"/><line x1="12" y1="8" x2="12" y2="12"/><line x1="12" y1="16" x2="12.01" y2="16"/></svg>`,
    disabled: `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="var(--color-text-tertiary)" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><line x1="4.93" y1="4.93" x2="19.07" y2="19.07"/></svg>`,
    ssl_active: `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="var(--color-success)" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="11" width="18" height="11" rx="2" ry="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/></svg>`,
    none: `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="var(--color-text-tertiary)" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><line x1="2" y1="2" x2="22" y2="22"/></svg>`
  };
  return icons[status] || icons.pending;
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
    User: "badge-default",
    live: "badge-verified",
    deploying: "badge-pending",
    none: "badge-default",
    publish_failed: "badge-failed"
  };
  const icon = statusIcon(status);
  return `<span class="badge ${cls[status] || "badge-default"}">${icon} ${status}</span>`;

}

function renderDomainCheckCell({ status, detail = "", verifyAttr = "", verifyLabel = "Verify", extraAction = "" }) {
  const safeStatus = status || "pending";
  const safeDetail = detail ? `<div style="font-size:12px;color:var(--color-text-tertiary);margin-top:6px">${escapeHTML(detail)}</div>` : "";
  const verifyButton = verifyAttr
    ? `<button ${verifyAttr} class="btn btn-secondary btn-xs">${verifyLabel}</button>`
    : "";
  const actions = verifyButton || extraAction
    ? `<div style="display:flex;gap:6px;flex-wrap:wrap;margin-top:8px">${verifyButton}${extraAction}</div>`
    : "";
  return `
    <div style="min-width:140px">
      ${badge(safeStatus)}
      ${safeDetail}
      ${actions}
    </div>
  `;
}

function normalizeDomainName(value) {
  return String(value || "").trim().toLowerCase();
}

function findWebsiteByDomain(domainName) {
  const normalized = normalizeDomainName(domainName);
  if (!normalized) return null;
  return (state.websites || []).find((site) => normalizeDomainName(site.assigned_domain) === normalized) || null;
}

function renderDomainWebsiteCell(domain, site) {
  let detail = domain.a_record_result || "Check website A/AAAA routing";
  let extraAction = "";

  if (site) {
    detail = `${detail}${detail ? " · " : ""}Website: ${site.name}`;
    const sslLabel = site.domain_binding_status === "ssl_active" ? "SSL Active" : "SSL";
    const disabled = domain.a_record_status !== "verified" || site.domain_binding_status === "ssl_active" ? "disabled" : "";
    extraAction = `<button data-domain-activate-ssl="${site.id}" class="btn btn-secondary btn-xs" ${disabled}>${sslLabel}</button>`;
  }

  return renderDomainCheckCell({
    status: domain.a_record_status,
    detail,
    verifyAttr: `data-domain-verify-a="${domain.id}"`,
    extraAction
  });
}

function renderDomainEmailCheckCell(domain) {
  const spfStatus = domain.spf_status || "pending";
  const dkimStatus = domain.dkim_status || "pending";
  const detail = `SPF: ${spfStatus} · DKIM: ${dkimStatus}`;
  return `
    <div style="min-width:170px">
      <div style="display:flex;gap:6px;flex-wrap:wrap">
        ${badge(spfStatus)}
        ${badge(dkimStatus)}
      </div>
      <div style="font-size:12px;color:var(--color-text-tertiary);margin-top:6px">${escapeHTML(detail)}</div>
      <div style="display:flex;gap:6px;flex-wrap:wrap;margin-top:8px">
        <button data-domain-verify-email-auth="${domain.id}" class="btn btn-secondary btn-xs">Verify</button>
        <button data-domain-email-auth="${domain.id}" class="btn btn-secondary btn-xs">DNS</button>
      </div>
    </div>
  `;
}

// Derive the base domain for static site URLs from window.location.
// e.g. "app.example.com" → "example.com", "localhost:8080" → "localhost"
function getBaseDomain() {
  const hostname = window.location.hostname;
  if (hostname === "localhost" || hostname === "127.0.0.1") return "localhost";
  const parts = hostname.split(".");
  // Strip the first subdomain part ("app", "www", etc.)
  if (parts.length >= 3) return parts.slice(1).join(".");
  return hostname;
}

function websiteThumbnailURL(site) {
  if (!site?.id || site.thumbnail_status !== "ready") return "";
  return `/static-thumbnails/${encodeURIComponent(site.id)}/thumbnail.png`;
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
  disconnectEvents({ reconnect: false });
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
function clearEventReconnectTimer() {
  if (eventReconnectTimer) {
    clearTimeout(eventReconnectTimer);
    eventReconnectTimer = null;
  }
}

function disconnectEvents({ reconnect = false } = {}) {
  shouldReconnectEvents = reconnect;
  clearEventReconnectTimer();
  if (eventSource) {
    eventSource.close();
    eventSource = null;
  }
  if (!reconnect) {
    eventReconnectAttempts = 0;
  }
}

function scheduleEventReconnect() {
  if (!shouldReconnectEvents || !accessToken) return;
  clearEventReconnectTimer();
  const delay = Math.min(10000, 1500 * Math.max(1, eventReconnectAttempts));
  eventReconnectTimer = setTimeout(() => {
    eventReconnectTimer = null;
    connectEvents();
  }, delay);
}

function connectEvents() {
  if (eventSource || !accessToken || !currentUser) return;
  shouldReconnectEvents = true;
  clearEventReconnectTimer();
  eventSource = new EventSource(`/api/events/stream?token=${encodeURIComponent(accessToken)}`);
  eventSource.onopen = () => {
    eventReconnectAttempts = 0;
  };
  eventSource.addEventListener("mail.received", async () => {
    if (currentView === "email") await renderEmail();
    if (currentView === "dashboard") await renderDashboard();
  });
  eventSource.onerror = () => {
    if (!shouldReconnectEvents) {
      disconnectEvents({ reconnect: false });
      return;
    }
    eventReconnectAttempts += 1;
    disconnectEvents({ reconnect: true });
    scheduleEventReconnect();
  };
}

window.addEventListener("beforeunload", () => {
  disconnectEvents({ reconnect: false });
});

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
  const [domains, websites] = await Promise.all([
    api("/domains"),
    api("/static-projects")
  ]);
  state.domains = domains || [];
  state.websites = websites || [];
  const mxTarget = state.domains[0]?.mx_target || "Configured on server";
  els.sidebarMx.textContent = mxTarget;

  const verifiedCount = state.domains.filter((d) => d.status === "verified").length;
  const warningCount = state.domains.filter((d) => d.warning_status).length;
  const failedCount = state.domains.filter((d) => d.status === "failed" || d.status === "pending").length;

  els.pageContent.innerHTML = `
    <div class="domain-summary">
      <div class="domain-summary-item">
        <span class="domain-summary-dot total"></span>
        <span><strong>${state.domains.length}</strong> Total</span>
      </div>
      <div class="domain-summary-item">
        <span class="domain-summary-dot verified"></span>
        <span><strong>${verifiedCount}</strong> Verified</span>
      </div>
      <div class="domain-summary-item">
        <span class="domain-summary-dot warning"></span>
        <span><strong>${warningCount}</strong> Warnings</span>
      </div>
      <div class="domain-summary-item">
        <span class="domain-summary-dot failed"></span>
        <span><strong>${failedCount}</strong> Pending / Failed</span>
      </div>
    </div>

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
                <th>Website A</th>
                <th>SMTP MX</th>
                <th>SMTP SPF/DKIM</th>
                <th>Overall</th>
                <th>Last Verified</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              ${state.domains.length ? state.domains.map((domain) => `
                <tr>
                  <td>
                    <div class="domain-cell">
                      <span class="domain-cell-name">${escapeHTML(domain.name)}</span>
                      ${domain.verification_error ? `<span class="domain-cell-error">${escapeHTML(domain.verification_error)}</span>` : ""}
                      <span class="domain-cell-meta">${domain.warning_status ? "Warning: check DNS records" : "All checks passing"}</span>
                    </div>
                  </td>
                  <td>
                    ${renderDomainWebsiteCell(domain, findWebsiteByDomain(domain.name))}
                  </td>
                  <td>
                    ${renderDomainCheckCell({
                      status: domain.mx_status || domain.status,
                      detail: domain.mx_target || "Configured on server",
                      verifyAttr: `data-domain-verify-mx="${domain.id}"`
                    })}
                  </td>
                  <td>
                    ${renderDomainEmailCheckCell(domain)}
                  </td>
                  <td>
                    <div class="domain-status-row">
                      ${badge(domain.warning_status || domain.status)}
                    </div>
                  </td>
                  <td style="font-size:13px;color:var(--color-text-secondary);white-space:nowrap">${relative(domain.last_verified_at)}</td>
                  <td>
                    <div class="domain-actions">
                      <button data-domain-delete="${domain.id}" class="icon-btn" title="Delete" style="color:var(--color-danger)">
                        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="3 6 5 6 21 6"/><path d="M19 6v14a2 2 0 01-2 2H7a2 2 0 01-2-2V6m3 0V4a2 2 0 012-2h4a2 2 0 012 2v2"/></svg>
                      </button>
                    </div>
                  </td>
                </tr>
              `).join("") : `
                <tr>
                  <td colspan="7">
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
  document.querySelectorAll("[data-domain-verify-a]").forEach((button) => {
    button.onclick = async () => {
      try {
        await api(`/domains/${button.dataset.domainVerifyA}/verify-a`, { method: "POST" });
        await renderDomains();
      } catch (error) {
        alert(error.message);
      }
    };
  });
  document.querySelectorAll("[data-domain-activate-ssl]").forEach((button) => {
    button.onclick = async () => {
      try {
        await api(`/static-projects/${button.dataset.domainActivateSsl}/domain/active-ssl`, { method: "POST" });
        await renderDomains();
      } catch (error) {
        alert(error.message);
      }
    };
  });
  document.querySelectorAll("[data-domain-verify-mx]").forEach((button) => {
    button.onclick = async () => {
      try {
        await api(`/domains/${button.dataset.domainVerifyMx}/verify-mx`, { method: "POST" });
        await renderDomains();
      } catch (error) {
        alert(error.message);
      }
    };
  });
  document.querySelectorAll("[data-domain-verify-email-auth]").forEach((button) => {
    button.onclick = async () => {
      try {
        await api(`/domains/${button.dataset.domainVerifyEmailAuth}/email-auth/verify`, { method: "POST" });
        await renderDomains();
      } catch (error) {
        alert(error.message);
      }
    };
  });
  document.querySelectorAll("[data-domain-email-auth]").forEach((button) => {
    button.onclick = async () => {
      await openDomainEmailAuthModal(button.dataset.domainEmailAuth);
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

async function openDomainEmailAuthModal(domainId) {
  const domain = state.domains.find((d) => d.id === domainId);
  openModal("SPF / DKIM", `<p style="font-size:13px;color:var(--color-text-secondary)">Loading DNS records...</p>`);
  try {
    const payload = await api(`/domains/${domainId}/email-auth`);
    renderDomainEmailAuthModal(domainId, domain?.name || "", payload);
  } catch (error) {
    els.modalBody.innerHTML = `<p class="form-message error">${escapeHTML(error.message)}</p>`;
  }
}

function renderDomainEmailAuthModal(domainId, domainName, payload) {
  const auth = payload.auth || {};
  const spf = payload.spf || {};
  const dkim = payload.dkim || {};
  els.modalBody.innerHTML = `
    <div style="display:grid;gap:14px">
      <div class="info-row">
        <span class="info-row-label">Domain</span>
        <span class="info-row-value">${escapeHTML(domainName)}</span>
      </div>
      <div style="display:grid;gap:10px">
        <div style="display:flex;align-items:center;justify-content:space-between;gap:12px">
          <h4 style="font-size:14px;font-weight:600">SPF</h4>
          ${badge(auth.spf_status || "pending")}
        </div>
        ${renderDNSInstruction(spf)}
        ${auth.spf_error ? `<p style="font-size:12px;color:var(--color-danger)">${escapeHTML(auth.spf_error)}</p>` : ""}
      </div>
      <div style="display:grid;gap:10px">
        <div style="display:flex;align-items:center;justify-content:space-between;gap:12px">
          <h4 style="font-size:14px;font-weight:600">DKIM</h4>
          ${badge(auth.dkim_status || "pending")}
        </div>
        ${dkim.value ? renderDNSInstruction(dkim) : `<p style="font-size:13px;color:var(--color-text-secondary)">Generate a DKIM selector to create the TXT record.</p>`}
        ${auth.dkim_error ? `<p style="font-size:12px;color:var(--color-danger)">${escapeHTML(auth.dkim_error)}</p>` : ""}
      </div>
      <div style="display:flex;gap:8px;justify-content:flex-end;flex-wrap:wrap">
        <button id="generateDKIMBtn" class="btn btn-secondary btn-sm">Generate DKIM</button>
        <button id="verifyEmailAuthBtn" class="btn btn-primary btn-sm">Verify SPF/DKIM</button>
      </div>
      <p id="domainEmailAuthMessage" class="form-message hidden"></p>
    </div>
  `;
  document.querySelectorAll("[data-copy-value]").forEach((button) => {
    button.onclick = async () => {
      const value = button.dataset.copyValue || "";
      try {
        await navigator.clipboard.writeText(value);
        button.textContent = "Copied";
        setTimeout(() => { button.textContent = "Copy"; }, 900);
      } catch (_) {
        alert(value);
      }
    };
  });
  document.getElementById("generateDKIMBtn").onclick = async () => {
    const message = document.getElementById("domainEmailAuthMessage");
    try {
      const next = await api(`/domains/${domainId}/email-auth/dkim/generate`, { method: "POST", body: JSON.stringify({}) });
      flash(message, "DKIM record generated.", true);
      renderDomainEmailAuthModal(domainId, domainName, next);
    } catch (error) {
      flash(message, error.message, false);
    }
  };
  document.getElementById("verifyEmailAuthBtn").onclick = async () => {
    const message = document.getElementById("domainEmailAuthMessage");
    try {
      const next = await api(`/domains/${domainId}/email-auth/verify`, { method: "POST" });
      flash(message, "Verification complete.", true);
      renderDomainEmailAuthModal(domainId, domainName, next);
    } catch (error) {
      flash(message, error.message, false);
    }
  };
}

function renderDNSInstruction(record) {
  const name = record.name || "";
  const value = record.value || "";
  return `
    <div style="display:grid;gap:8px;border:1px solid var(--color-border);border-radius:8px;padding:10px;background:var(--color-surface-hover)">
      <div class="info-row">
        <span class="info-row-label">Type</span>
        <span class="info-row-value">${escapeHTML(record.type || "TXT")}</span>
      </div>
      <div class="info-row">
        <span class="info-row-label">Name</span>
        <span class="info-row-value" style="font-size:12px;word-break:break-all">${escapeHTML(name)}</span>
      </div>
      <div>
        <div style="display:flex;align-items:center;justify-content:space-between;gap:8px;margin-bottom:6px">
          <span class="info-row-label">Value</span>
          <button type="button" class="btn btn-secondary btn-xs" data-copy-value="${escapeHTML(value)}">Copy</button>
        </div>
        <pre style="white-space:pre-wrap;word-break:break-all;font-size:12px;line-height:1.45;background:var(--color-surface);border:1px solid var(--color-border);border-radius:6px;padding:8px;margin:0">${escapeHTML(value)}</pre>
      </div>
    </div>
  `;
}

async function submitDomainForm(event) {
  event.preventDefault();
  const form = event.currentTarget;
  const message = document.getElementById("domainFormMessage");
  const name = form.elements.name.value.trim();

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

  state.selectedInboxID = inboxes.find((i) => i.id === state.selectedInboxID)?.id ||
    state.emails.find((m) => m.id === state.selectedEmailID)?.inbox_id ||
    inboxes[0]?.id || null;

  const filteredEmails = state.selectedInboxID
    ? state.emails.filter((m) => m.inbox_id === state.selectedInboxID)
    : state.emails;

  state.selectedEmailID = filteredEmails.find((m) => m.id === state.selectedEmailID)?.id ||
    filteredEmails[0]?.id || null;

  const selectedInbox = inboxes.find((i) => i.id === state.selectedInboxID);
  const unreadCount = state.emails.filter((m) => !m.is_read).length;

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
              <div style="display:flex;align-items:center;gap:4px;flex-shrink:0">
                <span class="inbox-status">${inbox.is_active ? "Active" : "Off"}</span>
                <button data-inbox-delete="${inbox.id}" class="icon-btn" title="Delete mailbox" style="width:24px;height:24px;color:var(--color-text-tertiary)">

                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="3 6 5 6 21 6"/><path d="M19 6v14a2 2 0 01-2 2H7a2 2 0 01-2-2V6"/><path d="M8 6V4a2 2 0 012-2h4a2 2 0 012 2v2"/></svg>
                </button>
              </div>
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
          <div>
            <h3>${selectedInbox ? escapeHTML(selectedInbox.address) : "All Mail"}</h3>
            <p class="sub">${state.emailPagination?.total ?? filteredEmails.length} messages</p>
          </div>
          <button id="refreshEmailsBtn" class="icon-btn" title="Refresh">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><polyline points="23 4 23 10 17 10"/><path d="M20.49 15a9 9 0 1 1-2.12-9.36L23 10"/></svg>
          </button>
        </div>
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
                <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M22 12h-4l-3 9L9 3l-3 9H2"/></svg>
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

  document.getElementById("refreshEmailsBtn").onclick = async () => {
    await renderEmail();
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

  document.querySelectorAll("[data-inbox-delete]").forEach((button) => {
    button.onclick = async (event) => {
      event.stopPropagation();
      const inboxId = button.dataset.inboxDelete;
      const inbox = state.inboxes.find((i) => i.id === inboxId);
      if (!confirm(`Delete mailbox "${inbox?.address || inboxId}"? All associated emails and attachments will be permanently removed.`)) return;
      try {
        await api(`/inboxes/${inboxId}`, { method: "DELETE" });
        if (state.selectedInboxID === inboxId) {
          state.selectedInboxID = null;
          state.selectedEmailID = null;
        }
        await renderEmail();
      } catch (error) {
        alert(error.message);
      }
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
        <div class="email-detail-html" id="emailHtmlBody">${email.html_body_sanitized || "<p style='color:var(--color-text-tertiary)'>This email has no HTML content.</p>"}</div>

        <!-- Plain Text (hidden by default, toggled) -->
        ${email.text_body ? `
        <div class="email-detail-plain" id="emailPlainBody" style="display:none">${escapeHTML(email.text_body)}</div>
        <div class="email-detail-actions">
          <button class="btn btn-ghost btn-xs" id="toggleViewBtn">Show plain text</button>
        </div>
        ` : ""}

        <!-- Attachments -->
        ${email.attachments?.length ? `
        <div class="email-detail-attachments">
          ${email.attachments.map((att) => `
            <div class="attachment-item">
              <div class="attachment-icon">
                <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21.44 11.05l-9.19 9.19a6 6 0 0 1-8.49-8.49l9.19-9.19a4 4 0 0 1 5.66 5.66l-9.2 9.19a2 2 0 0 1-2.83-2.83l8.49-8.48"/></svg>
              </div>
              <div class="attachment-info">
                <p class="attachment-name">${escapeHTML(att.filename)}</p>
                <p class="attachment-meta">${bytes(att.size_bytes)}</p>
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
          `).join("")}
        </div>
        ` : ""}

        <!-- Show Original (raw headers) -->
        <details class="email-detail-original">
          <summary>Show original</summary>
          <div class="email-detail-original-content">
            <div class="email-detail-original-row">
              <span class="original-label">Subject:</span>
              <span class="original-value">${escapeHTML(email.subject || "")}</span>
            </div>
            <div class="email-detail-original-row">
              <span class="original-label">From:</span>
              <span class="original-value">${escapeHTML(email.from_address || "")}</span>
            </div>
            <div class="email-detail-original-row">
              <span class="original-label">To:</span>
              <span class="original-value">${escapeHTML(email.to_address || "")}</span>
            </div>
            <div class="email-detail-original-row">
              <span class="original-label">Date:</span>
              <span class="original-value">${new Date(email.received_at).toLocaleString()}</span>
            </div>
            <div class="email-detail-original-row">
              <span class="original-label">Message-ID:</span>
              <span class="original-value">${escapeHTML(email.message_id || "")}</span>
            </div>
            ${email.headers_json ? `
            <div class="email-detail-original-headers">${escapeHTML(JSON.stringify(email.headers_json, null, 2))}</div>
            ` : ""}
          </div>
        </details>

      </div>

    </div>
  `;

  // Toggle plain text / HTML view
  const toggleBtn = document.getElementById("toggleViewBtn");
  const htmlBody = document.getElementById("emailHtmlBody");
  const plainBody = document.getElementById("emailPlainBody");
  if (toggleBtn && htmlBody && plainBody) {
    toggleBtn.onclick = () => {
      const showingHtml = htmlBody.style.display !== "none";
      htmlBody.style.display = showingHtml ? "none" : "";
      plainBody.style.display = showingHtml ? "" : "none";
      toggleBtn.textContent = showingHtml ? "Show HTML" : "Show plain text";
    };
  }

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

// --- API Keys ---
async function renderApiKeys() {
  setView("api-keys");
  els.pageContent.innerHTML = `<div style="text-align:center;padding:48px"><p style="color:var(--color-text-tertiary)">Loading API keys...</p></div>`;

  try {
    const [keys, settings] = await Promise.all([
      api("/api-keys"),
      api("/api-keys/settings")
    ]);
    state.apiKeys = Array.isArray(keys) ? keys : [];
    state.smtpSettings = settings || state.apiKeys[0]?.smtp_settings || defaultSmtpSettings();

    els.pageContent.innerHTML = `
      <div class="card" style="margin-bottom:20px">
        <div class="card-header">
          <div>
            <h3>API Keys</h3>
            <p class="sub">Credentials for SMTP relay clients.</p>
          </div>
          <div style="display:flex;gap:8px">
            <button id="refreshApiKeysBtn" class="btn btn-secondary btn-sm">Refresh</button>
            <button id="createApiKeyBtn" class="btn btn-primary btn-sm">
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/></svg>
              Create API Key
            </button>
          </div>
        </div>
        <div class="card-body" style="padding:0">
          <div class="table-container">
            <table>
              <thead>
                <tr>
                  <th>Name</th>
                  <th>API Key</th>
                  <th>Secret Key</th>
                  <th>Scope</th>
                  <th>Status</th>
                  <th>Last Used</th>
                  <th>Expires</th>
                  <th>Actions</th>
                </tr>
              </thead>
              <tbody>
                ${state.apiKeys.length ? state.apiKeys.map((key) => `
                  <tr>
                    <td>
                      <p style="font-weight:600">${escapeHTML(key.name)}</p>
                    </td>
                    <td>
                      <code class="inline-code">${escapeHTML(key.id || "-")}</code>
                      <p style="font-size:12px;color:var(--color-text-tertiary);margin-top:4px">Prefix ${escapeHTML(key.key_prefix || "-")}</p>
                    </td>
                    <td><span style="font-size:13px;color:var(--color-text-secondary)">Shown once at creation</span></td>
                    <td>${badge(key.scopes || "send_email")}</td>
                    <td>${badge(key.is_active ? "active" : "disabled")}</td>
                    <td style="font-size:13px;color:var(--color-text-secondary)">${dateTime(key.last_used_at)}</td>
                    <td style="font-size:13px;color:var(--color-text-secondary)">${key.expires_at ? dateTime(key.expires_at) : "Never"}</td>
                    <td>
                      <div style="display:flex;gap:4px">
                        <button data-api-key-copy="${key.id}" class="icon-btn" title="Copy API Key">
                          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>
                        </button>
                        ${key.is_active ? `
                          <button data-api-key-revoke="${key.id}" class="icon-btn" title="Revoke" style="color:var(--color-warning)">
                            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><line x1="4.93" y1="4.93" x2="19.07" y2="19.07"/></svg>
                          </button>
                        ` : ""}
                        <button data-api-key-delete="${key.id}" class="icon-btn" title="Delete" style="color:var(--color-danger)">
                          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="3 6 5 6 21 6"/><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6"/><path d="M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/></svg>
                        </button>
                      </div>
                    </td>
                  </tr>
                `).join("") : `
                  <tr>
                    <td colspan="8">
                      <div class="empty-state">
                        <div class="empty-state-icon">
                          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M15 7h.01"/><path d="M10.5 12.5 8 15l-2-2-3 3 2 2 3-3 2 2 2.5-2.5"/><path d="M16 3a5 5 0 0 1 3.54 8.54l-7 7A5 5 0 0 1 5.46 11.46l7-7A5 5 0 0 1 16 3Z"/></svg>
                        </div>
                        <p class="empty-state-title">No API keys yet</p>
                        <p class="empty-state-desc">Create a key to connect WordPress or another SMTP client.</p>
                      </div>
                    </td>
                  </tr>
                `}
              </tbody>
            </table>
          </div>
        </div>
      </div>

      ${renderSmtpSettingsPanel(state.smtpSettings)}
    `;

    document.getElementById("refreshApiKeysBtn").onclick = renderApiKeys;
    document.getElementById("createApiKeyBtn").onclick = openCreateApiKeyModal;
    document.querySelectorAll("[data-api-key-copy]").forEach((button) => {
      button.onclick = async () => {
        await copyText(button.dataset.apiKeyCopy);
        button.title = "Copied";
      };
    });
    document.querySelectorAll("[data-api-key-revoke]").forEach((button) => {
      button.onclick = async () => {
        const key = state.apiKeys.find((row) => row.id === button.dataset.apiKeyRevoke);
        if (!confirm(`Revoke API key "${key?.name || "this key"}"? Existing SMTP clients using it will stop working.`)) return;
        try {
          await api(`/api-keys/${button.dataset.apiKeyRevoke}/revoke`, { method: "POST" });
          await renderApiKeys();
        } catch (error) {
          alert(error.message);
        }
      };
    });
    document.querySelectorAll("[data-api-key-delete]").forEach((button) => {
      button.onclick = async () => {
        const key = state.apiKeys.find((row) => row.id === button.dataset.apiKeyDelete);
        if (!confirm(`Delete API key "${key?.name || "this key"}"?`)) return;
        try {
          await api(`/api-keys/${button.dataset.apiKeyDelete}`, { method: "DELETE" });
          await renderApiKeys();
        } catch (error) {
          alert(error.message);
        }
      };
    });
  } catch (error) {
    els.pageContent.innerHTML = `
      <div class="empty-state">
        <p class="empty-state-title">Failed to load API keys</p>
        <p class="empty-state-desc">${escapeHTML(error.message)}</p>
        <button onclick="renderApiKeys()" class="btn btn-secondary btn-sm" style="margin-top:12px">Retry</button>
      </div>
    `;
  }
}

function defaultSmtpSettings() {
  const base = getBaseDomain();
  return {
    host: base === "localhost" ? "smtp.localhost" : `smtp.${base}`,
    port_587: "587",
    port_465: "465",
    recommended_security: "STARTTLS on 587, implicit TLS on 465",
    username_format: "api_key",
    password_format: "secret_key"
  };
}

function renderSmtpSettingsPanel(settings) {
  const s = settings || defaultSmtpSettings();
  return `
    <div class="card">
      <div class="card-header">
        <div>
          <h3>SMTP Configuration</h3>
          <p class="sub">Use these values in your SMTP plugin or external app.</p>
        </div>
      </div>
      <div class="card-body">
        <div class="smtp-settings-grid">
          <div class="smtp-setting">
            <span class="smtp-setting-label">Host</span>
            <code class="smtp-setting-value">${escapeHTML(s.host || "-")}</code>
          </div>
          <div class="smtp-setting">
            <span class="smtp-setting-label">Port</span>
            <code class="smtp-setting-value">${escapeHTML(s.port_587 || "587")}</code>
            <span class="smtp-setting-note">STARTTLS</span>
          </div>
          <div class="smtp-setting">
            <span class="smtp-setting-label">TLS Port</span>
            <code class="smtp-setting-value">${escapeHTML(s.port_465 || "465")}</code>
            <span class="smtp-setting-note">Implicit TLS</span>
          </div>
          <div class="smtp-setting">
            <span class="smtp-setting-label">Username</span>
            <code class="smtp-setting-value">API Key</code>
          </div>
          <div class="smtp-setting">
            <span class="smtp-setting-label">Password</span>
            <code class="smtp-setting-value">Secret Key</code>
          </div>
          <div class="smtp-setting">
            <span class="smtp-setting-label">Security</span>
            <code class="smtp-setting-value">${escapeHTML(s.recommended_security || "STARTTLS on 587")}</code>
          </div>
        </div>
      </div>
    </div>
  `;
}

function openCreateApiKeyModal() {
  openModal("Create API Key", `
    <form id="apiKeyForm">
      <div class="form-group">
        <label for="apiKeyName">Name</label>
        <input id="apiKeyName" name="name" placeholder="WordPress production" required />
      </div>
      <div class="form-group">
        <label for="apiKeyScope">Scope</label>
        <select id="apiKeyScope" name="scope">
          <option value="send_email">Send email</option>
          <option value="full_access">Full access</option>
        </select>
      </div>
      <button type="submit" class="btn btn-primary btn-full">Create API Key</button>
      <p id="apiKeyFormMessage" class="form-message hidden"></p>
    </form>
  `);
  document.getElementById("apiKeyForm").onsubmit = submitApiKeyForm;
  setTimeout(() => document.getElementById("apiKeyName")?.focus(), 0);
}

async function submitApiKeyForm(event) {
  event.preventDefault();
  const form = event.currentTarget;
  const message = document.getElementById("apiKeyFormMessage");
  const name = form.elements.name.value.trim();
  const scope = form.elements.scope.value;

  if (!name) {
    flash(message, "Name is required.", false);
    return;
  }

  try {
    const created = await api("/api-keys", {
      method: "POST",
      body: JSON.stringify({ name, scope })
    });
    showCreatedApiKey(created);
  } catch (error) {
    flash(message, error.message, false);
  }
}

function showCreatedApiKey(created) {
  const settings = created.smtp_settings || state.smtpSettings || defaultSmtpSettings();
  const apiKey = created.api_key || created.id || "";
  const secretKey = created.secret_key || created.full_api_key || "";
  openModal("API Key Created", `
    <div class="api-key-reveal">
      <p class="api-key-reveal-note">The Secret Key is shown once. Use API Key as the SMTP username and Secret Key as the SMTP password.</p>
      <div class="form-group">
        <label>API Key</label>
        <div class="copy-field">
          <code id="createdApiKeyValue">${escapeHTML(apiKey)}</code>
          <button id="copyCreatedApiKeyBtn" class="btn btn-secondary btn-sm">Copy API Key</button>
        </div>
      </div>
      <div class="form-group">
        <label>Secret Key</label>
        <div class="copy-field">
          <code id="createdSecretKeyValue">${escapeHTML(secretKey)}</code>
          <button id="copyCreatedSecretKeyBtn" class="btn btn-secondary btn-sm">Copy Secret</button>
        </div>
      </div>
      <div class="smtp-settings-grid" style="margin-top:12px">
        <div class="smtp-setting">
          <span class="smtp-setting-label">SMTP host</span>
          <code class="smtp-setting-value">${escapeHTML(settings.host || "-")}</code>
        </div>
        <div class="smtp-setting">
          <span class="smtp-setting-label">Username</span>
          <code class="smtp-setting-value">${escapeHTML(apiKey)}</code>
        </div>
        <div class="smtp-setting">
          <span class="smtp-setting-label">Password</span>
          <code class="smtp-setting-value">Secret Key</code>
        </div>
        <div class="smtp-setting">
          <span class="smtp-setting-label">Port</span>
          <code class="smtp-setting-value">${escapeHTML(settings.port_587 || "587")}</code>
          <span class="smtp-setting-note">STARTTLS</span>
        </div>
        <div class="smtp-setting">
          <span class="smtp-setting-label">TLS Port</span>
          <code class="smtp-setting-value">${escapeHTML(settings.port_465 || "465")}</code>
          <span class="smtp-setting-note">Implicit TLS</span>
        </div>
      </div>
      <button id="doneCreatedApiKeyBtn" class="btn btn-primary btn-full" style="margin-top:16px">Done</button>
    </div>
  `);
  document.getElementById("copyCreatedApiKeyBtn").onclick = async () => {
    await copyText(apiKey);
    document.getElementById("copyCreatedApiKeyBtn").textContent = "Copied";
  };
  document.getElementById("copyCreatedSecretKeyBtn").onclick = async () => {
    await copyText(secretKey);
    document.getElementById("copyCreatedSecretKeyBtn").textContent = "Copied";
  };
  document.getElementById("doneCreatedApiKeyBtn").onclick = async () => {
    closeModal();
    await renderApiKeys();
  };
}

async function copyText(value) {
  if (!value) return;
  if (navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(value);
    return;
  }
  const input = document.createElement("textarea");
  input.value = value;
  input.style.position = "fixed";
  input.style.opacity = "0";
  document.body.appendChild(input);
  input.select();
  document.execCommand("copy");
  input.remove();
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
                <th>Websites</th>
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
                  <td style="color:var(--color-text-secondary)">${user.max_websites}</td>
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
                  <td colspan="10">
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
      <div class="grid-2 grid-2-equal" style="gap:12px">
        <div class="form-group">
          <label for="maxStorageGB">Max storage GB</label>
          <input id="maxStorageGB" name="max_storage_gb" type="number" min="1" step="0.1" value="${gb(user.max_storage_bytes)}" />
        </div>
        <div class="form-group">
          <label for="maxWebsites">Max websites</label>
          <input id="maxWebsites" name="max_websites" type="number" min="0" step="1" value="${user.max_websites ?? 5}" />
        </div>
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
      max_storage_bytes: Math.round(maxStorageGB * 1024 * 1024 * 1024),
      max_websites: Number(form.elements.max_websites.value)
    };

    if (
      payload.max_domains < 0 ||
      payload.max_inboxes < 0 ||
      payload.max_message_size_mb < 1 ||
      payload.max_attachment_size_mb < 1 ||
      payload.max_storage_bytes < 1 ||
      payload.max_websites < 0 ||
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

// =============================================
// WEBSITES VIEW
// =============================================

async function renderWebsites() {
  setView("websites");
  els.pageContent.innerHTML = `<div style="text-align:center;padding:48px"><p style="color:var(--color-text-tertiary)">Loading websites...</p></div>`;

  try {
    const projects = await api("/static-projects");
    state.websites = projects || [];
    const used = projects?.[0]?.websites_used || state.websites.length;
    const max = projects?.[0]?.max_websites || currentUser?.max_websites || 5;
    state.websiteQuota = { used, max };

    els.pageContent.innerHTML = `
      <div style="display:flex;align-items:center;justify-content:space-between;margin-bottom:20px">
        <div>
          <div style="font-size:13px;color:var(--color-text-secondary)">
            <strong>${used}</strong> / <strong>${max}</strong> websites used
            <span style="margin-left:8px;font-size:12px;color:var(--color-text-tertiary)">${max - used} remaining</span>
          </div>
        </div>
        <button id="deployWebsiteBtn" class="btn btn-primary btn-sm">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 15v4a2 2 0 01-2 2H5a2 2 0 01-2-2v-4"/><polyline points="17 8 12 3 7 8"/><line x1="12" y1="3" x2="12" y2="15"/></svg>
          Deploy Website
        </button>
      </div>
      ${state.websites.length ? `
        <div class="websites-grid">
          ${state.websites.map((site) => `
            <div class="website-card">
              <div class="website-card-thumb">
                ${websiteThumbnailURL(site)
                  ? `<img src="${escapeHTML(websiteThumbnailURL(site))}" alt="${escapeHTML(site.name)}" />`
                  : `<div class="website-card-placeholder">${initials(site.name)}</div>`
                }
              </div>
              <div class="website-card-body">
                <div class="website-card-top">
                  <h4 class="website-card-name">${escapeHTML(site.name)}</h4>
                  ${badge(site.ui_state || site.status)}
                </div>
                <p class="website-card-url">${escapeHTML(site.subdomain ? site.subdomain + "." + getBaseDomain() : "")}</p>
                <p class="website-card-meta">${bytes(site.archive_size_bytes || 0)} &middot; ${site.file_count || 0} files &middot; ${relative(site.published_at || site.created_at)}</p>
                ${site.deploy_error ? `<p class="website-card-error">${escapeHTML(site.deploy_error)}</p>` : ""}
              </div>
              <div class="website-card-actions">
                <button data-website-open="${site.id}" class="btn btn-secondary btn-xs" ${site.ui_state !== "live" ? "disabled" : ""}>Open</button>
                <button data-website-settings="${site.id}" class="btn btn-secondary btn-xs">Settings</button>
                <button data-website-delete="${site.id}" class="btn btn-ghost btn-xs" style="color:var(--color-danger)">Delete</button>
              </div>
            </div>
          `).join("")}
        </div>
      ` : `
        <div class="card">
          <div class="card-body">
            <div class="empty-state">
              <div class="empty-state-icon">
                <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="10"/><line x1="2" y1="12" x2="22" y2="12"/><path d="M12 2a15.3 15.3 0 014 10 15.3 15.3 0 01-4 10 15.3 15.3 0 01-4-10 15.3 15.3 0 014-10z"/></svg>
              </div>
              <p class="empty-state-title">No websites yet</p>
              <p class="empty-state-desc">Deploy your first static website by uploading a ZIP archive.</p>
            </div>
          </div>
        </div>
      `}
    `;

    document.getElementById("deployWebsiteBtn").onclick = openDeployModal;
    document.querySelectorAll("[data-website-open]").forEach((btn) => {
      btn.onclick = () => {
        const site = state.websites.find((s) => s.id === btn.dataset.websiteOpen);
        if (site) {
          const url = `http://${site.subdomain}.${getBaseDomain()}`;
          window.open(url, "_blank");
        }
      };
    });
    document.querySelectorAll("[data-website-settings]").forEach((btn) => {
      btn.onclick = () => renderWebsiteSettings(btn.dataset.websiteSettings);
    });
    document.querySelectorAll("[data-website-delete]").forEach((btn) => {
      btn.onclick = async () => {
        const site = state.websites.find((s) => s.id === btn.dataset.websiteDelete);
        if (!confirm(`Delete website "${site?.name || "this project"}"? This cannot be undone.`)) return;
        try {
          await api(`/static-projects/${btn.dataset.websiteDelete}`, { method: "DELETE" });
          await renderWebsites();
        } catch (error) {
          alert(error.message);
        }
      };
    });
  } catch (error) {
    els.pageContent.innerHTML = `
      <div class="card">
        <div class="card-body">
          <div class="empty-state">
            <div class="empty-state-icon">
              <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="12"/><line x1="12" y1="16" x2="12.01" y2="16"/></svg>
            </div>
            <p class="empty-state-title">Failed to load websites</p>
            <p class="empty-state-desc">${escapeHTML(error.message)}</p>
            <button onclick="renderWebsites()" class="btn btn-secondary btn-sm" style="margin-top:12px">Retry</button>
          </div>
        </div>
      </div>
    `;
  }
}

function openDeployModal() {
  openModal("Deploy New Website", `
    <form id="deployForm">
      <div class="form-group">
        <label for="deployName">Website name</label>
        <input id="deployName" name="name" placeholder="My Website" value="My Website" />
      </div>
      <div class="form-group">
        <label for="deployFile">ZIP archive</label>
        <input id="deployFile" name="file" type="file" accept=".zip" required />
        <p style="font-size:12px;color:var(--color-text-tertiary);margin-top:4px">Upload a ZIP file containing HTML, CSS, JS, and assets.</p>
      </div>
      <button type="submit" class="btn btn-primary btn-full" id="deploySubmitBtn">
        <span id="deployBtnText">Upload & Deploy</span>
        <span id="deployBtnSpinner" class="hidden" style="display:none">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="spin"><circle cx="12" cy="12" r="10"/><path d="M12 6v6l4 2"/></svg>
          Deploying...
        </span>
      </button>
      <p id="deployFormMessage" class="form-message hidden"></p>
    </form>
  `);
  document.getElementById("deployForm").onsubmit = submitDeployForm;
  setTimeout(() => document.getElementById("deployName")?.focus(), 0);
}

async function submitDeployForm(event) {
  event.preventDefault();
  const form = event.currentTarget;
  const message = document.getElementById("deployFormMessage");
  const submitBtn = document.getElementById("deploySubmitBtn");
  const btnText = document.getElementById("deployBtnText");
  const btnSpinner = document.getElementById("deployBtnSpinner");

  const name = form.elements.name.value.trim() || "My Website";
  const fileInput = form.elements.file;
  if (!fileInput.files?.length) {
    flash(message, "Please select a ZIP file.", false);
    return;
  }

  const formData = new FormData();
  formData.append("name", name);
  formData.append("file", fileInput.files[0]);

  submitBtn.disabled = true;
  btnText.style.display = "none";
  btnSpinner.style.display = "inline";
  flash(message, "", false);

  try {
    await api("/static-projects/deploy", {
      method: "POST",
      body: formData
    });
    closeModal();
    await renderWebsites();
  } catch (error) {
    flash(message, error.message, false);
    submitBtn.disabled = false;
    btnText.style.display = "inline";
    btnSpinner.style.display = "none";
  }
}

// --- Website Settings ---
async function renderWebsiteSettings(projectID) {
  setView(`websites/${projectID}`);

  els.pageContent.innerHTML = `<div style="text-align:center;padding:48px"><p style="color:var(--color-text-tertiary)">Loading...</p></div>`;

  try {
    const project = await api(`/static-projects/${projectID}`);
    const baseUrl = getBaseDomain();
    const siteUrl = project.subdomain ? `${project.subdomain}.${baseUrl}` : "";

    els.pageContent.innerHTML = `
      <div style="display:flex;align-items:center;justify-content:space-between;margin-bottom:20px">
        <div>
          <p style="font-size:13px;color:var(--color-text-tertiary)">Website / ${escapeHTML(project.name)}</p>
          <h2 style="font-size:22px;font-weight:700;margin-top:2px">${escapeHTML(project.name)}</h2>
        </div>
        <div style="display:flex;gap:8px">
          <button id="backToWebsitesBtn" class="btn btn-secondary btn-sm">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="19" y1="12" x2="5" y2="12"/><polyline points="12 19 5 12 12 5"/></svg>
            Back
          </button>
          ${siteUrl ? `<a href="http://${escapeHTML(siteUrl)}" target="_blank" class="btn btn-primary btn-sm">Open Site</a>` : ""}
        </div>
      </div>

      <div style="display:flex;gap:8px;margin-bottom:20px;border-bottom:1px solid var(--color-border);padding-bottom:0">
        <button class="website-tab active" data-tab="overview">Overview</button>
        <button class="website-tab" data-tab="upload">Upload New Version</button>
        <button class="website-tab" data-tab="domains">Domains</button>
      </div>

      <div id="websiteSettingsContent">
        ${renderWebsiteOverview(project, siteUrl)}
      </div>
    `;

    document.getElementById("backToWebsitesBtn").onclick = () => renderWebsites();

    document.querySelectorAll(".website-tab").forEach((tab) => {
      tab.onclick = () => {
        document.querySelectorAll(".website-tab").forEach((t) => t.classList.remove("active"));
        tab.classList.add("active");
        const tabName = tab.dataset.tab;
        const content = document.getElementById("websiteSettingsContent");
        if (tabName === "overview") {
          content.innerHTML = renderWebsiteOverview(project, siteUrl);
          wireOverviewHandlers(project.id);
        } else if (tabName === "upload") {
          content.innerHTML = renderWebsiteUploadTab(project);
          wireUploadHandlers(project.id);
        } else if (tabName === "domains") {
          content.innerHTML = renderWebsiteDomainsTab(project);
          wireDomainHandlers(project);
        }
      };
    });

    // Wire initial handlers
    wireOverviewHandlers(project.id);
  } catch (error) {

    els.pageContent.innerHTML = `
      <div class="card">
        <div class="card-body">
          <div class="empty-state">
            <p class="empty-state-title">Failed to load website</p>
            <p class="empty-state-desc">${escapeHTML(error.message)}</p>
            <button onclick="renderWebsites()" class="btn btn-secondary btn-sm" style="margin-top:12px">Back to Websites</button>
          </div>
        </div>
      </div>
    `;
  }
}

function renderWebsiteOverview(project, siteUrl) {
  return `
    <div class="grid-2 grid-2-equal">
      <div class="card">
        <div class="card-header"><h3>Details</h3></div>
        <div class="card-body">
          <div class="info-row">
            <span class="info-row-label">Status</span>
            <span class="info-row-value">${badge(project.ui_state || project.status)}</span>
          </div>
          <div class="info-row">
            <span class="info-row-label">Subdomain</span>
            <span class="info-row-value" style="font-size:12px">${escapeHTML(project.subdomain || "-")}</span>
          </div>
          <div class="info-row">
            <span class="info-row-label">Custom Domain</span>
            <span class="info-row-value">${escapeHTML(project.assigned_domain || "-")}</span>
          </div>
          <div class="info-row">
            <span class="info-row-label">Domain Binding</span>
            <span class="info-row-value">${badge(project.domain_binding_status || "none")}</span>
          </div>
          <div class="info-row">
            <span class="info-row-label">Archive Size</span>
            <span class="info-row-value">${bytes(project.archive_size_bytes || 0)}</span>
          </div>
          <div class="info-row">
            <span class="info-row-label">Files</span>
            <span class="info-row-value">${project.file_count || 0}</span>
          </div>
          <div class="info-row">
            <span class="info-row-label">Published</span>
            <span class="info-row-value">${relative(project.published_at)}</span>
          </div>
          <div class="info-row">
            <span class="info-row-label">Detected Root</span>
            <span class="info-row-value" style="font-size:12px">${escapeHTML(project.detected_root || "-")}</span>
          </div>
        </div>
      </div>

      <div class="card">
        <div class="card-header"><h3>Actions</h3></div>
        <div class="card-body" style="display:flex;flex-direction:column;gap:8px">
          <button id="toggleWebsiteBtn" class="btn ${project.is_active ? "btn-secondary" : "btn-primary"} btn-full">
            ${project.is_active ? "Disable Website" : "Enable Website"}
          </button>
          ${!project.is_active ? `<p style="font-size:12px;color:var(--color-text-tertiary)">Disabled websites return 404 to visitors.</p>` : ""}
          <button id="deleteWebsiteBtn" class="btn btn-danger btn-full">Delete Website</button>
        </div>
      </div>
    </div>
  `;
}

function renderWebsiteUploadTab(project) {
  return `
    <div class="card">
      <div class="card-header"><h3>Upload New Version</h3></div>
      <div class="card-body">
        <p style="font-size:13px;color:var(--color-text-secondary);margin-bottom:16px">Upload a new ZIP archive to replace the current website content. The subdomain and settings remain unchanged.</p>
        <form id="redeployForm">
          <div class="form-group">
            <label for="redeployFile">ZIP archive</label>
            <input id="redeployFile" name="file" type="file" accept=".zip" required />
          </div>
          <button type="submit" class="btn btn-primary btn-full" id="redeploySubmitBtn">
            <span id="redeployBtnText">Upload & Redeploy</span>
            <span id="redeployBtnSpinner" class="hidden" style="display:none">
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="spin"><circle cx="12" cy="12" r="10"/><path d="M12 6v6l4 2"/></svg>
              Deploying...
            </span>
          </button>
          <p id="redeployFormMessage" class="form-message hidden"></p>
        </form>
      </div>
    </div>
  `;
}

function renderWebsiteDomainsTab(project) {
  return `
    <div class="card" style="margin-bottom:20px">
      <div class="card-header"><h3>Custom Domain</h3></div>
      <div class="card-body">
        ${project.assigned_domain ? `
          <div class="info-row">
            <span class="info-row-label">Assigned Domain</span>
            <span class="info-row-value">${escapeHTML(project.assigned_domain)}</span>
          </div>
          <div class="info-row">
            <span class="info-row-label">Binding Status</span>
            <span class="info-row-value">${badge(project.domain_binding_status || "none")}</span>
          </div>
          <div class="info-row">
            <span class="info-row-label">DNS Check</span>
            <span class="info-row-value">${escapeHTML(project.domain_last_dns_result || "Not checked")}</span>
          </div>
          <div style="display:flex;gap:8px;margin-top:16px">
            <button id="checkDomainIPBtn" class="btn btn-secondary btn-sm">Check DNS</button>
            <button id="unassignDomainBtn" class="btn btn-ghost btn-sm" style="color:var(--color-danger)">Unassign</button>
          </div>
        ` : `
          <p style="font-size:13px;color:var(--color-text-secondary);margin-bottom:12px">Assign a verified domain to this website.</p>
          <div id="availableDomainsContainer">
            <p style="font-size:13px;color:var(--color-text-tertiary)">Loading available domains...</p>
          </div>
        `}
      </div>
    </div>
    ${!project.assigned_domain ? `
    <div class="card">
      <div class="card-header"><h3>Available Domains</h3></div>
      <div class="card-body" id="availableDomainsList">
        <p style="font-size:13px;color:var(--color-text-tertiary)">Loading...</p>
      </div>
    </div>
    ` : ""}
  `;
}

// --- Website Settings Event Handlers ---
function wireOverviewHandlers(projectID) {
  const toggleBtn = document.getElementById("toggleWebsiteBtn");
  const deleteBtn = document.getElementById("deleteWebsiteBtn");
  if (toggleBtn) {
    toggleBtn.onclick = async () => {
      try {
        const project = await api(`/static-projects/${projectID}`);
        await api(`/static-projects/${projectID}/status`, {
          method: "PATCH",
          body: JSON.stringify({ is_active: !project.is_active })
        });
        await renderWebsiteSettings(projectID);
      } catch (error) {
        alert(error.message);
      }
    };
  }
  if (deleteBtn) {
    deleteBtn.onclick = async () => {
      if (!confirm("Delete this website? This cannot be undone.")) return;
      try {
        await api(`/static-projects/${projectID}`, { method: "DELETE" });
        await renderWebsites();
      } catch (error) {
        alert(error.message);
      }
    };
  }
}

function wireUploadHandlers(projectID) {
  const form = document.getElementById("redeployForm");
  if (!form) return;
  form.onsubmit = async (event) => {
    event.preventDefault();
    const message = document.getElementById("redeployFormMessage");
    const submitBtn = document.getElementById("redeploySubmitBtn");
    const btnText = document.getElementById("redeployBtnText");
    const btnSpinner = document.getElementById("redeployBtnSpinner");

    const fileInput = form.elements.file;
    if (!fileInput.files?.length) {
      flash(message, "Please select a ZIP file.", false);
      return;
    }

    const formData = new FormData();
    formData.append("file", fileInput.files[0]);

    submitBtn.disabled = true;
    btnText.style.display = "none";
    btnSpinner.style.display = "inline";
    flash(message, "", false);

    try {
      await api(`/static-projects/${projectID}/redeploy`, {
        method: "POST",
        body: formData
      });
      await renderWebsiteSettings(projectID);
    } catch (error) {
      flash(message, error.message, false);
      submitBtn.disabled = false;
      btnText.style.display = "inline";
      btnSpinner.style.display = "none";
    }
  };
}

function wireDomainHandlers(project) {
  const projectID = project.id;

  // Load available domains if not assigned
  if (!project.assigned_domain) {
    (async () => {
      try {
        const domains = await api(`/static-projects/${projectID}/available-domains`);
        const container = document.getElementById("availableDomainsContainer");
        const list = document.getElementById("availableDomainsList");
        if (!domains || !domains.length) {
          const msg = '<p style="font-size:13px;color:var(--color-text-tertiary)">No verified domains available. Add and verify a domain first.</p>';
          if (container) container.innerHTML = msg;
          if (list) list.innerHTML = msg;
          return;
        }
        if (container) {
          container.innerHTML = domains.map((d) => `
            <button class="btn btn-secondary btn-sm assign-domain-btn" data-domain-id="${d.id}" style="margin-right:6px;margin-bottom:6px">
              ${escapeHTML(d.name)}
            </button>
          `).join("");
        }
        if (list) {
          list.innerHTML = domains.map((d) => `
            <div class="info-row">
              <span class="info-row-label">${escapeHTML(d.name)}</span>
              <button class="btn btn-secondary btn-xs assign-domain-btn" data-domain-id="${d.id}">Assign</button>
            </div>
          `).join("");
        }
        document.querySelectorAll(".assign-domain-btn").forEach((btn) => {
          btn.onclick = async () => {
            try {
              await api(`/static-projects/${projectID}/domain`, {
                method: "PATCH",
                body: JSON.stringify({ domain_id: btn.dataset.domainId })
              });
              await renderWebsiteSettings(projectID);
            } catch (error) {
              alert(error.message);
            }
          };
        });
      } catch (error) {
        const container = document.getElementById("availableDomainsContainer");
        if (container) container.innerHTML = `<p style="font-size:13px;color:var(--color-danger)">${escapeHTML(error.message)}</p>`;
      }
    })();
  }

  // Wire existing domain actions
  const checkBtn = document.getElementById("checkDomainIPBtn");
  const unassignBtn = document.getElementById("unassignDomainBtn");

  if (checkBtn) {
    checkBtn.onclick = async () => {
      try {
        const result = await api(`/static-projects/${projectID}/domain/check-ip`, { method: "POST" });
        await renderWebsiteSettings(projectID);
      } catch (error) {
        alert(error.message);
      }
    };
  }
  if (unassignBtn) {
    unassignBtn.onclick = async () => {
      if (!confirm("Unassign this domain from the website?")) return;
      try {
        await api(`/static-projects/${projectID}/domain`, { method: "DELETE" });
        await renderWebsiteSettings(projectID);
      } catch (error) {
        alert(error.message);
      }
    };
  }
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
