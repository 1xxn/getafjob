document.addEventListener('DOMContentLoaded', async () => {
  const unpairedView = document.getElementById('unpaired-view');
  const pairedView = document.getElementById('paired-view');
  const statusBadge = document.getElementById('status-badge');
  const userNameEl = document.getElementById('user-name');
  const serverUrlEl = document.getElementById('connected-server');
  const pairForm = document.getElementById('pair-form');
  const pairError = document.getElementById('pair-error');
  const pairBtn = document.getElementById('pair-btn');
  const inspectBtn = document.getElementById('inspect-btn');
  const dashboardBtn = document.getElementById('dashboard-btn');
  const unpairBtn = document.getElementById('unpair-btn');

  function updateStatus(isPaired, auth) {
    if (isPaired) {
      statusBadge.textContent = 'Connected';
      statusBadge.className = 'badge connected';
      userNameEl.textContent = auth.userName || 'User';
      serverUrlEl.textContent = auth.serverUrl || '';
      unpairedView.hidden = true;
      pairedView.hidden = false;
    } else {
      statusBadge.textContent = 'Disconnected';
      statusBadge.className = 'badge disconnected';
      unpairedView.hidden = false;
      pairedView.hidden = true;
    }
  }

  // Check initial state
  chrome.runtime.sendMessage({ type: 'GET_STATUS' }, (res) => {
    if (res && res.success) {
      updateStatus(res.paired, res.auth);
    }
  });

  pairForm.addEventListener('submit', (e) => {
    e.preventDefault();
    pairError.hidden = true;
    pairBtn.disabled = true;
    pairBtn.textContent = 'Connecting...';

    const serverUrl = document.getElementById('server-url').value.trim();
    const code = document.getElementById('pair-code').value.trim();

    chrome.runtime.sendMessage({ type: 'PAIR', serverUrl, code }, (res) => {
      pairBtn.disabled = false;
      pairBtn.textContent = 'Connect Extension';

      if (res && res.success) {
        chrome.runtime.sendMessage({ type: 'GET_STATUS' }, (st) => {
          updateStatus(true, st.auth);
        });
      } else {
        pairError.textContent = res ? res.error : 'Connection failed';
        pairError.hidden = false;
      }
    });
  });

  inspectBtn.addEventListener('click', async () => {
    const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
    if (tab && tab.id) {
      chrome.tabs.sendMessage(tab.id, { type: 'TRIGGER_INSPECT' }, () => {
        window.close();
      });
    }
  });

  dashboardBtn.addEventListener('click', async () => {
    chrome.runtime.sendMessage({ type: 'GET_STATUS' }, (res) => {
      if (res && res.auth && res.auth.serverUrl) {
        chrome.tabs.create({ url: res.auth.serverUrl });
      }
    });
  });

  unpairBtn.addEventListener('click', () => {
    if (confirm('Disconnect from this getafjob instance?')) {
      chrome.runtime.sendMessage({ type: 'UNPAIR' }, () => {
        updateStatus(false, {});
      });
    }
  });
});
