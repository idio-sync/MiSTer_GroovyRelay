(function () {
  function markFailed(img) {
    if (!img || img.dataset.streamsArtworkHandled === "1") {
      return;
    }
    img.dataset.streamsArtworkHandled = "1";
    img.classList.add("streams-artwork-failed");
    img.setAttribute("aria-hidden", "true");
  }

  function bind(root) {
    var scope = root || document;
    var images = scope.querySelectorAll("img[data-streams-artwork]");
    images.forEach(function (img) {
      if (img.complete && img.naturalWidth === 0) {
        markFailed(img);
        return;
      }
      img.addEventListener("error", function () {
        markFailed(img);
      }, { once: true });
    });
  }

  document.addEventListener("DOMContentLoaded", function () {
    bind(document);
  });
  document.body.addEventListener("htmx:afterSwap", function (event) {
    bind(event.target);
  });
})();
