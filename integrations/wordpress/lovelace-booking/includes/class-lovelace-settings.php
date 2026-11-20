<?php
/**
 * WordPress Admin Settings Page (Settings -> Lovelace Booking).
 */

if ( ! defined( 'ABSPATH' ) ) {
	exit;
}

class Lovelace_Booking_Settings {

	/**
	 * Registers admin hooks.
	 *
	 * @return void
	 */
	public static function init() {
		add_action( 'admin_init', array( __CLASS__, 'register_settings' ) );
		add_action( 'admin_menu', array( __CLASS__, 'register_menu' ) );
		add_action( 'admin_post_lovelace_booking_test_connection', array( __CLASS__, 'handle_test_connection' ) );
		add_action( 'admin_post_lovelace_booking_clear_cache', array( __CLASS__, 'handle_clear_cache' ) );
	}

	/**
	 * Returns merged sanitized settings.
	 *
	 * @return array<string,mixed>
	 */
	public static function get_settings() {
		$defaults = lovelace_booking_bundled_defaults();
		$saved    = get_option( LOVELACE_BOOKING_OPTION_KEY, array() );
		if ( ! is_array( $saved ) ) {
			$saved = array();
		}
		return self::sanitize_settings( array_merge( $defaults, $saved ) );
	}

	/**
	 * Sanitizes settings before persisting to wp_options.
	 *
	 * @param mixed $input Raw form/option input.
	 * @return array<string,mixed>
	 */
	public static function sanitize_settings( $input ) {
		$defaults = lovelace_booking_bundled_defaults();
		if ( ! is_array( $input ) ) {
			$input = array();
		}

		$origin = isset( $input['booking_origin'] )
			? Lovelace_Booking_API_Client::normalize_origin( (string) $input['booking_origin'] )
			: '';
		if ( '' === $origin && ! empty( $input['booking_origin'] ) && function_exists( 'add_settings_error' ) ) {
			add_settings_error(
				LOVELACE_BOOKING_OPTION_KEY,
				'invalid_booking_origin',
				__( 'Booking Site URL must be a valid https:// URL (or http://localhost for local testing).', 'lovelace-booking' ),
				'error'
			);
		}

		$slug = isset( $input['property_slug'] )
			? Lovelace_Booking_API_Client::sanitize_slug( (string) $input['property_slug'] )
			: '';

		$name = isset( $input['property_name'] )
			? sanitize_text_field( (string) $input['property_name'] )
			: '';

		$primary = isset( $input['primary_color'] ) ? sanitize_hex_color( (string) $input['primary_color'] ) : '';
		if ( empty( $primary ) ) {
			$primary = $defaults['primary_color'];
		}

		$primary_on = isset( $input['primary_on_color'] ) ? sanitize_hex_color( (string) $input['primary_on_color'] ) : '';
		if ( empty( $primary_on ) ) {
			$primary_on = $defaults['primary_on_color'];
		}

		$button_label = isset( $input['button_label'] ) ? sanitize_text_field( (string) $input['button_label'] ) : '';
		if ( '' === $button_label ) {
			$button_label = $defaults['button_label'];
		}

		$mode = isset( $input['default_mode'] ) ? sanitize_key( (string) $input['default_mode'] ) : 'modal';
		if ( ! in_array( $mode, array( 'modal', 'new_tab', 'same_tab' ), true ) ) {
			$mode = 'modal';
		}

		$ttl = isset( $input['cache_ttl'] ) ? absint( $input['cache_ttl'] ) : 600;
		$ttl = max( 60, min( 86400, $ttl ) );

		if ( '' !== $origin && '' !== $slug ) {
			Lovelace_Booking_API_Client::clear_cache( $origin, $slug );
		}

		return array(
			'booking_origin'   => $origin,
			'property_slug'    => $slug,
			'property_name'    => $name,
			'primary_color'    => strtoupper( $primary ),
			'primary_on_color' => strtoupper( $primary_on ),
			'button_label'     => $button_label,
			'default_mode'     => $mode,
			'cache_ttl'        => $ttl,
		);
	}

	/**
	 * Registers option with WordPress Settings API.
	 *
	 * @return void
	 */
	public static function register_settings() {
		register_setting(
			'lovelace_booking_group',
			LOVELACE_BOOKING_OPTION_KEY,
			array(
				'type'              => 'array',
				'sanitize_callback' => array( __CLASS__, 'sanitize_settings' ),
				'default'           => lovelace_booking_bundled_defaults(),
			)
		);
	}

