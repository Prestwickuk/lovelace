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
  guest_portal_enabled?: boolean;
  version: number;
}

export interface PublicPropertyBranding {
  schema_version: number;
  display_name: string;
  tagline: string;
  region_label?: string;
  primary_color: string;
  primary_on_color: string;
  primary_hover_color: string;
  primary_dark_color?: string;
  primary_dark_on_color?: string;
  secondary_color: string;
  secondary_on_color: string;
  secondary_hover_color?: string;
  secondary_dark_color?: string;
  secondary_dark_on_color?: string;
  has_custom_palette: boolean;
  has_logo: boolean;
  logo_url?: string;
  logo_alt?: string;
  logo_width?: number;
  logo_height?: number;
  logo_version?: number;
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
  branding?: PublicPropertyBranding;
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

export type PortalSessionScope = "BOOKING" | "ACCOUNT";

export interface PortalSessionGuestSummary {
  full_name: string;
  verified_email: string;
  pending_email?: string;
  preferred_language: string;
  is_do_not_contact: boolean;
  marketing_consent_state: string;
}

export interface PortalSessionView {
  authenticated: boolean;
  property_slug: string;
  property_name: string;
  scope_type: PortalSessionScope;
  scoped_booking_reference?: string;
  csrf_token: string;
  verified_at: string;
  step_up_fresh: boolean;
  idle_expires_at: string;
  absolute_expires_at: string;
  guest?: PortalSessionGuestSummary;
}

export interface PortalBookingUnitView {
  unit_name: string;
  unit_type_name: string;
  adults: number;
  children: number;
  infants: number;
}

export interface PortalBookingSummary {
  reference: string;
  status: string;
  channel_code: string;
  is_ota_managed: boolean;
  billing_notice?: string;
  arrival_date: string;
  departure_date: string;
  nights: number;
  check_in_time: string;
  check_out_time: string;
  adults: number;
  children: number;
  infants: number;
  units: PortalBookingUnitView[];
  payment_status?: string;
  total_pence?: number;
  deposit_pence?: number;
  amount_paid_pence?: number;
  balance_due_pence?: number;
  currency?: string;
  estimated_arrival_time?: string;
  guest_arrival_notes?: string;
  cancellation_requested: boolean;
  can_update_arrival: boolean;
  can_request_change: boolean;
  can_request_cancel: boolean;
}

export interface PortalPaymentSummaryView {
  amount_pence: number;
  currency: string;
  status: string;
  paid_at?: string;
}

export interface PortalQuoteLineItemView {
  description: string;
  quantity: number;
  unit_pence: number;
  total_pence: number;
}

export interface PortalQuoteSummaryView {
  currency: string;
  nights: number;
  subtotal_pence: number;
  extras_pence: number;
  tax_pence: number;
  total_pence: number;
  deposit_pence: number;
  balance_pence: number;
  line_items?: PortalQuoteLineItemView[];
}

export interface PortalBookingDetail extends PortalBookingSummary {
  property_name: string;
  property_slug: string;
  contact_email: string;
  contact_phone: string;
  cancellation_policy: string;
  quiet_hours?: string;
  fire_policy?: string;
  pet_policy?: string;
  quote?: PortalQuoteSummaryView;
  payments?: PortalPaymentSummaryView[];
}

export interface PortalGuestPreferenceView {
  category: string;
  preference_key: string;
  preference_value: string;
}

export interface PortalConsentEntryView {
  consent_type: string;
  status: string;
  method: string;
  occurred_at: string;
}

export interface PortalProfileView {
  first_name: string;
  last_name: string;
  full_name: string;
  verified_email: string;
  pending_email?: string;
  pending_email_expires_at?: string;
  phone: string;
  preferred_contact_method: string;
  preferred_language: string;
  is_do_not_contact: boolean;
  marketing_consent_state: string;
  consent_history: PortalConsentEntryView[];
  preferences: PortalGuestPreferenceView[];
}

export interface PortalEnquiryMessageView {
  id: string;
  direction: "INBOUND" | "OUTBOUND";
  sender_role: "GUEST" | "PROPERTY";
  sender_name: string;
  body: string;
  created_at: string;
}

export interface PortalEnquiryView {
  id: string;
  reference: string;
  reservation_reference?: string;
  category: string;
  status: string;
  subject: string;
  description: string;
  created_at: string;
  updated_at: string;
  messages?: PortalEnquiryMessageView[];
}

export interface PortalPrivacyRequestReceipt {
  accepted: boolean;
  message: string;
}
