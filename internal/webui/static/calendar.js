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
