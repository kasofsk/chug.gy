// The request form, sent without leaving the page. Without this script the
// form posts and the server redirects to /requested, which says the same.
(function () {
  var form = document.querySelector(".request-form");
  if (!form || !window.fetch) return;
  var status = form.querySelector(".form-status");
  var button = form.querySelector("button[type=submit]");

  form.addEventListener("submit", function (event) {
    event.preventDefault();
    if (!form.reportValidity()) return;
    button.disabled = true;
    status.textContent = "Sending…";
    status.className = "form-status";
    fetch(form.action, {
      method: "POST",
      headers: { Accept: "application/json" },
      body: new URLSearchParams(new FormData(form)),
    })
      .then(function (response) {
        return response.json().then(function (body) {
          return { ok: response.ok, message: body.message };
        });
      })
      .then(function (answer) {
        if (answer.ok) {
          form.reset();
          status.textContent = "Request received. We will write back to you.";
          status.className = "form-status form-status-ok";
        } else {
          status.textContent = answer.message || "The request could not be sent.";
          status.className = "form-status form-status-bad";
          button.disabled = false;
        }
      })
      .catch(function () {
        status.textContent = "The request could not be sent. Check your connection and try again.";
        status.className = "form-status form-status-bad";
        button.disabled = false;
      });
  });
})();
