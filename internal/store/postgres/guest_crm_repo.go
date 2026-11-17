package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tirbach/tir-bach/internal/domain"
)

func scanGuestRow(r scannable) (domain.Guest, error) {
	var g domain.Guest
	var status string
	err := r.Scan(
		&g.ID, &g.PropertyID, &g.FirstName, &g.LastName, &g.FullName,
		&g.Email, &g.NormalizedEmail, &g.Phone, &g.NormalizedPhone,
		&g.PreferredContactMethod, &g.PreferredLanguage, &status,
		&g.AddressJSON, &g.Preferences, &g.Notes,
		&g.MergedIntoGuestID, &g.MergedAt, &g.AnonymizedAt, &g.ErasureRequestedAt,
		&g.CreatedBy, &g.UpdatedBy, &g.Version, &g.CreatedAt, &g.UpdatedAt,
	)
	if err != nil {
		return domain.Guest{}, err
	}
	g.Status = domain.GuestStatus(status)
	return g, nil
}

// ResolveCanonicalGuestID follows merged_into_guest_id chains of arbitrary depth
// using an explicit cycle guard to return the active canonical guest ID.
func (s *Store) ResolveCanonicalGuestID(ctx context.Context, db DBTX, propertyID, guestID uuid.UUID) (uuid.UUID, error) {
	if db == nil {
		db = s.pool
	}
	curr := guestID
	visited := map[uuid.UUID]bool{curr: true}
	for {
		var mergedInto *uuid.UUID
		err := db.QueryRow(ctx, `
			SELECT merged_into_guest_id FROM guests WHERE property_id = $1 AND id = $2
		`, propertyID, curr).Scan(&mergedInto)
		if err != nil {
			return uuid.Nil, MapErr(err)
		}
		if mergedInto == nil || visited[*mergedInto] {
			return curr, nil
		}
		visited[*mergedInto] = true
		curr = *mergedInto
	}
}

// GetGuestClusterIDs resolves the canonical guest ID and collects all merged alias IDs
// in the same property-scoped merge tree (sorted deterministically for deadlock-safe locking).
func (s *Store) GetGuestClusterIDs(ctx context.Context, db DBTX, propertyID, guestID uuid.UUID) (uuid.UUID, []uuid.UUID, error) {
	if db == nil {
		db = s.pool
	}
	canonicalID, err := s.ResolveCanonicalGuestID(ctx, db, propertyID, guestID)
	if err != nil {
		return uuid.Nil, nil, err
	}
	rows, err := db.Query(ctx, `
		WITH RECURSIVE cluster AS (
			SELECT id, ARRAY[id] AS path
			FROM guests
			WHERE property_id = $1 AND id = $2
			UNION ALL
			SELECT child.id, p.path || child.id
			FROM guests child
			JOIN cluster p ON child.merged_into_guest_id = p.id
			WHERE child.property_id = $1
			  AND NOT (child.id = ANY(p.path))
		)
		SELECT DISTINCT id FROM cluster ORDER BY id ASC
	`, propertyID, canonicalID)
	if err != nil {
		return uuid.Nil, nil, MapErr(err)
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return uuid.Nil, nil, MapErr(err)
		}
		ids = append(ids, id)
	}
	return canonicalID, ids, MapErr(rows.Err())
}

// LockCanonicalGuestClusterTx locks the canonical guest and every merged alias row in sorted
// UUID order using SELECT ... FOR UPDATE before any mutation or communication queueing proceeds,
// repeating once if a concurrent merge moved canonicalID while waiting for the lock.
func (s *Store) LockCanonicalGuestClusterTx(ctx context.Context, tx pgx.Tx, propertyID, guestID uuid.UUID) (canonicalID uuid.UUID, clusterIDs []uuid.UUID, anonOrPending bool, err error) {
	for attempt := 0; attempt < 4; attempt++ {
		canonicalID, clusterIDs, err = s.GetGuestClusterIDs(ctx, tx, propertyID, guestID)
		if err != nil {
			return uuid.Nil, nil, false, err
		}
		if len(clusterIDs) == 0 {
			return uuid.Nil, nil, false, domain.ErrNotFound
		}
		rows, qErr := tx.Query(ctx, `
			SELECT id, status, anonymized_at, erasure_requested_at, merged_into_guest_id
			FROM guests
			WHERE property_id = $1 AND id = ANY($2)
			ORDER BY id ASC
			FOR UPDATE
		`, propertyID, clusterIDs)
		if qErr != nil {
			return uuid.Nil, nil, false, MapErr(qErr)
		}
		moved := false
		anonOrPending = false
		for rows.Next() {
			var id uuid.UUID
			var st string
			var anonAt, erasureAt *time.Time
			var mergedInto *uuid.UUID
			if sErr := rows.Scan(&id, &st, &anonAt, &erasureAt, &mergedInto); sErr != nil {
				rows.Close()
				return uuid.Nil, nil, false, MapErr(sErr)
			}
			if id == canonicalID && mergedInto != nil {
				moved = true
			}
			if st == string(domain.GuestStatusAnonymized) || anonAt != nil || erasureAt != nil {
				anonOrPending = true
			}
		}
		rows.Close()
		if sErr := rows.Err(); sErr != nil {
			return uuid.Nil, nil, false, MapErr(sErr)
		}
		if !moved {
			return canonicalID, clusterIDs, anonOrPending, nil
		}
	}
	return canonicalID, clusterIDs, anonOrPending, nil
}

// lockGuestClusterAndCheckErasureDB acquires cluster FOR UPDATE locks when db is a transaction,
// or checks cluster erasure status on a non-tx handle.
func (s *Store) lockGuestClusterAndCheckErasureDB(ctx context.Context, db DBTX, propertyID, guestID uuid.UUID) (bool, error) {
	if tx, ok := db.(pgx.Tx); ok {
		_, _, blocked, err := s.LockCanonicalGuestClusterTx(ctx, tx, propertyID, guestID)
		return blocked, err
	}
	_, clusterIDs, err := s.GetGuestClusterIDs(ctx, db, propertyID, guestID)
	if err != nil {
		return false, err
	}
	var blocked bool
	err = db.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM guests
			WHERE property_id = $1 AND id = ANY($2)
			  AND (status = 'ANONYMIZED' OR anonymized_at IS NOT NULL OR erasure_requested_at IS NOT NULL)
		)
	`, propertyID, clusterIDs).Scan(&blocked)
	return blocked, MapErr(err)
}

// ValidateGuestReservationLinkTx ensures that when both guestID and reservationID
// are supplied, both belong to propertyID and resolve to the same canonical guest.
func (s *Store) ValidateGuestReservationLinkTx(ctx context.Context, db DBTX, propertyID uuid.UUID, guestID, reservationID *uuid.UUID) error {
	if db == nil {
		db = s.pool
	}
	if guestID == nil || reservationID == nil {
		return nil
	}
	var resGuestID uuid.UUID
	err := db.QueryRow(ctx, `
		SELECT guest_id FROM reservations WHERE property_id = $1 AND id = $2
	`, propertyID, *reservationID).Scan(&resGuestID)
	if err != nil {
		return MapErr(err)
	}
	canonInput, err := s.ResolveCanonicalGuestID(ctx, db, propertyID, *guestID)
	if err != nil {
		return err
	}
	canonRes, err := s.ResolveCanonicalGuestID(ctx, db, propertyID, resGuestID)
	if err != nil {
		return err
	}
	if canonInput != canonRes {
		return domain.NewValidationError(map[string]string{
			"reservation_id": "reservation belongs to a different guest profile",
		})
	}
	return nil
}

func (s *Store) GetGuest(ctx context.Context, db DBTX, propertyID, guestID uuid.UUID) (domain.Guest, error) {
	if db == nil {
		db = s.pool
	}
	row := db.QueryRow(ctx, `
		SELECT id, property_id, first_name, last_name, full_name, email, normalized_email,
		       phone, normalized_phone, preferred_contact_method, preferred_language, status,
		       address_json, preferences, notes, merged_into_guest_id, merged_at, anonymized_at, erasure_requested_at,
		       created_by, updated_by, version, created_at, updated_at
		FROM guests
		WHERE property_id = $1 AND id = $2
	`, propertyID, guestID)
	g, err := scanGuestRow(row)
	return g, MapErr(err)
}

func (s *Store) CreateGuest(ctx context.Context, tx pgx.Tx, g domain.Guest) (domain.Guest, error) {
	if g.ID == uuid.Nil {
		id, err := NewUUID()
		if err != nil {
			return domain.Guest{}, err
		}
		g.ID = id
	}
	first, last, full := domain.SplitFullName(g.FirstName, g.LastName, g.FullName)
	normEmail := domain.NormalizeEmail(g.Email)
	normPhone := domain.NormalizePhone(g.Phone)
	method := strings.ToUpper(strings.TrimSpace(g.PreferredContactMethod))
	if method == "" {
		method = string(domain.ContactMethodEmail)
	}
	lang := strings.TrimSpace(strings.ToLower(g.PreferredLanguage))
	if lang == "" {
		lang = "en"
	}
	status := string(g.Status)
	if status == "" {
		status = string(domain.GuestStatusActive)
	}
	if len(g.AddressJSON) == 0 {
		g.AddressJSON = json.RawMessage(`{}`)
	}
	if len(g.Preferences) == 0 {
		g.Preferences = json.RawMessage(`{}`)
	}
	row := tx.QueryRow(ctx, `
		INSERT INTO guests (
			id, property_id, first_name, last_name, full_name, email, normalized_email,
			phone, normalized_phone, preferred_contact_method, preferred_language, status,
			address_json, preferences, notes, created_by, updated_by
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7,
			$8, $9, $10, $11, $12,
			$13, $14, $15, $16, $17
		)
		RETURNING id, property_id, first_name, last_name, full_name, email, normalized_email,
		          phone, normalized_phone, preferred_contact_method, preferred_language, status,
		          address_json, preferences, notes, merged_into_guest_id, merged_at, anonymized_at, erasure_requested_at,
		          created_by, updated_by, version, created_at, updated_at
	`,
		g.ID, g.PropertyID, first, last, full, normEmail, normEmail,
		strings.TrimSpace(g.Phone), normPhone, method, lang, status,
		g.AddressJSON, g.Preferences, strings.TrimSpace(g.Notes), g.CreatedBy, g.UpdatedBy,
	)
	out, err := scanGuestRow(row)
	return out, MapErr(err)
}

func (s *Store) UpdateGuest(ctx context.Context, tx pgx.Tx, g domain.Guest) (domain.Guest, error) {
	first, last, full := domain.SplitFullName(g.FirstName, g.LastName, g.FullName)
	normEmail := domain.NormalizeEmail(g.Email)
	normPhone := domain.NormalizePhone(g.Phone)
	method := strings.ToUpper(strings.TrimSpace(g.PreferredContactMethod))
	if method == "" {
		method = string(domain.ContactMethodEmail)
	}
	lang := strings.TrimSpace(strings.ToLower(g.PreferredLanguage))
	if lang == "" {
		lang = "en"
	}
	if len(g.AddressJSON) == 0 {
		g.AddressJSON = json.RawMessage(`{}`)
	}
	row := tx.QueryRow(ctx, `
		UPDATE guests
		SET first_name = $3,
		    last_name = $4,
		    full_name = $5,
		    email = $6,
		    normalized_email = $7,
		    phone = $8,
		    normalized_phone = $9,
		    preferred_contact_method = $10,
		    preferred_language = $11,
		    status = $12,
		    address_json = $13,
		    notes = $14,
		    updated_by = $15,
		    version = version + 1,
		    updated_at = NOW()
		WHERE property_id = $1 AND id = $2 AND version = $16 AND status NOT IN ('MERGED', 'ANONYMIZED')
		RETURNING id, property_id, first_name, last_name, full_name, email, normalized_email,
		          phone, normalized_phone, preferred_contact_method, preferred_language, status,
		          address_json, preferences, notes, merged_into_guest_id, merged_at, anonymized_at, erasure_requested_at,
		          created_by, updated_by, version, created_at, updated_at
	`,
		g.PropertyID, g.ID, first, last, full, normEmail, normEmail,
		strings.TrimSpace(g.Phone), normPhone, method, lang, string(g.Status),
		g.AddressJSON, strings.TrimSpace(g.Notes), g.UpdatedBy, g.Version,
	)
	out, err := scanGuestRow(row)
	if err == pgx.ErrNoRows {
		if existing, gerr := s.GetGuest(ctx, tx, g.PropertyID, g.ID); gerr == nil {
			if existing.Status == domain.GuestStatusMerged || existing.Status == domain.GuestStatusAnonymized {
				return domain.Guest{}, fmt.Errorf("%w: guest profile is %s and read-only", domain.ErrValidation, existing.Status)
			}
			return domain.Guest{}, domain.ErrConflict
		}
		return domain.Guest{}, domain.ErrNotFound
	}
	return out, MapErr(err)
}

func (s *Store) ListGuests(ctx context.Context, propertyID uuid.UUID, f domain.GuestDirectoryFilter, today time.Time) ([]domain.GuestListItem, error) {
	var sqlLimit *int
	if !f.NoLimit {
		limit := f.Limit
		if limit <= 0 || limit > 500 {
			limit = 100
		}
		sqlLimit = &limit
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}
	q := strings.TrimSpace(f.Query)
	qLower := strings.ToLower(q)
	qPhone := domain.NormalizePhone(q)
	segment := strings.ToUpper(strings.TrimSpace(f.Segment))
	statusFilter := strings.ToUpper(strings.TrimSpace(f.Status))
	todayStr := domain.FormatDate(today)

	rows, err := s.pool.Query(ctx, `
		WITH res_agg AS (
			SELECT guest_id,
			       COUNT(*) FILTER (WHERE status NOT IN ('CANCELLED', 'INQUIRY', 'NO_SHOW')) AS total_bookings,
			       COUNT(*) FILTER (WHERE status = 'CHECKED_OUT') AS completed_stays,
			       COUNT(*) FILTER (WHERE status IN ('CONFIRMED', 'PENDING', 'CHECKED_IN') AND upper(stay_range) > $2::date) AS upcoming_stays,
			       COUNT(*) FILTER (WHERE status = 'CANCELLED') AS cancelled_bookings,
			       MAX(upper(stay_range)) FILTER (WHERE status = 'CHECKED_OUT') AS last_stay_date,
			       MIN(lower(stay_range)) FILTER (WHERE status IN ('CONFIRMED', 'PENDING') AND lower(stay_range) >= $2::date) AS next_arrival_date,
			       COALESCE(SUM(total_pence) FILTER (WHERE status IN ('CONFIRMED', 'CHECKED_IN', 'CHECKED_OUT')), 0) AS total_revenue_pence
			FROM reservations
			WHERE property_id = $1
			GROUP BY guest_id
		),
		enq_agg AS (
			SELECT guest_id,
			       COUNT(*) FILTER (WHERE status NOT IN ('RESOLVED', 'CLOSED')) AS open_enquiries,
			       COUNT(*) FILTER (WHERE category = 'COMPLAINT' AND status NOT IN ('RESOLVED', 'CLOSED')) AS open_complaints
			FROM guest_enquiries
			WHERE property_id = $1 AND guest_id IS NOT NULL
			GROUP BY guest_id
		),
		latest_consent AS (
			SELECT DISTINCT ON (guest_id) guest_id, status
			FROM guest_consent_records_effective
			WHERE property_id = $1 AND consent_type = 'MARKETING_EMAIL'
			ORDER BY guest_id, occurred_at DESC, created_at DESC
		)
		SELECT g.id, g.property_id, g.first_name, g.last_name, g.full_name, g.email, g.normalized_email,
		       g.phone, g.normalized_phone, g.preferred_contact_method, g.preferred_language, g.status,
		       g.address_json, g.preferences, g.notes, g.merged_into_guest_id, g.merged_at, g.anonymized_at,
		       g.created_by, g.updated_by, g.version, g.created_at, g.updated_at,
		       COALESCE(ra.total_bookings, 0),
		       COALESCE(ra.completed_stays, 0),
		       COALESCE(ra.upcoming_stays, 0),
		       COALESCE(ra.cancelled_bookings, 0),
		       ra.last_stay_date,
		       ra.next_arrival_date,
		       COALESCE(ea.open_enquiries, 0),
		       COALESCE(ea.open_complaints, 0),
		       COALESCE(ra.total_revenue_pence, 0),
		       COALESCE(lc.status, 'NOT_SUBSCRIBED')
		FROM guests g
		LEFT JOIN res_agg ra ON ra.guest_id = g.id
		LEFT JOIN enq_agg ea ON ea.guest_id = g.id
		LEFT JOIN latest_consent lc ON lc.guest_id = g.id
		WHERE g.property_id = $1
		  AND ($3::boolean = TRUE OR g.status NOT IN ('MERGED', 'ANONYMIZED'))
		  AND ($4::text = '' OR g.status = $4)
		  AND (
		      $5::text = ''
		      OR lower(g.full_name) LIKE '%' || $5 || '%'
		      OR g.normalized_email LIKE '%' || $5 || '%'
		      OR ($6::text <> '' AND g.normalized_phone LIKE '%' || $6 || '%')
		      OR g.id::text = $7
		      OR EXISTS (
		          SELECT 1 FROM reservations r
		          WHERE r.property_id = $1 AND r.guest_id = g.id AND lower(r.reference) LIKE '%' || $5 || '%'
		      )
		  )
		  AND (
		      $8::text = '' OR $8 = 'ALL'
		      OR ($8 = 'RETURNING' AND COALESCE(ra.completed_stays, 0) >= 1)
		      OR ($8 = 'NEW' AND COALESCE(ra.completed_stays, 0) = 0)
		      OR ($8 = 'UPCOMING' AND COALESCE(ra.upcoming_stays, 0) >= 1)
		      OR ($8 = 'OUTSTANDING_REQUESTS' AND COALESCE(ea.open_enquiries, 0) >= 1)
		      OR ($8 = 'UNRESOLVED_COMPLAINTS' AND COALESCE(ea.open_complaints, 0) >= 1)
		      OR ($8 = 'OPTED_IN' AND COALESCE(lc.status, '') = 'GRANTED')
		  )
		ORDER BY g.updated_at DESC, g.full_name ASC
		LIMIT $9 OFFSET $10
	`, propertyID, todayStr, f.IncludeAll, statusFilter, qLower, qPhone, q, segment, sqlLimit, offset)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()

	var out []domain.GuestListItem
	for rows.Next() {
		var item domain.GuestListItem
		var status string
		if err := rows.Scan(
			&item.ID, &item.PropertyID, &item.FirstName, &item.LastName, &item.FullName,
			&item.Email, &item.NormalizedEmail, &item.Phone, &item.NormalizedPhone,
			&item.PreferredContactMethod, &item.PreferredLanguage, &status,
			&item.AddressJSON, &item.Preferences, &item.Notes,
			&item.MergedIntoGuestID, &item.MergedAt, &item.AnonymizedAt,
			&item.CreatedBy, &item.UpdatedBy, &item.Version, &item.CreatedAt, &item.UpdatedAt,
			&item.TotalBookings, &item.CompletedStays, &item.UpcomingStays, &item.CancelledBookings,
			&item.LastStayDate, &item.NextArrivalDate,
			&item.OpenEnquiriesCount, &item.OpenComplaintsCount, &item.TotalRevenuePence, &item.MarketingConsentState,
		); err != nil {
			return nil, MapErr(err)
		}
		item.Status = domain.GuestStatus(status)
		item.IsReturningGuest = item.CompletedStays >= 1
		out = append(out, item)
	}
	return out, MapErr(rows.Err())
}

func (s *Store) GetGuestSummaryItem(ctx context.Context, db DBTX, propertyID, guestID uuid.UUID, today time.Time) (domain.GuestListItem, error) {
	if db == nil {
		db = s.pool
	}
	todayStr := domain.FormatDate(today)
	var item domain.GuestListItem
	var status string
	err := db.QueryRow(ctx, `
		WITH res_agg AS (
			SELECT COUNT(*) FILTER (WHERE status NOT IN ('CANCELLED', 'INQUIRY', 'NO_SHOW')) AS total_bookings,
			       COUNT(*) FILTER (WHERE status = 'CHECKED_OUT') AS completed_stays,
			       COUNT(*) FILTER (WHERE status IN ('CONFIRMED', 'PENDING', 'CHECKED_IN') AND upper(stay_range) > $3::date) AS upcoming_stays,
			       COUNT(*) FILTER (WHERE status = 'CANCELLED') AS cancelled_bookings,
			       MAX(upper(stay_range)) FILTER (WHERE status = 'CHECKED_OUT') AS last_stay_date,
			       MIN(lower(stay_range)) FILTER (WHERE status IN ('CONFIRMED', 'PENDING') AND lower(stay_range) >= $3::date) AS next_arrival_date,
			       COALESCE(SUM(total_pence) FILTER (WHERE status IN ('CONFIRMED', 'CHECKED_IN', 'CHECKED_OUT')), 0) AS total_revenue_pence
			FROM reservations
			WHERE property_id = $1 AND guest_id = $2
		),
		enq_agg AS (
			SELECT COUNT(*) FILTER (WHERE status NOT IN ('RESOLVED', 'CLOSED')) AS open_enquiries,
			       COUNT(*) FILTER (WHERE category = 'COMPLAINT' AND status NOT IN ('RESOLVED', 'CLOSED')) AS open_complaints
			FROM guest_enquiries
			WHERE property_id = $1 AND guest_id = $2
		),
		latest_consent AS (
			SELECT status
			FROM guest_consent_records_effective
			WHERE property_id = $1 AND guest_id = $2 AND consent_type = 'MARKETING_EMAIL'
			ORDER BY occurred_at DESC, created_at DESC
			LIMIT 1
		)
		SELECT g.id, g.property_id, g.first_name, g.last_name, g.full_name, g.email, g.normalized_email,
		       g.phone, g.normalized_phone, g.preferred_contact_method, g.preferred_language, g.status,
		       g.address_json, g.preferences, g.notes, g.merged_into_guest_id, g.merged_at, g.anonymized_at,
		       g.created_by, g.updated_by, g.version, g.created_at, g.updated_at,
		       COALESCE((SELECT total_bookings FROM res_agg), 0),
		       COALESCE((SELECT completed_stays FROM res_agg), 0),
		       COALESCE((SELECT upcoming_stays FROM res_agg), 0),
		       COALESCE((SELECT cancelled_bookings FROM res_agg), 0),
		       (SELECT last_stay_date FROM res_agg),
		       (SELECT next_arrival_date FROM res_agg),
		       COALESCE((SELECT open_enquiries FROM enq_agg), 0),
		       COALESCE((SELECT open_complaints FROM enq_agg), 0),
		       COALESCE((SELECT total_revenue_pence FROM res_agg), 0),
		       COALESCE((SELECT status FROM latest_consent), 'NOT_SUBSCRIBED')
		FROM guests g
		WHERE g.property_id = $1 AND g.id = $2
	`, propertyID, guestID, todayStr).Scan(
		&item.ID, &item.PropertyID, &item.FirstName, &item.LastName, &item.FullName,
		&item.Email, &item.NormalizedEmail, &item.Phone, &item.NormalizedPhone,
		&item.PreferredContactMethod, &item.PreferredLanguage, &status,
		&item.AddressJSON, &item.Preferences, &item.Notes,
		&item.MergedIntoGuestID, &item.MergedAt, &item.AnonymizedAt,
		&item.CreatedBy, &item.UpdatedBy, &item.Version, &item.CreatedAt, &item.UpdatedAt,
		&item.TotalBookings, &item.CompletedStays, &item.UpcomingStays, &item.CancelledBookings,
		&item.LastStayDate, &item.NextArrivalDate,
		&item.OpenEnquiriesCount, &item.OpenComplaintsCount, &item.TotalRevenuePence, &item.MarketingConsentState,
	)
	if err != nil {
		return domain.GuestListItem{}, MapErr(err)
	}
	item.Status = domain.GuestStatus(status)
	item.IsReturningGuest = item.CompletedStays >= 1
	return item, nil
}

func (s *Store) ListGuestReservations(ctx context.Context, db DBTX, propertyID, guestID uuid.UUID) ([]domain.Reservation, error) {
	return s.ListGuestReservationsByCluster(ctx, db, propertyID, []uuid.UUID{guestID})
}

func (s *Store) ListGuestReservationsByCluster(ctx context.Context, db DBTX, propertyID uuid.UUID, clusterIDs []uuid.UUID) ([]domain.Reservation, error) {
	if db == nil {
		db = s.pool
	}
	if len(clusterIDs) == 0 {
		return nil, nil
	}
	rows, err := db.Query(ctx, `
		SELECT id, property_id, reference, guest_id, guest_name, guest_email, guest_phone,
		       channel_code, lower(stay_range), upper(stay_range),
		       adults, children, infants, status,
		       payment_status, total_pence, deposit_pence, amount_paid_pence, balance_due_pence, currency,
		       occupant_details, estimated_arrival_time, guest_arrival_notes, internal_notes, cancellation_reason, cancelled_at,
		       cancellation_requested_at, cancellation_request_note,
		       created_by, updated_by, version, created_at, updated_at
		FROM reservations
		WHERE property_id = $1 AND guest_id = ANY($2)
		ORDER BY lower(stay_range) DESC, created_at DESC
	`, propertyID, clusterIDs)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()

	var out []domain.Reservation
	var ids []uuid.UUID
	for rows.Next() {
		r, err := scanReservation(rows)
		if err != nil {
			return nil, MapErr(err)
		}
		out = append(out, r)
		ids = append(ids, r.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, MapErr(err)
	}
	if len(ids) > 0 {
		unitMap, err := s.loadReservationUnitsMap(ctx, db, propertyID, ids)
		if err != nil {
			return nil, err
		}
		for i := range out {
			out[i].Units = unitMap[out[i].ID]
			for _, u := range out[i].Units {
				out[i].UnitIDs = append(out[i].UnitIDs, u.UnitID)
			}
		}
	}
	return out, nil
}

func (s *Store) DetectGuestDuplicates(ctx context.Context, propertyID uuid.UUID, today time.Time) (domain.DuplicateScanResult, error) {
	const batchSize = 500
	const maxDuplicateScanGuests = 2000

	var totalActive int
	if err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM guests
		WHERE property_id = $1 AND status NOT IN ('MERGED', 'ANONYMIZED')
	`, propertyID).Scan(&totalActive); err != nil {
		return domain.DuplicateScanResult{}, MapErr(err)
	}

	var guests []domain.GuestListItem
	for off := 0; off < maxDuplicateScanGuests; off += batchSize {
		page, err := s.ListGuests(ctx, propertyID, domain.GuestDirectoryFilter{
			Limit:  batchSize,
			Offset: off,
		}, today)
		if err != nil {
			return domain.DuplicateScanResult{}, err
		}
		if len(page) == 0 {
			break
		}
		guests = append(guests, page...)
		if len(page) < batchSize {
			break
		}
	}
	if len(guests) > maxDuplicateScanGuests {
		guests = guests[:maxDuplicateScanGuests]
	}

	dismissed := map[string]bool{}
	dRows, err := s.pool.Query(ctx, `
		SELECT guest_a_id, guest_b_id FROM guest_duplicate_dismissals WHERE property_id = $1
	`, propertyID)
	if err != nil {
		return domain.DuplicateScanResult{}, MapErr(err)
	}
	defer dRows.Close()
	for dRows.Next() {
		var a, b uuid.UUID
		if err := dRows.Scan(&a, &b); err != nil {
			return domain.DuplicateScanResult{}, MapErr(err)
		}
		dismissed[a.String()+"|"+b.String()] = true
	}
	if err := dRows.Err(); err != nil {
		return domain.DuplicateScanResult{}, MapErr(err)
	}

	var out []domain.GuestDuplicateCandidate
	for i := 0; i < len(guests); i++ {
		for j := i + 1; j < len(guests); j++ {
			a, b := guests[i], guests[j]
			low, high := a.ID.String(), b.ID.String()
			if high < low {
				low, high = high, low
			}
			if dismissed[low+"|"+high] {
				continue
			}
			score, reasons := domain.ScoreDuplicatePair(a, b)
			if score > 0 {
				out = append(out, domain.GuestDuplicateCandidate{
					GuestA:       a,
					GuestB:       b,
					Score:        score,
					MatchReasons: reasons,
				})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].GuestA.FullName < out[j].GuestA.FullName
	})
	return domain.DuplicateScanResult{
		Candidates:        out,
		ScannedGuests:     len(guests),
		TotalActiveGuests: totalActive,
		IsComplete:        len(guests) >= totalActive,
	}, nil
}

