package webui

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/tirbach/tir-bach/internal/channels"
	"github.com/tirbach/tir-bach/internal/domain"
	"github.com/tirbach/tir-bach/internal/service"
	"github.com/tirbach/tir-bach/internal/store/postgres"
)

func (h *Handler) registerPhase1_1Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /calendar", h.requirePageAuth(false, h.handleCalendarGet))

	mux.HandleFunc("GET /reservations", h.requirePageAuth(false, h.handleReservationsListGet))
	mux.HandleFunc("GET /reservations/new", h.requirePageAuth(false, h.handleReservationNewGet))
	mux.HandleFunc("POST /reservations", h.requirePageAuth(true, h.handleReservationCreatePost))
	mux.HandleFunc("GET /reservations/{id}", h.requirePageAuth(false, h.handleReservationDetailGet))
	mux.HandleFunc("POST /reservations/{id}/update", h.requirePageAuth(true, h.handleReservationUpdatePost))
	mux.HandleFunc("POST /reservations/{id}/transition", h.requirePageAuth(true, h.handleReservationTransitionPost))
	mux.HandleFunc("POST /channels", h.requirePageAuth(true, h.handleBookingChannelCreatePost))

	mux.HandleFunc("GET /blocks", h.requirePageAuth(false, h.handleBlocksGet))
	mux.HandleFunc("POST /blocks/preview", h.requirePageAuth(true, h.handleBlockoutPreviewPost))
	mux.HandleFunc("POST /blocks/bulk", h.requirePageAuth(true, h.handleBlockoutBulkPost))
	mux.HandleFunc("POST /blocks/groups/{id}/release", h.requirePageAuth(true, h.handleBlockoutGroupReleasePost))
	mux.HandleFunc("POST /blocks/groups/{id}/reinstate", h.requirePageAuth(true, h.handleBlockoutGroupReinstatePost))
	mux.HandleFunc("POST /blocks/groups/{id}/dates", h.requirePageAuth(true, h.handleBlockoutGroupDatesPost))

	mux.HandleFunc("GET /releases", h.requirePageAuth(false, h.handleReleasesGet))
	mux.HandleFunc("POST /releases", h.requirePageAuth(true, h.handleReleasesCreatePost))
	mux.HandleFunc("POST /releases/{id}/release-now", h.requirePageAuth(true, h.handleReleasesManualPost))
	mux.HandleFunc("POST /releases/{id}/cancel", h.requirePageAuth(true, h.handleReleasesCancelPost))

	mux.HandleFunc("GET /settings/integrations/channel-manager", h.requirePageAuth(false, h.handleChannelManagerGet))
	mux.HandleFunc("POST /settings/integrations/channel-manager/{providerCode}", h.requirePageAuth(true, h.handleChannelManagerSavePost))
	mux.HandleFunc("POST /settings/integrations/channel-manager/{providerCode}/status", h.requirePageAuth(true, h.handleChannelManagerStatusPost))
	mux.HandleFunc("POST /settings/integrations/channel-manager/{providerCode}/delete", h.requirePageAuth(true, h.handleChannelManagerDeletePost))
}

// --- Calendar ---

