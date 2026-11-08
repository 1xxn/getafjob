// getafjob Assist Content Script
// Enforces: Closed Shadow DOM, Approval before Submit, Trusted Clicks Only, and CAPTCHA isolation.

(() => {
  // 1. Guard against execution in CAPTCHA or subframes
  if (window !== window.top) {
    return;
  }
  const href = window.location.href.toLowerCase();
  if (href.includes('recaptcha') || href.includes('hcaptcha') || href.includes('challenges.cloudflare.com')) {
    return;
  }

  let hostEl = null;
  let shadowRoot = null;
  let inspectedData = null;
  let isApproved = false;

  function initUI() {
    if (hostEl) return;

    hostEl = document.createElement('div');
    hostEl.id = 'gaj-assist-root';
    hostEl.style.position = 'fixed';
    hostEl.style.bottom = '20px';
    hostEl.style.right = '20px';
    hostEl.style.zIndex = '2147483647'; // Highest layer

    // 2. Closed Shadow Root: Host page scripts cannot inspect or access our DOM or data
    shadowRoot = hostEl.attachShadow({ mode: 'closed' });

    const style = document.createElement('style');
    style.textContent = `
      * { box-sizing: border-box; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; }
      .dock {
        background: #0f172a;
        color: #f8fafc;
        border: 1px solid #334155;
        border-radius: 12px;
        box-shadow: 0 10px 25px -5px rgba(0, 0, 0, 0.5), 0 8px 10px -6px rgba(0, 0, 0, 0.5);
        width: 340px;
        max-height: 480px;
        display: flex;
        flex-direction: column;
        overflow: hidden;
        font-size: 13px;
      }
      .dock-header {
        background: #1e293b;
        padding: 10px 14px;
        display: flex;
        justify-content: space-between;
        align-items: center;
        border-bottom: 1px solid #334155;
      }
      .dock-title {
        font-weight: 600;
        display: flex;
        align-items: center;
        gap: 6px;
      }
      .dock-close {
        background: transparent;
        border: none;
        color: #94a3b8;
        cursor: pointer;
        font-size: 16px;
        padding: 2px 6px;
      }
      .dock-close:hover { color: #f8fafc; }
      .dock-body {
        padding: 12px 14px;
        overflow-y: auto;
        display: flex;
        flex-direction: column;
        gap: 10px;
      }
      .match-item {
        background: #1e293b;
        border: 1px solid #334155;
        border-radius: 6px;
        padding: 8px 10px;
      }
      .match-label {
        font-size: 11px;
        color: #94a3b8;
        display: flex;
        justify-content: space-between;
        margin-bottom: 3px;
      }
      .match-prov {
        color: #38bdf8;
        font-family: monospace;
        font-size: 10px;
      }
      .match-val {
        font-weight: 500;
        color: #f8fafc;
        word-break: break-word;
      }
      .warning-box {
        background: #451a03;
        border: 1px solid #78350f;
        color: #fde68a;
        padding: 8px 10px;
        border-radius: 6px;
        font-size: 12px;
      }
      .dock-footer {
        padding: 10px 14px;
        background: #1e293b;
        border-top: 1px solid #334155;
        display: flex;
        flex-direction: column;
        gap: 8px;
      }
      .btn {
        display: inline-flex;
        justify-content: center;
        align-items: center;
        width: 100%;
        padding: 8px 12px;
        border-radius: 6px;
        font-size: 12px;
        font-weight: 600;
        cursor: pointer;
        border: none;
      }
      .btn-primary { background: #2563eb; color: white; }
      .btn-primary:hover { background: #1d4ed8; }
      .btn-success { background: #059669; color: white; }
      .trusted-note {
        font-size: 11px;
        color: #94a3b8;
        text-align: center;
        line-height: 1.3;
      }
      .fab {
        background: #2563eb;
        color: white;
        border: 1px solid #3b82f6;
        border-radius: 9999px;
        padding: 8px 14px;
        display: flex;
        align-items: center;
        gap: 6px;
        cursor: pointer;
        box-shadow: 0 4px 12px rgba(0, 0, 0, 0.4);
        font-weight: 600;
        font-size: 13px;
      }
      .fab:hover { background: #1d4ed8; }
    `;

    shadowRoot.appendChild(style);
    document.body.appendChild(hostEl);
  }

  function renderFloatingButton() {
    initUI();
    const existing = shadowRoot.querySelector('.dock, .fab');
    if (existing) existing.remove();

    const btn = document.createElement('button');
    btn.className = 'fab';
    btn.innerHTML = '<span>💼</span> getafjob Assist';
    btn.addEventListener('click', (e) => {
      // 3. Trusted Clicks Only: verify user initiated
      if (!e.isTrusted) return;
      inspectAndShowDock();
    });
    shadowRoot.appendChild(btn);
  }

  function findFieldsOnPage() {
    const inputs = Array.from(document.querySelectorAll('input:not([type="hidden"]):not([type="submit"]):not([type="button"]), select, textarea'));
    const detected = [];

    for (const el of inputs) {
      if (el.closest('#gaj-assist-root')) continue;
      // Skip invisible elements
      const rect = el.getBoundingClientRect();
      if (rect.width === 0 && rect.height === 0) continue;

      let label = '';
      if (el.id) {
        const lbl = document.querySelector(`label[for="${el.id}"]`);
        if (lbl) label = lbl.innerText.trim();
      }
      if (!label && el.closest('label')) {
        label = el.closest('label').innerText.trim();
      }
      if (!label && el.placeholder) {
        label = el.placeholder.trim();
      }
      if (!label && el.getAttribute('aria-label')) {
        label = el.getAttribute('aria-label').trim();
      }

      const id = el.id || el.name || `field_${detected.length}`;
      detected.push({
        id,
        name: el.name || id,
        label,
        type: el.type || el.tagName.toLowerCase(),
        required: el.required || el.getAttribute('aria-required') === 'true',
      });
    }

    return detected;
  }

  function inspectAndShowDock() {
    initUI();
    const existing = shadowRoot.querySelector('.dock, .fab');
    if (existing) existing.remove();

    const fields = findFieldsOnPage();
    if (fields.length === 0) {
      renderDock({
        matches: [],
        missing_required: [],
        requires_approval: true,
        empty: true,
      });
      return;
    }

    chrome.runtime.sendMessage({
      type: 'INSPECT',
      url: window.location.href,
      title: document.title,
      fields,
    }, (res) => {
      if (res && res.success) {
        inspectedData = res.data;
        renderDock(res.data);
      } else {
        renderError(res ? res.error : 'Inspection failed');
      }
    });
  }

  function renderDock(data) {
    const existing = shadowRoot.querySelector('.dock, .fab');
    if (existing) existing.remove();

    const dock = document.createElement('div');
    dock.className = 'dock';

    const header = document.createElement('div');
    header.className = 'dock-header';
    header.innerHTML = `
      <div class="dock-title"><span>💼</span> getafjob Assist</div>
      <button class="dock-close" title="Minimize">✕</button>
    `;
    header.querySelector('.dock-close').addEventListener('click', renderFloatingButton);
    dock.appendChild(header);

    const body = document.createElement('div');
    body.className = 'dock-body';

    if (data.empty) {
      body.innerHTML = `<p style="color: #94a3b8; text-align: center;">No application form fields detected on this page.</p>`;
    } else {
      if (data.missing_required && data.missing_required.length > 0) {
        const warn = document.createElement('div');
        warn.className = 'warning-box';
        warn.innerHTML = `<strong>Missing required fields:</strong><br>${data.missing_required.map(m => `• ${m}`).join('<br>')}`;
        body.appendChild(warn);
      }

      if (data.matches && data.matches.length > 0) {
        for (const m of data.matches) {
          const item = document.createElement('div');
          item.className = 'match-item';
          item.innerHTML = `
            <div class="match-label">
              <span>${m.field_id}</span>
              <span class="match-prov">${m.provenance}</span>
            </div>
            <div class="match-val">${m.value}</div>
          `;
          body.appendChild(item);
        }
      }
    }
    dock.appendChild(body);

    const footer = document.createElement('div');
    footer.className = 'dock-footer';

    if (data.matches && data.matches.length > 0 && !isApproved) {
      const fillBtn = document.createElement('button');
      fillBtn.className = 'btn btn-primary';
      fillBtn.textContent = 'Approve & Fill Form';
      fillBtn.addEventListener('click', (e) => {
        // 4. Trusted Clicks Only: require human interaction
        if (!e.isTrusted) return;
        applyApprovedValues(data.matches);
        isApproved = true;
        renderDock(data);
      });
      footer.appendChild(fillBtn);

      const note = document.createElement('div');
      note.className = 'trusted-note';
      note.textContent = 'Requires explicit approval before filling. You review and click submit yourself.';
      footer.appendChild(note);
    } else if (isApproved) {
      const filledNote = document.createElement('div');
      filledNote.className = 'trusted-note';
      filledNote.style.color = '#34d399';
      filledNote.innerHTML = '<strong>✓ Values filled with your approval.</strong><br>Please review the form and submit it yourself.';
      footer.appendChild(filledNote);
    }

    dock.appendChild(footer);
    shadowRoot.appendChild(dock);
  }

  function renderError(msg) {
    const existing = shadowRoot.querySelector('.dock, .fab');
    if (existing) existing.remove();

    const dock = document.createElement('div');
    dock.className = 'dock';
    dock.innerHTML = `
      <div class="dock-header">
        <div class="dock-title">getafjob Assist</div>
        <button class="dock-close">✕</button>
      </div>
      <div class="dock-body">
        <div class="warning-box">${msg}</div>
      </div>
    `;
    dock.querySelector('.dock-close').addEventListener('click', renderFloatingButton);
    shadowRoot.appendChild(dock);
  }

  function applyApprovedValues(matches) {
    for (const m of matches) {
      let el = document.getElementById(m.field_id);
      if (!el) {
        el = document.querySelector(`[name="${m.field_id}"]`);
      }
      if (el) {
        el.focus();
        el.value = m.value;
        el.dispatchEvent(new Event('input', { bubbles: true }));
        el.dispatchEvent(new Event('change', { bubbles: true }));
        el.blur();
      }
    }
  }

  // 5. Track human submission with trusted clicks only
  document.addEventListener('submit', (e) => {
    if (!e.isTrusted || !isApproved || !inspectedData) return;

    // Report successful submission to getafjob submit register
    chrome.runtime.sendMessage({
      type: 'SUBMIT',
      url: window.location.href,
      company: document.domain,
      title: document.title,
      fieldsFilled: inspectedData.matches ? inspectedData.matches.length : 0,
      approved: true,
      notes: 'Submitted via getafjob Assist extension with trusted human click',
    });
  }, true);

  // Listen for trigger from popup
  chrome.runtime.onMessage.addListener((msg, sender, sendResponse) => {
    if (msg.type === 'TRIGGER_INSPECT') {
      inspectAndShowDock();
      sendResponse({ success: true });
    }
  });

  // Automatically show floating button if application fields are present
  setTimeout(() => {
    const fields = findFieldsOnPage();
    if (fields.length >= 2) {
      renderFloatingButton();
    }
  }, 1000);
})();
