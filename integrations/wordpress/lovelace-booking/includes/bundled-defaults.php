<?php
/**
 * Default configuration values bundled with the plugin archive.
 * When downloaded from Lovelace Site Settings or the Onboarding Wizard,
 * this file is pre-populated with the property's booking URL, slug, and brand palette.
 */

if ( ! defined( 'ABSPATH' ) ) {
	exit;
}

/**
 * Returns default settings array.
 *
 * @return array<string,mixed>
 */
function lovelace_booking_bundled_defaults() {
	return array(
		'booking_origin'   => '',
		'property_slug'    => '',
		'property_name'    => '',
		'primary_color'    => '#2F4F4A',
		'primary_on_color' => '#FFFFFF',
		'button_label'     => 'Book Direct',
		'default_mode'     => 'modal',
		'cache_ttl'        => 600,
	);
}