	/**
	 * Adds Settings -> Lovelace Booking submenu item.
	 *
	 * @return void
	 */
	public static function register_menu() {
		add_options_page(
			__( 'Lovelace Direct Booking', 'lovelace-booking' ),
			__( 'Lovelace Booking', 'lovelace-booking' ),
			'manage_options',
			'lovelace-booking',
			array( __CLASS__, 'render_page' )
		);
	}

	/**
	 * Tests connection against /api/public/catalog and syncs verified brand colours.
	 *
	 * @return void
	 */
	public static function handle_test_connection() {
		if ( ! current_user_can( 'manage_options' ) ) {
			wp_die( esc_html__( 'You do not have permission to manage Lovelace settings.', 'lovelace-booking' ) );
		}
		check_admin_referer( 'lovelace_booking_test_connection' );

		$settings = self::get_settings();
		$result   = Lovelace_Booking_API_Client::fetch_catalog(
			(string) $settings['booking_origin'],
			(string) $settings['property_slug'],
			true,
			(int) $settings['cache_ttl']
		);

		$redirect = admin_url( 'options-general.php?page=lovelace-booking' );
		if ( is_wp_error( $result ) ) {
			$redirect = add_query_arg(
				array(
					'lv_status' => 'error',
					'lv_msg'    => rawurlencode( $result->get_error_message() ),
				),
				$redirect
			);
			wp_safe_redirect( $redirect );
			exit;
		}

		$prop       = $result['property'];
		$unit_count = isset( $result['unit_types'] ) && is_array( $result['unit_types'] ) ? count( $result['unit_types'] ) : 0;

		if ( ! empty( $prop['branding']['display_name'] ) ) {
			$settings['property_name'] = sanitize_text_field( (string) $prop['branding']['display_name'] );
		} elseif ( ! empty( $prop['name'] ) ) {
			$settings['property_name'] = sanitize_text_field( (string) $prop['name'] );
		}
		if ( ! empty( $prop['branding']['primary_color'] ) ) {
			$hex = sanitize_hex_color( (string) $prop['branding']['primary_color'] );
			if ( ! empty( $hex ) ) {
				$settings['primary_color'] = strtoupper( $hex );
			}
		}
		if ( ! empty( $prop['branding']['primary_on_color'] ) ) {
			$on_hex = sanitize_hex_color( (string) $prop['branding']['primary_on_color'] );
			if ( ! empty( $on_hex ) ) {
				$settings['primary_on_color'] = strtoupper( $on_hex );
			}
		}
		update_option( LOVELACE_BOOKING_OPTION_KEY, self::sanitize_settings( $settings ) );

		$summary = sprintf(
			/* translators: 1: property name, 2: accommodation count */
			__( 'Connected to %1$s (%2$d accommodation type(s) synced along with verified brand colours).', 'lovelace-booking' ),
			$settings['property_name'],
			$unit_count
		);

		$redirect = add_query_arg(
			array(
				'lv_status' => 'ok',
				'lv_msg'    => rawurlencode( $summary ),
			),
			$redirect
		);
		wp_safe_redirect( $redirect );
		exit;
	}

	/**
	 * Clears the cached accommodation catalog transient.
	 *
	 * @return void
	 */
	public static function handle_clear_cache() {
		if ( ! current_user_can( 'manage_options' ) ) {
			wp_die( esc_html__( 'You do not have permission to manage Lovelace settings.', 'lovelace-booking' ) );
		}
		check_admin_referer( 'lovelace_booking_clear_cache' );

		$settings = self::get_settings();
		Lovelace_Booking_API_Client::clear_cache(
			(string) $settings['booking_origin'],
			(string) $settings['property_slug']
		);

		$redirect = add_query_arg(
			array(
				'lv_status' => 'ok',
				'lv_msg'    => rawurlencode( __( 'Cleared cached accommodation catalog.', 'lovelace-booking' ) ),
			),
			admin_url( 'options-general.php?page=lovelace-booking' )
		);
		wp_safe_redirect( $redirect );
		exit;
	}

