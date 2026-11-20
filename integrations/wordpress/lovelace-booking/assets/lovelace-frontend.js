(function () {
  "use strict";

  function isoDateOffset(daysAhead) {
    var d = new Date();
    d.setDate(d.getDate() + daysAhead);
    var yyyy = d.getFullYear();
    var mm = String(d.getMonth() + 1).padStart(2, "0");
    var dd = String(d.getDate()).padStart(2, "0");
    return yyyy + "-" + mm + "-" + dd;
  }

  // 1. Origin-verified postMessage height auto-resizer for embedded Lovelace iframes
  window.addEventListener("message", function (ev) {
    if (!ev.data || ev.data.type !== "lovelace:embed:resize") return;
    var height = Number(ev.data.height);
    if (!(height >= 420 && height <= 4000)) return;

    var frames = document.querySelectorAll("iframe.lovelace-wp-iframe");
    frames.forEach(function (frame) {
      var expectedOrigin = frame.getAttribute("data-lovelace-origin") || "";
      var expectedSlug = frame.getAttribute("data-lovelace-slug") || "";
      if (expectedOrigin && ev.origin !== expectedOrigin) return;
      if (expectedSlug && ev.data.slug && ev.data.slug !== expectedSlug) return;
      frame.style.height = height + 16 + "px";
    });
  });

  // 2.Accessible modal booking overlay for [lovelace_book_button], [lovelace_accommodation], & [lovelace_search target="modal"]
  var activeModal = null;
  var lastTrigger = null;

  function closeModal() {
    if (!activeModal) return;
    if (activeModal.parentNode) {
      activeModal.parentNode.removeChild(activeModal);
    }
    activeModal = null;
    document.documentElement.style.overflow = "";
    if (lastTrigger && typeof lastTrigger.focus === "function") {
      lastTrigger.focus();
    }
    lastTrigger = null;
  }

  function openBookingModal(embedUrl, origin, slug, triggerEl) {
    closeModal();
    lastTrigger = triggerEl || null;

    var backdrop = document.createElement("div");
    backdrop.className = "lovelace-wp-modal-backdrop";
    backdrop.setAttribute("role", "dialog");
    backdrop.setAttribute("aria-modal", "true");
    backdrop.setAttribute("aria-label", "Direct Online Booking");

    var dialog = document.createElement("div");
    dialog.className = "lovelace-wp-modal-dialog";

    var topbar = document.createElement("div");
    topbar.className = "lovelace-wp-modal-topbar";
    var titleSpan = document.createElement("span");
    titleSpan.textContent = "Direct Online Booking";
    var closeBtn = document.createElement("button");
    closeBtn.type = "button";
    closeBtn.className = "lovelace-wp-modal-close";
    closeBtn.textContent = "Close ✕";
    closeBtn.addEventListener("click", closeModal);
    topbar.appendChild(titleSpan);
    topbar.appendChild(closeBtn);

    var body = document.createElement("div");
    body.className = "lovelace-wp-modal-body";

    var iframe = document.createElement("iframe");
    iframe.className = "lovelace-wp-iframe";
    iframe.src = embedUrl;
    iframe.title = "Direct Online Booking";
    iframe.style.minHeight = "680px";
    if (origin) iframe.setAttribute("data-lovelace-origin", origin);
    if (slug) iframe.setAttribute("data-lovelace-slug", slug);

    body.appendChild(iframe);
    dialog.appendChild(topbar);
    dialog.appendChild(body);
    backdrop.appendChild(dialog);

    backdrop.addEventListener("click", function (e) {
      if (e.target === backdrop) closeModal();
    });

    document.body.appendChild(backdrop);
    document.documentElement.style.overflow = "hidden";
    activeModal = backdrop;
    closeBtn.focus();
  }

  document.addEventListener("keydown", function (e) {
    if (e.key === "Escape" && activeModal) {
      closeModal();
    }
  });

  document.addEventListener("click", function (e) {
    var trigger =
      e.target && e.target.closest
        ? e.target.closest("[data-lovelace-modal-url]")
        : null;
    if (!trigger) return;
    if (e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    var embedUrl = trigger.getAttribute("data-lovelace-modal-url");
    if (!embedUrl) return;
    e.preventDefault();
    openBookingModal(
      embedUrl,
      trigger.getAttribute("data-lovelace-origin") || "",
      trigger.getAttribute("data-lovelace-slug") || "",
      trigger
    );
  });

  // 3. Date defaults & modal submission support for [lovelace_search] forms
  function initSearchForms() {
    var forms = document.querySelectorAll("form.lovelace-wp-search-form");
    var today = isoDateOffset(0);
    var defaultArr = isoDateOffset(7);
    var defaultDep = isoDateOffset(9);

    forms.forEach(function (form) {
      var arrInput = form.querySelector('input[name="arrival"]');
      var depInput = form.querySelector('input[name="departure"]');
      if (arrInput) {
        arrInput.min = today;
        if (!arrInput.value) arrInput.value = defaultArr;
      }
      if (depInput) {
        depInput.min = today;
        if (!depInput.value) depInput.value = defaultDep;
      }
      if (arrInput && depInput) {
        arrInput.addEventListener("change", function () {
          if (arrInput.value && (!depInput.value || depInput.value <= arrInput.value)) {
            var next = new Date(arrInput.value + "T00:00:00");
            next.setDate(next.getDate() + 2);
            var y = next.getFullYear();
            var m = String(next.getMonth() + 1).padStart(2, "0");
            var d = String(next.getDate()).padStart(2, "0");
            depInput.value = y + "-" + m + "-" + d;
          }
          if (arrInput.value) depInput.min = arrInput.value;
        });
      }

      var modalBase = form.getAttribute("data-lovelace-search-modal");
      if (modalBase) {
        form.addEventListener("submit", function (e) {
          e.preventDefault();
          var params = new URLSearchParams();
          if (arrInput && arrInput.value) params.set("arrival", arrInput.value);
          if (depInput && depInput.value) params.set("departure", depInput.value);
          var adSel = form.querySelector('select[name="adults"]');
          var chSel = form.querySelector('select[name="children"]');
          if (adSel && adSel.value) params.set("adults", adSel.value);
          if (chSel && chSel.value) params.set("children", chSel.value);
          var sep = modalBase.indexOf("?") >= 0 ? "&" : "?";
          openBookingModal(
            modalBase + sep + params.toString(),
            form.getAttribute("data-lovelace-origin") || "",
            form.getAttribute("data-lovelace-slug") || "",
            form.querySelector('button[type="submit"]')
          );
        });
      }
    });
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", initSearchForms);
  } else {
    initSearchForms();
  }
})();
