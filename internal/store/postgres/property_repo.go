package postgres

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tirbach/tir-bach/internal/domain"
)

func (s *Store) ListProperties(ctx context.Context) ([]domain.Property, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, slug, name, tagline, country_code, region, address_line1, address_line2,
		       town, postcode, timezone, currency,
		       to_char(check_in_time, 'HH24:MI'), to_char(check_out_time, 'HH24:MI'),
		       vat_registered, vat_number, contact_email, contact_phone,
		       settings, branding_config, version, created_at, updated_at
		FROM properties
		ORDER BY created_at ASC
	`)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()

	var out []domain.Property
	for rows.Next() {
		var p domain.Property
		if err := rows.Scan(
			&p.ID, &p.Slug, &p.Name, &p.Tagline, &p.CountryCode, &p.Region,
			&p.AddressLine1, &p.AddressLine2, &p.Town, &p.Postcode,
			&p.Timezone, &p.Currency, &p.CheckInTime, &p.CheckOutTime,
			&p.VATRegistered, &p.VATNumber, &p.ContactEmail, &p.ContactPhone,
			&p.Settings, &p.BrandingConfig, &p.Version, &p.CreatedAt, &p.UpdatedAt,
		); err != nil {
			return nil, MapErr(err)
		}
		out = append(out, p)
	}
	return out, MapErr(rows.Err())
}

func (s *Store) GetPropertyByID(ctx context.Context, db DBTX, id uuid.UUID) (domain.Property, error) {
	if db == nil {
		db = s.pool
	}
	var p domain.Property
	err := db.QueryRow(ctx, `
		SELECT id, slug, name, tagline, country_code, region, address_line1, address_line2,
		       town, postcode, timezone, currency,
		       to_char(check_in_time, 'HH24:MI'), to_char(check_out_time, 'HH24:MI'),
		       vat_registered, vat_number, contact_email, contact_phone,
		       settings, branding_config, version, created_at, updated_at
		FROM properties
		WHERE id = $1
	`, id).Scan(
		&p.ID, &p.Slug, &p.Name, &p.Tagline, &p.CountryCode, &p.Region,
		&p.AddressLine1, &p.AddressLine2, &p.Town, &p.Postcode,
		&p.Timezone, &p.Currency, &p.CheckInTime, &p.CheckOutTime,
		&p.VATRegistered, &p.VATNumber, &p.ContactEmail, &p.ContactPhone,
		&p.Settings, &p.BrandingConfig, &p.Version, &p.CreatedAt, &p.UpdatedAt,
	)
	return p, MapErr(err)
}

func (s *Store) GetPropertyBySlug(ctx context.Context, db DBTX, slug string) (domain.Property, error) {
	if db == nil {
		db = s.pool
	}
	var p domain.Property
	err := db.QueryRow(ctx, `
		SELECT id, slug, name, tagline, country_code, region, address_line1, address_line2,
		       town, postcode, timezone, currency,
		       to_char(check_in_time, 'HH24:MI'), to_char(check_out_time, 'HH24:MI'),
		       vat_registered, vat_number, contact_email, contact_phone,
		       settings, branding_config, version, created_at, updated_at
		FROM properties
		WHERE slug = $1
	`, slug).Scan(
		&p.ID, &p.Slug, &p.Name, &p.Tagline, &p.CountryCode, &p.Region,
		&p.AddressLine1, &p.AddressLine2, &p.Town, &p.Postcode,
		&p.Timezone, &p.Currency, &p.CheckInTime, &p.CheckOutTime,
		&p.VATRegistered, &p.VATNumber, &p.ContactEmail, &p.ContactPhone,
		&p.Settings, &p.BrandingConfig, &p.Version, &p.CreatedAt, &p.UpdatedAt,
	)
	return p, MapErr(err)
}

func (s *Store) CreateProperty(ctx context.Context, db DBTX, p domain.Property) (domain.Property, error) {
	if db == nil {
		db = s.pool
	}
	settings := p.Settings
	if len(settings) == 0 {
		settings = json.RawMessage(`{}`)
	}
	brandingCfg := p.BrandingConfig
	if len(brandingCfg) == 0 {
		brandingCfg = json.RawMessage(`{}`)
	}
	var out domain.Property
	err := db.QueryRow(ctx, `
		INSERT INTO properties (
			id, slug, name, tagline, country_code, region, address_line1, address_line2,
			town, postcode, timezone, currency, check_in_time, check_out_time,
			vat_registered, vat_number, contact_email, contact_phone, settings, branding_config
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8,
			$9, $10, $11, $12, $13::time, $14::time,
			$15, $16, $17, $18, $19, $20
		)
		RETURNING id, slug, name, tagline, country_code, region, address_line1, address_line2,
		          town, postcode, timezone, currency,
		          to_char(check_in_time, 'HH24:MI'), to_char(check_out_time, 'HH24:MI'),
		          vat_registered, vat_number, contact_email, contact_phone,
		          settings, branding_config, version, created_at, updated_at
	`,
		p.ID, p.Slug, p.Name, p.Tagline, p.CountryCode, p.Region, p.AddressLine1, p.AddressLine2,
		p.Town, p.Postcode, p.Timezone, p.Currency, p.CheckInTime, p.CheckOutTime,
		p.VATRegistered, p.VATNumber, p.ContactEmail, p.ContactPhone, settings, brandingCfg,
	).Scan(
		&out.ID, &out.Slug, &out.Name, &out.Tagline, &out.CountryCode, &out.Region,
		&out.AddressLine1, &out.AddressLine2, &out.Town, &out.Postcode,
		&out.Timezone, &out.Currency, &out.CheckInTime, &out.CheckOutTime,
		&out.VATRegistered, &out.VATNumber, &out.ContactEmail, &out.ContactPhone,
		&out.Settings, &out.BrandingConfig, &out.Version, &out.CreatedAt, &out.UpdatedAt,
	)
	if err != nil {
		return out, MapErr(err)
	}
	if _, err := db.Exec(ctx, `
		INSERT INTO booking_channels (id, property_id, code, name, category, sort_order)
		VALUES
			(gen_random_uuid(), $1, 'DIRECT',      'Direct Website / Admin', 'direct', 10),
			(gen_random_uuid(), $1, 'PHONE',       'Telephone',              'manual', 20),
			(gen_random_uuid(), $1, 'EMAIL',       'Email Inquiry',          'manual', 30),
			(gen_random_uuid(), $1, 'WALK_IN',     'Walk-in',                'manual', 40),
			(gen_random_uuid(), $1, 'AIRBNB',      'Airbnb',                 'ota',    50),
			(gen_random_uuid(), $1, 'BOOKING_COM', 'Booking.com',            'ota',    60),
			(gen_random_uuid(), $1, 'OTHER',       'Other Source',           'other',  70)
		ON CONFLICT (property_id, code) DO NOTHING
	`, out.ID); err != nil {
		return domain.Property{}, MapErr(err)
	}
	if _, err := db.Exec(ctx, `
		INSERT INTO property_booking_policies (property_id)
		VALUES ($1)
		ON CONFLICT (property_id) DO NOTHING
	`, out.ID); err != nil {
		return domain.Property{}, MapErr(err)
	}
	return out, nil
}

func (s *Store) UpdateProperty(ctx context.Context, tx pgx.Tx, p domain.Property) (domain.Property, error) {
	settings := p.Settings
	if len(settings) == 0 {
		settings = json.RawMessage(`{}`)
	}
	brandingCfg := p.BrandingConfig
	if len(brandingCfg) == 0 {
		brandingCfg = json.RawMessage(`{}`)
	}
	var out domain.Property
	err := tx.QueryRow(ctx, `
		UPDATE properties
		SET name = $2,
		    tagline = $3,
		    country_code = $4,
		    region = $5,
		    address_line1 = $6,
		    address_line2 = $7,
		    town = $8,
		    postcode = $9,
		    timezone = $10,
		    currency = $11,
		    check_in_time = $12::time,
		    check_out_time = $13::time,
		    vat_registered = $14,
		    vat_number = $15,
		    contact_email = $16,
		    contact_phone = $17,
		    settings = $18,
		    branding_config = $19,
		    version = version + 1,
		    updated_at = NOW()
		WHERE id = $1 AND version = $20
		RETURNING id, slug, name, tagline, country_code, region, address_line1, address_line2,
		          town, postcode, timezone, currency,
		          to_char(check_in_time, 'HH24:MI'), to_char(check_out_time, 'HH24:MI'),
		          vat_registered, vat_number, contact_email, contact_phone,
		          settings, branding_config, version, created_at, updated_at
	`,
		p.ID, p.Name, p.Tagline, p.CountryCode, p.Region, p.AddressLine1, p.AddressLine2,
		p.Town, p.Postcode, p.Timezone, p.Currency, p.CheckInTime, p.CheckOutTime,
		p.VATRegistered, p.VATNumber, p.ContactEmail, p.ContactPhone, settings, brandingCfg, p.Version,
	).Scan(
		&out.ID, &out.Slug, &out.Name, &out.Tagline, &out.CountryCode, &out.Region,
		&out.AddressLine1, &out.AddressLine2, &out.Town, &out.Postcode,
		&out.Timezone, &out.Currency, &out.CheckInTime, &out.CheckOutTime,
		&out.VATRegistered, &out.VATNumber, &out.ContactEmail, &out.ContactPhone,
		&out.Settings, &out.BrandingConfig, &out.Version, &out.CreatedAt, &out.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		// Distinguish not found vs stale version
		if _, gerr := s.GetPropertyByID(ctx, tx, p.ID); gerr == nil {
			return domain.Property{}, domain.ErrConflict
		}
		return domain.Property{}, domain.ErrNotFound
	}
	return out, MapErr(err)
}
