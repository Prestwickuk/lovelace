(function () {
  "use strict";

  // Blockout scope field visibility toggle
  var scopeSelect = document.getElementById("bulk_scope_type");
  if (scopeSelect) {
    var unitsBox = document.getElementById("bulk_units_wrap");
    var unitTypeBox = document.getElementById("bulk_unit_type_wrap");
    var syncScope = function () {
      var val = scopeSelect.value;
      if (unitsBox) {
        unitsBox.classList.toggle("hidden", val !== "UNIT" && val !== "UNITS");
      }
      if (unitTypeBox) {
        unitTypeBox.classList.toggle("hidden", val !== "UNIT_TYPE");
      }
    };
    scopeSelect.addEventListener("change", syncScope);
    syncScope();
  }

  // Release rule scope field visibility toggle
  var relScope = document.getElementById("rel_scope_type");
  if (relScope) {
    var relUnitWrap = document.getElementById("rel_unit_wrap");
    var relTypeWrap = document.getElementById("rel_unit_type_wrap");
    var syncRelScope = function () {
      var v = relScope.value;
      if (relUnitWrap) relUnitWrap.classList.toggle("hidden", v !== "UNIT");
      if (relTypeWrap) relTypeWrap.classList.toggle("hidden", v !== "UNIT_TYPE");
    };
    relScope.addEventListener("change", syncRelScope);
    syncRelScope();
  }

  // Calendar interactive inspection & multi-cell selection for reservations / bulk blockouts
  var calGrid = document.getElementById("calendar-grid");
  if (!calGrid) return;

  var infoBox = document.getElementById("calendar-cell-inspector");
  var infoText = document.getElementById("calendar-cell-reason");
  var infoLink = document.getElementById("calendar-cell-action");
  var selSummary = document.getElementById("calendar-selection-summary");
  var selStartInput = document.getElementById("sel_start_date");
  var selEndInput = document.getElementById("sel_end_date");
  var selUnitsContainer = document.getElementById("sel_units_inputs");
  var selBlockBtn = document.getElementById("sel-block-btn");
  var selReserveBtn = document.getElementById("sel-reserve-btn");
  var selClearBtn = document.getElementById("sel-clear-btn");

  var selectedCells = new Map(); // key: unitId|date -> { unitId, unitCode, date }

  function addDaysISO(isoDate, days) {
    var parts = isoDate.split("-");
    var d = new Date(Date.UTC(Number(parts[0]), Number(parts[1]) - 1, Number(parts[2]) + days));
    return d.toISOString().slice(0, 10);
  }

  function updateSelectionUI() {
    if (!selSummary || !selUnitsContainer) return;
    selUnitsContainer.innerHTML = "";

    if (selectedCells.size === 0) {
      selSummary.textContent = "Click available cells while holding Shift (or click dates directly) to select units & dates for bulk blockout or new reservation.";
      if (selBlockBtn) selBlockBtn.disabled = true;
      if (selReserveBtn) selReserveBtn.disabled = true;
      return;
    }

    var minDate = null;
    var maxDate = null;
    var unitIds = new Set();
    selectedCells.forEach(function (item) {
      unitIds.add(item.unitId);
      if (!minDate || item.date < minDate) minDate = item.date;
      if (!maxDate || item.date > maxDate) maxDate = item.date;
    });
    var endExclusive = addDaysISO(maxDate, 1);

    if (selStartInput) selStartInput.value = minDate;
    if (selEndInput) selEndInput.value = endExclusive;

    unitIds.forEach(function (uid) {
      var input = document.createElement("input");
      input.type = "hidden";
      input.name = "unit_ids";
      input.value = uid;
      selUnitsContainer.appendChild(input);
    });

    selSummary.textContent = unitIds.size + " unit(s), stay/block window " + minDate + " → " + endExclusive;
    if (selBlockBtn) selBlockBtn.disabled = false;
    if (selReserveBtn) selReserveBtn.disabled = false;
  }

  calGrid.addEventListener("click", function (ev) {
    var cell = ev.target.closest("td.cal-cell");
    if (!cell) return;
    if (ev.target.tagName === "A") return;

    var status = cell.dataset.status || "";
    var unitId = cell.dataset.unitId || "";
    var unitCode = cell.dataset.unitCode || "";
    var date = cell.dataset.date || "";
    var reason = cell.dataset.reason || "";
    var resId = cell.dataset.reservationId || "";
    var grpId = cell.dataset.blockoutGroupId || "";

    if (infoBox && infoText) {
      infoBox.classList.remove("hidden");
      infoText.textContent = unitCode + " · " + date + " · " + status + (reason ? " — " + reason : "");
      if (infoLink) {
        if (resId) {
          infoLink.href = "/reservations/" + encodeURIComponent(resId);
          infoLink.textContent = "Open Reservation";
          infoLink.classList.remove("hidden");
        } else if (grpId) {
          infoLink.href = "/blocks";
          infoLink.textContent = "Manage Blockout";
          infoLink.classList.remove("hidden");
        } else if (status === "AVAILABLE") {
          infoLink.href = "/reservations/new?unit_ids=" + encodeURIComponent(unitId) + "&arrival_date=" + encodeURIComponent(date) + "&departure_date=" + encodeURIComponent(addDaysISO(date, 1));
          infoLink.textContent = "Create Reservation";
          infoLink.classList.remove("hidden");
        } else if (status === "UNRELEASED") {
          infoLink.href = "/releases";
          infoLink.textContent = "View Release Rules";
          infoLink.classList.remove("hidden");
        } else {
          infoLink.classList.add("hidden");
        }
      }
    }

    if (status === "AVAILABLE" || ev.shiftKey) {
      var key = unitId + "|" + date;
      if (selectedCells.has(key)) {
        selectedCells.delete(key);
        cell.classList.remove("cal-cell-selected");
      } else {
        selectedCells.set(key, { unitId: unitId, unitCode: unitCode, date: date });
        cell.classList.add("cal-cell-selected");
      }
      updateSelectionUI();
    }
  });

  if (selClearBtn) {
    selClearBtn.addEventListener("click", function () {
      selectedCells.clear();
      calGrid.querySelectorAll(".cal-cell-selected").forEach(function (el) {
        el.classList.remove("cal-cell-selected");
      });
      updateSelectionUI();
    });
  }

  if (selReserveBtn) {
    selReserveBtn.addEventListener("click", function () {
      if (selectedCells.size === 0) return;
      var unitIds = new Set();
      var minDate = null;
      var maxDate = null;
      selectedCells.forEach(function (item) {
        unitIds.add(item.unitId);
        if (!minDate || item.date < minDate) minDate = item.date;
        if (!maxDate || item.date > maxDate) maxDate = item.date;
      });
      var params = new URLSearchParams();
      params.set("arrival_date", minDate);
      params.set("departure_date", addDaysISO(maxDate, 1));
      unitIds.forEach(function (u) { params.append("unit_ids", u); });
      window.location.href = "/reservations/new?" + params.toString();
    });
  }

  updateSelectionUI();
})();