func (h *Handler) handleCalendarGet(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	base, err := h.buildBaseView(r.Context(), r, actor, "Booking Calendar", "calendar")
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load property")
		return
	}

	q := r.URL.Query()
	cq := service.CalendarQuery{
		PropertyID:  base.Property.ID,
		View:        q.Get("view"),
		UnitTypeID:  parseOptionalUUID(q.Get("unit_type_id")),
		ChannelCode: q.Get("channel_code"),
		Status:      q.Get("status"),
	}
	if sRaw := q.Get("start_date"); sRaw != "" {
		if d, err := domain.ParseDate(sRaw); err == nil {
			cq.StartDate = d
		}
	}

	cal, err := h.svc.GetCalendarView(r.Context(), actor, cq)
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load calendar")
		return
	}

	var prevStart, nextStart time.Time
	switch cal.View {
	case "day":
		prevStart = cal.StartDate.AddDate(0, 0, -1)
		nextStart = cal.StartDate.AddDate(0, 0, 1)
	case "week":
		prevStart = cal.StartDate.AddDate(0, 0, -7)
		nextStart = cal.StartDate.AddDate(0, 0, 7)
	default:
		prevStart = cal.StartDate.AddDate(0, -1, 0)
		nextStart = cal.StartDate.AddDate(0, 1, 0)
	}

	h.renderPage(w, "calendar.html", struct {
		baseView
		Calendar          service.CalendarViewData
		PrevStart         string
		NextStart         string
		FilterUnitTypeID  string
		FilterChannelCode string
		FilterStatus      string
	}{
		baseView:          base,
		Calendar:          cal,
		PrevStart:         domain.FormatDate(prevStart),
		NextStart:         domain.FormatDate(nextStart),
		FilterUnitTypeID:  q.Get("unit_type_id"),
		FilterChannelCode: strings.ToUpper(strings.TrimSpace(q.Get("channel_code"))),
		FilterStatus:      strings.ToUpper(strings.TrimSpace(q.Get("status"))),
	})
}

// --- Reservations & Channels ---

func (h *Handler) handleReservationsListGet(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	base, err := h.buildBaseView(r.Context(), r, actor, "Reservations", "reservations")
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load property")
		return
	}

	q := r.URL.Query()
	filter := postgres.ReservationFilter{
		Status:      q.Get("status"),
		ChannelCode: q.Get("channel_code"),
	}
	if sRaw, eRaw := q.Get("start_date"), q.Get("end_date"); sRaw != "" && eRaw != "" {
		if sd, err1 := domain.ParseDate(sRaw); err1 == nil {
			if ed, err2 := domain.ParseDate(eRaw); err2 == nil {
				filter.StartDate = &sd
				filter.EndDate = &ed
			}
		}
	}

	reservations, err := h.svc.ListReservations(r.Context(), actor, base.Property.ID, filter)
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load reservations")
		return
	}
	channelsList, _ := h.svc.ListBookingChannels(r.Context(), actor, base.Property.ID, true)

	h.renderPage(w, "reservations.html", struct {
		baseView
		Reservations      []domain.Reservation
		Channels          []domain.BookingChannel
		Statuses          []domain.ReservationStatus
		FilterStatus      string
		FilterChannelCode string
		FilterStartDate   string
		FilterEndDate     string
	}{
		baseView:     base,
		Reservations: reservations,
		Channels:     channelsList,
		Statuses: []domain.ReservationStatus{
			domain.ResStatusInquiry,
			domain.ResStatusPending,
			domain.ResStatusConfirmed,
			domain.ResStatusCheckedIn,
			domain.ResStatusCheckedOut,
			domain.ResStatusCancelled,
			domain.ResStatusNoShow,
		},
		FilterStatus:      strings.ToUpper(strings.TrimSpace(q.Get("status"))),
		FilterChannelCode: strings.ToUpper(strings.TrimSpace(q.Get("channel_code"))),
		FilterStartDate:   q.Get("start_date"),
		FilterEndDate:     q.Get("end_date"),
	})
}

