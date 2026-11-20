<?php
/**
 * Read-only HTTP client for Lovelace's public BFF catalog endpoint with WordPress Transient caching.
 * Zero API secrets or credentials are stored in WordPress.
 */

if ( ! defined( 'ABSPATH' ) ) {
	exit;
}

class Lovelace_Booking_API_Client {

	const TRANSIENT_PREFIX = 'lovelace_cat_';

	/**
	 * Normalizes and validates a public Lovelace booking origin URL.
	 * Allows https:// in production and http://localhost or http://127.0.0.1 for local development.
	 *
	 * @param string $raw Raw URL input.
	 * @return string Normalized scheme://host[:port] or empty string if invalid.
	 */
	public static function normalize_origin( $raw ) {
		$trimmed = trim( (string) $raw );
		if ( '' === $trimmed ) {
			return '';
		}
		$clean = esc_url_raw( rtrim( $trimmed, '/' ), array( 'https', 'http' ) );
		if ( '' === $clean ) {
			return '';
		}
		$parts = wp_parse_url( $clean );
		if ( empty( $parts['scheme'] ) || empty( $parts['host'] ) ) {
			return '';
		}
		$scheme = strtolower( (string) $parts['scheme'] );
		$host   = strtolower( (string) $parts['host'] );

		if ( 'https' !== $scheme ) {
			$is_local = ( 'localhost' === $host || '127.0.0.1' === $host || '::1' === $host );
			if ( ! $is_local ) {
				return '';
			}
		}

		$origin = $scheme . '://' . $host;
		if ( ! empty( $parts['port'] ) ) {
			$origin .= ':' . absint( $parts['port'] );
		}
		return $origin;
	}

	/**
	 * Sanitizes a Lovelace property or unit-type slug.
	 *
	 * @param string $raw Raw slug input.
	 * @return string Safe lowercase slug or empty string.
	 */
	public static function sanitize_slug( $raw ) {
		$clean = strtolower( trim( (string) $raw ) );
		if ( preg_match( '/^[a-z0-9][a-z0-9_-]{0,63}$/', $clean ) ) {
			return $clean;
		}
		return '';
	}

	/**
	 * Computes the transient key for a given origin + slug pair.
	 *
	 * @param string $origin Normalized origin.
	 * @param string $slug   Sanitized property slug.
	 * @return string
	 */
	public static function transient_key( $origin, $slug ) {
		return self::TRANSIENT_PREFIX . substr( md5( $origin . '|' . $slug ), 0, 20 );
	}

	/**
	 * Clears cached catalog transients.
	 *
	 * @param string $origin Normalized origin.
	 * @param string $slug   Sanitized property slug.
	 * @return void
	 */
	public static function clear_cache( $origin, $slug ) {
		if ( '' !== $origin && '' !== $slug ) {
			delete_transient( self::transient_key( $origin, $slug ) );
		}
	}

	/**
	 * Fetches public property & accommodation catalog from the Lovelace public BFF.
	 *
	 * @param string $origin       Normalized booking origin URL.
	 * @param string $slug         Sanitized property slug.
	 * @param bool   $bypass_cache Whether to skip reading from WP Transients.
	 * @param int    $ttl_seconds  Cache TTL in seconds (60..86400).
	 * @return array<string,mixed>|WP_Error
	 */
	public static function fetch_catalog( $origin, $slug, $bypass_cache = false, $ttl_seconds = 600 ) {
		$clean_origin = self::normalize_origin( $origin );
		$clean_slug   = self::sanitize_slug( $slug );

		if ( '' === $clean_origin || '' === $clean_slug ) {
			return new WP_Error(
				'lovelace_unconfigured',
				__( 'Please configure both your Lovelace Booking URL and Property Slug in Settings → Lovelace Booking.', 'lovelace-booking' )
			);
		}

		$t_key = self::transient_key( $clean_origin, $clean_slug );
		if ( ! $bypass_cache ) {
			$cached = get_transient( $t_key );
			if ( is_array( $cached ) && ! empty( $cached['property'] ) ) {
				return $cached;
			}
		}

		$endpoint = $clean_origin . '/api/public/catalog?property=' . rawurlencode( $clean_slug );
		$is_local = ( 0 === strpos( $clean_origin, 'http://localhost' ) || 0 === strpos( $clean_origin, 'http://127.0.0.1' ) );

		$args = array(
			'timeout'     => 8,
			'redirection' => 2,
			'headers'     => array(
				'Accept'     => 'application/json',
				'User-Agent' => 'Lovelace-WordPress-Plugin/' . LOVELACE_BOOKING_VERSION,
			),
		);

		$response = $is_local ? wp_remote_get( $endpoint, $args ) : wp_safe_remote_get( $endpoint, $args );
		if ( is_wp_error( $response ) ) {
			return new WP_Error(
				'lovelace_network_error',
				sprintf(
					/* translators: %s: error message */
					__( 'Could not reach Lovelace booking server: %s', 'lovelace-booking' ),
					$response->get_error_message()
				)
			);
		}

		$code = (int) wp_remote_retrieve_response_code( $response );
		$body = (string) wp_remote_retrieve_body( $response );
		$data = json_decode( $body, true );

		if ( 200 !== $code || ! is_array( $data ) || empty( $data['property'] ) || ! is_array( $data['property'] ) ) {
			$remote_msg = '';
			if ( is_array( $data ) && isset( $data['error']['message'] ) && is_string( $data['error']['message'] ) ) {
				$remote_msg = sanitize_text_field( $data['error']['message'] );
			}
			if ( '' === $remote_msg ) {
				$remote_msg = sprintf( 'HTTP %d', $code );
			}
			return new WP_Error(
				'lovelace_catalog_error',
				sprintf(
					/* translators: %s: error detail */
					__( 'Lovelace property lookup failed (%s). Verify your Booking URL and Property Slug.', 'lovelace-booking' ),
					$remote_msg
				)
			);
		}

		$ttl = max( 60, min( 86400, absint( $ttl_seconds ) ) );
		set_transient( $t_key, $data, $ttl );

		return $data;
	}
}
