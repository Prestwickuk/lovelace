package httpapi

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/tirbach/tir-bach/internal/domain"
	"github.com/tirbach/tir-bach/internal/service"
	"github.com/tirbach/tir-bach/internal/store/postgres"
)

func (h *Handler) registerReservationRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/properties/{propertyID}/channels", h.requireAuth(false, h.handleListBookingChannels))
	mux.HandleFunc("POST /api/v1/properties/{propertyID}/channels", h.requireAuth(true, h.handleCreateBookingChannel))

	mux.HandleFunc("GET /api/v1/properties/{propertyID}/reservations", h.requireAuth(false, h.handleListReservations))
	mux.HandleFunc("POST /api/v1/properties/{propertyID}/reservations", h.requireAuth(true, h.handleCreateReservation))
	mux.HandleFunc("GET /api/v1/properties/{propertyID}/reservations/{id}", h.requireAuth(false, h.handleGetReservation))
	mux.HandleFunc("PATCH /api/v1/properties/{propertyID}/reservations/{id}", h.requireAuth(true, h.handleUpdateReservation))
	mux.HandleFunc("POST /api/v1/properties/{propertyID}/reservations/{id}/transition", h.requireAuth(true, h.handleTransitionReservation))
}

func (h *Handler) handleListBookingChannels(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	activeOnly := r.URL.Query().Get("active_only") == "true"
	items, err := h.svc.ListBookingChannels(r.Context(), actor, propID, activeOnly)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": nonNil(items)})
}