func (h *Handler) handleReservationNewGet(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	base, err := h.buildBaseView(r.Context(), r, actor, "New Reservation", "reservations")
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Failed to Load Property", "Unable to retrieve property metadata.")
		return
	}
	allUnits, _ := h.svc.ListUnits(r.Context(), actor, base.Property.ID, nil)
	unitTypes, _ := h.svc.ListUnitTypes(r.Context(), actor, base.Property.ID)
	utMap := make(map[uuid.UUID]domain.UnitType, len(unitTypes))
	for _, ut := range unitTypes {
		utMap[ut.ID] = ut
	}
	var bookableUnits []domain.Unit
	for _, u := range allUnits {
		if u.Status != domain.UnitStatusInactive && u.Status != domain.UnitStatusRetired && u.ArchivedAt == nil {
			bookableUnits = append(bookableUnits, u)
		}
	}
	channelsList, _ := h.svc.ListBookingChannels(r.Context(), actor, base.Property.ID, true)

	q := r.URL.Query()
	var prefillUnits []uuid.UUID
	for _, raw := range q["unit_ids"] {
		if id, err := uuid.Parse(raw); err == nil {
			prefillUnits = append(prefillUnits, id)
		}
	}
	arrival := q.Get("arrival_date")
	departure := q.Get("departure_date")
	if arrival == "" {
		today := time.Now().UTC()
		arrival = domain.FormatDate(today)
		departure = domain.FormatDate(today.AddDate(0, 0, 2))
	} else if departure == "" {
		if sd, err := domain.ParseDate(arrival); err == nil {
			departure = domain.FormatDate(sd.AddDate(0, 0, 1))
		}
	}

	h.renderPage(w, "reservation_new.html", struct {
		baseView
		Units            []domain.Unit
		UnitTypesByID    map[uuid.UUID]domain.UnitType
		Channels         []domain.BookingChannel
		PrefillUnits     []uuid.UUID
		PrefillArrival   string
		PrefillDeparture string
	}{
		baseView:         base,
		Units:            bookableUnits,
		UnitTypesByID:    utMap,
		Channels:         channelsList,
		PrefillUnits:     prefillUnits,
		PrefillArrival:   arrival,
		PrefillDeparture: departure,
	})
}

func (h *Handler) handleReservationCreatePost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	base, err := h.buildBaseView(r.Context(), r, actor, "New Reservation", "reservations")
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Failed to Load Property", "Unable to retrieve property metadata.")
		return
	}
	arrival, err := domain.ParseDate(r.FormValue("arrival_date"))
	if err != nil {
		h.redirectErr(w, r, "/reservations/new", err)
		return
	}
	departure, err := domain.ParseDate(r.FormValue("departure_date"))
	if err != nil {
		h.redirectErr(w, r, "/reservations/new", err)
		return
	}
	unitIDs := parseUUIDsForm(r.Form["unit_ids"])
	allocUnits := parseReservationUnitAllocationsForm(r, unitIDs)

	created, err := h.svc.CreateReservation(r.Context(), actor, domain.Reservation{
		PropertyID:    base.Property.ID,
		GuestName:     r.FormValue("guest_name"),
		GuestEmail:    r.FormValue("guest_email"),
		GuestPhone:    r.FormValue("guest_phone"),
		ChannelCode:   r.FormValue("channel_code"),
		ArrivalDate:   arrival,
		DepartureDate: departure,
		Adults:        parseIntDefault(r.FormValue("adults"), 1),
		Children:      parseIntDefault(r.FormValue("children"), 0),
		Infants:       parseIntDefault(r.FormValue("infants"), 0),
		Status:        domain.ReservationStatus(r.FormValue("status")),
		InternalNotes: r.FormValue("internal_notes"),
		UnitIDs:       unitIDs,
		Units:         allocUnits,
	})
	if err != nil {
		h.redirectErr(w, r, "/reservations/new", err)
		return
	}
	redirectMsg(w, r, "/reservations/"+created.ID.String(), "Reservation "+created.Reference+" created.")
}

