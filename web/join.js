/* ============================================
   GoMail — Join Team via Invite
   ============================================ */

const params = new URLSearchParams(location.search);
const inviteToken = params.get("token");

const $ = (id) => document.getElementById(id);
const els = {
  inviteInfo: $("invite-info"),
  loggedInSection: $("logged-in-section"),
  guestSection: $("guest-section"),
  resultSection: $("result-section"),
  resultMessage: $("result-message"),
};

if (!inviteToken) {
  els.inviteInfo.innerHTML = '<p class="error">Missing invite token. Please use the link from your invitation email.</p>';
} else {
  loadInvitePreview();
}

async function loadInvitePreview() {
  try {
    const resp = await fetch(`/api/team-invites/${inviteToken}`);
    const data = await resp.json();

    if (!resp.ok) {
      els.inviteInfo.innerHTML = `<p class="error">${data.message || "Invite not found or expired."}</p>`;
      return;
    }

    const status = data.status;
    if (status !== "pending") {
      els.inviteInfo.innerHTML = `<p class="error">This invite is no longer pending (status: ${status}).</p>`;
      return;
    }

    els.inviteInfo.innerHTML = `
      <p><strong>${escHtml(data.inviter_name)}</strong> invited you to join <strong>${escHtml(data.team_name)}</strong></p>
      <p>Role: <span class="badge">${escHtml(data.role)}</span></p>
      <p class="text-muted small">Invite sent to: ${escHtml(data.email)}</p>
    `;

    // Pre-fill register email
    $("register-email").value = data.email;
    $("login-email").value = data.email;

    const token = localStorage.getItem("access_token");
    if (token) {
      els.loggedInSection.classList.remove("hidden");
    } else {
      els.guestSection.classList.remove("hidden");
      $("register-password").focus();
    }
  } catch (err) {
    els.inviteInfo.innerHTML = '<p class="error">Failed to load invite. Please try again.</p>';
  }
}

// ── Already logged in ──────────────────────────────────────────────────────
$("accept-btn").addEventListener("click", async () => {
  const token = localStorage.getItem("access_token");
  try {
    const resp = await fetch(`/api/team-invites/${inviteToken}/accept`, {
      method: "POST",
      headers: { Authorization: "Bearer " + token, "Content-Type": "application/json" },
    });
    const data = await resp.json();
    if (!resp.ok) {
      throw new Error(data.message || "Failed to accept invite");
    }
    showResult("You've joined the team!", true);
  } catch (err) {
    els.resultMessage.textContent = err.message;
    els.resultSection.classList.remove("hidden");
  }
});

$("decline-btn").addEventListener("click", async () => {
  const token = localStorage.getItem("access_token");
  await fetch(`/api/team-invites/${inviteToken}/decline`, {
    method: "POST",
    headers: { Authorization: "Bearer " + token, "Content-Type": "application/json" },
  });
  showResult("Invite declined.", false);
});

// ── Login & Accept ─────────────────────────────────────────────────────────
$("login-btn").addEventListener("click", async () => {
  const email = $("login-email").value.trim();
  const password = $("login-password").value;
  const errEl = $("login-error");
  errEl.classList.add("hidden");

  try {
    // Step 1: Login
    const loginResp = await fetch("/api/auth/login", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ email, password }),
    });
    const loginData = await loginResp.json();
    if (!loginResp.ok) throw new Error(loginData.message || "Login failed");

    localStorage.setItem("access_token", loginData.access_token);
    localStorage.setItem("refresh_token", loginData.refresh_token);

    // Step 2: Accept invite
    const acceptResp = await fetch(`/api/team-invites/${inviteToken}/accept`, {
      method: "POST",
      headers: { Authorization: "Bearer " + loginData.access_token, "Content-Type": "application/json" },
    });
    const acceptData = await acceptResp.json();
    if (!acceptResp.ok) throw new Error(acceptData.message || "Failed to accept invite");

    showResult("Welcome! You've joined the team.", true);
  } catch (err) {
    errEl.textContent = err.message;
    errEl.classList.remove("hidden");
  }
});

// ── Register & Join ────────────────────────────────────────────────────────
$("register-btn").addEventListener("click", async () => {
  const name = $("register-name").value.trim();
  const email = $("register-email").value.trim();
  const password = $("register-password").value;
  const confirmPassword = $("register-password-confirm").value;
  const errEl = $("register-error");
  errEl.classList.add("hidden");

  if (password.length < 8) {
    errEl.textContent = "Password must be at least 8 characters";
    errEl.classList.remove("hidden");
    return;
  }
  if (password !== confirmPassword) {
    errEl.textContent = "Passwords do not match";
    errEl.classList.remove("hidden");
    return;
  }

  try {
    const resp = await fetch(`/api/team-invites/${inviteToken}/register`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name, email, password }),
    });
    const data = await resp.json();
    if (!resp.ok) throw new Error(data.message || "Registration failed");

    localStorage.setItem("access_token", data.access_token);
    localStorage.setItem("refresh_token", data.refresh_token);

    showResult("Account created! Welcome to the team.", true);
  } catch (err) {
    errEl.textContent = err.message;
    errEl.classList.remove("hidden");
  }
});

// ── Result ─────────────────────────────────────────────────────────────────
function showResult(message, success) {
  els.loggedInSection.classList.add("hidden");
  els.guestSection.classList.add("hidden");
  els.resultMessage.textContent = message;
  els.resultMessage.className = success ? "success" : "error";
  els.resultSection.classList.remove("hidden");
}

// ── Tab switching ──────────────────────────────────────────────────────────
document.querySelectorAll(".tab").forEach(tab => {
  tab.addEventListener("click", () => {
    document.querySelectorAll(".tab").forEach(t => t.classList.remove("active"));
    tab.classList.add("active");
    document.querySelectorAll(".tab-content").forEach(c => c.classList.add("hidden"));
    document.getElementById(tab.dataset.tab).classList.remove("hidden");
  });
});

function escHtml(s) {
  const d = document.createElement("div");
  d.textContent = s || "";
  return d.innerHTML;
}
