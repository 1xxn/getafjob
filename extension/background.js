// getafjob Assist background service worker (MV3)

async function getAuth() {
  const data = await chrome.storage.local.get(['serverUrl', 'token', 'userId', 'userName']);
  return data;
}

async function apiRequest(endpoint, method = 'GET', body = null) {
  const { serverUrl, token } = await getAuth();
  if (!serverUrl) {
    throw new Error('Not paired with a getafjob server');
  }

  const base = serverUrl.replace(/\/+$/, '');
  const url = `${base}/api/v1${endpoint}`;

  const headers = {
    'Accept': 'application/json',
  };
  if (token) {
    headers['Authorization'] = `Bearer ${token}`;
  }
  if (body) {
    headers['Content-Type'] = 'application/json';
  }

  const res = await fetch(url, {
    method,
    headers,
    body: body ? JSON.stringify(body) : null,
  });

  if (!res.ok) {
    let errDetail = `HTTP ${res.status}`;
    try {
      const prob = await res.json();
      if (prob.code) {
        errDetail = `${prob.code} (status ${prob.status})`;
      }
    } catch (_) {}
    throw new Error(errDetail);
  }

  return res.json();
}

chrome.runtime.onMessage.addListener((msg, sender, sendResponse) => {
  (async () => {
    try {
      switch (msg.type) {
        case 'GET_STATUS': {
          const auth = await getAuth();
          sendResponse({ success: true, paired: Boolean(auth.token), auth });
          break;
        }

        case 'PAIR': {
          const base = msg.serverUrl.replace(/\/+$/, '');
          const pairUrl = `${base}/api/v1/assist/pair`;
          const res = await fetch(pairUrl, {
            method: 'POST',
            headers: {
              'Content-Type': 'application/json',
              'Accept': 'application/json',
            },
            body: JSON.stringify({
              code: msg.code,
              client_name: msg.clientName || navigator.userAgent,
            }),
          });

          if (!res.ok) {
            let err = `Pairing failed (${res.status})`;
            try {
              const prob = await res.json();
              if (prob.code) err = prob.code;
            } catch (_) {}
            sendResponse({ success: false, error: err });
            return;
          }

          const data = await res.json();
          await chrome.storage.local.set({
            serverUrl: base,
            token: data.token,
            userId: data.user_id,
            userName: data.user_name,
          });
          sendResponse({ success: true, data });
          break;
        }

        case 'UNPAIR': {
          await chrome.storage.local.remove(['serverUrl', 'token', 'userId', 'userName']);
          sendResponse({ success: true });
          break;
        }

        case 'INSPECT': {
          const res = await apiRequest('/assist/inspect', 'POST', {
            url: msg.url,
            title: msg.title,
            fields: msg.fields,
          });
          sendResponse({ success: true, data: res });
          break;
        }

        case 'SUBMIT': {
          const res = await apiRequest('/assist/submit', 'POST', {
            url: msg.url,
            company: msg.company,
            title: msg.title,
            fields_filled: msg.fieldsFilled,
            approved: msg.approved,
            notes: msg.notes,
          });
          sendResponse({ success: true, data: res });
          break;
        }

        case 'GET_PROFILE': {
          const res = await apiRequest('/assist/profile', 'GET');
          sendResponse({ success: true, data: res });
          break;
        }

        case 'GET_APPROVALS': {
          const res = await apiRequest('/assist/approvals', 'GET');
          sendResponse({ success: true, data: res });
          break;
        }

        case 'DECIDE_APPROVAL': {
          const res = await apiRequest(`/assist/approvals/${msg.id}/decide`, 'POST', {
            decision: msg.decision,
          });
          sendResponse({ success: true, data: res });
          break;
        }

        default:
          sendResponse({ success: false, error: 'Unknown message type' });
      }
    } catch (err) {
      sendResponse({ success: false, error: err.message });
    }
  })();

  return true; // async sendResponse
});