func (h *Handler) handleReservationDetailGet(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.renderError(w, http.StatusBadRequest, "Invalid Request", "invalid reservation id")
		return
	}
	base, err := h.buildBaseView(r.Context(), r, actor, "Reservation Details", "reservations")
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load property")
		return
	}
	res, err := h.svc.GetReservation(r.Context(), actor, base.Property.ID, id)
	if err != nil {
		h.renderError(w, http.StatusNotFound, "Not Found", "reservation not found")
		return
	}
	allUnits, _ := h.svc.ListUnits(r.Context(), actor, base.Property.ID, nil)
	var bookableUnits []domain.Unit
	for _, u := range allUnits {
		if u.Status != domain.UnitStatusInactive && u.Status != domain.UnitStatusRetired && u.ArchivedAt == nil {
			bookableUnits = append(bookableUnits, u)
		}
	}
	units := bookableUnits
	channelsList, _ := h.svc.ListBookingChannels(r.Context(), actor, base.Property.ID, true)

	candidates := []domain.ReservationStatus{
		domain.ResStatusPending,
		domain.ResStatusConfirmed,
		domain.ResStatusCheckedIn,
		domain.ResStatusCheckedOut,
		domain.ResStatusNoShow,
		domain.ResStatusCancelled,
	}
	var allowed []domain.ReservationStatus
	for _, c := range candidates {
		if res.Status.CanTransitionTo(c) {
			allowed = append(allowed, c)
		}
	}
	canEdit := res.Status != domain.ResStatusCancelled && res.Status != domain.ResStatusCheckedOut && res.Status != domain.ResStatusNoShow
	fin, _ := h.svc.GetReservationFinancials(r.Context(), actor, base.Property.ID, id)

	h.renderPage(w, "reservation_detail.html", struct {
		baseView
		Reservation        domain.Reservation
		Units              []domain.Unit
		Channels           []domain.BookingChannel
		AllowedTransitions []domain.ReservationStatus
		CanEdit            bool
		Quote              *domain.BookingQuote
		Payments           []domain.PaymentRecord
	}{
		baseView:           base,
		Reservation:        res,
		Units:              units,
		Channels:           channelsList,
		AllowedTransitions: allowed,
		CanEdit:            canEdit,
		Quote:              fin.Quote,
		Payments:           fin.Payments,
	})
}

func (h *Handler) handleReservationUpdatePost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.renderError(w, http.StatusBadRequest, "Invalid Request", "invalid id")
		return
	}
	base, err := h.buildBaseView(r.Context(), r, actor, "Reservation Details", "reservations")
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load property")
		return
	}
	arrival, err := domain.ParseDate(r.FormValue("arrival_date"))
	if err != nil {
		h.redirectErr(w, r, "/reservations/"+id.String(), err)
		return
	}
	departure, err := domain.ParseDate(r.FormValue("departure_date"))
	if err != nil {
		h.redirectErr(w, r, "/reservations/"+id.String(), err)
		return
	}
	unitIDs := parseUUIDsForm(r.Form["unit_ids"])
	allocUnits := parseReservationUnitAllocationsForm(r, unitIDs)
	_, err = h.svc.UpdateReservation(r.Context(), actor, domain.Reservation{
		ID:              id,
		PropertyID:      base.Property.ID,
		GuestName:       r.FormValue("guest_name"),
		GuestEmail:      r.FormValue("guest_email"),
		GuestPhone:      r.FormValue("guest_phone"),
		ChannelCode:     r.FormValue("channel_code"),
		ArrivalDate:     arrival,
		DepartureDate:   departure,
		Adults:          parseIntDefault(r.FormValue("adults"), 1),
		Children:        parseIntDefault(r.FormValue("children"), 0),
		Infants:         parseIntDefault(r.FormValue("infants"), 0),
		OccupantDetails: r.FormValue("occupant_details"),
		InternalNotes:   r.FormValue("internal_notes"),
		UnitIDs:         unitIDs,
		Units:           allocUnits,
		Version:         parseIntDefault(r.FormValue("version"), 1),
	})
	if err != nil {
		h.redirectErr(w, r, "/reservations/"+id.String(), err)
		return
	}
	redirectMsg(w, r, "/reservations/"+id.String(), "Reservation updated.")
}

func (h *Handler) handleReservationTransitionPost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.renderError(w, http.StatusBadRequest, "Invalid Request", "invalid id")
		return
	}
	base, err := h.buildBaseView(r.Context(), r, actor, "Reservation Details", "reservations")
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load property")
		return
	}
	nextStatus := domain.ReservationStatus(r.FormValue("status"))
	retPath := safeAdminReturnPath(r.FormValue("return_to"), "/reservations/"+id.String())
	updated, err := h.svc.TransitionReservationStatus(
		r.Context(), actor, base.Property.ID, id, nextStatus, r.FormValue("reason"), parseIntDefault(r.FormValue("version"), 1),
	)
	if err != nil {
		if retPath != "/reservations/"+id.String() {
			redirectAdminWithQuery(w, r, retPath, "err", err.Error())
			return
		}
		h.redirectErr(w, r, "/reservations/"+id.String(), err)
		return
	}
	if retPath != "/reservations/"+id.String() {
		msg := "Reservation " + updated.Reference + " transitioned to " + string(updated.Status) + "."
		if updated.Status == domain.ResStatusCheckedOut {
			msg = "Checked out " + updated.Reference + " and queued unit(s) for cleaning."
		}
		redirectAdminWithQuery(w, r, retPath, "msg", msg)
		return
	}
	redirectMsg(w, r, "/reservations/"+id.String(), "Reservation status transitioned to "+string(updated.Status)+".")
}