(function () {
  "use strict";

  function getRecoveryText() {
    var src = document.getElementById("recovery-codes-plain");
    if (!src) return "";
    return (src.value || src.textContent || "").trim();
  }

  var copyBtn = document.getElementById("copy-recovery-codes-btn");
  if (copyBtn) {
    copyBtn.addEventListener("click", function () {
      var text = getRecoveryText();
      if (!text || !navigator.clipboard || !navigator.clipboard.writeText) return;
      var original = copyBtn.textContent;
      navigator.clipboard.writeText(text).then(function () {
        copyBtn.textContent = "Copied!";
        setTimeout(function () {
          copyBtn.textContent = original;
        }, 2200);
      });
    });
  }

  var dlBtn = document.getElementById("download-recovery-codes-btn");
  if (dlBtn) {
    dlBtn.addEventListener("click", function () {
      var text = getRecoveryText();
      if (!text) return;
      var email = dlBtn.getAttribute("data-account-email") || "admin";
      var header = "LOVELACE Booking System — One-Time Recovery Codes\n" +
        "Account: " + email + "\n" +
        "Keep these codes in a password manager or safe place. Each code works once at /login/recover.\n\n";
      var blob = new Blob([header + text + "\n"], { type: "text/plain;charset=utf-8" });
      var url = URL.createObjectURL(blob);
      var a = document.createElement("a");
      a.href = url;
      a.download = "lovelace-recovery-codes.txt";
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);
      URL.revokeObjectURL(url);
    });
  }
})();