func (s *Store) DismissGuestDuplicate(ctx context.Context, tx pgx.Tx, propertyID, idA, idB uuid.UUID, reason string, actorID uuid.UUID) error {
	return s.DismissGuestDuplicateWithCategory(ctx, tx, propertyID, idA, idB, "NOT_DUPLICATE", reason, actorID)
}

func (s *Store) DismissGuestDuplicateWithCategory(ctx context.Context, tx pgx.Tx, propertyID, idA, idB uuid.UUID, category, reason string, actorID uuid.UUID) error {
	if idA == idB {
		return fmt.Errorf("%w: cannot dismiss self pair", domain.ErrValidation)
	}
	if idB.String() < idA.String() {
		idA, idB = idB, idA
	}
	cat := strings.ToUpper(strings.TrimSpace(category))
	switch cat {
	case "NOT_DUPLICATE", "DISTINCT_PEOPLE", "SHARED_CONTACT", "HOUSEHOLD", "CORPORATE", "OTHER":
	default:
		cat = "NOT_DUPLICATE"
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO guest_duplicate_dismissals (property_id, guest_a_id, guest_b_id, reason_category, reason, dismissed_by)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (property_id, guest_a_id, guest_b_id) DO UPDATE
		SET reason_category = EXCLUDED.reason_category,
		    reason = EXCLUDED.reason,
		    dismissed_by = EXCLUDED.dismissed_by,
		    dismissed_at = NOW()
	`, propertyID, idA, idB, cat, strings.TrimSpace(reason), actorID)
	return MapErr(err)
}

func (s *Store) ListGuestDuplicateDismissals(ctx context.Context, db DBTX, propertyID, guestID uuid.UUID) ([]domain.GuestDuplicateDismissal, error) {
	if db == nil {
		db = s.pool
	}
	rows, err := db.Query(ctx, `
		WITH RECURSIVE cluster AS (
			SELECT id FROM guests WHERE property_id = $1 AND id = $2
			UNION
			SELECT child.id
			FROM guests child
			JOIN cluster p ON child.merged_into_guest_id = p.id
			WHERE child.property_id = $1
		)
		SELECT DISTINCT d.property_id, d.guest_a_id, d.guest_b_id, d.reason, d.reason_category, d.dismissed_by, d.dismissed_at
		FROM guest_duplicate_dismissals d
		WHERE d.property_id = $1
		  AND (d.guest_a_id IN (SELECT id FROM cluster) OR d.guest_b_id IN (SELECT id FROM cluster))
		ORDER BY d.dismissed_at DESC
	`, propertyID, guestID)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()
	var out []domain.GuestDuplicateDismissal
	for rows.Next() {
		var d domain.GuestDuplicateDismissal
		if err := rows.Scan(&d.PropertyID, &d.GuestAID, &d.GuestBID, &d.Reason, &d.ReasonCategory, &d.DismissedBy, &d.DismissedAt); err != nil {
			return nil, MapErr(err)
		}
		out = append(out, d)
	}
	return out, MapErr(rows.Err())
}

func (s *Store) ListGuestBookingHolds(ctx context.Context, db DBTX, propertyID, guestID uuid.UUID) ([]domain.BookingHold, error) {
	if db == nil {
		db = s.pool
	}
	rows, err := db.Query(ctx, `
		WITH RECURSIVE cluster AS (
			SELECT id FROM guests WHERE property_id = $1 AND id = $2
			UNION
			SELECT child.id
			FROM guests child
			JOIN cluster p ON child.merged_into_guest_id = p.id
			WHERE child.property_id = $1
		)
		SELECT h.id, h.property_id, h.guest_id, h.hold_reference, h.unit_type_id, ut.name,
		       h.rate_plan_id, h.channel_code, lower(h.stay_range), upper(h.stay_range),
		       h.adults, h.children, h.infants, h.guest_name, h.guest_email, h.guest_phone,
		       h.special_requests, h.terms_version, h.terms_accepted_at,
		       h.status, h.expires_at, h.confirmed_reservation_id, h.version, h.created_at, h.updated_at
		FROM booking_holds h
		JOIN unit_types ut ON ut.id = h.unit_type_id
		WHERE h.property_id = $1
		  AND (
			h.guest_id IN (SELECT id FROM cluster)
			OR h.confirmed_reservation_id IN (
				SELECT id FROM reservations WHERE property_id = $1 AND guest_id IN (SELECT id FROM cluster)
			)
		  )
		ORDER BY h.created_at DESC
	`, propertyID, guestID)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()
	var out []domain.BookingHold
	for rows.Next() {
		var h domain.BookingHold
		if err := rows.Scan(
			&h.ID, &h.PropertyID, &h.GuestID, &h.HoldReference, &h.UnitTypeID, &h.UnitTypeName,
			&h.RatePlanID, &h.ChannelCode, &h.ArrivalDate, &h.DepartureDate,
			&h.Adults, &h.Children, &h.Infants, &h.GuestName, &h.GuestEmail, &h.GuestPhone,
			&h.SpecialRequests, &h.TermsVersion, &h.TermsAcceptedAt,
			&h.Status, &h.ExpiresAt, &h.ConfirmedReservationID, &h.Version, &h.CreatedAt, &h.UpdatedAt,
		); err != nil {
			return nil, MapErr(err)
		}
		h.Arrival = domain.FormatDate(h.ArrivalDate)
		h.Departure = domain.FormatDate(h.DepartureDate)
		out = append(out, h)
	}
	return out, MapErr(rows.Err())
}

func (s *Store) MergeGuests(ctx context.Context, tx pgx.Tx, propertyID, sourceID, targetID uuid.UUID, notes string, actorID uuid.UUID) (domain.Guest, domain.GuestMergeEvent, error) {
	if sourceID == targetID {
		return domain.Guest{}, domain.GuestMergeEvent{}, fmt.Errorf("%w: source and target guest must differ", domain.ErrValidation)
	}
	firstLock, secondLock := sourceID, targetID
	if secondLock.String() < firstLock.String() {
		firstLock, secondLock = secondLock, firstLock
	}
	for _, gid := range []uuid.UUID{firstLock, secondLock} {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT TRUE FROM guests WHERE property_id = $1 AND id = $2 FOR UPDATE`, propertyID, gid).Scan(&exists); err != nil {
			if err == pgx.ErrNoRows {
				return domain.Guest{}, domain.GuestMergeEvent{}, domain.ErrNotFound
			}
			return domain.Guest{}, domain.GuestMergeEvent{}, MapErr(err)
		}
	}

	source, err := s.GetGuest(ctx, tx, propertyID, sourceID)
	if err != nil {
		return domain.Guest{}, domain.GuestMergeEvent{}, err
	}
	target, err := s.GetGuest(ctx, tx, propertyID, targetID)
	if err != nil {
		return domain.Guest{}, domain.GuestMergeEvent{}, err
	}
	if source.Status == domain.GuestStatusMerged || source.Status == domain.GuestStatusAnonymized {
		return domain.Guest{}, domain.GuestMergeEvent{}, fmt.Errorf("%w: source guest is %s and cannot be merged", domain.ErrConflict, source.Status)
	}
	if target.Status == domain.GuestStatusMerged || target.Status == domain.GuestStatusAnonymized {
		return domain.Guest{}, domain.GuestMergeEvent{}, fmt.Errorf("%w: target guest is %s and cannot accept merges", domain.ErrConflict, target.Status)
	}

	sourceSnap, _ := json.Marshal(source)

	counts := map[string]int64{}
	reparent := func(name, q string) error {
		tag, execErr := tx.Exec(ctx, q, propertyID, targetID, sourceID)
		if execErr != nil {
			return MapErr(execErr)
		}
		counts[name] = tag.RowsAffected()
		return nil
	}
	if err := reparent("reservations", `UPDATE reservations SET guest_id = $2 WHERE property_id = $1 AND guest_id = $3`); err != nil {
		return domain.Guest{}, domain.GuestMergeEvent{}, err
	}
	if err := reparent("booking_holds", `UPDATE booking_holds SET guest_id = $2 WHERE property_id = $1 AND guest_id = $3`); err != nil {
		return domain.Guest{}, domain.GuestMergeEvent{}, err
	}
	if err := reparent("preferences", `UPDATE guest_preferences SET guest_id = $2 WHERE property_id = $1 AND guest_id = $3`); err != nil {
		return domain.Guest{}, domain.GuestMergeEvent{}, err
	}
	if err := reparent("notes", `UPDATE guest_notes SET guest_id = $2 WHERE property_id = $1 AND guest_id = $3`); err != nil {
		return domain.Guest{}, domain.GuestMergeEvent{}, err
	}
	if err := reparent("enquiries", `UPDATE guest_enquiries SET guest_id = $2 WHERE property_id = $1 AND guest_id = $3`); err != nil {
		return domain.Guest{}, domain.GuestMergeEvent{}, err
	}
	if err := reparent("communications", `UPDATE guest_communications SET guest_id = $2 WHERE property_id = $1 AND guest_id = $3`); err != nil {
		return domain.Guest{}, domain.GuestMergeEvent{}, err
	}
	if err := reparent("email_outbox", `UPDATE email_outbox SET guest_id = $2 WHERE property_id = $1 AND guest_id = $3`); err != nil {
		return domain.Guest{}, domain.GuestMergeEvent{}, err
	}
	if err := reparent("communication_jobs", `UPDATE communication_jobs SET guest_id = $2 WHERE property_id = $1 AND guest_id = $3`); err != nil {
		return domain.Guest{}, domain.GuestMergeEvent{}, err
	}
	var consentCount int64
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM guest_consent_records WHERE property_id = $1 AND guest_id = $2`, propertyID, sourceID).Scan(&consentCount); err != nil {
		return domain.Guest{}, domain.GuestMergeEvent{}, MapErr(err)
	}
	counts["consent_records"] = consentCount
	if _, err := tx.Exec(ctx, `UPDATE guests SET merged_into_guest_id = $2 WHERE property_id = $1 AND merged_into_guest_id = $3`, propertyID, targetID, sourceID); err != nil {
		return domain.Guest{}, domain.GuestMergeEvent{}, MapErr(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE guest_portal_challenges SET guest_id = $2 WHERE property_id = $1 AND guest_id = $3`, propertyID, targetID, sourceID); err != nil {
		return domain.Guest{}, domain.GuestMergeEvent{}, MapErr(err)
	}

	// Enrich missing contact metadata onto canonical target without overwriting target values.
	mergedEmail := target.Email
	if mergedEmail == "" {
		mergedEmail = source.Email
	}
	mergedNormEmail := domain.NormalizeEmail(mergedEmail)
	if err := s.reconcileMergedPortalIdentitiesTx(ctx, tx, propertyID, sourceID, targetID, mergedNormEmail); err != nil {
		return domain.Guest{}, domain.GuestMergeEvent{}, err
	}
	mergedPhone := target.Phone
	if mergedPhone == "" {
		mergedPhone = source.Phone
	}
	mergedFirst, mergedLast, mergedFull := target.FirstName, target.LastName, target.FullName
	if mergedFirst == "" {
		mergedFirst = source.FirstName
	}
	if mergedLast == "" {
		mergedLast = source.LastName
	}
	mergedNotes := strings.TrimSpace(target.Notes)
	if srcNotes := strings.TrimSpace(source.Notes); srcNotes != "" {
		if mergedNotes != "" {
			mergedNotes += "\n\n"
		}
		mergedNotes += fmt.Sprintf("[Merged from %s] %s", source.FullName, srcNotes)
	}
	nextStatus := target.Status
	if source.Status == domain.GuestStatusVIP && target.Status == domain.GuestStatusActive {
		nextStatus = domain.GuestStatusVIP
	}

	if _, err := tx.Exec(ctx, `
		UPDATE guests
		SET first_name = $3, last_name = $4, full_name = $5,
		    email = $6, normalized_email = $7,
		    phone = $8, normalized_phone = $9,
		    notes = $10, status = $11, updated_by = $12,
		    version = version + 1, updated_at = NOW()
		WHERE property_id = $1 AND id = $2
	`, propertyID, targetID, mergedFirst, mergedLast, mergedFull,
		domain.NormalizeEmail(mergedEmail), domain.NormalizeEmail(mergedEmail),
		mergedPhone, domain.NormalizePhone(mergedPhone),
		mergedNotes, string(nextStatus), actorID,
	); err != nil {
		return domain.Guest{}, domain.GuestMergeEvent{}, MapErr(err)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE guests
		SET status = 'MERGED', merged_into_guest_id = $3, merged_at = NOW(),
		    normalized_email = '', normalized_phone = '', updated_by = $4,
		    version = version + 1, updated_at = NOW()
		WHERE property_id = $1 AND id = $2
	`, propertyID, sourceID, targetID, actorID); err != nil {
		return domain.Guest{}, domain.GuestMergeEvent{}, MapErr(err)
	}

	evID, err := NewUUID()
	if err != nil {
		return domain.Guest{}, domain.GuestMergeEvent{}, err
	}
	movedJSON, _ := json.Marshal(counts)
	var ev domain.GuestMergeEvent
	err = tx.QueryRow(ctx, `
		INSERT INTO guest_merge_events (id, property_id, source_guest_id, target_guest_id, source_snapshot, moved_counts, notes, merged_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, property_id, source_guest_id, target_guest_id, source_snapshot, moved_counts, notes, merged_by, merged_at
	`, evID, propertyID, sourceID, targetID, sourceSnap, movedJSON, strings.TrimSpace(notes), actorID).Scan(
		&ev.ID, &ev.PropertyID, &ev.SourceGuestID, &ev.TargetGuestID,
		&ev.SourceSnap, &ev.MovedCounts, &ev.Notes, &ev.MergedBy, &ev.MergedAt,
	)
	if err != nil {
		return domain.Guest{}, domain.GuestMergeEvent{}, MapErr(err)
	}

	// Reassign guest_consent_records ONLY AFTER source status='MERGED' and guest_merge_events exist
	// so the append-only trigger verifies explicit merge provenance without session-variable bypasses.
	if err := reparent("consent_records", `UPDATE guest_consent_records SET guest_id = $2 WHERE property_id = $1 AND guest_id = $3`); err != nil {
		return domain.Guest{}, domain.GuestMergeEvent{}, err
	}

	updatedTarget, err := s.GetGuest(ctx, tx, propertyID, targetID)
	return updatedTarget, ev, err
}

func (s *Store) ListGuestMergeHistory(ctx context.Context, db DBTX, propertyID, guestID uuid.UUID) ([]domain.GuestMergeEvent, error) {
	if db == nil {
		db = s.pool
	}
	rows, err := db.Query(ctx, `
		WITH RECURSIVE root AS (
			SELECT COALESCE(public.resolve_canonical_guest_id($1, $2), $2) AS canonical_id
		),
		lineage AS (
			SELECT m.id, m.property_id, m.source_guest_id, m.target_guest_id, m.source_snapshot, m.moved_counts, m.notes, m.merged_by, m.merged_at,
			       ARRAY[m.id] AS visited
			FROM guest_merge_events m, root
			WHERE m.property_id = $1
			  AND (m.target_guest_id = $2 OR m.source_guest_id = $2 OR m.target_guest_id = root.canonical_id OR m.source_guest_id = root.canonical_id)
			UNION ALL
			SELECT parent.id, parent.property_id, parent.source_guest_id, parent.target_guest_id, parent.source_snapshot, parent.moved_counts, parent.notes, parent.merged_by, parent.merged_at,
			       l.visited || parent.id
			FROM guest_merge_events parent
			JOIN lineage l ON parent.property_id = l.property_id
				AND (parent.target_guest_id = l.source_guest_id OR parent.source_guest_id = l.target_guest_id)
			WHERE NOT (parent.id = ANY(l.visited))
		),
		distinct_events AS (
			SELECT DISTINCT ON (id) id, property_id, source_guest_id, target_guest_id, source_snapshot, moved_counts, notes, merged_by, merged_at
			FROM lineage
		)
		SELECT id, property_id, source_guest_id, target_guest_id, source_snapshot, moved_counts, notes, merged_by, merged_at
		FROM distinct_events
		ORDER BY merged_at DESC, id DESC
	`, propertyID, guestID)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()
	var out []domain.GuestMergeEvent
	for rows.Next() {
		var ev domain.GuestMergeEvent
		if err := rows.Scan(&ev.ID, &ev.PropertyID, &ev.SourceGuestID, &ev.TargetGuestID, &ev.SourceSnap, &ev.MovedCounts, &ev.Notes, &ev.MergedBy, &ev.MergedAt); err != nil {
			return nil, MapErr(err)
		}
		out = append(out, ev)
	}
	return out, MapErr(rows.Err())
}

// --- Preferences & Notes ---

func (s *Store) ListGuestPreferences(ctx context.Context, db DBTX, propertyID, guestID uuid.UUID) ([]domain.GuestPreference, error) {
	if db == nil {
		db = s.pool
	}
	rows, err := db.Query(ctx, `
		SELECT id, property_id, guest_id, category, preference_key, preference_value, notes, created_by, updated_by, created_at, updated_at
		FROM guest_preferences
		WHERE property_id = $1 AND guest_id = $2
		ORDER BY category ASC, preference_key ASC
	`, propertyID, guestID)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()
	var out []domain.GuestPreference
	for rows.Next() {
		var p domain.GuestPreference
		var cat string
		if err := rows.Scan(&p.ID, &p.PropertyID, &p.GuestID, &cat, &p.PreferenceKey, &p.PreferenceValue, &p.Notes, &p.CreatedBy, &p.UpdatedBy, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, MapErr(err)
		}
		p.Category = domain.GuestPreferenceCategory(cat)
		out = append(out, p)
	}
	return out, MapErr(rows.Err())
}

func (s *Store) CreateGuestPreference(ctx context.Context, tx pgx.Tx, p domain.GuestPreference) (domain.GuestPreference, error) {
	if p.ID == uuid.Nil {
		id, err := NewUUID()
		if err != nil {
			return domain.GuestPreference{}, err
		}
		p.ID = id
	}
	var out domain.GuestPreference
	var cat string
	err := tx.QueryRow(ctx, `
		INSERT INTO guest_preferences (id, property_id, guest_id, category, preference_key, preference_value, notes, created_by, updated_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, property_id, guest_id, category, preference_key, preference_value, notes, created_by, updated_by, created_at, updated_at
	`, p.ID, p.PropertyID, p.GuestID, string(p.Category), strings.TrimSpace(p.PreferenceKey), strings.TrimSpace(p.PreferenceValue), strings.TrimSpace(p.Notes), p.CreatedBy, p.UpdatedBy).Scan(
		&out.ID, &out.PropertyID, &out.GuestID, &cat, &out.PreferenceKey, &out.PreferenceValue, &out.Notes, &out.CreatedBy, &out.UpdatedBy, &out.CreatedAt, &out.UpdatedAt,
	)
	if err != nil {
		return domain.GuestPreference{}, MapErr(err)
	}
	out.Category = domain.GuestPreferenceCategory(cat)
	return out, nil
}

func (s *Store) DeleteGuestPreference(ctx context.Context, tx pgx.Tx, propertyID, guestID, prefID uuid.UUID) error {
	tag, err := tx.Exec(ctx, `
		DELETE FROM guest_preferences WHERE property_id = $1 AND guest_id = $2 AND id = $3
	`, propertyID, guestID, prefID)
	if err != nil {
		return MapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) ListGuestNotes(ctx context.Context, db DBTX, propertyID, guestID uuid.UUID) ([]domain.GuestNote, error) {
	if db == nil {
		db = s.pool
	}
	rows, err := db.Query(ctx, `
		SELECT n.id, n.property_id, n.guest_id, n.reservation_id, n.category, n.content, n.is_pinned,
		       COALESCE(au.full_name, ''), n.created_by, n.updated_by, n.created_at, n.updated_at
		FROM guest_notes n
		LEFT JOIN admin_users au ON au.id = n.created_by
		WHERE n.property_id = $1 AND n.guest_id = $2
		ORDER BY n.is_pinned DESC, n.created_at DESC
	`, propertyID, guestID)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()
	var out []domain.GuestNote
	for rows.Next() {
		var n domain.GuestNote
		var cat string
		if err := rows.Scan(&n.ID, &n.PropertyID, &n.GuestID, &n.ReservationID, &cat, &n.Content, &n.IsPinned, &n.AuthorName, &n.CreatedBy, &n.UpdatedBy, &n.CreatedAt, &n.UpdatedAt); err != nil {
			return nil, MapErr(err)
		}
		n.Category = domain.GuestNoteCategory(cat)
		out = append(out, n)
	}
	return out, MapErr(rows.Err())
}

func (s *Store) CreateGuestNote(ctx context.Context, tx pgx.Tx, n domain.GuestNote) (domain.GuestNote, error) {
	if n.ID == uuid.Nil {
		id, err := NewUUID()
		if err != nil {
			return domain.GuestNote{}, err
		}
		n.ID = id
	}
	var out domain.GuestNote
	var cat string
	err := tx.QueryRow(ctx, `
		INSERT INTO guest_notes (id, property_id, guest_id, reservation_id, category, content, is_pinned, created_by, updated_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, property_id, guest_id, reservation_id, category, content, is_pinned, created_by, updated_by, created_at, updated_at
	`, n.ID, n.PropertyID, n.GuestID, n.ReservationID, string(n.Category), strings.TrimSpace(n.Content), n.IsPinned, n.CreatedBy, n.UpdatedBy).Scan(
		&out.ID, &out.PropertyID, &out.GuestID, &out.ReservationID, &cat, &out.Content, &out.IsPinned, &out.CreatedBy, &out.UpdatedBy, &out.CreatedAt, &out.UpdatedAt,
	)
	if err != nil {
		return domain.GuestNote{}, MapErr(err)
	}
	out.Category = domain.GuestNoteCategory(cat)
	return out, nil
}

func (s *Store) DeleteGuestNote(ctx context.Context, tx pgx.Tx, propertyID, guestID, noteID uuid.UUID) error {
	tag, err := tx.Exec(ctx, `
		DELETE FROM guest_notes WHERE property_id = $1 AND guest_id = $2 AND id = $3
	`, propertyID, guestID, noteID)
	if err != nil {
		return MapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// --- Guest Enquiries ---

func (s *Store) nextEnquiryReferenceTx(ctx context.Context, tx pgx.Tx, year int) (string, error) {
	var seq int64
	if err := tx.QueryRow(ctx, `SELECT nextval('enquiry_ref_seq')`).Scan(&seq); err != nil {
		return "", MapErr(err)
	}
	return fmt.Sprintf("ENQ-%d-%04d", year, seq), nil
}

func scanEnquiryRow(r scannable) (domain.GuestEnquiry, error) {
	var e domain.GuestEnquiry
	var cat, st, pri, src string
	err := r.Scan(
		&e.ID, &e.PropertyID, &e.Reference, &e.GuestID, &e.GuestName,
		&e.ReservationID, &e.ReservationReference, &e.MaintenanceRequestID, &e.MaintenanceReference,
		&cat, &st, &pri, &src, &e.Subject, &e.Description,
		&e.RequesterName, &e.RequesterEmail, &e.RequesterPhone,
		&e.AssignedUserID, &e.AssignedUserName, &e.TargetResponseAt, &e.FollowUpAt,
		&e.ResolvedAt, &e.ResolvedBy, &e.ResolutionNotes, &e.ClosedAt, &e.ClosedBy,
		&e.Version, &e.CreatedBy, &e.UpdatedBy, &e.CreatedAt, &e.UpdatedAt,
	)
	if err != nil {
		return domain.GuestEnquiry{}, err
	}
	e.Category = domain.EnquiryCategory(cat)
	e.Status = domain.EnquiryStatus(st)
	e.Priority = domain.EnquiryPriority(pri)
	e.Source = domain.EnquirySource(src)
	return e, nil
}

const enquirySelectCols = `
	SELECT e.id, e.property_id, e.reference, e.guest_id, COALESCE(g.full_name, ''),
	       e.reservation_id, COALESCE(r.reference, ''), e.maintenance_request_id, COALESCE(m.reference, ''),
	       e.category, e.status, e.priority, e.source, e.subject, e.description,
	       e.requester_name, e.requester_email, e.requester_phone,
	       e.assigned_user_id, COALESCE(au.full_name, ''), e.target_response_at, e.follow_up_at,
	       e.resolved_at, e.resolved_by, e.resolution_notes, e.closed_at, e.closed_by,
	       e.version, e.created_by, e.updated_by, e.created_at, e.updated_at
	FROM guest_enquiries e
	LEFT JOIN guests g ON g.id = e.guest_id
	LEFT JOIN reservations r ON r.id = e.reservation_id
	LEFT JOIN maintenance_requests m ON m.id = e.maintenance_request_id
	LEFT JOIN admin_users au ON au.id = e.assigned_user_id
`

func (s *Store) ListGuestEnquiries(ctx context.Context, db DBTX, propertyID uuid.UUID, f domain.GuestEnquiryFilter) ([]domain.GuestEnquiry, error) {
	if db == nil {
		db = s.pool
	}
	var sqlLimit *int
	if !f.NoLimit {
		limit := f.Limit
		if limit <= 0 || limit > 250 {
			limit = 100
		}
		sqlLimit = &limit
	}
	q := strings.ToLower(strings.TrimSpace(f.Query))
	st := strings.ToUpper(strings.TrimSpace(f.Status))
	cat := strings.ToUpper(strings.TrimSpace(f.Category))
	pri := strings.ToUpper(strings.TrimSpace(f.Priority))

	rows, err := db.Query(ctx, enquirySelectCols+`
		WHERE e.property_id = $1
		  AND ($2::text = '' OR $2 = 'ALL' OR e.status = $2)
		  AND ($3::text = '' OR $3 = 'ALL' OR e.category = $3)
		  AND ($4::text = '' OR $4 = 'ALL' OR e.priority = $4)
		  AND ($5::uuid IS NULL OR e.assigned_user_id = $5)
		  AND ($6::uuid IS NULL OR e.guest_id = $6)
		  AND ($7::boolean = FALSE OR e.status NOT IN ('RESOLVED', 'CLOSED'))
		  AND ($8::boolean = FALSE OR (e.status NOT IN ('RESOLVED', 'CLOSED') AND e.follow_up_at IS NOT NULL AND e.follow_up_at <= NOW()))
		  AND ($9::text = '' OR lower(e.reference) LIKE '%' || $9 || '%' OR lower(e.subject) LIKE '%' || $9 || '%' OR lower(e.requester_name) LIKE '%' || $9 || '%' OR lower(COALESCE(g.full_name, '')) LIKE '%' || $9 || '%')
		ORDER BY
			CASE WHEN e.status IN ('RESOLVED', 'CLOSED') THEN 1 ELSE 0 END ASC,
			CASE e.priority WHEN 'URGENT' THEN 1 WHEN 'HIGH' THEN 2 WHEN 'NORMAL' THEN 3 ELSE 4 END ASC,
			e.created_at DESC
		LIMIT $10
	`, propertyID, st, cat, pri, f.AssignedTo, f.GuestID, f.OpenOnly, f.Overdue, q, sqlLimit)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()
	var out []domain.GuestEnquiry
	for rows.Next() {
		e, err := scanEnquiryRow(rows)
		if err != nil {
			return nil, MapErr(err)
		}
		out = append(out, e)
	}
	return out, MapErr(rows.Err())
}

func (s *Store) GetGuestEnquiry(ctx context.Context, db DBTX, propertyID, enquiryID uuid.UUID) (domain.GuestEnquiry, error) {
	if db == nil {
		db = s.pool
	}
	row := db.QueryRow(ctx, enquirySelectCols+`WHERE e.property_id = $1 AND e.id = $2`, propertyID, enquiryID)
	e, err := scanEnquiryRow(row)
	return e, MapErr(err)
}

func (s *Store) CreateGuestEnquiry(ctx context.Context, tx pgx.Tx, e domain.GuestEnquiry) (domain.GuestEnquiry, error) {
	if e.ID == uuid.Nil {
		id, err := NewUUID()
		if err != nil {
			return domain.GuestEnquiry{}, err
		}
		e.ID = id
	}
	if e.Reference == "" {
		ref, err := s.nextEnquiryReferenceTx(ctx, tx, time.Now().UTC().Year())
		if err != nil {
			return domain.GuestEnquiry{}, err
		}
		e.Reference = ref
	}
	if e.AssignedUserID != nil {
		if err := s.validateMaintenanceAssigneesTx(ctx, tx, e.PropertyID, e.AssignedUserID, nil); err != nil {
			return domain.GuestEnquiry{}, err
		}
	}
	if e.Status == "" {
		e.Status = domain.EnqStatusNew
	}
	if e.Priority == "" {
		e.Priority = domain.EnqPriorityNormal
	}
	if e.Source == "" {
		e.Source = domain.EnqSourceAdmin
	}

	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO guest_enquiries (
			id, property_id, reference, guest_id, reservation_id, maintenance_request_id,
			category, status, priority, source, subject, description,
			requester_name, requester_email, requester_phone,
			assigned_user_id, target_response_at, follow_up_at, created_by, updated_by
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9, $10, $11, $12,
			$13, $14, $15,
			$16, $17, $18, $19, $20
		)
		RETURNING id
	`,
		e.ID, e.PropertyID, e.Reference, e.GuestID, e.ReservationID, e.MaintenanceRequestID,
		string(e.Category), string(e.Status), string(e.Priority), string(e.Source),
		strings.TrimSpace(e.Subject), strings.TrimSpace(e.Description),
		strings.TrimSpace(e.RequesterName), domain.NormalizeEmail(e.RequesterEmail), strings.TrimSpace(e.RequesterPhone),
		e.AssignedUserID, e.TargetResponseAt, e.FollowUpAt, e.CreatedBy, e.UpdatedBy,
	).Scan(&id)
	if err != nil {
		return domain.GuestEnquiry{}, MapErr(err)
	}
	return s.GetGuestEnquiry(ctx, tx, e.PropertyID, id)
}

func (s *Store) UpdateGuestEnquiry(ctx context.Context, tx pgx.Tx, e domain.GuestEnquiry) (domain.GuestEnquiry, error) {
	if e.AssignedUserID != nil {
		if err := s.validateMaintenanceAssigneesTx(ctx, tx, e.PropertyID, e.AssignedUserID, nil); err != nil {
			return domain.GuestEnquiry{}, err
		}
	}
	tag, err := tx.Exec(ctx, `
		UPDATE guest_enquiries
		SET guest_id = $3,
		    reservation_id = $4,
		    maintenance_request_id = $5,
		    category = $6,
		    status = $7,
		    priority = $8,
		    subject = $9,
		    description = $10,
		    assigned_user_id = $11,
		    target_response_at = $12,
		    follow_up_at = $13,
		    resolved_at = $14,
		    resolved_by = $15,
		    resolution_notes = $16,
		    closed_at = $17,
		    closed_by = $18,
		    updated_by = $19,
		    version = version + 1,
		    updated_at = NOW()
		WHERE property_id = $1 AND id = $2 AND version = $20
	`,
		e.PropertyID, e.ID, e.GuestID, e.ReservationID, e.MaintenanceRequestID,
		string(e.Category), string(e.Status), string(e.Priority),
		strings.TrimSpace(e.Subject), strings.TrimSpace(e.Description),
		e.AssignedUserID, e.TargetResponseAt, e.FollowUpAt,
		e.ResolvedAt, e.ResolvedBy, strings.TrimSpace(e.ResolutionNotes),
		e.ClosedAt, e.ClosedBy, e.UpdatedBy, e.Version,
	)
	if err != nil {
		return domain.GuestEnquiry{}, MapErr(err)
	}
	if tag.RowsAffected() == 0 {
		if _, gerr := s.GetGuestEnquiry(ctx, tx, e.PropertyID, e.ID); gerr == nil {
			return domain.GuestEnquiry{}, domain.ErrConflict
		}
		return domain.GuestEnquiry{}, domain.ErrNotFound
	}
	return s.GetGuestEnquiry(ctx, tx, e.PropertyID, e.ID)
}

func (s *Store) ListGuestEnquiryMessages(ctx context.Context, db DBTX, propertyID, enquiryID uuid.UUID) ([]domain.GuestEnquiryMessage, error) {
	if db == nil {
		db = s.pool
	}
	rows, err := db.Query(ctx, `
		SELECT id, property_id, enquiry_id, direction, sender_name, sender_email, body, is_internal, staff_user_id, created_at
		FROM guest_enquiry_messages
		WHERE property_id = $1 AND enquiry_id = $2
		ORDER BY created_at ASC
	`, propertyID, enquiryID)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()
	var out []domain.GuestEnquiryMessage
	for rows.Next() {
		var m domain.GuestEnquiryMessage
		var dir string
		if err := rows.Scan(&m.ID, &m.PropertyID, &m.EnquiryID, &dir, &m.SenderName, &m.SenderEmail, &m.Body, &m.IsInternal, &m.StaffUserID, &m.CreatedAt); err != nil {
			return nil, MapErr(err)
		}
		m.Direction = domain.CommDirection(dir)
		out = append(out, m)
	}
	return out, MapErr(rows.Err())
}

func (s *Store) ListGuestEnquiryMessagesByGuest(ctx context.Context, db DBTX, propertyID, guestID uuid.UUID) ([]domain.GuestEnquiryMessage, error) {
	if db == nil {
		db = s.pool
	}
	rows, err := db.Query(ctx, `
		SELECT m.id, m.property_id, m.enquiry_id, m.direction, m.sender_name, m.sender_email, m.body, m.is_internal, m.staff_user_id, m.created_at
		FROM guest_enquiry_messages m
		JOIN guest_enquiries e ON e.property_id = m.property_id AND e.id = m.enquiry_id
		WHERE m.property_id = $1 AND e.guest_id = $2
		ORDER BY m.created_at ASC
	`, propertyID, guestID)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()
	var out []domain.GuestEnquiryMessage
	for rows.Next() {
		var m domain.GuestEnquiryMessage
		var dir string
		if err := rows.Scan(&m.ID, &m.PropertyID, &m.EnquiryID, &dir, &m.SenderName, &m.SenderEmail, &m.Body, &m.IsInternal, &m.StaffUserID, &m.CreatedAt); err != nil {
			return nil, MapErr(err)
		}
		m.Direction = domain.CommDirection(dir)
		out = append(out, m)
	}
	return out, MapErr(rows.Err())
}

// ListGuestEnquiriesByClusterOrReservations returns all enquiries in propertyID linked directly to
// any guest in clusterIDs OR linked via reservation_id (including reservation-only enquiries where guest_id IS NULL).
func (s *Store) ListGuestEnquiriesByClusterOrReservations(ctx context.Context, db DBTX, propertyID uuid.UUID, clusterIDs, reservationIDs []uuid.UUID) ([]domain.GuestEnquiry, error) {
	if db == nil {
		db = s.pool
	}
	if len(clusterIDs) == 0 && len(reservationIDs) == 0 {
		return nil, nil
	}
	rows, err := db.Query(ctx, enquirySelectCols+`
		WHERE e.property_id = $1
		  AND (
			(cardinality($2::uuid[]) > 0 AND e.guest_id = ANY($2::uuid[]))
			OR (cardinality($3::uuid[]) > 0 AND e.reservation_id = ANY($3::uuid[]))
		  )
		ORDER BY e.created_at DESC, e.id DESC
	`, propertyID, clusterIDs, reservationIDs)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()
	var out []domain.GuestEnquiry
	for rows.Next() {
		e, err := scanEnquiryRow(rows)
		if err != nil {
			return nil, MapErr(err)
		}
		out = append(out, e)
	}
	return out, MapErr(rows.Err())
}

// ListGuestEnquiryMessagesByEnquiryIDs returns all messages across the given enquiry IDs scoped by propertyID.
func (s *Store) ListGuestEnquiryMessagesByEnquiryIDs(ctx context.Context, db DBTX, propertyID uuid.UUID, enquiryIDs []uuid.UUID) ([]domain.GuestEnquiryMessage, error) {
	if db == nil {
		db = s.pool
	}
	if len(enquiryIDs) == 0 {
		return nil, nil
	}
	rows, err := db.Query(ctx, `
		SELECT m.id, m.property_id, m.enquiry_id, m.direction, m.sender_name, m.sender_email, m.body, m.is_internal, m.staff_user_id, m.created_at
		FROM guest_enquiry_messages m
		JOIN guest_enquiries e ON e.property_id = m.property_id AND e.id = m.enquiry_id
		WHERE m.property_id = $1 AND m.enquiry_id = ANY($2::uuid[])
		ORDER BY m.created_at ASC, m.id ASC
	`, propertyID, enquiryIDs)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()
	var out []domain.GuestEnquiryMessage
	for rows.Next() {
		var m domain.GuestEnquiryMessage
		var dir string
		if err := rows.Scan(&m.ID, &m.PropertyID, &m.EnquiryID, &dir, &m.SenderName, &m.SenderEmail, &m.Body, &m.IsInternal, &m.StaffUserID, &m.CreatedAt); err != nil {
			return nil, MapErr(err)
		}
		m.Direction = domain.CommDirection(dir)
		out = append(out, m)
	}
	return out, MapErr(rows.Err())
}

// ListGuestCommunicationsByClusterOrLinks returns all communications in propertyID linked by
// cluster guest_id, cluster reservation_id, or cluster enquiry_id.
func (s *Store) ListGuestCommunicationsByClusterOrLinks(ctx context.Context, db DBTX, propertyID uuid.UUID, clusterIDs, reservationIDs, enquiryIDs []uuid.UUID) ([]domain.GuestCommunication, error) {
	if db == nil {
		db = s.pool
	}
	if len(clusterIDs) == 0 && len(reservationIDs) == 0 && len(enquiryIDs) == 0 {
		return nil, nil
	}
	rows, err := db.Query(ctx, `
		SELECT c.id, c.property_id, c.guest_id, COALESCE(g.full_name, ''),
		       c.reservation_id, COALESCE(r.reference, ''), c.enquiry_id,
		       c.interaction_type, c.direction, c.subject, c.body_text, c.body_html, c.summary,
		       c.recipient_email, c.sender_email, c.template_id, c.template_version_id, c.rule_id, c.trigger_event,
		       c.email_outbox_id, c.delivery_status, c.provider_message_id,
		       c.follow_up_required, c.follow_up_assigned_to, c.follow_up_at, c.follow_up_completed_at,
		       c.staff_user_id, COALESCE(au.full_name, ''), c.occurred_at, c.created_at, c.updated_at
		FROM guest_communications c
		LEFT JOIN guests g ON g.id = c.guest_id
		LEFT JOIN reservations r ON r.id = c.reservation_id
		LEFT JOIN admin_users au ON au.id = c.staff_user_id
		WHERE c.property_id = $1
		  AND (
			(cardinality($2::uuid[]) > 0 AND c.guest_id = ANY($2::uuid[]))
			OR (cardinality($3::uuid[]) > 0 AND c.reservation_id = ANY($3::uuid[]))
			OR (cardinality($4::uuid[]) > 0 AND c.enquiry_id = ANY($4::uuid[]))
		  )
		ORDER BY c.occurred_at DESC, c.created_at DESC, c.id DESC
	`, propertyID, clusterIDs, reservationIDs, enquiryIDs)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()
	var out []domain.GuestCommunication
	for rows.Next() {
		var c domain.GuestCommunication
		var it, dir, ds string
		if err := rows.Scan(
			&c.ID, &c.PropertyID, &c.GuestID, &c.GuestName,
			&c.ReservationID, &c.ReservationReference, &c.EnquiryID,
			&it, &dir, &c.Subject, &c.BodyText, &c.BodyHTML, &c.Summary,
			&c.RecipientEmail, &c.SenderEmail, &c.TemplateID, &c.TemplateVersionID, &c.RuleID, &c.TriggerEvent,
			&c.EmailOutboxID, &ds, &c.ProviderMessageID,
			&c.FollowUpRequired, &c.FollowUpAssignedTo, &c.FollowUpAt, &c.FollowUpCompletedAt,
			&c.StaffUserID, &c.StaffUserName, &c.OccurredAt, &c.CreatedAt, &c.UpdatedAt,
		); err != nil {
			return nil, MapErr(err)
		}
		c.InteractionType = domain.InteractionType(it)
		c.Direction = domain.CommDirection(dir)
		c.DeliveryStatus = domain.DeliveryStatus(ds)
		out = append(out, c)
	}
	return out, MapErr(rows.Err())
}

// ListGuestDeliveryEventsByCommunicationIDs returns sanitized operational delivery history
// for the guest's exported communications, omitting raw webhook secrets/payload blobs.
func (s *Store) ListGuestDeliveryEventsByCommunicationIDs(ctx context.Context, db DBTX, propertyID uuid.UUID, communicationIDs []uuid.UUID) ([]domain.CommunicationDeliveryEvent, error) {
	if db == nil {
		db = s.pool
	}
	if len(communicationIDs) == 0 {
		return nil, nil
	}
	rows, err := db.Query(ctx, `
		SELECT d.id, d.property_id, d.communication_id, d.email_outbox_id, d.event_type, d.provider, d.detail, d.occurred_at
		FROM communication_delivery_events d
		WHERE d.property_id = $1 AND d.communication_id = ANY($2::uuid[])
		ORDER BY d.occurred_at ASC, d.id ASC
	`, propertyID, communicationIDs)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()
	var out []domain.CommunicationDeliveryEvent
	for rows.Next() {
		var ev domain.CommunicationDeliveryEvent
		if err := rows.Scan(&ev.ID, &ev.PropertyID, &ev.CommunicationID, &ev.EmailOutboxID, &ev.EventType, &ev.Provider, &ev.Detail, &ev.OccurredAt); err != nil {
			return nil, MapErr(err)
		}
		out = append(out, ev)
	}
	return out, MapErr(rows.Err())
}

func (s *Store) AddGuestEnquiryMessage(ctx context.Context, tx pgx.Tx, m domain.GuestEnquiryMessage) (domain.GuestEnquiryMessage, error) {
	if m.ID == uuid.Nil {
		id, err := NewUUID()
		if err != nil {
			return domain.GuestEnquiryMessage{}, err
		}
		m.ID = id
	}
	var out domain.GuestEnquiryMessage
	var dir string
	err := tx.QueryRow(ctx, `
		INSERT INTO guest_enquiry_messages (id, property_id, enquiry_id, direction, sender_name, sender_email, body, is_internal, staff_user_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, property_id, enquiry_id, direction, sender_name, sender_email, body, is_internal, staff_user_id, created_at
	`, m.ID, m.PropertyID, m.EnquiryID, string(m.Direction), strings.TrimSpace(m.SenderName), domain.NormalizeEmail(m.SenderEmail), strings.TrimSpace(m.Body), m.IsInternal, m.StaffUserID).Scan(
		&out.ID, &out.PropertyID, &out.EnquiryID, &dir, &out.SenderName, &out.SenderEmail, &out.Body, &out.IsInternal, &out.StaffUserID, &out.CreatedAt,
	)
	if err != nil {
		return domain.GuestEnquiryMessage{}, MapErr(err)
	}
	out.Direction = domain.CommDirection(dir)
	return out, nil
}

// --- Guest Communications & Delivery Lifecycle ---

func (s *Store) ListGuestCommunications(ctx context.Context, db DBTX, propertyID uuid.UUID, guestID, reservationID *uuid.UUID, limit int) ([]domain.GuestCommunication, error) {
	if db == nil {
		db = s.pool
	}
	var sqlLimit *int
	if limit >= 0 {
		if limit == 0 || limit > 250 {
			limit = 100
		}
		sqlLimit = &limit
	}
	rows, err := db.Query(ctx, `
		SELECT c.id, c.property_id, c.guest_id, COALESCE(g.full_name, ''),
		       c.reservation_id, COALESCE(r.reference, ''), c.enquiry_id,
		       c.interaction_type, c.direction, c.subject, c.body_text, c.body_html, c.summary,
		       c.recipient_email, c.sender_email, c.template_id, c.template_version_id, c.rule_id, c.trigger_event,
		       c.email_outbox_id, c.delivery_status, c.provider_message_id,
		       c.follow_up_required, c.follow_up_assigned_to, c.follow_up_at, c.follow_up_completed_at,
		       c.staff_user_id, COALESCE(au.full_name, ''), c.occurred_at, c.created_at, c.updated_at
		FROM guest_communications c
		LEFT JOIN guests g ON g.id = c.guest_id
		LEFT JOIN reservations r ON r.id = c.reservation_id
		LEFT JOIN admin_users au ON au.id = c.staff_user_id
		WHERE c.property_id = $1
		  AND ($2::uuid IS NULL OR c.guest_id = $2)
		  AND ($3::uuid IS NULL OR c.reservation_id = $3)
		ORDER BY c.occurred_at DESC, c.created_at DESC
		LIMIT $4
	`, propertyID, guestID, reservationID, sqlLimit)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()
	var out []domain.GuestCommunication
	for rows.Next() {
		var c domain.GuestCommunication
		var it, dir, ds string
		if err := rows.Scan(
			&c.ID, &c.PropertyID, &c.GuestID, &c.GuestName,
			&c.ReservationID, &c.ReservationReference, &c.EnquiryID,
			&it, &dir, &c.Subject, &c.BodyText, &c.BodyHTML, &c.Summary,
			&c.RecipientEmail, &c.SenderEmail, &c.TemplateID, &c.TemplateVersionID, &c.RuleID, &c.TriggerEvent,
			&c.EmailOutboxID, &ds, &c.ProviderMessageID,
			&c.FollowUpRequired, &c.FollowUpAssignedTo, &c.FollowUpAt, &c.FollowUpCompletedAt,
			&c.StaffUserID, &c.StaffUserName, &c.OccurredAt, &c.CreatedAt, &c.UpdatedAt,
		); err != nil {
			return nil, MapErr(err)
		}
		c.InteractionType = domain.InteractionType(it)
		c.Direction = domain.CommDirection(dir)
		c.DeliveryStatus = domain.DeliveryStatus(ds)
		out = append(out, c)
	}
	return out, MapErr(rows.Err())
}

func (s *Store) CreateGuestCommunication(ctx context.Context, tx pgx.Tx, c domain.GuestCommunication) (domain.GuestCommunication, error) {
	if c.ID == uuid.Nil {
		id, err := NewUUID()
		if err != nil {
			return domain.GuestCommunication{}, err
		}
		c.ID = id
	}
	if c.OccurredAt.IsZero() {
		c.OccurredAt = time.Now().UTC()
	}
	if c.DeliveryStatus == "" {
		c.DeliveryStatus = domain.DeliveryLogged
	}
	if c.FollowUpAssignedTo != nil {
		if err := s.validateMaintenanceAssigneesTx(ctx, tx, c.PropertyID, c.FollowUpAssignedTo, nil); err != nil {
			return domain.GuestCommunication{}, err
		}
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO guest_communications (
			id, property_id, guest_id, reservation_id, enquiry_id,
			interaction_type, direction, subject, body_text, body_html, summary,
			recipient_email, sender_email, template_id, template_version_id, rule_id, trigger_event,
			email_outbox_id, delivery_status, provider_message_id,
			follow_up_required, follow_up_assigned_to, follow_up_at, staff_user_id, occurred_at
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8, $9, $10, $11,
			$12, $13, $14, $15, $16, $17,
			$18, $19, $20,
			$21, $22, $23, $24, $25
		)
	`,
		c.ID, c.PropertyID, c.GuestID, c.ReservationID, c.EnquiryID,
		string(c.InteractionType), string(c.Direction), strings.TrimSpace(c.Subject), c.BodyText, c.BodyHTML, strings.TrimSpace(c.Summary),
		domain.NormalizeEmail(c.RecipientEmail), domain.NormalizeEmail(c.SenderEmail),
		c.TemplateID, c.TemplateVersionID, c.RuleID, c.TriggerEvent,
		c.EmailOutboxID, string(c.DeliveryStatus), c.ProviderMessageID,
		c.FollowUpRequired, c.FollowUpAssignedTo, c.FollowUpAt, c.StaffUserID, c.OccurredAt,
	)
	if err != nil {
		return domain.GuestCommunication{}, MapErr(err)
	}
	return c, nil
}

// LinkCommunicationOutbox attaches a queued email_outbox row to its communication timeline entry.
func (s *Store) LinkCommunicationOutbox(ctx context.Context, tx pgx.Tx, commID, outboxID uuid.UUID) error {
	_, err := tx.Exec(ctx, `UPDATE guest_communications SET email_outbox_id = $2 WHERE id = $1`, commID, outboxID)
	return MapErr(err)
}

// ResolveGuestForReservationTx returns the guest_id owning reservationID in propertyID.
func (s *Store) ResolveGuestForReservationTx(ctx context.Context, db DBTX, propertyID, reservationID uuid.UUID) (uuid.UUID, error) {
	if db == nil {
		db = s.pool
	}
	var guestID uuid.UUID
	err := db.QueryRow(ctx, `
		SELECT guest_id FROM reservations WHERE property_id = $1 AND id = $2
	`, propertyID, reservationID).Scan(&guestID)
	if err != nil {
		return uuid.Nil, MapErr(err)
	}
	return guestID, nil
}

// LockGuestByEnquiryTx resolves and locks the guest cluster (via enquiry guest_id or reservation guest_id)
// if the enquiry is linked to a guest profile.
func (s *Store) LockGuestByEnquiryTx(ctx context.Context, tx pgx.Tx, propertyID, enquiryID uuid.UUID) (anonOrPending bool, err error) {
	var gID, rID *uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT guest_id, reservation_id FROM guest_enquiries WHERE property_id = $1 AND id = $2
	`, propertyID, enquiryID).Scan(&gID, &rID)
	if err != nil {
		return false, MapErr(err)
	}
	var targetGuest uuid.UUID
	if gID != nil {
		targetGuest = *gID
	} else if rID != nil {
		targetGuest, err = s.ResolveGuestForReservationTx(ctx, tx, propertyID, *rID)
		if err != nil {
			return false, err
		}
	}
	if targetGuest == uuid.Nil {
		return false, nil
	}
	_, _, anonOrPending, err = s.LockCanonicalGuestClusterTx(ctx, tx, propertyID, targetGuest)
	return anonOrPending, err
}

func (s *Store) CompleteCommunicationFollowUp(ctx context.Context, tx pgx.Tx, propertyID, commID uuid.UUID) error {
	var gID, rID, eID *uuid.UUID
	if err := tx.QueryRow(ctx, `
		SELECT guest_id, reservation_id, enquiry_id
		FROM guest_communications
		WHERE property_id = $1 AND id = $2
	`, propertyID, commID).Scan(&gID, &rID, &eID); err != nil {
		return MapErr(err)
	}
	var targetGuest uuid.UUID
	if gID != nil {
		targetGuest = *gID
	} else if rID != nil {
		targetGuest, _ = s.ResolveGuestForReservationTx(ctx, tx, propertyID, *rID)
	} else if eID != nil {
		anon, lErr := s.LockGuestByEnquiryTx(ctx, tx, propertyID, *eID)
		if lErr != nil {
			return lErr
		}
		if anon {
			return fmt.Errorf("%w: cannot mutate follow-up on anonymized or erasure-pending guest", domain.ErrConflict)
		}
	}
	if targetGuest != uuid.Nil {
		_, _, anon, lErr := s.LockCanonicalGuestClusterTx(ctx, tx, propertyID, targetGuest)
		if lErr != nil {
			return lErr
		}
		if anon {
			return fmt.Errorf("%w: cannot mutate follow-up on anonymized or erasure-pending guest", domain.ErrConflict)
		}
	}
	tag, err := tx.Exec(ctx, `
		UPDATE guest_communications
		SET follow_up_completed_at = COALESCE(follow_up_completed_at, NOW()), updated_at = NOW()
		WHERE property_id = $1 AND id = $2
	`, propertyID, commID)
	if err != nil {
		return MapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) RecordCommunicationDeliveryEvent(
	ctx context.Context,
	tx pgx.Tx,
	emailOutboxID *uuid.UUID,
	communicationID *uuid.UUID,
	provider, providerEventID, eventType, detail string,
	payload json.RawMessage,
	occurredAt time.Time,
) error {
	var propID uuid.UUID
	var resolvedCommID uuid.UUID
	var resolvedOutboxID *uuid.UUID
	if communicationID != nil {
		resolvedCommID = *communicationID
		err := tx.QueryRow(ctx, `SELECT property_id, email_outbox_id FROM guest_communications WHERE id = $1`, resolvedCommID).Scan(&propID, &resolvedOutboxID)
		if err != nil {
			return MapErr(err)
		}
	} else if emailOutboxID != nil {
		var commPtr *uuid.UUID
		err := tx.QueryRow(ctx, `SELECT property_id, communication_id FROM email_outbox WHERE id = $1`, *emailOutboxID).Scan(&propID, &commPtr)
		if err != nil {
			return MapErr(err)
		}
		if commPtr == nil {
			return domain.ErrNotFound
		}
		resolvedCommID = *commPtr
		resolvedOutboxID = emailOutboxID
	} else {
		return fmt.Errorf("%w: communication_id or email_outbox_id required", domain.ErrValidation)
	}

	// Lock canonical guest cluster BEFORE mutating outbox / delivery events so webhook
	// bounce updates serialize cleanly with guest anonymisation and scrub PII when erased.
	var commGuestID, commResID *uuid.UUID
	_ = tx.QueryRow(ctx, `SELECT guest_id, reservation_id FROM guest_communications WHERE property_id = $1 AND id = $2`, propID, resolvedCommID).Scan(&commGuestID, &commResID)
	targetGuest := uuid.Nil
	if commGuestID != nil {
		targetGuest = *commGuestID
	} else if commResID != nil {
		var rg *uuid.UUID
		_ = tx.QueryRow(ctx, `SELECT guest_id FROM reservations WHERE property_id = $1 AND id = $2`, propID, *commResID).Scan(&rg)
		if rg != nil {
			targetGuest = *rg
		}
	}
	guestBlocked := false
	if targetGuest != uuid.Nil {
		blocked, lErr := s.lockGuestClusterAndCheckErasureDB(ctx, tx, propID, targetGuest)
		if lErr == nil && blocked {
			guestBlocked = true
		}
	}
	if !guestBlocked && resolvedOutboxID != nil {
		var outboxRedacted bool
		_ = tx.QueryRow(ctx, `
			SELECT status IN ('CANCELLED', 'CANCEL_REQUESTED') OR recipient_email IN ('', '[redacted]')
			FROM email_outbox WHERE property_id = $1 AND id = $2
		`, propID, *resolvedOutboxID).Scan(&outboxRedacted)
		guestBlocked = outboxRedacted
	}

	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	cleanDetail := strings.TrimSpace(detail)
	if guestBlocked {
		cleanDetail = "[Anonymised]"
		payload = json.RawMessage(`{"anonymized":true}`)
	}
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}

	evID, err := NewUUID()
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `
		INSERT INTO communication_delivery_events (
			id, property_id, communication_id, email_outbox_id, event_type, provider, provider_event_id, detail, payload, occurred_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (provider, provider_event_id) WHERE provider <> '' AND provider_event_id <> '' DO NOTHING
	`, evID, propID, resolvedCommID, resolvedOutboxID, eventType, strings.TrimSpace(provider), strings.TrimSpace(providerEventID), cleanDetail, payload, occurredAt)
	if err != nil {
		return MapErr(err)
	}
	if tag.RowsAffected() == 0 && providerEventID != "" {
		return nil // idempotent webhook duplicate
	}

	switch strings.ToUpper(strings.TrimSpace(eventType)) {
	case "DELIVERED":
		if _, err := tx.Exec(ctx, `
			UPDATE guest_communications
			SET delivery_status = CASE WHEN delivery_status IN ('BOUNCED', 'CANCELLED') THEN delivery_status ELSE 'DELIVERED' END,
			    updated_at = NOW()
			WHERE id = $1
		`, resolvedCommID); err != nil {
			return MapErr(err)
		}
	case "BOUNCED":
		if _, err := tx.Exec(ctx, `
			UPDATE guest_communications
			SET delivery_status = CASE WHEN delivery_status = 'CANCELLED' THEN delivery_status ELSE 'BOUNCED' END,
			    updated_at = NOW()
			WHERE id = $1
		`, resolvedCommID); err != nil {
			return MapErr(err)
		}
		if resolvedOutboxID != nil {
			lastErrVal := "Bounced: " + detail
			if guestBlocked {
				lastErrVal = sanitizeOutboxErrorForAnon(detail, true)
			}
			if _, err := tx.Exec(ctx, `
				UPDATE email_outbox
				SET status = CASE WHEN status IN ('CANCELLED', 'CANCEL_REQUESTED') THEN status ELSE 'FAILED' END,
				    last_error = $2,
				    updated_at = NOW()
				WHERE id = $1
			`, *resolvedOutboxID, lastErrVal); err != nil {
				return MapErr(err)
			}
		}
	case "FAILED":
		if _, err := tx.Exec(ctx, `
			UPDATE guest_communications
			SET delivery_status = CASE WHEN delivery_status = 'CANCELLED' THEN delivery_status ELSE 'FAILED' END,
			    updated_at = NOW()
			WHERE id = $1
		`, resolvedCommID); err != nil {
			return MapErr(err)
		}
	}
	return nil
}

func (s *Store) ListFailedPropertyEmails(ctx context.Context, propertyID uuid.UUID) ([]domain.FailedEmailSummary, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT o.id, o.communication_id, o.guest_id, o.reservation_id, o.template, o.recipient_email, o.subject, o.status, o.attempts, o.last_error, o.updated_at
		FROM email_outbox o
		LEFT JOIN guest_communications c ON c.id = o.communication_id
		WHERE o.property_id = $1
		  AND o.status NOT IN ('CANCELLED', 'CANCEL_REQUESTED', 'DISPATCHING')
		  AND o.recipient_email <> '[redacted]'
		  AND (o.status IN ('FAILED', 'DISPATCH_UNKNOWN') OR c.delivery_status IN ('FAILED', 'BOUNCED'))
		ORDER BY o.updated_at DESC
		LIMIT 50
	`, propertyID)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()
	var out []domain.FailedEmailSummary
	for rows.Next() {
		var f domain.FailedEmailSummary
		if err := rows.Scan(&f.OutboxID, &f.CommunicationID, &f.GuestID, &f.ReservationID, &f.Template, &f.Recipient, &f.Subject, &f.Status, &f.Attempts, &f.LastError, &f.UpdatedAt); err != nil {
			return nil, MapErr(err)
		}
		out = append(out, f)
	}
	return out, MapErr(rows.Err())
}

func (s *Store) RetryFailedOutboxEmail(ctx context.Context, tx pgx.Tx, propertyID, outboxID uuid.UUID) error {
	guestBlocked, lockErr := s.lockGuestForOutboxTx(ctx, tx, outboxID)
	if lockErr != nil {
		return lockErr
	}
	var commID *uuid.UUID
	var curStatus, tpl string
	var rowGuestBlocked bool
	err := tx.QueryRow(ctx, `
		SELECT o.communication_id, o.status, o.template,
		       COALESCE(g.status = 'ANONYMIZED' OR g.anonymized_at IS NOT NULL OR g.erasure_requested_at IS NOT NULL, FALSE)
		FROM email_outbox o
		LEFT JOIN reservations r ON r.property_id = o.property_id AND r.id = o.reservation_id
		LEFT JOIN guests g ON g.property_id = o.property_id AND g.id = COALESCE(o.guest_id, r.guest_id)
		WHERE o.property_id = $1 AND o.id = $2
		FOR UPDATE OF o
	`, propertyID, outboxID).Scan(&commID, &curStatus, &tpl, &rowGuestBlocked)
	if err != nil {
		return MapErr(err)
	}
	if domain.IsPortalSecurityChallengeTemplate(tpl) {
		return fmt.Errorf("%w: security verification challenge emails cannot be retried; send a fresh portal verification code instead", domain.ErrConflict)
	}
	if guestBlocked || rowGuestBlocked || curStatus == domain.OutboxStatusCancelled || curStatus == domain.OutboxStatusCancelRequested {
		return fmt.Errorf("%w: cannot retry cancelled or anonymized email", domain.ErrConflict)
	}
	if curStatus != domain.OutboxStatusFailed {
		return fmt.Errorf("%w: only FAILED emails can be manually retried (current status: %s)", domain.ErrConflict, curStatus)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE email_outbox
		SET status = 'PENDING', attempts = 0, next_attempt_at = NOW(), last_error = '', locked_at = NULL, worker_token = NULL, updated_at = NOW()
		WHERE property_id = $1 AND id = $2
	`, propertyID, outboxID); err != nil {
		return MapErr(err)
	}
	if commID != nil {
		if _, err := tx.Exec(ctx, `UPDATE guest_communications SET delivery_status = 'QUEUED', updated_at = NOW() WHERE id = $1`, *commID); err != nil {
			return MapErr(err)
		}
		evID, err := NewUUID()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO communication_delivery_events (id, property_id, communication_id, email_outbox_id, event_type, provider, detail)
			VALUES ($1, $2, $3, $4, 'RETRY_SCHEDULED', 'manual', 'Queued for manual retry by staff')
		`, evID, propertyID, *commID, outboxID); err != nil {
			return MapErr(err)
		}
	}
	return nil
}

// --- Communication Templates & Automation ---

func (s *Store) ListCommunicationTemplates(ctx context.Context, db DBTX, propertyID uuid.UUID) ([]domain.CommunicationTemplate, error) {
	if db == nil {
		db = s.pool
	}
	rows, err := db.Query(ctx, `
		SELECT id, property_id, code, name, category, locale, subject_template, body_text_template, body_html_template,
		       is_active, version, created_by, updated_by, created_at, updated_at
		FROM communication_templates
		WHERE property_id = $1
		ORDER BY category ASC, name ASC, locale ASC
	`, propertyID)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()
	var out []domain.CommunicationTemplate
	for rows.Next() {
		var t domain.CommunicationTemplate
		if err := rows.Scan(&t.ID, &t.PropertyID, &t.Code, &t.Name, &t.Category, &t.Locale, &t.SubjectTemplate, &t.BodyTextTemplate, &t.BodyHTMLTemplate, &t.IsActive, &t.Version, &t.CreatedBy, &t.UpdatedBy, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, MapErr(err)
		}
		out = append(out, t)
	}
	return out, MapErr(rows.Err())
}

func (s *Store) GetCommunicationTemplate(ctx context.Context, db DBTX, propertyID, id uuid.UUID) (domain.CommunicationTemplate, error) {
	if db == nil {
		db = s.pool
	}
	var t domain.CommunicationTemplate
	err := db.QueryRow(ctx, `
		SELECT id, property_id, code, name, category, locale, subject_template, body_text_template, body_html_template,
		       is_active, version, created_by, updated_by, created_at, updated_at
		FROM communication_templates
		WHERE property_id = $1 AND id = $2
	`, propertyID, id).Scan(&t.ID, &t.PropertyID, &t.Code, &t.Name, &t.Category, &t.Locale, &t.SubjectTemplate, &t.BodyTextTemplate, &t.BodyHTMLTemplate, &t.IsActive, &t.Version, &t.CreatedBy, &t.UpdatedBy, &t.CreatedAt, &t.UpdatedAt)
	return t, MapErr(err)
}

func (s *Store) GetLatestTemplateVersion(ctx context.Context, db DBTX, propertyID, templateID uuid.UUID) (domain.CommunicationTemplateVersion, error) {
	if db == nil {
		db = s.pool
	}
	var v domain.CommunicationTemplateVersion
	err := db.QueryRow(ctx, `
		SELECT id, property_id, template_id, version, subject_template, body_text_template, body_html_template, edited_by, created_at
		FROM communication_template_versions
		WHERE property_id = $1 AND template_id = $2
		ORDER BY version DESC
		LIMIT 1
	`, propertyID, templateID).Scan(&v.ID, &v.PropertyID, &v.TemplateID, &v.Version, &v.SubjectTemplate, &v.BodyTextTemplate, &v.BodyHTMLTemplate, &v.EditedBy, &v.CreatedAt)
	return v, MapErr(err)
}

func (s *Store) CreateCommunicationTemplate(ctx context.Context, tx pgx.Tx, t domain.CommunicationTemplate) (domain.CommunicationTemplate, error) {
	if t.ID == uuid.Nil {
		id, err := NewUUID()
		if err != nil {
			return domain.CommunicationTemplate{}, err
		}
		t.ID = id
	}
	if strings.TrimSpace(t.Locale) == "" {
		t.Locale = "en"
	}
	var out domain.CommunicationTemplate
	err := tx.QueryRow(ctx, `
		INSERT INTO communication_templates (
			id, property_id, code, name, category, locale, subject_template, body_text_template, body_html_template, is_active, created_by, updated_by
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING id, property_id, code, name, category, locale, subject_template, body_text_template, body_html_template,
		          is_active, version, created_by, updated_by, created_at, updated_at
	`,
		t.ID, t.PropertyID, strings.ToUpper(strings.TrimSpace(t.Code)), strings.TrimSpace(t.Name),
		strings.ToUpper(strings.TrimSpace(t.Category)), strings.ToLower(strings.TrimSpace(t.Locale)),
		t.SubjectTemplate, t.BodyTextTemplate, t.BodyHTMLTemplate, t.IsActive, t.CreatedBy, t.UpdatedBy,
	).Scan(&out.ID, &out.PropertyID, &out.Code, &out.Name, &out.Category, &out.Locale, &out.SubjectTemplate, &out.BodyTextTemplate, &out.BodyHTMLTemplate, &out.IsActive, &out.Version, &out.CreatedBy, &out.UpdatedBy, &out.CreatedAt, &out.UpdatedAt)
	if err != nil {
		return domain.CommunicationTemplate{}, MapErr(err)
	}
	verID, err := NewUUID()
	if err != nil {
		return domain.CommunicationTemplate{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO communication_template_versions (id, property_id, template_id, version, subject_template, body_text_template, body_html_template, edited_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, verID, out.PropertyID, out.ID, out.Version, out.SubjectTemplate, out.BodyTextTemplate, out.BodyHTMLTemplate, t.CreatedBy); err != nil {
		return domain.CommunicationTemplate{}, MapErr(err)
	}
	return out, nil
}

func (s *Store) UpdateCommunicationTemplate(ctx context.Context, tx pgx.Tx, t domain.CommunicationTemplate) (domain.CommunicationTemplate, error) {
	var out domain.CommunicationTemplate
	err := tx.QueryRow(ctx, `
		UPDATE communication_templates
		SET name = $3,
		    category = $4,
		    subject_template = $5,
		    body_text_template = $6,
		    body_html_template = $7,
		    is_active = $8,
		    updated_by = $9,
		    version = version + 1,
		    updated_at = NOW()
		WHERE property_id = $1 AND id = $2 AND version = $10
		RETURNING id, property_id, code, name, category, locale, subject_template, body_text_template, body_html_template,
		          is_active, version, created_by, updated_by, created_at, updated_at
	`,
		t.PropertyID, t.ID, strings.TrimSpace(t.Name), strings.ToUpper(strings.TrimSpace(t.Category)),
		t.SubjectTemplate, t.BodyTextTemplate, t.BodyHTMLTemplate, t.IsActive, t.UpdatedBy, t.Version,
	).Scan(&out.ID, &out.PropertyID, &out.Code, &out.Name, &out.Category, &out.Locale, &out.SubjectTemplate, &out.BodyTextTemplate, &out.BodyHTMLTemplate, &out.IsActive, &out.Version, &out.CreatedBy, &out.UpdatedBy, &out.CreatedAt, &out.UpdatedAt)
	if err == pgx.ErrNoRows {
		if _, gerr := s.GetCommunicationTemplate(ctx, tx, t.PropertyID, t.ID); gerr == nil {
			return domain.CommunicationTemplate{}, domain.ErrConflict
		}
		return domain.CommunicationTemplate{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.CommunicationTemplate{}, MapErr(err)
	}
	verID, err := NewUUID()
	if err != nil {
		return domain.CommunicationTemplate{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO communication_template_versions (id, property_id, template_id, version, subject_template, body_text_template, body_html_template, edited_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, verID, out.PropertyID, out.ID, out.Version, out.SubjectTemplate, out.BodyTextTemplate, out.BodyHTMLTemplate, t.UpdatedBy); err != nil {
		return domain.CommunicationTemplate{}, MapErr(err)
	}
	return out, nil
}

func (s *Store) ListCommunicationAutomationRules(ctx context.Context, db DBTX, propertyID uuid.UUID) ([]domain.CommunicationAutomationRule, error) {
	if db == nil {
		db = s.pool
	}
	rows, err := db.Query(ctx, `
		SELECT r.id, r.property_id, r.name, r.trigger_event, r.offset_hours, r.send_hour_local,
		       r.template_id, COALESCE(t.name, ''), r.applicable_statuses, r.is_active,
		       r.created_by, r.updated_by, r.created_at, r.updated_at
		FROM communication_automation_rules r
		LEFT JOIN communication_templates t ON t.id = r.template_id
		WHERE r.property_id = $1
		ORDER BY r.trigger_event ASC, r.offset_hours ASC, r.name ASC
	`, propertyID)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()
	var out []domain.CommunicationAutomationRule
	for rows.Next() {
		var r domain.CommunicationAutomationRule
		var tr string
		if err := rows.Scan(&r.ID, &r.PropertyID, &r.Name, &tr, &r.OffsetHours, &r.SendHourLocal, &r.TemplateID, &r.TemplateName, &r.ApplicableStatuses, &r.IsActive, &r.CreatedBy, &r.UpdatedBy, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, MapErr(err)
		}
		r.TriggerEvent = domain.AutomationTrigger(tr)
		out = append(out, r)
	}
	return out, MapErr(rows.Err())
}

func (s *Store) CreateCommunicationAutomationRule(ctx context.Context, tx pgx.Tx, r domain.CommunicationAutomationRule) (domain.CommunicationAutomationRule, error) {
	if r.ID == uuid.Nil {
		id, err := NewUUID()
		if err != nil {
			return domain.CommunicationAutomationRule{}, err
		}
		r.ID = id
	}
	if len(r.ApplicableStatuses) == 0 {
		r.ApplicableStatuses = []string{"CONFIRMED"}
	}
	var out domain.CommunicationAutomationRule
	var tr string
	err := tx.QueryRow(ctx, `
		INSERT INTO communication_automation_rules (
			id, property_id, name, trigger_event, offset_hours, send_hour_local, template_id, applicable_statuses, is_active, created_by, updated_by
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING id, property_id, name, trigger_event, offset_hours, send_hour_local, template_id, applicable_statuses, is_active, created_by, updated_by, created_at, updated_at
	`, r.ID, r.PropertyID, strings.TrimSpace(r.Name), string(r.TriggerEvent), r.OffsetHours, r.SendHourLocal, r.TemplateID, r.ApplicableStatuses, r.IsActive, r.CreatedBy, r.UpdatedBy).Scan(
		&out.ID, &out.PropertyID, &out.Name, &tr, &out.OffsetHours, &out.SendHourLocal, &out.TemplateID, &out.ApplicableStatuses, &out.IsActive, &out.CreatedBy, &out.UpdatedBy, &out.CreatedAt, &out.UpdatedAt,
	)
	if err != nil {
		return domain.CommunicationAutomationRule{}, MapErr(err)
	}
	out.TriggerEvent = domain.AutomationTrigger(tr)
	return out, nil
}

func (s *Store) UpdateCommunicationAutomationRule(ctx context.Context, tx pgx.Tx, r domain.CommunicationAutomationRule) (domain.CommunicationAutomationRule, error) {
	var out domain.CommunicationAutomationRule
	var tr string
	err := tx.QueryRow(ctx, `
		UPDATE communication_automation_rules
		SET name = $3,
		    offset_hours = $4,
		    send_hour_local = $5,
		    template_id = $6,
		    applicable_statuses = $7,
		    is_active = $8,
		    updated_by = $9,
		    updated_at = NOW()
		WHERE property_id = $1 AND id = $2
		RETURNING id, property_id, name, trigger_event, offset_hours, send_hour_local, template_id, applicable_statuses, is_active, created_by, updated_by, created_at, updated_at
	`, r.PropertyID, r.ID, strings.TrimSpace(r.Name), r.OffsetHours, r.SendHourLocal, r.TemplateID, r.ApplicableStatuses, r.IsActive, r.UpdatedBy).Scan(
		&out.ID, &out.PropertyID, &out.Name, &tr, &out.OffsetHours, &out.SendHourLocal, &out.TemplateID, &out.ApplicableStatuses, &out.IsActive, &out.CreatedBy, &out.UpdatedBy, &out.CreatedAt, &out.UpdatedAt,
	)
	if err != nil {
		return domain.CommunicationAutomationRule{}, MapErr(err)
	}
	out.TriggerEvent = domain.AutomationTrigger(tr)
	return out, nil
}

func statusInList(st string, allowed []string) bool {
	for _, a := range allowed {
		if strings.EqualFold(st, strings.TrimSpace(a)) {
			return true
		}
	}
	return false
}

// SyncReservationAutomationJobs recalculates scheduled automated emails for a reservation
// inside a transaction whenever the reservation is created, amended, or cancelled.
func (s *Store) SyncReservationAutomationJobs(ctx context.Context, tx pgx.Tx, prop domain.Property, res domain.Reservation, nowUTC time.Time) error {
	rules, err := s.ListCommunicationAutomationRules(ctx, tx, prop.ID)
	if err != nil {
		return err
	}
	fp := domain.ReservationStayFingerprint(res)

	if res.Status == domain.ResStatusCancelled || res.Status == domain.ResStatusNoShow {
		_, err := tx.Exec(ctx, `
			UPDATE communication_jobs
			SET status = 'CANCELLED', skip_reason = $3, updated_at = NOW()
			WHERE property_id = $1 AND reservation_id = $2 AND status = 'SCHEDULED'
		`, prop.ID, res.ID, "Reservation status changed to "+string(res.Status))
		return MapErr(err)
	}

	for _, rule := range rules {
		if !rule.IsActive || !statusInList(string(res.Status), rule.ApplicableStatuses) {
			if _, err := tx.Exec(ctx, `
				UPDATE communication_jobs
				SET status = 'CANCELLED', skip_reason = $4, updated_at = NOW()
				WHERE property_id = $1 AND rule_id = $2 AND reservation_id = $3 AND status = 'SCHEDULED'
			`, prop.ID, rule.ID, res.ID, "Rule inactive or status inapplicable"); err != nil {
				return MapErr(err)
			}
			continue
		}
		if rule.TriggerEvent == domain.TriggerPaymentReminder && res.BalanceDuePence <= 0 {
			if _, err := tx.Exec(ctx, `
				UPDATE communication_jobs
				SET status = 'CANCELLED', skip_reason = 'No remaining balance due', updated_at = NOW()
				WHERE property_id = $1 AND rule_id = $2 AND reservation_id = $3 AND status = 'SCHEDULED'
			`, prop.ID, rule.ID, res.ID); err != nil {
				return MapErr(err)
			}
			continue
		}
		// Never duplicate Phase 2's authoritative booking_confirmation outbox item!
		if rule.TriggerEvent == domain.TriggerBookingConfirmation {
			var alreadyExists bool
			if err := tx.QueryRow(ctx, `
				SELECT EXISTS (SELECT 1 FROM email_outbox WHERE reservation_id = $1 AND template = 'booking_confirmation')
			`, res.ID).Scan(&alreadyExists); err != nil {
				return MapErr(err)
			}
			if alreadyExists {
				continue
			}
		}

		// Check whether an identical active job already exists for this stay fingerprint.
		var sameActive bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM communication_jobs
				WHERE rule_id = $1 AND reservation_id = $2 AND stay_fingerprint = $3 AND status IN ('SCHEDULED', 'QUEUED')
			)
		`, rule.ID, res.ID, fp).Scan(&sameActive); err != nil {
			return MapErr(err)
		}
		if sameActive {
			continue
		}

		// Supersede any stale SCHEDULED job for previous stay dates/version.
		if _, err := tx.Exec(ctx, `
			UPDATE communication_jobs
			SET status = 'SUPERSEDED', skip_reason = 'Reservation updated', updated_at = NOW()
			WHERE property_id = $1 AND rule_id = $2 AND reservation_id = $3 AND status = 'SCHEDULED'
		`, prop.ID, rule.ID, res.ID); err != nil {
			return MapErr(err)
		}

		// Skip past trigger windows (more than 12 hours overdue on non-immediate triggers, unless never sent)
		scheduledUTC := domain.ComputeRuleTriggerUTC(rule, prop, res, nowUTC)
		if rule.TriggerEvent != domain.TriggerBookingConfirmation && scheduledUTC.Before(nowUTC.Add(-24*time.Hour)) {
			continue
		}

		var queuedBefore bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM communication_jobs WHERE rule_id = $1 AND reservation_id = $2 AND status = 'QUEUED')
		`, rule.ID, res.ID).Scan(&queuedBefore); err != nil {
			return MapErr(err)
		}
		if queuedBefore {
			// Do not re-send non-immediate stay triggers twice simply because notes were edited.
			continue
		}

		jobID, err := NewUUID()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO communication_jobs (
				id, property_id, rule_id, reservation_id, guest_id, trigger_event, scheduled_for_utc, stay_fingerprint, status
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'SCHEDULED')
			ON CONFLICT (rule_id, reservation_id, stay_fingerprint) DO NOTHING
		`, jobID, prop.ID, rule.ID, res.ID, res.GuestID, string(rule.TriggerEvent), scheduledUTC, fp); err != nil {
			return MapErr(err)
		}
	}
	return nil
}

