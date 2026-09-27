"use strict";
// No tracking, installation detection, automatic redirects or signer access.
const key = "bitcoinwalk-chat-choice-v1";
const remember = document.getElementById("remember");
const preferred = document.getElementById("preferred");
const status = document.getElementById("preference-status");
try { remember.checked = localStorage.getItem(key) === "browser"; } catch { /* Optional storage. */ }
preferred.hidden = !remember.checked;
remember.addEventListener("change", () => {
  try {
    if (remember.checked) localStorage.setItem(key, "browser");
    else localStorage.removeItem(key);
    preferred.hidden = !remember.checked;
    status.textContent = remember.checked ? "Browser preference saved. You can still choose each time." : "Preference cleared.";
  } catch { status.textContent = "Your browser could not save this preference. You can still open the chat."; }
});
