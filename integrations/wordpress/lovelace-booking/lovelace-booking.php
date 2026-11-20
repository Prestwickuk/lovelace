<?php
/**
 * Plugin Name:       Lovelace Direct Booking
 * Plugin URI:        https://github.com/Prestwickuk/lovelace
 * Description:       Official WordPress integration for Lovelace Hospitality & Campsite Management. Adds Gutenberg blocks and shortcodes for live embedded booking sections, availability search bars, modal "Book Direct" buttons, and SEO-friendly accommodation cards.
 * Version:           1.0.0
 * Requires at least: 6.0
 * Requires PHP:      7.4
 * Author:            Lovelace
 * License:           MIT
 * Text Domain:       lovelace-booking
 */

if ( ! defined( 'ABSPATH' ) ) {
	exit;
}

define( 'LOVELACE_BOOKING_VERSION', '1.0.0' );
define( 'LOVELACE_BOOKING_OPTION_KEY', 'lovelace_booking_settings' );
define( 'LOVELACE_BOOKING_PLUGIN_FILE', __FILE__ );
define( 'LOVELACE_BOOKING_PLUGIN_DIR', plugin_dir_path( __FILE__ ) );
define( 'LOVELACE_BOOKING_PLUGIN_URL', plugin_dir_url( __FILE__ ) );

require_once LOVELACE_BOOKING_PLUGIN_DIR . 'includes/bundled-defaults.php';
require_once LOVELACE_BOOKING_PLUGIN_DIR . 'includes/class-lovelace-api-client.php';
require_once LOVELACE_BOOKING_PLUGIN_DIR . 'includes/class-lovelace-settings.php';
require_once LOVELACE_BOOKING_PLUGIN_DIR . 'includes/class-lovelace-renderer.php';
require_once LOVELACE_BOOKING_PLUGIN_DIR . 'includes/class-lovelace-blocks.php';

/**
 * Seeds initial plugin settings from bundled defaults on first activation
 * without overwriting existing user-saved WordPress options.
 */
function lovelace_booking_activate() {
	$existing = get_option( LOVELACE_BOOKING_OPTION_KEY, false );
	$defaults = lovelace_booking_bundled_defaults();

	if ( false === $existing || ! is_array( $existing ) ) {
		update_option( LOVELACE_BOOKING_OPTION_KEY, Lovelace_Booking_Settings::sanitize_settings( $defaults ) );
		return;
	}

	// Fill in blank origin/slug if upgrading from an unconfigured install using a pre-configured bundle.
	$changed = false;
	foreach ( array( 'booking_origin', 'property_slug', 'property_name', 'primary_color', 'primary_on_color' ) as $key ) {
		if ( empty( $existing[ $key ] ) && ! empty( $defaults[ $key ] ) ) {
			$existing[ $key ] = $defaults[ $key ];
			$changed          = true;
		}
	}
	if ( $changed ) {
		update_option( LOVELACE_BOOKING_OPTION_KEY, Lovelace_Booking_Settings::sanitize_settings( $existing ) );
	}
}
register_activation_hook( __FILE__, 'lovelace_booking_activate' );

/**
 * Bootstraps plugin components on plugins_loaded.
 */
function lovelace_booking_init() {
	Lovelace_Booking_Settings::init();
	Lovelace_Booking_Renderer::init();
	Lovelace_Booking_Blocks::init();
}
add_action( 'plugins_loaded', 'lovelace_booking_init' );

/**
 * Adds a quick "Settings" link on the WordPress Plugins list row.
 *
 * @param array<int,string> $links Existing plugin action links.
 * @return array<int,string>
 */
function lovelace_booking_plugin_action_links( $links ) {
	$settings_url  = admin_url( 'options-general.php?page=lovelace-booking' );
	$settings_link = '<a href="' . esc_url( $settings_url ) . '">' . esc_html__( 'Settings', 'lovelace-booking' ) . '</a>';
	array_unshift( $links, $settings_link );
	return $links;
}
add_filter( 'plugin_action_links_' . plugin_basename( __FILE__ ), 'lovelace_booking_plugin_action_links' );