func (s *Store) ListCommunicationJobs(ctx context.Context, db DBTX, propertyID uuid.UUID, limit int) ([]domain.CommunicationJob, error) {
	if db == nil {
		db = s.pool
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := db.Query(ctx, `
		SELECT j.id, j.property_id, j.rule_id, COALESCE(r.name, ''), j.reservation_id, COALESCE(res.reference, ''),
		       j.guest_id, COALESCE(g.full_name, ''), j.trigger_event, j.scheduled_for_utc, j.stay_fingerprint,
		       j.status, j.communication_id, j.email_outbox_id, j.skip_reason, j.processed_at, j.created_at, j.updated_at
		FROM communication_jobs j
		LEFT JOIN communication_automation_rules r ON r.id = j.rule_id
		LEFT JOIN reservations res ON res.id = j.reservation_id
		LEFT JOIN guests g ON g.id = j.guest_id
		WHERE j.property_id = $1
		ORDER BY j.scheduled_for_utc DESC
		LIMIT $2
	`, propertyID, limit)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()
	var out []domain.CommunicationJob
	for rows.Next() {
		var j domain.CommunicationJob
		if err := rows.Scan(&j.ID, &j.PropertyID, &j.RuleID, &j.RuleName, &j.ReservationID, &j.ReservationRef, &j.GuestID, &j.GuestName, &j.TriggerEvent, &j.ScheduledForUTC, &j.StayFingerprint, &j.Status, &j.CommunicationID, &j.EmailOutboxID, &j.SkipReason, &j.ProcessedAt, &j.CreatedAt, &j.UpdatedAt); err != nil {
			return nil, MapErr(err)
		}
		out = append(out, j)
	}
	return out, MapErr(rows.Err())
}

// AcquireDueCommunicationJobs claims due automated jobs atomically and materializes
// guest_communications + email_outbox entries with template version snapshots.
func (s *Store) AcquireDueCommunicationJobs(ctx context.Context, nowUTC time.Time, limit int) (int, error) {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	queuedCount := 0
	err := s.WithTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, property_id, rule_id, reservation_id, guest_id, trigger_event
			FROM communication_jobs
			WHERE status = 'SCHEDULED' AND scheduled_for_utc <= $1
			ORDER BY scheduled_for_utc ASC
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		`, nowUTC, limit)
		if err != nil {
			return MapErr(err)
		}
		type jobClaim struct {
			ID, PropertyID, RuleID, ReservationID uuid.UUID
			GuestID                               *uuid.UUID
			TriggerEvent                          string
		}
		var claims []jobClaim
		for rows.Next() {
			var c jobClaim
			if err := rows.Scan(&c.ID, &c.PropertyID, &c.RuleID, &c.ReservationID, &c.GuestID, &c.TriggerEvent); err != nil {
				rows.Close()
				return MapErr(err)
			}
			claims = append(claims, c)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return MapErr(err)
		}
		rows.Close()

		for _, cl := range claims {
			prop, pErr := s.GetPropertyByID(ctx, tx, cl.PropertyID)
			if pErr != nil {
				_, _ = tx.Exec(ctx, `UPDATE communication_jobs SET status='SKIPPED', skip_reason='Property missing', processed_at=NOW() WHERE id=$1`, cl.ID)
				continue
			}
			res, rErr := s.GetReservation(ctx, tx, cl.PropertyID, cl.ReservationID)
			if rErr != nil {
				_, _ = tx.Exec(ctx, `UPDATE communication_jobs SET status='CANCELLED', skip_reason='Reservation missing', processed_at=NOW() WHERE id=$1`, cl.ID)
				continue
			}
			// Lock canonical guest cluster BEFORE status checks and queueing so concurrent
			// anonymisation cannot race past automated communication creation.
			canonGuestID, _, anonOrPending, lockErr := s.LockCanonicalGuestClusterTx(ctx, tx, cl.PropertyID, res.GuestID)
			if lockErr != nil || anonOrPending {
				_, _ = tx.Exec(ctx, `UPDATE communication_jobs SET status='CANCELLED', skip_reason='Guest anonymised or erasure pending', processed_at=NOW() WHERE id=$1`, cl.ID)
				continue
			}
			var tplID uuid.UUID
			var ruleActive bool
			var statuses []string
			if err := tx.QueryRow(ctx, `SELECT template_id, is_active, applicable_statuses FROM communication_automation_rules WHERE id = $1`, cl.RuleID).Scan(&tplID, &ruleActive, &statuses); err != nil {
				_, _ = tx.Exec(ctx, `UPDATE communication_jobs SET status='SKIPPED', skip_reason='Rule missing', processed_at=NOW() WHERE id=$1`, cl.ID)
				continue
			}
			if !ruleActive || !statusInList(string(res.Status), statuses) {
				_, _ = tx.Exec(ctx, `UPDATE communication_jobs SET status='CANCELLED', skip_reason='Reservation status ineligible', processed_at=NOW() WHERE id=$1`, cl.ID)
				continue
			}
			guest, gErr := s.GetGuest(ctx, tx, cl.PropertyID, canonGuestID)
			if gErr != nil || guest.Status == domain.GuestStatusAnonymized || guest.AnonymizedAt != nil || guest.Status == domain.GuestStatusDoNotContact {
				_, _ = tx.Exec(ctx, `UPDATE communication_jobs SET status='SKIPPED', skip_reason='Guest status restricts automated contact', processed_at=NOW() WHERE id=$1`, cl.ID)
				continue
			}
			recipient, recErr := domain.ValidateEmailRecipient(res.GuestEmail)
			if recErr != nil {
				recipient, recErr = domain.ValidateEmailRecipient(guest.Email)
			}
			if recErr != nil {
				_, _ = tx.Exec(ctx, `UPDATE communication_jobs SET status='SKIPPED', skip_reason='No valid recipient email address', processed_at=NOW() WHERE id=$1`, cl.ID)
				continue
			}
			tplVer, tvErr := s.GetLatestTemplateVersion(ctx, tx, cl.PropertyID, tplID)
			if tvErr != nil {
				_, _ = tx.Exec(ctx, `UPDATE communication_jobs SET status='SKIPPED', skip_reason='Template version missing', processed_at=NOW() WHERE id=$1`, cl.ID)
				continue
			}
			var unitNames []string
			for _, u := range res.Units {
				unitNames = append(unitNames, u.UnitName)
			}
			vars := domain.BuildReservationTemplateVars(prop, guest, &res, unitNames, nil)
			subj, txt, htm, rErr := domain.RenderTemplate(tplVer.SubjectTemplate, tplVer.BodyTextTemplate, tplVer.BodyHTMLTemplate, vars)
			if rErr != nil {
				_, _ = tx.Exec(ctx, `UPDATE communication_jobs SET status='SKIPPED', skip_reason=$2, processed_at=NOW() WHERE id=$1`, cl.ID, rErr.Error())
				continue
			}

			commID, err := NewUUID()
			if err != nil {
				return err
			}
			outboxID, err := NewUUID()
			if err != nil {
				return err
			}
			_, err = s.CreateGuestCommunication(ctx, tx, domain.GuestCommunication{
				ID:                commID,
				PropertyID:        prop.ID,
				GuestID:           &guest.ID,
				ReservationID:     &res.ID,
				InteractionType:   domain.InteractionBookingNotice,
				Direction:         domain.CommDirOutbound,
				Subject:           subj,
				BodyText:          txt,
				BodyHTML:          htm,
				Summary:           fmt.Sprintf("Automated %s email queued", cl.TriggerEvent),
				RecipientEmail:    recipient,
				SenderEmail:       prop.ContactEmail,
				TemplateID:        &tplID,
				TemplateVersionID: &tplVer.ID,
				RuleID:            &cl.RuleID,
				TriggerEvent:      cl.TriggerEvent,
				DeliveryStatus:    domain.DeliveryQueued,
				OccurredAt:        nowUTC,
			})
			if err != nil {
				return err
			}
			if _, err := s.QueueEmail(ctx, tx, domain.EmailOutboxItem{
				ID:              outboxID,
				PropertyID:      prop.ID,
				ReservationID:   &res.ID,
				GuestID:         &guest.ID,
				CommunicationID: &commID,
				RecipientEmail:  recipient,
				Template:        "auto_" + strings.ToLower(cl.TriggerEvent),
				Subject:         subj,
				BodyText:        txt,
				BodyHTML:        htm,
				NextAttemptAt:   nowUTC,
			}); err != nil {
				return err
			}
			if err := s.LinkCommunicationOutbox(ctx, tx, commID, outboxID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `
				UPDATE communication_jobs
				SET status = 'QUEUED', communication_id = $2, email_outbox_id = $3, processed_at = NOW(), updated_at = NOW()
				WHERE id = $1
			`, cl.ID, commID, outboxID); err != nil {
				return MapErr(err)
			}
			queuedCount++
		}
		return nil
	})
	return queuedCount, err
}

// --- Marketing Consent, Data Retention & Anonymisation ---

func (s *Store) ListGuestConsentRecords(ctx context.Context, db DBTX, propertyID, guestID uuid.UUID) ([]domain.GuestConsentRecord, error) {
	if db == nil {
		db = s.pool
	}
	rows, err := db.Query(ctx, `
		SELECT id, property_id, guest_id, consent_type, status, method, source, policy_version,
		       notes, recorded_by, occurred_at, created_at, is_note_redacted, note_redacted_at
		FROM guest_consent_records_effective
		WHERE property_id = $1 AND guest_id = $2
		ORDER BY occurred_at DESC, created_at DESC
	`, propertyID, guestID)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()
	var out []domain.GuestConsentRecord
	for rows.Next() {
		var r domain.GuestConsentRecord
		var st string
		if err := rows.Scan(
			&r.ID, &r.PropertyID, &r.GuestID, &r.ConsentType, &st, &r.Method, &r.Source, &r.PolicyVersion,
			&r.Notes, &r.RecordedBy, &r.OccurredAt, &r.CreatedAt, &r.IsNoteRedacted, &r.NoteRedactedAt,
		); err != nil {
			return nil, MapErr(err)
		}
		r.Status = domain.ConsentStatus(st)
		out = append(out, r)
	}
	return out, MapErr(rows.Err())
}

func (s *Store) RecordGuestConsent(ctx context.Context, tx pgx.Tx, r domain.GuestConsentRecord) (domain.GuestConsentRecord, error) {
	if r.ID == uuid.Nil {
		id, err := NewUUID()
		if err != nil {
			return domain.GuestConsentRecord{}, err
		}
		r.ID = id
	}
	if r.ConsentType == "" {
		r.ConsentType = "MARKETING_EMAIL"
	}
	if r.PolicyVersion == "" {
		r.PolicyVersion = "v1"
	}
	if r.OccurredAt.IsZero() {
		r.OccurredAt = time.Now().UTC()
	}
	var out domain.GuestConsentRecord
	var st string
	err := tx.QueryRow(ctx, `
		INSERT INTO guest_consent_records (
			id, property_id, guest_id, consent_type, status, method, source, policy_version, notes, recorded_by, occurred_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING id, property_id, guest_id, consent_type, status, method, source, policy_version, notes, recorded_by, occurred_at, created_at
	`, r.ID, r.PropertyID, r.GuestID, r.ConsentType, string(r.Status), strings.TrimSpace(r.Method), strings.TrimSpace(r.Source), strings.TrimSpace(r.PolicyVersion), strings.TrimSpace(r.Notes), r.RecordedBy, r.OccurredAt).Scan(
		&out.ID, &out.PropertyID, &out.GuestID, &out.ConsentType, &st, &out.Method, &out.Source, &out.PolicyVersion, &out.Notes, &out.RecordedBy, &out.OccurredAt, &out.CreatedAt,
	)
	if err != nil {
		return domain.GuestConsentRecord{}, MapErr(err)
	}
	out.Status = domain.ConsentStatus(st)
	return out, nil
}

func (s *Store) GetPropertyRetentionPolicy(ctx context.Context, db DBTX, propertyID uuid.UUID) (domain.PropertyRetentionPolicy, error) {
	if db == nil {
		db = s.pool
	}
	var p domain.PropertyRetentionPolicy
	err := db.QueryRow(ctx, `
		SELECT property_id, enquiry_retention_days, inactive_guest_retention_days, comms_retention_days, auto_anonymize_enabled, updated_by, updated_at
		FROM property_retention_policies
		WHERE property_id = $1
	`, propertyID).Scan(&p.PropertyID, &p.EnquiryRetentionDays, &p.InactiveGuestRetentionDays, &p.CommsRetentionDays, &p.AutoAnonymizeEnabled, &p.UpdatedBy, &p.UpdatedAt)
	if err == pgx.ErrNoRows {
		return domain.PropertyRetentionPolicy{
			PropertyID:                 propertyID,
			EnquiryRetentionDays:       1095,
			InactiveGuestRetentionDays: 2555,
			CommsRetentionDays:         1095,
			AutoAnonymizeEnabled:       false,
			ExecutionMode:              "ADVISORY_ONLY",
		}, nil
	}
	if err != nil {
		return domain.PropertyRetentionPolicy{}, MapErr(err)
	}
	p.ExecutionMode = "ADVISORY_ONLY"
	return p, nil
}

func (s *Store) UpsertPropertyRetentionPolicy(ctx context.Context, tx pgx.Tx, p domain.PropertyRetentionPolicy) (domain.PropertyRetentionPolicy, error) {
	// Automated background anonymisation worker is deferred to a future reviewed phase;
	// retention policy persists advisory windows with auto_anonymize_enabled disabled.
	p.AutoAnonymizeEnabled = false
	var out domain.PropertyRetentionPolicy
	err := tx.QueryRow(ctx, `
		INSERT INTO property_retention_policies (
			property_id, enquiry_retention_days, inactive_guest_retention_days, comms_retention_days, auto_anonymize_enabled, updated_by
		) VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (property_id) DO UPDATE SET
			enquiry_retention_days = EXCLUDED.enquiry_retention_days,
			inactive_guest_retention_days = EXCLUDED.inactive_guest_retention_days,
			comms_retention_days = EXCLUDED.comms_retention_days,
			auto_anonymize_enabled = EXCLUDED.auto_anonymize_enabled,
			updated_by = EXCLUDED.updated_by,
			updated_at = NOW()
		RETURNING property_id, enquiry_retention_days, inactive_guest_retention_days, comms_retention_days, auto_anonymize_enabled, updated_by, updated_at
	`, p.PropertyID, p.EnquiryRetentionDays, p.InactiveGuestRetentionDays, p.CommsRetentionDays, p.AutoAnonymizeEnabled, p.UpdatedBy).Scan(
		&out.PropertyID, &out.EnquiryRetentionDays, &out.InactiveGuestRetentionDays, &out.CommsRetentionDays, &out.AutoAnonymizeEnabled, &out.UpdatedBy, &out.UpdatedAt,
	)
	if err != nil {
		return domain.PropertyRetentionPolicy{}, MapErr(err)
	}
	out.ExecutionMode = "ADVISORY_ONLY"
	return out, nil
}

// AnonymizeGuest scrubs personal PII from the guest CRM record and associated correspondence
// while leaving reservation financial figures, tax ledgers, and payment history completely intact.
func (s *Store) AnonymizeGuest(ctx context.Context, tx pgx.Tx, propertyID, guestID, actorID uuid.UUID) (domain.Guest, error) {
	return s.AnonymizeGuestAt(ctx, tx, propertyID, guestID, actorID, time.Now().UTC())
}

func (s *Store) AnonymizeGuestAt(ctx context.Context, tx pgx.Tx, propertyID, guestID, actorID uuid.UUID, nowUTC time.Time) (domain.Guest, error) {
	out, pending, err := s.PrepareOrAnonymizeGuestAt(ctx, tx, propertyID, guestID, actorID, nowUTC)
	if err != nil {
		return domain.Guest{}, err
	}
	if pending > 0 {
		return domain.Guest{}, fmt.Errorf("%w: anonymisation pending: %d email dispatch(es) active in transport or awaiting reconciliation", domain.ErrConflict, pending)
	}
	return out, nil
}

// PrepareOrAnonymizeGuestAt locks the canonical guest cluster in sorted UUID order BEFORE inspecting
// email_outbox so it never deadlocks with mail workers. If any linked outbox item is actively
// DISPATCHING or in DISPATCH_UNKNOWN, it durably records guests.erasure_requested_at, cancels only
// pre-transport items, and returns pendingDispatches > 0 WITHOUT setting ANONYMIZED so the caller
// can commit the pending barrier and return an explicit conflict error for safe retry/reconciliation.
func (s *Store) PrepareOrAnonymizeGuestAt(ctx context.Context, tx pgx.Tx, propertyID, guestID, actorID uuid.UUID, nowUTC time.Time) (domain.Guest, int, error) {
	canonicalID, clusterIDs, _, err := s.LockCanonicalGuestClusterTx(ctx, tx, propertyID, guestID)
	if err != nil {
		return domain.Guest{}, 0, err
	}

	g, err := s.GetGuest(ctx, tx, propertyID, canonicalID)
	if err != nil {
		return domain.Guest{}, 0, err
	}
	if g.Status == domain.GuestStatusAnonymized {
		return g, 0, nil
	}

	var hasUpcoming bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM reservations
			WHERE property_id = $1 AND guest_id = ANY($2) AND status IN ('PENDING', 'CONFIRMED', 'CHECKED_IN')
		)
	`, propertyID, clusterIDs).Scan(&hasUpcoming); err != nil {
		return domain.Guest{}, 0, MapErr(err)
	}
	if hasUpcoming {
		return domain.Guest{}, 0, fmt.Errorf("%w: cannot anonymize a guest with active or upcoming reservations", domain.ErrConflict)
	}

	// Lock all email_outbox rows linked to the guest cluster, their reservations, their enquiries,
	// or their communications AFTER acquiring guest cluster locks (canonical lock hierarchy).
	oRows, err := tx.Query(ctx, `
		SELECT o.id, o.communication_id, o.status, o.locked_at
		FROM email_outbox o
		WHERE o.property_id = $1
		  AND (
			o.guest_id = ANY($2)
			OR o.reservation_id IN (SELECT id FROM reservations WHERE property_id = $1 AND guest_id = ANY($2))
			OR o.communication_id IN (
				SELECT id FROM guest_communications
				WHERE property_id = $1
				  AND (
					guest_id = ANY($2)
					OR reservation_id IN (SELECT id FROM reservations WHERE property_id = $1 AND guest_id = ANY($2))
					OR enquiry_id IN (
						SELECT id FROM guest_enquiries
						WHERE property_id = $1
						  AND (guest_id = ANY($2) OR reservation_id IN (SELECT id FROM reservations WHERE property_id = $1 AND guest_id = ANY($2)))
					)
				  )
			)
		  )
		ORDER BY o.id ASC
		FOR UPDATE OF o
	`, propertyID, clusterIDs)
	if err != nil {
		return domain.Guest{}, 0, MapErr(err)
	}
	type lockedOutbox struct {
		id       uuid.UUID
		commID   *uuid.UUID
		status   string
		lockedAt *time.Time
	}
	var outboxes []lockedOutbox
	pendingDispatches := 0
	for oRows.Next() {
		var lo lockedOutbox
		if sErr := oRows.Scan(&lo.id, &lo.commID, &lo.status, &lo.lockedAt); sErr != nil {
			oRows.Close()
			return domain.Guest{}, 0, MapErr(sErr)
		}
		if lo.status == domain.OutboxStatusDispatching || lo.status == domain.OutboxStatusDispatchUnknown {
			pendingDispatches++
		}
		outboxes = append(outboxes, lo)
	}
	oRows.Close()
	if err := oRows.Err(); err != nil {
		return domain.Guest{}, 0, MapErr(err)
	}

	staleCutoff := nowUTC.Add(-5 * time.Minute)
	if pendingDispatches > 0 {
		// Record durable erasure_requested_at across the cluster to prevent any NEW sends or dispatches,
		// cancel any pre-transport outbox items immediately, and leave active DISPATCHING/DISPATCH_UNKNOWN
		// intact so anonymisation does not falsely complete until resolved/reconciled.
		if _, err := tx.Exec(ctx, `
			UPDATE guests
			SET erasure_requested_at = COALESCE(erasure_requested_at, $3),
			    updated_by = $4,
			    updated_at = NOW()
			WHERE property_id = $1 AND id = ANY($2)
		`, propertyID, clusterIDs, nowUTC, actorID); err != nil {
			return domain.Guest{}, 0, MapErr(err)
		}
		cRows, cErr := tx.Query(ctx, `
			UPDATE email_outbox
			SET recipient_email = '[redacted]',
			    subject = '[Anonymised Email]',
			    body_text = '[redacted by GDPR erasure]',
			    body_html = '',
			    last_error = CASE WHEN TRIM(last_error) = '' THEN '' ELSE '[redacted on anonymisation]' END,
			    status = CASE
			        WHEN status IN ('PENDING', 'FAILED') THEN 'CANCELLED'
			        WHEN status = 'PROCESSING' AND (locked_at IS NULL OR locked_at < $3) THEN 'CANCELLED'
			        WHEN status = 'PROCESSING' THEN 'CANCEL_REQUESTED'
			        ELSE status
			    END,
			    cancel_reason = CASE
			        WHEN status IN ('PENDING', 'FAILED') OR (status = 'PROCESSING' AND (locked_at IS NULL OR locked_at < $3))
			            THEN 'Cancelled due to pending guest GDPR anonymisation'
			        WHEN status = 'PROCESSING'
			            THEN 'Cancellation requested during pending guest GDPR anonymisation'
			        ELSE cancel_reason
			    END,
			    cancelled_at = CASE
			        WHEN status IN ('PENDING', 'FAILED', 'PROCESSING') THEN COALESCE(cancelled_at, $4)
			        ELSE cancelled_at
			    END,
			    locked_at = CASE
			        WHEN status IN ('PENDING', 'FAILED') OR (status = 'PROCESSING' AND (locked_at IS NULL OR locked_at < $3)) THEN NULL
			        ELSE locked_at
			    END,
			    worker_token = CASE
			        WHEN status IN ('PENDING', 'FAILED') OR (status = 'PROCESSING' AND (locked_at IS NULL OR locked_at < $3)) THEN NULL
			        ELSE worker_token
			    END,
			    updated_at = NOW()
			WHERE property_id = $1
			  AND id = ANY($2)
			  AND status NOT IN ('DISPATCHING', 'DISPATCH_UNKNOWN')
			RETURNING id, communication_id, status
		`, propertyID, func() []uuid.UUID {
			ids := make([]uuid.UUID, 0, len(outboxes))
			for _, o := range outboxes {
				ids = append(ids, o.id)
			}
			return ids
		}(), staleCutoff, nowUTC)
		if cErr != nil {
			return domain.Guest{}, 0, MapErr(cErr)
		}
		type cancelledItem struct {
			id     uuid.UUID
			commID *uuid.UUID
			status string
		}
		var cancelledItems []cancelledItem
		for cRows.Next() {
			var ci cancelledItem
			if sErr := cRows.Scan(&ci.id, &ci.commID, &ci.status); sErr != nil {
				cRows.Close()
				return domain.Guest{}, 0, MapErr(sErr)
			}
			cancelledItems = append(cancelledItems, ci)
		}
		cRows.Close()
		if err := cRows.Err(); err != nil {
			return domain.Guest{}, 0, MapErr(err)
		}
		for _, ci := range cancelledItems {
			if ci.commID != nil && ci.status == domain.OutboxStatusCancelled {
				if err := s.recordCancelledCommunicationTx(ctx, tx, propertyID, ci.id, *ci.commID, "Cancelled due to pending guest GDPR anonymisation", nowUTC); err != nil {
					return domain.Guest{}, 0, err
				}
			}
		}
		if err := s.purgeGuestPortalAndConfirmationAccessTx(ctx, tx, propertyID, clusterIDs, false, nowUTC); err != nil {
			return domain.Guest{}, 0, err
		}
		curGuest, gErr := s.GetGuest(ctx, tx, propertyID, canonicalID)
		if gErr != nil {
			return domain.Guest{}, 0, gErr
		}
		return curGuest, pendingDispatches, nil
	}

	anonLabel := fmt.Sprintf("Anonymised Guest (%s)", g.ID.String()[:8])
	if err := s.purgeGuestPortalAndConfirmationAccessTx(ctx, tx, propertyID, clusterIDs, true, nowUTC); err != nil {
		return domain.Guest{}, 0, err
	}

	// 1. Scrub canonical guest AND all merged alias rows (merged aliases keep status='MERGED' + link for audit provenance, with anonymized_at set and all PII scrubbed).
	if _, err := tx.Exec(ctx, `
		UPDATE guests
		SET first_name = 'Anonymised',
		    last_name = 'Guest',
		    full_name = CASE WHEN id = $2 THEN $4 ELSE 'Anonymised Guest (' || left(id::text, 8) || ')' END,
		    email = '',
		    normalized_email = '',
		    phone = '',
		    normalized_phone = '',
		    address_json = '{}'::jsonb,
		    preferences = '{}'::jsonb,
		    notes = '',
		    status = CASE WHEN id = $2 THEN 'ANONYMIZED' ELSE status END,
		    erasure_requested_at = COALESCE(erasure_requested_at, $6),
		    anonymized_at = COALESCE(anonymized_at, $6),
		    updated_by = $5,
		    version = version + 1,
		    updated_at = NOW()
		WHERE property_id = $1 AND id = ANY($3)
	`, propertyID, canonicalID, clusterIDs, anonLabel, actorID, nowUTC); err != nil {
		return domain.Guest{}, 0, MapErr(err)
	}

	// 2. Scrub reservations PII while preserving financial ledgers, stay dates, status, and unit history.
	if _, err := tx.Exec(ctx, `
		UPDATE reservations
		SET guest_name = $3,
		    guest_email = '',
		    guest_phone = '',
		    occupant_details = '',
		    estimated_arrival_time = '',
		    guest_arrival_notes = '',
		    internal_notes = CASE WHEN internal_notes <> '' THEN '[Anonymised]' ELSE '' END,
		    cancellation_reason = CASE WHEN cancellation_reason <> '' THEN '[Anonymised]' ELSE '' END,
		    cancellation_request_note = CASE WHEN cancellation_request_note <> '' THEN '[Anonymised]' ELSE '' END,
		    updated_by = $4,
		    updated_at = NOW(),
		    version = version + 1
		WHERE property_id = $1 AND guest_id = ANY($2)
	`, propertyID, clusterIDs, anonLabel, actorID); err != nil {
		return domain.Guest{}, 0, MapErr(err)
	}

	// Scrub any booking_holds associated with this guest cluster directly (guest_id) or via reservation_id across all statuses.
	if _, err := tx.Exec(ctx, `
		UPDATE booking_holds
		SET guest_name = $3,
		    guest_email = '',
		    guest_phone = '',
		    special_requests = '',
		    customer_session_hash = '',
		    updated_at = NOW()
		WHERE property_id = $1
		  AND (
			guest_id = ANY($2)
			OR confirmed_reservation_id IN (
				SELECT id FROM reservations WHERE property_id = $1 AND guest_id = ANY($2)
			)
		  )
	`, propertyID, clusterIDs, anonLabel); err != nil {
		return domain.Guest{}, 0, MapErr(err)
	}

	// 3. Delete preferences & internal CRM notes.
	if _, err := tx.Exec(ctx, `
		DELETE FROM guest_preferences WHERE property_id = $1 AND guest_id = ANY($2)
	`, propertyID, clusterIDs); err != nil {
		return domain.Guest{}, 0, MapErr(err)
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM guest_notes WHERE property_id = $1 AND guest_id = ANY($2)
	`, propertyID, clusterIDs); err != nil {
		return domain.Guest{}, 0, MapErr(err)
	}

	// 4. Scrub merge audit snapshots and duplicate dismissal free-text reasons referencing this guest cluster.
	if _, err := tx.Exec(ctx, `
		UPDATE guest_merge_events
		SET source_snapshot = jsonb_build_object('anonymized', true),
		    notes = CASE WHEN notes <> '' THEN '[Anonymised]' ELSE '' END
		WHERE property_id = $1 AND (source_guest_id = ANY($2) OR target_guest_id = ANY($2))
	`, propertyID, clusterIDs); err != nil {
		return domain.Guest{}, 0, MapErr(err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE guest_duplicate_dismissals
		SET reason = CASE WHEN reason <> '' THEN '[Redacted on GDPR anonymisation]' ELSE '' END,
		    reason_category = 'ANONYMISED'
		WHERE property_id = $1 AND (guest_a_id = ANY($2) OR guest_b_id = ANY($2))
	`, propertyID, clusterIDs); err != nil {
		return domain.Guest{}, 0, MapErr(err)
	}

	// 5. Scrub guest enquiries & all nested enquiry messages.
	if _, err := tx.Exec(ctx, `
		UPDATE guest_enquiry_messages m
		SET sender_name = CASE WHEN m.staff_user_id IS NOT NULL THEN m.sender_name ELSE $3 END,
		    sender_email = '',
		    body = '[Personal data removed under GDPR erasure]'
		FROM guest_enquiries e
		WHERE e.property_id = m.property_id
		  AND e.id = m.enquiry_id
		  AND e.property_id = $1
		  AND (
			e.guest_id = ANY($2)
			OR e.reservation_id IN (SELECT id FROM reservations WHERE property_id = $1 AND guest_id = ANY($2))
		  )
	`, propertyID, clusterIDs, anonLabel); err != nil {
		return domain.Guest{}, 0, MapErr(err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE guest_enquiries
		SET subject = '[Anonymised Enquiry]',
		    description = '',
		    resolution_notes = CASE WHEN resolution_notes <> '' THEN '[Anonymised]' ELSE '' END,
		    requester_name = $3,
		    requester_email = '',
		    requester_phone = '',
		    updated_by = $4,
		    updated_at = NOW(),
		    version = version + 1
		WHERE property_id = $1
		  AND (
			guest_id = ANY($2)
			OR reservation_id IN (SELECT id FROM reservations WHERE property_id = $1 AND guest_id = ANY($2))
		  )
	`, propertyID, clusterIDs, anonLabel, actorID); err != nil {
		return domain.Guest{}, 0, MapErr(err)
	}

	// 6. Scrub communication delivery events & guest communications.
	if _, err := tx.Exec(ctx, `
		UPDATE communication_delivery_events d
		SET detail = '[Anonymised]',
		    payload = jsonb_build_object('anonymized', true)
		FROM guest_communications c
		WHERE c.property_id = d.property_id
		  AND c.id = d.communication_id
		  AND c.property_id = $1
		  AND (
			c.guest_id = ANY($2)
			OR c.reservation_id IN (SELECT id FROM reservations WHERE property_id = $1 AND guest_id = ANY($2))
			OR c.enquiry_id IN (
				SELECT id FROM guest_enquiries
				WHERE property_id = $1
				  AND (guest_id = ANY($2) OR reservation_id IN (SELECT id FROM reservations WHERE property_id = $1 AND guest_id = ANY($2)))
			)
		  )
	`, propertyID, clusterIDs); err != nil {
		return domain.Guest{}, 0, MapErr(err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE guest_communications
		SET subject = '[Anonymised Communication]',
		    recipient_email = CASE WHEN recipient_email <> '' THEN '[redacted]' ELSE '' END,
		    sender_email = CASE WHEN direction = 'INBOUND' AND sender_email <> '' THEN '[redacted]' ELSE sender_email END,
		    body_text = '[redacted by GDPR erasure]',
		    body_html = '',
		    summary = 'Communication redacted on guest anonymisation',
		    follow_up_required = FALSE,
		    updated_at = NOW()
		WHERE property_id = $1
		  AND (
			guest_id = ANY($2)
			OR reservation_id IN (SELECT id FROM reservations WHERE property_id = $1 AND guest_id = ANY($2))
			OR enquiry_id IN (
				SELECT id FROM guest_enquiries
				WHERE property_id = $1
				  AND (guest_id = ANY($2) OR reservation_id IN (SELECT id FROM reservations WHERE property_id = $1 AND guest_id = ANY($2)))
			)
		  )
	`, propertyID, clusterIDs); err != nil {
		return domain.Guest{}, 0, MapErr(err)
	}

	// 7. Scrub email_outbox content AND last_error across all linked outbox rows (including FAILED/SENT/CANCELLED).
	outboxIDs := make([]uuid.UUID, 0, len(outboxes))
	for _, o := range outboxes {
		outboxIDs = append(outboxIDs, o.id)
	}
	if len(outboxIDs) > 0 {
		cRows, err := tx.Query(ctx, `
			UPDATE email_outbox
			SET recipient_email = '[redacted]',
			    subject = '[Anonymised Email]',
			    body_text = '[redacted by GDPR erasure]',
			    body_html = '',
			    last_error = CASE
			        WHEN TRIM(last_error) = '' THEN ''
			        WHEN status = 'SENT' THEN 'Delivered to transport before GDPR erasure finalised (details redacted)'
			        ELSE '[redacted on anonymisation]'
			    END,
			    status = CASE
			        WHEN status IN ('PENDING', 'FAILED') THEN 'CANCELLED'
			        WHEN status = 'PROCESSING' AND (locked_at IS NULL OR locked_at < $3) THEN 'CANCELLED'
			        WHEN status = 'PROCESSING' THEN 'CANCEL_REQUESTED'
			        ELSE status
			    END,
			    cancel_reason = CASE
			        WHEN status IN ('PENDING', 'FAILED') OR (status = 'PROCESSING' AND (locked_at IS NULL OR locked_at < $3))
			            THEN 'Cancelled due to guest GDPR anonymisation'
			        WHEN status = 'PROCESSING'
			            THEN 'Cancellation requested during concurrent guest GDPR anonymisation'
			        ELSE cancel_reason
			    END,
			    cancelled_at = CASE
			        WHEN status IN ('PENDING', 'FAILED', 'PROCESSING') THEN COALESCE(cancelled_at, $4)
			        ELSE cancelled_at
			    END,
			    locked_at = CASE
			        WHEN status IN ('PENDING', 'FAILED') OR (status = 'PROCESSING' AND (locked_at IS NULL OR locked_at < $3)) THEN NULL
			        ELSE locked_at
			    END,
			    worker_token = CASE
			        WHEN status IN ('PENDING', 'FAILED') OR (status = 'PROCESSING' AND (locked_at IS NULL OR locked_at < $3)) THEN NULL
			        ELSE worker_token
			    END,
			    updated_at = NOW()
			WHERE property_id = $1 AND id = ANY($2)
			RETURNING id, communication_id, status
		`, propertyID, outboxIDs, staleCutoff, nowUTC)
		if err != nil {
			return domain.Guest{}, 0, MapErr(err)
		}
		type anonOutbox struct {
			id     uuid.UUID
			commID *uuid.UUID
			status string
		}
		var anonOutboxes []anonOutbox
		for cRows.Next() {
			var ao anonOutbox
			if sErr := cRows.Scan(&ao.id, &ao.commID, &ao.status); sErr != nil {
				cRows.Close()
				return domain.Guest{}, 0, MapErr(sErr)
			}
			anonOutboxes = append(anonOutboxes, ao)
		}
		cRows.Close()
		if err := cRows.Err(); err != nil {
			return domain.Guest{}, 0, MapErr(err)
		}
		for _, ao := range anonOutboxes {
			if ao.commID != nil && ao.status == domain.OutboxStatusCancelled {
				if err := s.recordCancelledCommunicationTx(ctx, tx, propertyID, ao.id, *ao.commID, "Cancelled due to guest GDPR anonymisation", nowUTC); err != nil {
					return domain.Guest{}, 0, err
				}
			}
		}
	}

	// 8. Cancel scheduled automation jobs for this guest cluster or their reservations.
	if _, err := tx.Exec(ctx, `
		UPDATE communication_jobs
		SET status = 'CANCELLED',
		    skip_reason = 'Cancelled due to guest GDPR anonymisation',
		    processed_at = NOW(),
		    updated_at = NOW()
		WHERE property_id = $1
		  AND status = 'SCHEDULED'
		  AND (
			guest_id = ANY($2)
			OR reservation_id IN (SELECT id FROM reservations WHERE property_id = $1 AND guest_id = ANY($2))
		  )
	`, propertyID, clusterIDs); err != nil {
		return domain.Guest{}, 0, MapErr(err)
	}

	// 9. Redact any historical consent record notes via append-only guest_consent_redactions overlay
	// without mutating or weakening append-only guest_consent_records triggers.
	consentRows, err := tx.Query(ctx, `
		SELECT id
		FROM guest_consent_records
		WHERE property_id = $1 AND guest_id = ANY($2) AND TRIM(notes) <> ''
	`, propertyID, clusterIDs)
	if err != nil {
		return domain.Guest{}, 0, MapErr(err)
	}
	var consentIDsToRedact []uuid.UUID
	for consentRows.Next() {
		var cid uuid.UUID
		if sErr := consentRows.Scan(&cid); sErr != nil {
			consentRows.Close()
			return domain.Guest{}, 0, MapErr(sErr)
		}
		consentIDsToRedact = append(consentIDsToRedact, cid)
	}
	consentRows.Close()
	if err := consentRows.Err(); err != nil {
		return domain.Guest{}, 0, MapErr(err)
	}
	for _, cid := range consentIDsToRedact {
		redID, rErr := NewUUID()
		if rErr != nil {
			return domain.Guest{}, 0, rErr
		}
		if _, rErr := tx.Exec(ctx, `
			SELECT public.redact_guest_consent_note($1, $2, '[Redacted on guest anonymisation]', 'Guest GDPR anonymisation', $3)
		`, redID, cid, actorID); rErr != nil {
			return domain.Guest{}, 0, MapErr(rErr)
		}
	}

	// 10. Append immutable GDPR consent withdrawal entry on canonical guest using non-PII note text.
	evID, err := NewUUID()
	if err != nil {
		return domain.Guest{}, 0, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO guest_consent_records (id, property_id, guest_id, consent_type, status, method, source, notes, recorded_by)
		VALUES ($1, $2, $3, 'MARKETING_EMAIL', 'WITHDRAWN', 'ANONYMISATION', 'GDPR_ERASURE', 'Consent withdrawn on erasure', $4)
	`, evID, propertyID, canonicalID, actorID); err != nil {
		return domain.Guest{}, 0, MapErr(err)
	}

	outGuest, err := s.GetGuest(ctx, tx, propertyID, canonicalID)
	return outGuest, 0, err
}

// --- Operations Centre CRM Summary & Attention Items ---

func (s *Store) GetOpsGuestCRMAlerts(ctx context.Context, propertyID uuid.UUID, selectedDate time.Time) (domain.OpsGuestCRMSummary, []domain.OpsAttentionItem, error) {
	var summary domain.OpsGuestCRMSummary
	summary.Visible = true
	selDateStr := domain.FormatDate(selectedDate)

	err := s.pool.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*) FROM guest_enquiries WHERE property_id = $1 AND status = 'NEW'),
			(SELECT COUNT(*) FROM guest_enquiries WHERE property_id = $1 AND status NOT IN ('RESOLVED', 'CLOSED') AND assigned_user_id IS NULL),
			(SELECT COUNT(*) FROM guest_enquiries WHERE property_id = $1 AND status NOT IN ('RESOLVED', 'CLOSED') AND follow_up_at IS NOT NULL AND follow_up_at <= NOW())
			+ (SELECT COUNT(*) FROM guest_communications WHERE property_id = $1 AND follow_up_required = TRUE AND follow_up_completed_at IS NULL AND (follow_up_at IS NULL OR follow_up_at <= NOW())),
			(SELECT COUNT(*) FROM guest_enquiries WHERE property_id = $1 AND category = 'COMPLAINT' AND status NOT IN ('RESOLVED', 'CLOSED')),
			(SELECT COUNT(DISTINCT r.id) FROM reservations r
			 JOIN guest_preferences gp ON gp.property_id = r.property_id AND gp.guest_id = r.guest_id
			 WHERE r.property_id = $1 AND r.status IN ('CONFIRMED', 'PENDING') AND lower(r.stay_range) >= $2::date AND lower(r.stay_range) <= ($2::date + 2)),
			(SELECT COUNT(*) FROM email_outbox WHERE property_id = $1 AND status = 'FAILED'),
			(SELECT COUNT(*) FROM communication_jobs WHERE property_id = $1 AND status = 'SCHEDULED' AND scheduled_for_utc <= NOW() + INTERVAL '48 hours')
	`, propertyID, selDateStr).Scan(
		&summary.NewEnquiriesCount,
		&summary.UnassignedEnquiriesCount,
		&summary.OverdueFollowUpsCount,
		&summary.UnresolvedComplaints,
		&summary.UpcomingArrivalRequests,
		&summary.FailedEmailsCount,
		&summary.ScheduledAutoEmailsCount,
	)
	if err != nil {
		return domain.OpsGuestCRMSummary{}, nil, MapErr(err)
	}

	var items []domain.OpsAttentionItem
	if summary.UnresolvedComplaints > 0 {
		items = append(items, domain.OpsAttentionItem{
			Severity:    domain.SeverityUrgent,
			Category:    "GUEST_COMPLAINT",
			Title:       fmt.Sprintf("%d unresolved guest complaint(s)", summary.UnresolvedComplaints),
			Detail:      "Active guest complaint requires review and resolution.",
			ActionLabel: "Review Complaints",
			ActionURL:   "/enquiries?category=COMPLAINT&open=1",
		})
	}
	if summary.OverdueFollowUpsCount > 0 {
		items = append(items, domain.OpsAttentionItem{
			Severity:    domain.SeverityHigh,
			Category:    "GUEST_FOLLOWUP",
			Title:       fmt.Sprintf("%d guest follow-up(s) due or overdue", summary.OverdueFollowUpsCount),
			Detail:      "Enquiries or phone callbacks awaiting staff action.",
			ActionLabel: "View Enquiries",
			ActionURL:   "/enquiries?overdue=1",
		})
	}
	if summary.NewEnquiriesCount > 0 {
		items = append(items, domain.OpsAttentionItem{
			Severity:    domain.SeverityMedium,
			Category:    "NEW_ENQUIRIES",
			Title:       fmt.Sprintf("%d new guest enquiry(ies) to triage", summary.NewEnquiriesCount),
			Detail:      "Unreviewed guest enquiries or special requests.",
			ActionLabel: "Triage Enquiries",
			ActionURL:   "/enquiries?status=NEW",
		})
	}
	if summary.FailedEmailsCount > 0 {
		items = append(items, domain.OpsAttentionItem{
			Severity:    domain.SeverityHigh,
			Category:    "FAILED_EMAILS",
			Title:       fmt.Sprintf("%d failed outbound email(s)", summary.FailedEmailsCount),
			Detail:      "Email delivery failed after retries; inspect bounce or retry send.",
			ActionLabel: "Email Queue",
			ActionURL:   "/communications/settings#failed-emails",
		})
	}
	return summary, items, nil
}

// EnsureDefaultCommsTemplatesAndRules idempotently seeds default English service templates
// and conservative stay-lifecycle automation rules for a property without overwriting customizations.
func (s *Store) EnsureDefaultCommsTemplatesAndRules(ctx context.Context, propertyID uuid.UUID) error {
	return s.WithTx(ctx, func(tx pgx.Tx) error {
		var existingCount int
		if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM communication_templates WHERE property_id = $1`, propertyID).Scan(&existingCount); err != nil {
			return MapErr(err)
		}
		if existingCount > 0 {
			return nil
		}

		defaults := []struct {
			code, name, cat, subj, txt, htm string
			autoName, trigger               string
			offsetHours                     int
			sendHour                        *int
			autoActive                      bool
		}{
			{
				code:        "PRE_ARRIVAL_INFO",
				name:        "Pre-Arrival Welcome & Preparation",
				cat:         "PRE_ARRIVAL",
				subj:        "Looking forward to welcoming you — {{property_name}} ({{reservation_reference}})",
				txt:         "Hello {{guest_first_name}},\n\nYour stay at {{property_name}} is coming up soon.\n\nReference: {{reservation_reference}}\nAccommodation: {{unit_names}}\nCheck-in: {{arrival_date}} from {{check_in_time}}\nCheck-out: {{departure_date}} by {{check_out_time}}\nRemaining balance: {{balance_due}}\n\nIf you have any questions before arrival, reply to {{property_email}} or call {{property_phone}}.\n\nWarm regards,\n{{property_name}}",
				htm:         "<p>Hello {{guest_first_name}},</p><p>Your stay at <strong>{{property_name}}</strong> is coming up soon.</p><ul><li><strong>Reference:</strong> {{reservation_reference}}</li><li><strong>Accommodation:</strong> {{unit_names}}</li><li><strong>Check-in:</strong> {{arrival_date}} from {{check_in_time}}</li><li><strong>Check-out:</strong> {{departure_date}} by {{check_out_time}}</li><li><strong>Balance due:</strong> {{balance_due}}</li></ul><p>Contact {{property_email}} or {{property_phone}} if you need anything ahead of arrival.</p>",
				autoName:    "3 Days Pre-Arrival Welcome (09:00 local)",
				trigger:     string(domain.TriggerPreArrival),
				offsetHours: -72,
				sendHour:    intPtr(9),
				autoActive:  true,
			},
			{
				code:        "ARRIVAL_DAY_CHECKIN",
				name:        "Arrival Day Instructions",
				cat:         "ARRIVAL_INFO",
				subj:        "Today's arrival details for {{property_name}} ({{reservation_reference}})",
				txt:         "Hello {{guest_first_name}},\n\nWelcome to {{property_name}} today! Check-in for {{unit_names}} opens at {{check_in_time}}.\nNeed directions or late-arrival support? Call {{property_phone}}.\n\nSafe travels,\n{{property_name}}",
				htm:         "<p>Hello {{guest_first_name}},</p><p>We look forward to welcoming you today at <strong>{{property_name}}</strong>. Check-in for <strong>{{unit_names}}</strong> opens from {{check_in_time}}.</p>",
				autoName:    "Arrival Morning Check-in Info (08:00 local)",
				trigger:     string(domain.TriggerArrivalInfo),
				offsetHours: 0,
				sendHour:    intPtr(8),
				autoActive:  false,
			},
			{
				code:        "DEPARTURE_INSTRUCTIONS",
				name:        "Departure & Check-out Reminder",
				cat:         "DEPARTURE_INSTRUCTIONS",
				subj:        "Departure details for {{reservation_reference}} — {{property_name}}",
				txt:         "Hello {{guest_first_name}},\n\nWe hope you are enjoying your stay at {{property_name}}. Check-out on {{departure_date}} is by {{check_out_time}}.\nPlease leave keys as advised and drop by reception if we can help with anything before departure.\n\nWarm regards,\n{{property_name}}",
				htm:         "<p>Hello {{guest_first_name}},</p><p>Check-out on <strong>{{departure_date}}</strong> is by <strong>{{check_out_time}}</strong>. Safe onward journey from {{property_name}}.</p>",
				autoName:    "Evening Before Departure Reminder (17:00 local)",
				trigger:     string(domain.TriggerDepartureInstructions),
				offsetHours: -18,
				sendHour:    intPtr(17),
				autoActive:  true,
			},
			{
				code:        "POST_STAY_THANKS",
				name:        "Post-Stay Thank You",
				cat:         "POST_STAY_THANK_YOU",
				subj:        "Thank you for staying at {{property_name}} ({{reservation_reference}})",
				txt:         "Hello {{guest_first_name}},\n\nThank you for staying at {{property_name}} ({{unit_names}}). We hope you had a restorative break.\nIf you have any feedback, reply directly to {{property_email}}.\n\nWarm regards,\n{{property_name}}",
				htm:         "<p>Hello {{guest_first_name}},</p><p>Thank you for staying with us at <strong>{{property_name}}</strong>. We look forward to welcoming you back.</p>",
				autoName:    "Post-Stay Thank You (Day After Checkout)",
				trigger:     string(domain.TriggerPostStayThankYou),
				offsetHours: 24,
				sendHour:    intPtr(11),
				autoActive:  true,
			},
			{
				code:        "PAYMENT_REMINDER",
				name:        "Balance Payment Reminder",
				cat:         "PAYMENT_REMINDER",
				subj:        "Balance reminder for {{reservation_reference}} — {{property_name}}",
				txt:         "Hello {{guest_first_name}},\n\nThis is a friendly reminder that {{balance_due}} remains outstanding for reservation {{reservation_reference}} arriving {{arrival_date}}.\nPlease contact {{property_email}} or call {{property_phone}} to settle the balance.\n\n{{property_name}}",
				htm:         "<p>Hello {{guest_first_name}},</p><p>Friendly reminder: <strong>{{balance_due}}</strong> remains due for booking <strong>{{reservation_reference}}</strong> arriving {{arrival_date}}.</p>",
				autoName:    "Balance Reminder (7 Days Pre-Arrival)",
				trigger:     string(domain.TriggerPaymentReminder),
				offsetHours: -168,
				sendHour:    intPtr(10),
				autoActive:  false,
			},
			{
				code: "ENQUIRY_REPLY",
				name: "Guest Enquiry Follow-Up",
				cat:  "ENQUIRY_RESPONSE",
				subj: "Re: {{enquiry_subject}} ({{enquiry_reference}}) — {{property_name}}",
				txt:  "Hello {{guest_first_name}},\n\nThank you for getting in touch with {{property_name}} regarding {{enquiry_subject}} ({{enquiry_reference}}).\n\nWarm regards,\n{{property_name}}\n{{property_phone}}",
				htm:  "<p>Hello {{guest_first_name}},</p><p>Thank you for contacting <strong>{{property_name}}</strong> regarding {{enquiry_subject}} ({{enquiry_reference}}).</p>",
			},
		}

		for _, d := range defaults {
			tpl, err := s.CreateCommunicationTemplate(ctx, tx, domain.CommunicationTemplate{
				PropertyID:       propertyID,
				Code:             d.code,
				Name:             d.name,
				Category:         d.cat,
				Locale:           "en",
				SubjectTemplate:  d.subj,
				BodyTextTemplate: d.txt,
				BodyHTMLTemplate: d.htm,
				IsActive:         true,
			})
			if err != nil {
				return err
			}
			if d.trigger != "" {
				if _, err := s.CreateCommunicationAutomationRule(ctx, tx, domain.CommunicationAutomationRule{
					PropertyID:         propertyID,
					Name:               d.autoName,
					TriggerEvent:       domain.AutomationTrigger(d.trigger),
					OffsetHours:        d.offsetHours,
					SendHourLocal:      d.sendHour,
					TemplateID:         tpl.ID,
					ApplicableStatuses: []string{"CONFIRMED"},
					IsActive:           d.autoActive,
				}); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func intPtr(v int) *int {
	return &v
}
