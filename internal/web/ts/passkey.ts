// Passkey sign-in and registration (WebAuthn). The server renders the buttons, texts and status areas; this script
// only runs the ceremonies. It never builds HTML (Trusted Types) and talks only to its own origin.

type Json = Record<string, any>;

const toB64 = (buf: ArrayBuffer): string => {
  let s = "";
  for (const b of new Uint8Array(buf)) s += String.fromCharCode(b);
  return btoa(s).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
};

const fromB64 = (s: string): ArrayBuffer => {
  const p = s.replace(/-/g, "+").replace(/_/g, "/");
  const bin = atob(p + "===".slice((p.length + 3) % 4));
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out.buffer;
};

const post = (url: string, body: Json): Promise<Response> =>
  fetch(url, {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json", Accept: "application/json" },
    body: JSON.stringify(body),
  });

const say = (btn: HTMLElement, text: string): void => {
  const el = document.getElementById(btn.dataset.status ?? "");
  if (el) el.textContent = text;
};

// fail shows the server's message (already translated) or the button's generic one.
const fail = async (btn: HTMLElement, res?: Response): Promise<void> => {
  let text = btn.dataset.error ?? "";
  if (res) {
    try {
      const j = await res.json();
      if (typeof j.error === "string") text = j.error;
    } catch {
      // keep the generic text
    }
  }
  say(btn, text);
};

const options = async (btn: HTMLElement, url: string): Promise<{ challenge: string; publicKey: Json } | null> => {
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

async function register(btn: HTMLButtonElement): Promise<void> {
  const o = await options(btn, "/account/passkeys/options");
  if (!o) return;
  const pk = o.publicKey;
  pk.challenge = fromB64(pk.challenge);
  pk.user.id = fromB64(pk.user.id);
  for (const c of pk.excludeCredentials ?? []) c.id = fromB64(c.id);
  const cred = (await navigator.credentials.create({ publicKey: pk as PublicKeyCredentialCreationOptions })) as PublicKeyCredential | null;
  if (!cred) return fail(btn);
  const r = cred.response as AuthenticatorAttestationResponse;
  const name = (document.getElementById(btn.dataset.name ?? "") as HTMLInputElement | null)?.value ?? "";
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
        transports: typeof r.getTransports === "function" ? r.getTransports() : [],
      },
      clientExtensionResults: cred.getClientExtensionResults(),
    },
  });
  if (res.ok) location.assign("/account?added=1");
  else await fail(btn, res);
}

async function signIn(btn: HTMLButtonElement): Promise<void> {
  const o = await options(btn, "/login/passkey/options");
  if (!o) return;
  const pk = o.publicKey;
  pk.challenge = fromB64(pk.challenge);
  for (const c of pk.allowCredentials ?? []) c.id = fromB64(c.id);
  const cred = (await navigator.credentials.get({ publicKey: pk as PublicKeyCredentialRequestOptions })) as PublicKeyCredential | null;
  if (!cred) return fail(btn);
  const r = cred.response as AuthenticatorAssertionResponse;
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
        userHandle: r.userHandle ? toB64(r.userHandle) : null,
      },
      clientExtensionResults: cred.getClientExtensionResults(),
    },
  });
  if (res.ok) location.assign("/");
  else await fail(btn, res);
}

const actions: Record<string, (b: HTMLButtonElement) => Promise<void>> = { register, "sign-in": signIn };

// bind wires passkey buttons; it runs on load and after every boosted page swap.
function bind(): void {
  if (!window.PublicKeyCredential) return;
  for (const btn of document.querySelectorAll<HTMLButtonElement>("button[data-passkey]")) {
    const run = actions[btn.dataset.passkey ?? ""];
    if (!run || btn.dataset.bound) continue;
    btn.dataset.bound = "1";
    btn.hidden = false;
    const box = btn.closest<HTMLElement>("[data-passkey-box]");
    if (box) box.hidden = false; // the "or" line around the button shows only with passkey support
    btn.addEventListener("click", () => {
      btn.disabled = true;
      say(btn, "");
      run(btn)
        .catch(() => fail(btn))
        .finally(() => {
          btn.disabled = false;
        });
    });
  }
}

document.addEventListener("htmx:after:settle", bind);
bind();