func (h *Handler) handleCreateBookingChannel(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	var body struct {
		Code      string `json:"code"`
		Name      string `json:"name"`
		Category  string `json:"category"`
		SortOrder int    `json:"sort_order"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	created, err := h.svc.CreateBookingChannel(r.Context(), actor, domain.BookingChannel{
		PropertyID: propID,
		Code:       body.Code,
		Name:       body.Name,
		Category:   body.Category,
		SortOrder:  body.SortOrder,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *Handler) handleListReservations(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	q := r.URL.Query()
	filter := postgres.ReservationFilter{
		Status:      q.Get("status"),
		ChannelCode: q.Get("channel_code"),
	}
	if sRaw, eRaw := q.Get("start_date"), q.Get("end_date"); sRaw != "" && eRaw != "" {
		s, err1 := domain.ParseDate(sRaw)
		e, err2 := domain.ParseDate(eRaw)
		if err1 != nil || err2 != nil {
			writeError(w, http.StatusBadRequest, "invalid_date", "start_date and end_date must be YYYY-MM-DD")
			return
		}
		filter.StartDate = &s
		filter.EndDate = &e
	}
	items, err := h.svc.ListReservations(r.Context(), actor, propID, filter)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": nonNil(items)})
}

func (h *Handler) handleGetReservation(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	id, ok := parseUUIDParam(w, r, "id")
	if !ok {
		return
	}
	item, err := h.svc.GetReservation(r.Context(), actor, propID, id)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h *Handler) handleCreateReservation(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	type reservationUnitReq struct {
		UnitID   string `json:"unit_id"`
		Adults   int    `json:"adults"`
		Children int    `json:"children"`
		Infants  int    `json:"infants"`
	}
	var body struct {
		GuestName     string               `json:"guest_name"`
		GuestEmail    string               `json:"guest_email"`
		GuestPhone    string               `json:"guest_phone"`
		ChannelCode   string               `json:"channel_code"`
		ArrivalDate   string               `json:"arrival_date"`
		DepartureDate string               `json:"departure_date"`
		Adults        int                  `json:"adults"`
		Children      int                  `json:"children"`
		Infants       int                  `json:"infants"`
		Status        string               `json:"status"`
		InternalNotes string               `json:"internal_notes"`
		UnitIDs       []string             `json:"unit_ids"`
		Units         []reservationUnitReq `json:"units"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}

	arrival, err := domain.ParseDate(body.ArrivalDate)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_date", "arrival_date must be YYYY-MM-DD")
		return
	}
	departure, err := domain.ParseDate(body.DepartureDate)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_date", "departure_date must be YYYY-MM-DD")
		return
	}
	unitIDs, ok := parseUUIDSlice(w, "unit_ids", body.UnitIDs)
	if !ok {
		return
	}
	var allocUnits []domain.ReservationUnit
	for _, ru := range body.Units {
		uid, err := uuid.Parse(ru.UnitID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_id", "invalid unit_id in units")
			return
		}
		allocUnits = append(allocUnits, domain.ReservationUnit{
			UnitID:   uid,
			Adults:   ru.Adults,
			Children: ru.Children,
			Infants:  ru.Infants,
		})
	}

	created, err := h.svc.CreateReservation(r.Context(), actor, domain.Reservation{
		PropertyID:    propID,
		GuestName:     body.GuestName,
		GuestEmail:    body.GuestEmail,
		GuestPhone:    body.GuestPhone,
		ChannelCode:   body.ChannelCode,
		ArrivalDate:   arrival,
		DepartureDate: departure,
		Adults:        body.Adults,
		Children:      body.Children,
		Infants:       body.Infants,
		Status:        domain.ReservationStatus(body.Status),
		InternalNotes: body.InternalNotes,
		UnitIDs:       unitIDs,
		Units:         allocUnits,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *Handler) handleUpdateReservation(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	id, ok := parseUUIDParam(w, r, "id")
	if !ok {
		return
	}
	type reservationUnitReq struct {
		UnitID   string `json:"unit_id"`
		Adults   int    `json:"adults"`
		Children int    `json:"children"`
		Infants  int    `json:"infants"`
	}
	var body struct {
		GuestName     string               `json:"guest_name"`
		GuestEmail    string               `json:"guest_email"`
		GuestPhone    string               `json:"guest_phone"`
		ChannelCode   string               `json:"channel_code"`
		ArrivalDate   string               `json:"arrival_date"`
		DepartureDate string               `json:"departure_date"`
		Adults        int                  `json:"adults"`
		Children      int                  `json:"children"`
		Infants       int                  `json:"infants"`
		InternalNotes string               `json:"internal_notes"`
		UnitIDs       []string             `json:"unit_ids"`
		Units         []reservationUnitReq `json:"units"`
		Version       int                  `json:"version"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}

	arrival, err := domain.ParseDate(body.ArrivalDate)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_date", "arrival_date must be YYYY-MM-DD")
		return
	}
	departure, err := domain.ParseDate(body.DepartureDate)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_date", "departure_date must be YYYY-MM-DD")
		return
	}
	unitIDs, ok := parseUUIDSlice(w, "unit_ids", body.UnitIDs)
	if !ok {
		return
	}
	var allocUnits []domain.ReservationUnit
	for _, ru := range body.Units {
		uid, err := uuid.Parse(ru.UnitID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_id", "invalid unit_id in units")
			return
		}
		allocUnits = append(allocUnits, domain.ReservationUnit{
			UnitID:   uid,
			Adults:   ru.Adults,
			Children: ru.Children,
			Infants:  ru.Infants,
		})
	}

	updated, err := h.svc.UpdateReservation(r.Context(), actor, domain.Reservation{
		ID:            id,
		PropertyID:    propID,
		GuestName:     body.GuestName,
		GuestEmail:    body.GuestEmail,
		GuestPhone:    body.GuestPhone,
		ChannelCode:   body.ChannelCode,
		ArrivalDate:   arrival,
		DepartureDate: departure,
		Adults:        body.Adults,
		Children:      body.Children,
		Infants:       body.Infants,
		InternalNotes: body.InternalNotes,
		UnitIDs:       unitIDs,
		Units:         allocUnits,
		Version:       body.Version,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *Handler) handleTransitionReservation(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	id, ok := parseUUIDParam(w, r, "id")
	if !ok {
		return
	}
	var body struct {
		Status  string `json:"status"`
		Reason  string `json:"reason"`
		Version int    `json:"version"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	updated, err := h.svc.TransitionReservationStatus(
		r.Context(),
		actor,
		propID,
		id,
		domain.ReservationStatus(body.Status),
		body.Reason,
		body.Version,
	)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func parseUUIDSlice(w http.ResponseWriter, field string, raw []string) ([]uuid.UUID, bool) {
	out := make([]uuid.UUID, 0, len(raw))
	for _, s := range raw {
		id, err := uuid.Parse(s)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_id", "invalid UUID in "+field)
			return nil, false
		}
		out = append(out, id)
	}
	return out, true
}
