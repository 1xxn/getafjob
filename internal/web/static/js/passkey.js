// ts/passkey.ts
var toB64 = (buf) => {
  let s = "";
  for (const b of new Uint8Array(buf)) s += String.fromCharCode(b);
  return btoa(s).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
};
var fromB64 = (s) => {
  const p = s.replace(/-/g, "+").replace(/_/g, "/");
  const bin = atob(p + "===".slice((p.length + 3) % 4));
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out.buffer;
};
var post = (url, body) => fetch(url, {
  method: "POST",
  credentials: "same-origin",
  headers: { "Content-Type": "application/json", Accept: "application/json" },
  body: JSON.stringify(body)
});
var say = (btn, text) => {
  const el = document.getElementById(btn.dataset.status ?? "");
  if (el) el.textContent = text;
};
var fail = async (btn, res) => {
  let text = btn.dataset.error ?? "";
  if (res) {
    try {
      const j = await res.json();
      if (typeof j.error === "string") text = j.error;
    } catch {
    }
  }
  say(btn, text);
};
var options = async (btn, url) => {
  const res = await post(url, {});
  if (res.status === 403) {
    location.assign("/login?reauth=1");
    return null;
  }
  if (!res.ok) {
    await fail(btn, res);
    return null;
  }
  const j = await res.json();
  return { challenge: j.challenge, publicKey: j.options.publicKey };
};
async function register(btn) {
  const o = await options(btn, "/account/passkeys/options");
  if (!o) return;
  const pk = o.publicKey;
  pk.challenge = fromB64(pk.challenge);
  pk.user.id = fromB64(pk.user.id);
  for (const c of pk.excludeCredentials ?? []) c.id = fromB64(c.id);
  const cred = await navigator.credentials.create({ publicKey: pk });
  if (!cred) return fail(btn);
  const r = cred.response;
  const name = document.getElementById(btn.dataset.name ?? "")?.value ?? "";
  const res = await post("/account/passkeys", {
    challenge: o.challenge,
    name,
    credential: {
      id: cred.id,
      rawId: toB64(cred.rawId),
      type: cred.type,
      response: {
        clientDataJSON: toB64(r.clientDataJSON),
        attestationObject: toB64(r.attestationObject),
        transports: typeof r.getTransports === "function" ? r.getTransports() : []
      },
      clientExtensionResults: cred.getClientExtensionResults()
    }
  });
  if (res.ok) location.assign("/account?added=1");
  else await fail(btn, res);
}
async function signIn(btn) {
  const o = await options(btn, "/login/passkey/options");
  if (!o) return;
  const pk = o.publicKey;
  pk.challenge = fromB64(pk.challenge);
  for (const c of pk.allowCredentials ?? []) c.id = fromB64(c.id);
  const cred = await navigator.credentials.get({ publicKey: pk });
  if (!cred) return fail(btn);
  const r = cred.response;
  const res = await post("/login/passkey", {
    challenge: o.challenge,
    credential: {
      id: cred.id,
      rawId: toB64(cred.rawId),
      type: cred.type,
      response: {
        clientDataJSON: toB64(r.clientDataJSON),
        authenticatorData: toB64(r.authenticatorData),
        signature: toB64(r.signature),
        userHandle: r.userHandle ? toB64(r.userHandle) : null
      },
      clientExtensionResults: cred.getClientExtensionResults()
    }
  });
  if (res.ok) location.assign("/");
  else await fail(btn, res);
}
var actions = { register, "sign-in": signIn };
function bind() {
  if (!window.PublicKeyCredential) return;
  for (const btn of document.querySelectorAll("button[data-passkey]")) {
    const run = actions[btn.dataset.passkey ?? ""];
    if (!run || btn.dataset.bound) continue;
    btn.dataset.bound = "1";
    btn.hidden = false;
    const box = btn.closest("[data-passkey-box]");
    if (box) box.hidden = false;
    btn.addEventListener("click", () => {
      btn.disabled = true;
      say(btn, "");
      run(btn).catch(() => fail(btn)).finally(() => {
        btn.disabled = false;
      });
    });
  }
}
document.addEventListener("htmx:after:settle", bind);
bind();