func (h *Handler) handleBookingChannelCreatePost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	base, err := h.buildBaseView(r.Context(), r, actor, "Reservations", "reservations")
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load property")
		return
	}
	ch, err := h.svc.CreateBookingChannel(r.Context(), actor, domain.BookingChannel{
		PropertyID: base.Property.ID,
		Code:       r.FormValue("code"),
		Name:       r.FormValue("name"),
		Category:   r.FormValue("category"),
	})
	if err != nil {
		h.redirectErr(w, r, "/reservations", err)
		return
	}
	redirectMsg(w, r, "/reservations", "Booking channel "+ch.Code+" added.")
}

// --- Bulk Blockouts ---

type blocksPageData struct {
	baseView
	Units           []domain.Unit
	UnitTypes       []domain.UnitType
	Channels        []domain.BookingChannel
	Groups          []domain.BlockoutGroup
	IncludeReleased bool
	Preview         *domain.BlockoutPreview
	PreviewInput    domain.BulkBlockoutInput
	SelectedGroup   *domain.BlockoutGroup
	SelectedClaims  []domain.InventoryClaim
}

func (h *Handler) buildBlocksPageData(r *http.Request, actor service.Actor, base baseView) (blocksPageData, error) {
	includeReleased := r.URL.Query().Get("include_released") == "true"
	units, _ := h.svc.ListUnits(r.Context(), actor, base.Property.ID, nil)
	unitTypes, _ := h.svc.ListUnitTypes(r.Context(), actor, base.Property.ID)
	channelsList, _ := h.svc.ListBookingChannels(r.Context(), actor, base.Property.ID, true)
	groups, err := h.svc.ListBlockoutGroups(r.Context(), actor, base.Property.ID, includeReleased)
	if err != nil {
		return blocksPageData{}, err
	}

	data := blocksPageData{
		baseView:        base,
		Units:           units,
		UnitTypes:       unitTypes,
		Channels:        channelsList,
		Groups:          groups,
		IncludeReleased: includeReleased,
	}
	if gid := parseOptionalUUID(r.URL.Query().Get("group_id")); gid != nil {
		if grp, claims, err := h.svc.GetBlockoutGroup(r.Context(), actor, base.Property.ID, *gid); err == nil {
			data.SelectedGroup = &grp
			data.SelectedClaims = claims
		}
	}
	return data, nil
}

func (h *Handler) handleBlocksGet(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	base, err := h.buildBaseView(r.Context(), r, actor, "Bulk Blockouts", "blocks")
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load property")
		return
	}
	data, err := h.buildBlocksPageData(r, actor, base)
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load blockouts")
		return
	}
	h.renderPage(w, "blocks.html", data)
}

func (h *Handler) parseBulkBlockoutForm(r *http.Request, propID uuid.UUID) (domain.BulkBlockoutInput, error) {
	sd, err := domain.ParseDate(r.FormValue("start_date"))
	if err != nil {
		return domain.BulkBlockoutInput{}, err
	}
	ed, err := domain.ParseDate(r.FormValue("end_date"))
	if err != nil {
		return domain.BulkBlockoutInput{}, err
	}
	unitIDs := parseUUIDsForm(r.Form["unit_ids"])
	scope := domain.BlockoutScope(r.FormValue("scope_type"))
	if scope == domain.BlockScopeUnit && len(unitIDs) > 1 {
		unitIDs = unitIDs[:1]
	}
	return domain.BulkBlockoutInput{
		PropertyID:         propID,
		ScopeType:          scope,
		UnitIDs:            unitIDs,
		UnitTypeID:         parseOptionalUUID(r.FormValue("unit_type_id")),
		Category:           domain.ClaimType(r.FormValue("category")),
		ChannelScope:       r.FormValue("channel_scope"),
		StartDate:          sd,
		EndDate:            ed,
		Reason:             r.FormValue("reason"),
		ApplyToFutureUnits: r.FormValue("apply_to_future_units") == "on" || r.FormValue("apply_to_future_units") == "true",
	}, nil
}