(function () {
  "use strict";

  function slugify(input) {
    return String(input || "")
      .toLowerCase()
      .replace(/[&+_/\s]+/g, "-")
      .replace(/[^a-z0-9-]/g, "")
      .replace(/-+/g, "-")
      .replace(/^-|-$/g, "")
      .slice(0, 48);
  }

  // Step 1: auto-derive URL slug from Property Name unless edited manually
  var wizName = document.getElementById("wiz_name");
  var wizSlug = document.getElementById("wiz_slug");
  if (wizName && wizSlug && !wizSlug.disabled) {
    var slugTouched = Boolean(wizSlug.value);
    wizSlug.addEventListener("input", function () {
      slugTouched = wizSlug.value.trim() !== "";
    });
    wizName.addEventListener("input", function () {
      if (!slugTouched) {
        wizSlug.value = slugify(wizName.value);
      }
    });
  }

  // Step 1: preset card selection updates default check-in/out & console labels
  var presetRadios = document.querySelectorAll('input[name="preset"][data-preset-checkin]');
  if (presetRadios.length > 0) {
    presetRadios.forEach(function (radio) {
      radio.addEventListener("change", function () {
        if (!radio.checked) return;
        document.querySelectorAll(".wizard-preset-card").forEach(function (card) {
          card.classList.remove("wizard-preset-selected");
        });
        var parentCard = radio.closest(".wizard-preset-card");
        if (parentCard) parentCard.classList.add("wizard-preset-selected");

        var ci = document.getElementById("wiz_checkin");
        var co = document.getElementById("wiz_checkout");
        var uSing = document.getElementById("wiz_unit_singular");
        var uPlur = document.getElementById("wiz_unit_plural");
        var gSing = document.getElementById("wiz_guest_singular");
        var gPlur = document.getElementById("wiz_guest_plural");
        var stayLbl = document.getElementById("wiz_stay_label");

        if (ci && radio.dataset.presetCheckin) ci.value = radio.dataset.presetCheckin;
        if (co && radio.dataset.presetCheckout) co.value = radio.dataset.presetCheckout;
        if (uSing && radio.dataset.presetUnitSingular) uSing.value = radio.dataset.presetUnitSingular;
        if (uPlur && radio.dataset.presetUnitPlural) uPlur.value = radio.dataset.presetUnitPlural;
        if (gSing && radio.dataset.presetGuestSingular) gSing.value = radio.dataset.presetGuestSingular;
        if (gPlur && radio.dataset.presetGuestPlural) gPlur.value = radio.dataset.presetGuestPlural;
        if (stayLbl && radio.dataset.presetStayLabel) stayLbl.value = radio.dataset.presetStayLabel;
      });
    });
  }

  // Step 2: quick-fill accommodation starter buttons
  var starterBtns = document.querySelectorAll(".wizard-starter-btn");
  if (starterBtns.length > 0) {
    starterBtns.forEach(function (btn) {
      btn.addEventListener("click", function () {
        var setVal = function (id, val) {
          var el = document.getElementById(id);
          if (el && val !== undefined) el.value = val;
        };
        setVal("ut_name", btn.dataset.starterName || "");
        setVal("ut_code", btn.dataset.starterCode || "");
        setVal("ut_category", btn.dataset.starterCategory || "other");
        setVal("ut_desc", btn.dataset.starterDesc || "");
        setVal("ut_prefix", btn.dataset.starterPrefix || "");
        setVal("ut_quantity", btn.dataset.starterQty || "1");
        setVal("ut_zone", btn.dataset.starterZone || "");
        setVal("ut_base_adults", btn.dataset.starterBaseAdults || "2");
        setVal("ut_max_adults", btn.dataset.starterMaxAdults || "2");
        setVal("ut_max_children", btn.dataset.starterMaxChildren || "0");
        setVal("ut_max_infants", btn.dataset.starterMaxInfants || "0");
        setVal("ut_max_guests", btn.dataset.starterMaxGuests || "2");
        setVal("ut_max_pets", btn.dataset.starterMaxPets || "0");
        setVal("ut_price", btn.dataset.starterPrice || "95.00");
        setVal("ut_min_nights", btn.dataset.starterMinNights || "1");
        setVal("ut_max_nights", btn.dataset.starterMaxNights || "28");
        setVal("ut_amenities", btn.dataset.starterAmenities || "");
        var petsBox = document.getElementById("ut_pets");
        if (petsBox) petsBox.checked = btn.dataset.starterPets === "true";
        var nameInput = document.getElementById("ut_name");
        if (nameInput) nameInput.focus();
      });
    });
  }

  // Step 3: toggle seasonal/weekend rate uplift fields
  var peakToggle = document.getElementById("s3_add_peak");
  var peakFields = document.getElementById("s3_peak_fields");
  if (peakToggle && peakFields) {
    var syncPeak = function () {
      peakFields.classList.toggle("hidden", !peakToggle.checked);
    };
    peakToggle.addEventListener("change", syncPeak);
    syncPeak();
  }

  // Backups: toggle cloud credential inputs based on selected provider
  var btProvider = document.getElementById("bt_provider");
  if (btProvider) {
    var s3Box = document.getElementById("bt_creds_s3");
    var gcsBox = document.getElementById("bt_creds_gcs");
    var azBox = document.getElementById("bt_creds_azure");
    var syncBackupProvider = function () {
      var p = btProvider.value;
      if (s3Box) s3Box.classList.toggle("hidden", p !== "AWS_S3" && p !== "S3_COMPATIBLE");
      if (gcsBox) gcsBox.classList.toggle("hidden", p !== "GCS");
      if (azBox) azBox.classList.toggle("hidden", p !== "AZURE_BLOB");
    };
    btProvider.addEventListener("change", syncBackupProvider);
    syncBackupProvider();
  }

  // Copy-to-clipboard buttons for website booking embed code blocks
  var copyBtns = document.querySelectorAll("[data-copy-target]");
  if (copyBtns.length > 0) {
    copyBtns.forEach(function (btn) {
      var defaultLabel = btn.textContent;
      btn.addEventListener("click", function () {
        var targetId = btn.getAttribute("data-copy-target");
        var targetEl = targetId ? document.getElementById(targetId) : null;
        if (!targetEl) return;
        var text = typeof targetEl.value === "string" ? targetEl.value : (targetEl.textContent || "");
        var markCopied = function () {
          btn.textContent = "Copied!";
          window.setTimeout(function () {
            btn.textContent = defaultLabel;
          }, 2200);
        };
        if (navigator.clipboard && typeof navigator.clipboard.writeText === "function") {
          navigator.clipboard.writeText(text).then(markCopied).catch(function () {
            if (typeof targetEl.select === "function") {
              targetEl.focus();
              targetEl.select();
              try {
                document.execCommand("copy");
                markCopied();
              } catch (_) {}
            }
          });
        } else if (typeof targetEl.select === "function") {
          targetEl.focus();
          targetEl.select();
          try {
            document.execCommand("copy");
            markCopied();
          } catch (_) {}
        }
      });
    });
  }
})();
