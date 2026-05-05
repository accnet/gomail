/* ============================================
   GoMail — Login / Register Page
   ============================================ */

const api = async (path, options = {}) => {
  const res = await fetch(`/api${path}`, {
    ...options,
    headers: {
      "Content-Type": "application/json",
      ...(options.headers || {})
    }
  });
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

function clearStoredSession() {
  localStorage.removeItem("access_token");
  localStorage.removeItem("refresh_token");
  localStorage.removeItem("active_team_id");
}

// --- DOM refs ---
const els = {
  tabSignin: document.getElementById("tab-signin"),
  tabSignup: document.getElementById("tab-signup"),
  loginForm: document.getElementById("login-form"),
  registerForm: document.getElementById("register-form"),
  loginEmail: document.getElementById("login-email"),
  loginPassword: document.getElementById("login-password"),
  loginError: document.getElementById("login-error"),
  registerName: document.getElementById("register-name"),
  registerEmail: document.getElementById("register-email"),
  registerPassword: document.getElementById("register-password"),
  registerError: document.getElementById("register-error"),
  registerSuccess: document.getElementById("register-success")
};

// --- Tab switching ---
function showTab(tab) {
  els.tabSignin.classList.toggle("active", tab === "signin");
  els.tabSignup.classList.toggle("active", tab === "signup");
  els.loginForm.classList.toggle("hidden", tab !== "signin");
  els.registerForm.classList.toggle("hidden", tab !== "signup");
  // Clear messages
  els.loginError.classList.add("hidden");
  els.registerError.classList.add("hidden");
  els.registerSuccess.classList.add("hidden");
}

els.tabSignin.onclick = () => showTab("signin");
els.tabSignup.onclick = () => showTab("signup");

// --- Login ---
els.loginForm.onsubmit = async (e) => {
  e.preventDefault();
  els.loginError.classList.add("hidden");
  try {
    const data = await api("/auth/login", {
      method: "POST",
      body: JSON.stringify({
        email: els.loginEmail.value.trim(),
        password: els.loginPassword.value
      })
    });
    localStorage.setItem("access_token", data.access_token);
    localStorage.setItem("refresh_token", data.refresh_token);
    localStorage.removeItem("active_team_id");
    window.location.href = "/app/";
  } catch (error) {
    els.loginError.textContent = error.message;
    els.loginError.classList.remove("hidden");
  }
};

// --- Register ---
els.registerForm.onsubmit = async (e) => {
  e.preventDefault();
  els.registerError.classList.add("hidden");
  els.registerSuccess.classList.add("hidden");
  try {
    await api("/auth/register", {
      method: "POST",
      body: JSON.stringify({
        name: els.registerName.value.trim(),
        email: els.registerEmail.value,
        password: els.registerPassword.value
      })
    });
    els.registerSuccess.textContent = "Account created. Please contact admin to activate your account before signing in.";
    els.registerSuccess.classList.remove("hidden");
    els.registerForm.reset();
    // Auto-switch to sign in after a moment
    setTimeout(() => showTab("signin"), 2000);
  } catch (error) {
    els.registerError.textContent = error.message;
    els.registerError.classList.remove("hidden");
  }
};

// --- Check if already logged in ---
(async () => {
  const existingToken = localStorage.getItem("access_token");
  if (!existingToken) return;
  try {
    const res = await fetch("/api/me", {
      headers: { Authorization: `Bearer ${existingToken}` }
    });
    if (res.ok) {
      window.location.href = "/app/";
      return;
    }
  } catch (_) {
    // Fall through and clear stale credentials.
  }
  clearStoredSession();
})();
