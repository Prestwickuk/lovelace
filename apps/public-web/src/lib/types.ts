export interface PropertyBookingPolicy {
  property_id: string;
  hold_duration_minutes: number;
  deposit_mode: "FULL" | "PERCENTAGE" | "FIXED" | string;
  deposit_percentage: number;
  deposit_fixed_pence: number;
  balance_due_days_before_arrival: number;
  extra_adult_nightly_pence: number;
  child_nightly_pence: number;
  infant_nightly_pence: number;
  booking_terms_version: string;
  booking_terms_url: string;
  privacy_policy_url: string;
  cancellation_policy: string;
  quiet_hours?: string;
  fire_policy?: string;
  pet_policy?: string;
  privacy_notice?: string;
  stripe_enabled: boolean;
  version: number;
}

export interface PublicPropertyView {
  id: string;
  slug: string;
  name: string;
  tagline: string;
  public_description: string;
  region: string;
  country_code: string;
  address_line1: string;
  town: string;
  postcode: string;
  timezone: string;
  currency: string;
  check_in_time: string;
  check_out_time: string;
  contact_email: string;
  contact_phone: string;
  website_url: string;
  policy: PropertyBookingPolicy;
}

export interface UnitTypeImage {
  url: string;
  alt: string;
  caption?: string;
}

export interface PublicUnitTypeView {
  id: string;
  code: string;
  name: string;
  category: string;
  description: string;
  base_capacity_adults: number;
  max_adults: number;
  max_children: number;
  max_infants: number;
  max_guests: number;
  pets_allowed: boolean;
  max_pets: number;
  base_nightly_rate_pence: number;
  tax_rate_bps: number;
  min_nights: number;
  max_nights: number;
  amenities: string[];
  images: UnitTypeImage[];
}

export interface PublicExtraView {
  id: string;
  code: string;
  name: string;
  description: string;
  pricing_model: string;
  price_pence: number;
  tax_rate_bps: number;
  max_quantity: number;
}

export interface PublicCatalogResponse {
  property: PublicPropertyView;
  unit_types: PublicUnitTypeView[];
  extras: PublicExtraView[];
}

export interface PublicUnitTypeDetailResponse {
  property: PublicPropertyView;
  unit_type: PublicUnitTypeView;
  extras: PublicExtraView[];
}

export interface AvailabilitySearchItem {
  unit_type: PublicUnitTypeView;
  available_units: number;
  rate_plan_id: string;
  rate_plan_name: string;
  nights: number;
  effective_min_nights: number;
  effective_max_nights: number;
  meets_stay_rules: boolean;
  accommodation_subtotal_pence: number;
  occupancy_surcharge_pence: number;
  tax_pence: number;
  total_pence: number;
  deposit_due_pence: number;
  balance_due_pence: number;
  currency: string;
}

export interface PublicAvailabilityResponse {
  property: PublicPropertyView;
  arrival_date: string;
  departure_date: string;
  nights: number;
  adults: number;
  children: number;
  infants: number;
  items: AvailabilitySearchItem[];
}

export interface NightlyRateBreakdown {
  date: string;
  rate_pence: number;
  source: string;
}

export interface QuoteExtraSelection {
  extra_id: string;
  quantity: number;
}

export interface QuoteExtraLine {
  extra_id: string;
  code: string;
  name: string;
  pricing_model: string;
  quantity: number;
  unit_pence: number;
  total_pence: number;
  tax_pence: number;
}

export interface TaxLine {
  label: string;
  rate_bps: number;
  taxable_pence: number;
  tax_pence: number;
}

export interface BookingQuote {
  id: string;
  property_id: string;
  unit_type_id: string;
  unit_type_code?: string;
  unit_type_name?: string;
  rate_plan_id: string;
  rate_plan_name?: string;
  currency: string;
  start_date: string;
  end_date: string;
  nights: number;
  adults: number;
  children: number;
  infants: number;
  nightly_breakdown: NightlyRateBreakdown[];
  extras_breakdown: QuoteExtraLine[];
  tax_breakdown: TaxLine[];
  accommodation_subtotal_pence: number;
  occupancy_surcharge_pence: number;
  extras_subtotal_pence: number;
  discount_pence: number;
  tax_pence: number;
  total_pence: number;
  deposit_mode: string;
  deposit_due_pence: number;
  balance_due_pence: number;
  balance_due_date?: string;
  policy_version: number;
  quote_hash: string;
  expires_at: string;
}

export interface BookingHold {
  id: string;
  property_id: string;
  hold_reference: string;
  unit_type_id: string;
  unit_type_name?: string;
  arrival_date: string;
  departure_date: string;
  adults: number;
  children: number;
  infants: number;
  guest_name: string;
  guest_email: string;
  guest_phone: string;
  special_requests?: string;
  status: string;
  expires_at: string;
}

export interface CreateHoldResponse {
  hold: BookingHold;
  quote: BookingQuote;
  confirmation_token: string;
}

export interface CreateCheckoutResponse {
  hold: BookingHold;
  checkout_url: string;
  payment_attempt_id: string;
  hold_expires_at: string;
}

export interface PaymentRecord {
  id: string;
  provider: string;
  payment_type: string;
  amount_pence: number;
  currency: string;
  status: string;
  created_at: string;
}

export interface PublicConfirmationView {
  status: string;
  hold_reference?: string;
  reservation_reference?: string;
  property_name: string;
  property_slug: string;
  contact_email: string;
  contact_phone: string;
  check_in_time: string;
  check_out_time: string;
  cancellation_policy: string;
  unit_type_name: string;
  arrival_date: string;
  departure_date: string;
  nights: number;
  adults: number;
  children: number;
  infants: number;
  guest_name: string;
  guest_email: string;
  payment_status: string;
  quote?: BookingQuote;
  payments?: PaymentRecord[];
  cancellation_requested: boolean;
  hold_expires_at?: string;
}

export interface ApiErrorEnvelope {
  error?: {
    code: string;
    message: string;
    fields?: Record<string, string>;
  };
}
