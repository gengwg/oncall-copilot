// Condition trigger for the alert-intake cron job. Fires the investigation
// agent turn only when the host alert-relay has queued alerts. The GET drains
// the relay queue; the batch is embedded in the fire message so the agent
// turn has the payload without a second fetch.
//
// This OpenClaw build's code-mode exposes `tools` (not `exec`); stdout is at
// r.result.content[i].text for type=="text".
const relayToken = (typeof process !== "undefined" && process.env && process.env.RELAY_TOKEN) || "";
const r = await tools.call("exec", {
  command:
    "curl -sf -m 8 " +
    (relayToken ? `-H "Authorization: Bearer ${relayToken}" ` : "") +
    "http://172.18.0.1:9099/alerts",
});
const parts = r?.result?.content;
const text = Array.isArray(parts)
  ? parts.filter((p) => p && p.type === "text").map((p) => String(p.text || "")).join("\n")
  : String(r?.result ?? "");

// Extract the JSON array (strip any non-JSON warning lines like oom_score_adj).
let alerts = [];
const m = text.match(/\[[\s\S]*\]/);
if (m) {
  try { alerts = JSON.parse(m[0]); } catch { alerts = []; }
}

if (Array.isArray(alerts) && alerts.length > 0) {
  json({
    fire: true,
    message: "Alert batch (JSON):\n" + JSON.stringify(alerts, null, 2),
    state: { lastCount: alerts.length, at: Date.now() },
  });
} else {
  json({ fire: false, state: { lastCount: 0, at: Date.now() } });
}