	/**
	 * Renders the Settings -> Lovelace Booking page in wp-admin.
	 *
	 * @return void
	 */
	public static function render_page() {
		if ( ! current_user_can( 'manage_options' ) ) {
			return;
		}
		$s      = self::get_settings();
		$status = isset( $_GET['lv_status'] ) ? sanitize_key( wp_unslash( $_GET['lv_status'] ) ) : ''; // phpcs:ignore WordPress.Security.NonceVerification.Recommended
		$msg    = isset( $_GET['lv_msg'] ) ? sanitize_text_field( rawurldecode( wp_unslash( $_GET['lv_msg'] ) ) ) : ''; // phpcs:ignore WordPress.Security.NonceVerification.Recommended
		?>
		<div class="wrap">
			<h1><?php echo esc_html__( 'Lovelace Direct Booking', 'lovelace-booking' ); ?></h1>
			<p><?php echo esc_html__( 'Connect your WordPress site to your Lovelace booking engine. No API keys or guest PII are stored inside WordPress.', 'lovelace-booking' ); ?></p>

			<?php if ( '' !== $msg ) : ?>
				<div class="notice <?php echo 'ok' === $status ? 'notice-success' : 'notice-error'; ?> is-dismissible">
					<p><?php echo esc_html( $msg ); ?></p>
				</div>
			<?php endif; ?>

			<form method="post" action="options.php">
				<?php settings_fields( 'lovelace_booking_group' ); ?>
				<table class="form-table" role="presentation">
					<tr>
						<th scope="row">
							<label for="lv_booking_origin"><?php echo esc_html__( 'Public Booking Site URL', 'lovelace-booking' ); ?></label>
						</th>
						<td>
							<input
								id="lv_booking_origin"
								name="<?php echo esc_attr( LOVELACE_BOOKING_OPTION_KEY ); ?>[booking_origin]"
								type="url"
								class="regular-text code"
								placeholder="https://book.yourproperty.co.uk"
								value="<?php echo esc_attr( (string) $s['booking_origin'] ); ?>"
								required
							/>
							<p class="description"><?php echo esc_html__( 'Your public Lovelace booking web origin (https://...).', 'lovelace-booking' ); ?></p>
						</td>
					</tr>
					<tr>
						<th scope="row">
							<label for="lv_property_slug"><?php echo esc_html__( 'Property Slug', 'lovelace-booking' ); ?></label>
						</th>
						<td>
							<input
								id="lv_property_slug"
								name="<?php echo esc_attr( LOVELACE_BOOKING_OPTION_KEY ); ?>[property_slug]"
								type="text"
								class="regular-text code"
								placeholder="tir-bach"
								value="<?php echo esc_attr( (string) $s['property_slug'] ); ?>"
								required
							/>
							<p class="description"><?php echo esc_html__( 'Found in Lovelace Admin → Site Settings (e.g. tir-bach).', 'lovelace-booking' ); ?></p>
						</td>
					</tr>
					<tr>
						<th scope="row">
							<label for="lv_property_name"><?php echo esc_html__( 'Property Display Name', 'lovelace-booking' ); ?></label>
						</th>
						<td>
							<input
								id="lv_property_name"
								name="<?php echo esc_attr( LOVELACE_BOOKING_OPTION_KEY ); ?>[property_name]"
								type="text"
								class="regular-text"
								value="<?php echo esc_attr( (string) $s['property_name'] ); ?>"
							/>
						</td>
					</tr>
					<tr>
						<th scope="row">
							<label for="lv_primary_color"><?php echo esc_html__( 'Brand Button Colour (#RRGGBB)', 'lovelace-booking' ); ?></label>
						</th>
						<td>
							<input
								id="lv_primary_color"
								name="<?php echo esc_attr( LOVELACE_BOOKING_OPTION_KEY ); ?>[primary_color]"
								type="text"
								class="small-text code"
								maxlength="7"
								value="<?php echo esc_attr( (string) $s['primary_color'] ); ?>"
							/>
							<label for="lv_primary_on_color" style="margin-left:12px;"><?php echo esc_html__( 'Button Text Colour:', 'lovelace-booking' ); ?></label>
							<input
								id="lv_primary_on_color"
								name="<?php echo esc_attr( LOVELACE_BOOKING_OPTION_KEY ); ?>[primary_on_color]"
								type="text"
								class="small-text code"
								maxlength="7"
								value="<?php echo esc_attr( (string) $s['primary_on_color'] ); ?>"
							/>
							<p class="description"><?php echo esc_html__( 'Synced automatically when you click "Test Connection & Sync Brand Colours" below.', 'lovelace-booking' ); ?></p>
						</td>
					</tr>
					<tr>
						<th scope="row">
							<label for="lv_button_label"><?php echo esc_html__( 'Default CTA Button Text', 'lovelace-booking' ); ?></label>
						</th>
						<td>
							<input
								id="lv_button_label"
								name="<?php echo esc_attr( LOVELACE_BOOKING_OPTION_KEY ); ?>[button_label]"
								type="text"
								class="regular-text"
								value="<?php echo esc_attr( (string) $s['button_label'] ); ?>"
							/>
						</td>
					</tr>
					<tr>
						<th scope="row">
							<label for="lv_default_mode"><?php echo esc_html__( 'Button Click Behaviour', 'lovelace-booking' ); ?></label>
						</th>
						<td>
							<select id="lv_default_mode" name="<?php echo esc_attr( LOVELACE_BOOKING_OPTION_KEY ); ?>[default_mode]">
								<option value="modal" <?php selected( $s['default_mode'], 'modal' ); ?>><?php echo esc_html__( 'Open Booking Overlay Modal on Current Page (Recommended)', 'lovelace-booking' ); ?></option>
								<option value="new_tab" <?php selected( $s['default_mode'], 'new_tab' ); ?>><?php echo esc_html__( 'Open Booking Page in New Tab', 'lovelace-booking' ); ?></option>
								<option value="same_tab" <?php selected( $s['default_mode'], 'same_tab' ); ?>><?php echo esc_html__( 'Navigate in Current Tab', 'lovelace-booking' ); ?></option>
							</select>
						</td>
					</tr>
					<tr>
						<th scope="row">
							<label for="lv_cache_ttl"><?php echo esc_html__( 'Accommodation Card Cache (Seconds)', 'lovelace-booking' ); ?></label>
						</th>
						<td>
							<input
								id="lv_cache_ttl"
								name="<?php echo esc_attr( LOVELACE_BOOKING_OPTION_KEY ); ?>[cache_ttl]"
								type="number"
								min="60"
								max="86400"
								class="small-text"
								value="<?php echo esc_attr( (string) $s['cache_ttl'] ); ?>"
							/>
						</td>
					</tr>
				</table>
				<?php submit_button( __( 'Save Settings', 'lovelace-booking' ) ); ?>
			</form>

			<hr />

			<h2><?php echo esc_html__( 'Connection Check & Cache Tools', 'lovelace-booking' ); ?></h2>
			<div style="display:flex;gap:12px;flex-wrap:wrap;margin-bottom:24px;">
				<form method="post" action="<?php echo esc_url( admin_url( 'admin-post.php' ) ); ?>">
					<input type="hidden" name="action" value="lovelace_booking_test_connection" />
					<?php wp_nonce_field( 'lovelace_booking_test_connection' ); ?>
					<?php submit_button( __( 'Test Connection & Sync Brand Colours', 'lovelace-booking' ), 'secondary', 'submit', false ); ?>
				</form>
				<form method="post" action="<?php echo esc_url( admin_url( 'admin-post.php' ) ); ?>">
					<input type="hidden" name="action" value="lovelace_booking_clear_cache" />
					<?php wp_nonce_field( 'lovelace_booking_clear_cache' ); ?>
					<?php submit_button( __( 'Clear Cached Accommodation Catalog', 'lovelace-booking' ), 'secondary', 'submit', false ); ?>
				</form>
			</div>

			<h2><?php echo esc_html__( 'Gutenberg Blocks & Shortcodes Reference', 'lovelace-booking' ); ?></h2>
			<p><?php echo esc_html__( 'Search for "Lovelace" in the WordPress Block Editor (+ button), or paste any shortcode below into Elementor, Divi, Classic Editor, or widget areas:', 'lovelace-booking' ); ?></p>
			<table class="widefat striped" style="max-width:920px;">
				<thead>
					<tr>
						<th><?php echo esc_html__( 'Element', 'lovelace-booking' ); ?></th>
						<th><?php echo esc_html__( 'Shortcode', 'lovelace-booking' ); ?></th>
						<th><?php echo esc_html__( 'Optional Attributes', 'lovelace-booking' ); ?></th>
					</tr>
				</thead>
				<tbody>
					<tr>
						<td><strong><?php echo esc_html__( 'Full Live Booking Section', 'lovelace-booking' ); ?></strong></td>
						<td><code>[lovelace_booking]</code></td>
						<td><code>property="..." adults="2" children="0" category="GLAMPING" unit_type="..."</code></td>
					</tr>
					<tr>
						<td><strong><?php echo esc_html__( 'Availability Search Bar', 'lovelace-booking' ); ?></strong></td>
						<td><code>[lovelace_search]</code></td>
						<td><code>layout="horizontal|compact" target="modal|_top|_blank" button_text="Check Availability"</code></td>
					</tr>
					<tr>
						<td><strong><?php echo esc_html__( '"Book Direct" Button', 'lovelace-booking' ); ?></strong></td>
						<td><code>[lovelace_book_button]</code></td>
						<td><code>label="Book Direct" mode="modal|new_tab|same_tab" unit_type="..."</code></td>
					</tr>
					<tr>
						<td><strong><?php echo esc_html__( 'SEO Accommodation Cards', 'lovelace-booking' ); ?></strong></td>
						<td><code>[lovelace_accommodation]</code></td>
						<td><code>columns="2|3" category="..." button_mode="modal|link" schema="true|false"</code></td>
					</tr>
				</tbody>
			</table>
		</div>
		<?php
	}
}