func (h *Handler) handleBlockoutPreviewPost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	base, err := h.buildBaseView(r.Context(), r, actor, "Bulk Blockouts", "blocks")
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load property")
		return
	}
	in, err := h.parseBulkBlockoutForm(r, base.Property.ID)
	if err != nil {
		h.redirectErr(w, r, "/blocks", err)
		return
	}
	preview, err := h.svc.PreviewBulkBlockout(r.Context(), actor, in)
	if err != nil {
		h.redirectErr(w, r, "/blocks", err)
		return
	}
	data, err := h.buildBlocksPageData(r, actor, base)
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load blockouts")
		return
	}
	data.Preview = &preview
	data.PreviewInput = in
	h.renderPage(w, "blocks.html", data)
}

func (h *Handler) handleBlockoutBulkPost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	base, err := h.buildBaseView(r.Context(), r, actor, "Bulk Blockouts", "blocks")
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load property")
		return
	}
	in, err := h.parseBulkBlockoutForm(r, base.Property.ID)
	if err != nil {
		h.redirectErr(w, r, "/blocks", err)
		return
	}
	grp, claims, err := h.svc.CreateBulkBlockout(r.Context(), actor, in)
	if err != nil {
		h.redirectErr(w, r, "/blocks", err)
		return
	}
	redirectMsg(w, r, "/blocks", "Created "+string(grp.Category)+" blockout across "+strconv.Itoa(len(claims))+" unit(s).")
}

func (h *Handler) handleBlockoutGroupReleasePost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.renderError(w, http.StatusBadRequest, "Invalid Request", "invalid group id")
		return
	}
	base, err := h.buildBaseView(r.Context(), r, actor, "Bulk Blockouts", "blocks")
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load property")
		return
	}
	unitIDs := parseUUIDsForm(r.Form["unit_ids"])
	if _, _, err := h.svc.ReleaseBlockout(r.Context(), actor, base.Property.ID, id, unitIDs); err != nil {
		h.redirectErr(w, r, "/blocks", err)
		return
	}
	http.Redirect(w, r, "/blocks?group_id="+id.String()+"&msg="+url.QueryEscape("Blockout claims released."), http.StatusSeeOther)
}

func (h *Handler) handleBlockoutGroupReinstatePost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.renderError(w, http.StatusBadRequest, "Invalid Request", "invalid group id")
		return
	}
	base, err := h.buildBaseView(r.Context(), r, actor, "Bulk Blockouts", "blocks")
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load property")
		return
	}
	claimIDs := parseUUIDsForm(r.Form["claim_ids"])
	unitIDs := parseUUIDsForm(r.Form["unit_ids"])
	if _, _, err := h.svc.ReinstateBlockout(r.Context(), actor, base.Property.ID, id, claimIDs, unitIDs); err != nil {
		h.redirectErr(w, r, "/blocks?group_id="+id.String(), err)
		return
	}
	http.Redirect(w, r, "/blocks?group_id="+id.String()+"&msg="+url.QueryEscape("Released blockout claim(s) reinstated."), http.StatusSeeOther)
}

func (h *Handler) handleBlockoutGroupDatesPost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.renderError(w, http.StatusBadRequest, "Invalid Request", "invalid group id")
		return
	}
	base, err := h.buildBaseView(r.Context(), r, actor, "Bulk Blockouts", "blocks")
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load property")
		return
	}
	sd, err := domain.ParseDate(r.FormValue("start_date"))
	if err != nil {
		h.redirectErr(w, r, "/blocks", err)
		return
	}
	ed, err := domain.ParseDate(r.FormValue("end_date"))
	if err != nil {
		h.redirectErr(w, r, "/blocks", err)
		return
	}
	version := parseIntDefault(r.FormValue("version"), 1)
	if _, _, err := h.svc.UpdateBlockoutDates(r.Context(), actor, base.Property.ID, id, sd, ed, version); err != nil {
		h.redirectErr(w, r, "/blocks", err)
		return
	}
	redirectMsg(w, r, "/blocks", "Blockout dates updated.")
}

