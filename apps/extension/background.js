// Zatiti MV3 service worker (T8.1 scaffold).
//
// The full controller link lands in T8.3 (lib/link.js, lib/vault.js,
// lib/keepalive.js): proof-verified pairing, the 20 s JSON ping, the
// chrome.alarms reconnect, and the no-resend send path. This scaffold only
// opens the side panel when the toolbar action is clicked so the panel
// surface exists for T8.4 to build on.

chrome.sidePanel
  .setPanelBehavior({ openPanelOnActionClick: true })
  .catch((error) => console.error("sidePanel setup failed", error));

chrome.runtime.onInstalled.addListener(() => {
  // Reconnection scheduling (chrome.alarms with backoff) is wired in T8.3.
});
