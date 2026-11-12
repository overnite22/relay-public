"use strict";
const $ = (id) => document.getElementById(id);
const show = (text, kind) => { $("msg").textContent = text; $("msg").className = kind || ""; };

$("server").addEventListener("input", () => {
  // A pasted relay:// link already carries the token.
  $("token").required = !$("server").value.trim().startsWith("relay:");
});

$("pair").addEventListener("submit", async (event) => {
  event.preventDefault();
  $("submit").disabled = true;
  show("Vérification auprès du serveur…");
  try {
    const resp = await fetch("/__desktop/pair", {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-Relay": "1" },
      body: JSON.stringify({ server: $("server").value, token: $("token").value }),
    });
    const data = await resp.json().catch(() => ({}));
    if (!resp.ok) throw new Error(data.error || "Appairage refusé (" + resp.status + ")");
    $("token").value = "";
    show("Appairé en tant que " + data.user + ". Ouverture de Relay…", "ok");
    setTimeout(() => location.replace("/"), 600);
  } catch (err) {
    show(err.message, "err");
  } finally {
    $("submit").disabled = false;
  }
});
