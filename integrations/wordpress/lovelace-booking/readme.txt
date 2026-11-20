=== Lovelace Direct Booking ===
Contributors: lovelace
Requires at least: 6.0
Tested up to: 6.7
Requires PHP: 7.4
Stable tag: 1.0.0
License: MIT

Official WordPress frontend integration for the Lovelace Hospitality, Campsite & Holiday Let Booking Engine.

== Description ==

Connects any WordPress website (Gutenberg Block Editor, Elementor, Divi, Classic Editor, or custom themes) to your Lovelace property booking engine without storing API keys, guest PII, or payment credentials inside WordPress.

Included Gutenberg Blocks & Shortcodes:

1. Full Live Booking Section (`[lovelace_booking]` / `lovelace/booking-widget`)
   Embeds live availability search, tariff & deposit pricing, optional extras, and guest reservation holds directly on your page with automatic height resizing and top-window Stripe 3DS checkout.

2. Availability Search Bar (`[lovelace_search]` / `lovelace/search-bar`)
   Lightweight Check-in / Check-out / Adults / Children bar for headers, hero sections, or sidebars. Supports opening results inside an inline modal overlay (`target="modal"`).

3. Branded "Book Direct" Button (`[lovelace_book_button]` / `lovelace/book-button`)
   Call-to-action button styled in your property's contrast-verified brand colour, opening either an accessible booking modal overlay on the current page or your direct booking page.

4. SEO Accommodation Cards (`[lovelace_accommodation]` / `lovelace/accommodation-grid`)
   Server-rendered, search-engine-indexable accommodation cards cached via WordPress Transients, with optional `LodgingBusiness` JSON-LD structured data.

== Installation ==

1. In Lovelace Admin Console, open **Site Settings -> Website Booking Section** and click **Download Pre-Configured WordPress Plugin (.zip)**.
2. In your WordPress dashboard, go to **Plugins -> Add New -> Upload Plugin**, select the `.zip` file, and click **Install Now** then **Activate Plugin**.
3. Open **Settings -> Lovelace Booking** (your Booking URL, Property Slug, and Brand Colours are already pre-filled!) and click **Test Connection & Sync Brand Colours**.
4. Add any **Lovelace** block in the WordPress Block Editor, or paste `[lovelace_booking]`, `[lovelace_search]`, `[lovelace_book_button]`, or `[lovelace_accommodation]` into any page.