// --- Scheduled Availability Releases ---

func (h *Handler) handleReleasesGet(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	base, err := h.buildBaseView(r.Context(), r, actor, "Scheduled Releases", "releases")
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load property")
		return
	}
	includeCancelled := r.URL.Query().Get("include_cancelled") == "true"
	rules, err := h.svc.ListReleaseRules(r.Context(), actor, base.Property.ID, includeCancelled)
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load release rules")
		return
	}
	units, _ := h.svc.ListUnits(r.Context(), actor, base.Property.ID, nil)
	unitTypes, _ := h.svc.ListUnitTypes(r.Context(), actor, base.Property.ID)
	channelsList, _ := h.svc.ListBookingChannels(r.Context(), actor, base.Property.ID, true)

	h.renderPage(w, "releases.html", struct {
		baseView
		Rules            []domain.AvailabilityReleaseRule
		Units            []domain.Unit
		UnitTypes        []domain.UnitType
		Channels         []domain.BookingChannel
		IncludeCancelled bool
	}{
		baseView:         base,
		Rules:            rules,
		Units:            units,
		UnitTypes:        unitTypes,
		Channels:         channelsList,
		IncludeCancelled: includeCancelled,
	})
}

func (h *Handler) handleReleasesCreatePost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	base, err := h.buildBaseView(r.Context(), r, actor, "Scheduled Releases", "releases")
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load property")
		return
	}
	sd, err := domain.ParseDate(r.FormValue("start_date"))
	if err != nil {
		h.redirectErr(w, r, "/releases", err)
		return
	}
	ed, err := domain.ParseDate(r.FormValue("end_date"))
	if err != nil {
		h.redirectErr(w, r, "/releases", err)
		return
	}
	loc, err := time.LoadLocation(base.Property.Timezone)
	if err != nil {
		loc = time.UTC
	}
	releaseAt, err := time.ParseInLocation("2006-01-02T15:04", r.FormValue("release_at"), loc)
	if err != nil {
		if tRFC, err2 := time.Parse(time.RFC3339, r.FormValue("release_at")); err2 == nil {
			releaseAt = tRFC
		} else {
			h.redirectErr(w, r, "/releases", err)
			return
		}
	}

	_, err = h.svc.CreateReleaseRule(r.Context(), actor, domain.AvailabilityReleaseRule{
		PropertyID:  base.Property.ID,
		ScopeType:   domain.ReleaseScope(r.FormValue("scope_type")),
		UnitID:      parseOptionalUUID(r.FormValue("unit_id")),
		UnitTypeID:  parseOptionalUUID(r.FormValue("unit_type_id")),
		StartDate:   sd,
		EndDate:     ed,
		ChannelCode: r.FormValue("channel_code"),
		ReleaseAt:   releaseAt.UTC(),
		Notes:       r.FormValue("notes"),
	})
	if err != nil {
		h.redirectErr(w, r, "/releases", err)
		return
	}
	redirectMsg(w, r, "/releases", "Scheduled availability release rule created.")
}

func (h *Handler) handleReleasesManualPost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.renderError(w, http.StatusBadRequest, "Invalid Request", "invalid rule id")
		return
	}
	base, err := h.buildBaseView(r.Context(), r, actor, "Scheduled Releases", "releases")
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load property")
		return
	}
	if _, err := h.svc.ManualReleaseRule(r.Context(), actor, base.Property.ID, id); err != nil {
		h.redirectErr(w, r, "/releases", err)
		return
	}
	redirectMsg(w, r, "/releases", "Inventory availability released immediately.")
}

func (h *Handler) handleReleasesCancelPost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.renderError(w, http.StatusBadRequest, "Invalid Request", "invalid rule id")
		return
	}
	base, err := h.buildBaseView(r.Context(), r, actor, "Scheduled Releases", "releases")
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load property")
		return
	}
	if _, err := h.svc.CancelReleaseRule(r.Context(), actor, base.Property.ID, id); err != nil {
		h.redirectErr(w, r, "/releases", err)
		return
	}
	redirectMsg(w, r, "/releases", "Scheduled release rule cancelled.")
}

// --- Channel Manager Integrations ---

func (h *Handler) handleChannelManagerGet(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	base, err := h.buildBaseView(r.Context(), r, actor, "Channel Manager Integrations", "channel-manager")
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load property")
		return
	}
	providers, err := h.svc.ListChannelProviders(r.Context(), actor, base.Property.ID)
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load channel integrations")
		return
	}
	h.renderPage(w, "channel_manager.html", struct {
		baseView
		Providers []service.ChannelProviderView
	}{
		baseView:  base,
		Providers: providers,
	})
}

func (h *Handler) handleChannelManagerSavePost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	providerCode := r.PathValue("providerCode")
	base, err := h.buildBaseView(r.Context(), r, actor, "Channel Manager Integrations", "channel-manager")
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load property")
		return
	}
	inputs := make(map[string]string)
	for key, vals := range r.PostForm {
		if strings.HasPrefix(key, "input_") && len(vals) > 0 {
			inputs[strings.TrimPrefix(key, "input_")] = vals[0]
		}
	}
	_, err = h.svc.SaveChannelConfig(r.Context(), actor, service.SaveChannelConfigInput{
		PropertyID:   base.Property.ID,
		ProviderCode: providerCode,
		DisplayName:  r.FormValue("display_name"),
		Inputs:       inputs,
	})
	if err != nil {
		h.redirectErr(w, r, "/settings/integrations/channel-manager", err)
		return
	}
	redirectMsg(w, r, "/settings/integrations/channel-manager", "Channel configuration encrypted and saved.")
}

func (h *Handler) handleChannelManagerStatusPost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	providerCode := r.PathValue("providerCode")
	base, err := h.buildBaseView(r.Context(), r, actor, "Channel Manager Integrations", "channel-manager")
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load property")
		return
	}
	if _, err := h.svc.SetChannelStatus(r.Context(), actor, base.Property.ID, providerCode, channels.ConnectionStatus(r.FormValue("status"))); err != nil {
		h.redirectErr(w, r, "/settings/integrations/channel-manager", err)
		return
	}
	redirectMsg(w, r, "/settings/integrations/channel-manager", "Integration status updated.")
}

func (h *Handler) handleChannelManagerDeletePost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	providerCode := r.PathValue("providerCode")
	base, err := h.buildBaseView(r.Context(), r, actor, "Channel Manager Integrations", "channel-manager")
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load property")
		return
	}
	if err := h.svc.DeleteChannelConfig(r.Context(), actor, base.Property.ID, providerCode); err != nil {
		h.redirectErr(w, r, "/settings/integrations/channel-manager", err)
		return
	}
	redirectMsg(w, r, "/settings/integrations/channel-manager", "Stored channel credentials removed.")
}

func parseUUIDsForm(vals []string) []uuid.UUID {
	var out []uuid.UUID
	for _, s := range vals {
		if id, err := uuid.Parse(strings.TrimSpace(s)); err == nil {
			out = append(out, id)
		}
	}
	return out
}

func parseReservationUnitAllocationsForm(r *http.Request, unitIDs []uuid.UUID) []domain.ReservationUnit {
	if len(unitIDs) == 0 {
		return nil
	}
	var out []domain.ReservationUnit
	hasAny := false
	for _, uid := range unitIDs {
		a := parseIntDefault(r.FormValue("unit_adults_"+uid.String()), 0)
		c := parseIntDefault(r.FormValue("unit_children_"+uid.String()), 0)
		inf := parseIntDefault(r.FormValue("unit_infants_"+uid.String()), 0)
		if a > 0 || c > 0 || inf > 0 {
			hasAny = true
		}
		out = append(out, domain.ReservationUnit{
			UnitID:   uid,
			Adults:   a,
			Children: c,
			Infants:  inf,
		})
	}
	if !hasAny {
		return nil
	}
	return out
}
