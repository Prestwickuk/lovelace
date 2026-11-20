<?php
/**
 * Renders Lovelace shortcodes and Gutenberg server-rendered blocks on the public WordPress frontend.
 */

if ( ! defined( 'ABSPATH' ) ) {
	exit;
}

class Lovelace_Booking_Renderer {

	/**
	 * Registers shortcodes and frontend assets.
	 *
	 * @return void
	 */
	public static function init() {
		add_action( 'wp_enqueue_scripts', array( __CLASS__, 'register_assets' ) );
		add_shortcode( 'lovelace_booking', array( __CLASS__, 'render_booking_widget' ) );
		add_shortcode( 'lovelace_search', array( __CLASS__, 'render_search_bar' ) );
		add_shortcode( 'lovelace_book_button', array( __CLASS__, 'render_book_button' ) );
		add_shortcode( 'lovelace_accommodation', array( __CLASS__, 'render_accommodation_grid' ) );
	}

	/**
	 * Registers CSS & JS so they are only enqueued on pages that render a Lovelace block or shortcode.
	 *
	 * @return void
	 */
	public static function register_assets() {
		wp_register_style(
			'lovelace-booking-frontend',
			LOVELACE_BOOKING_PLUGIN_URL . 'assets/lovelace-frontend.css',
			array(),
			LOVELACE_BOOKING_VERSION
		);
		wp_register_script(
			'lovelace-booking-frontend',
			LOVELACE_BOOKING_PLUGIN_URL . 'assets/lovelace-frontend.js',
			array(),
			LOVELACE_BOOKING_VERSION,
			true
		);
	}

	/**
	 * Enqueues frontend stylesheet and enhancer script on demand.
	 *
	 * @return void
	 */
	private static function enqueue_frontend_assets() {
		wp_enqueue_style( 'lovelace-booking-frontend' );
		wp_enqueue_script( 'lovelace-booking-frontend' );
	}

	/**
	 * Resolves effective origin, property slug, and brand style variables.
	 *
	 * @param array<string,mixed> $atts Shortcode/block attributes.
	 * @return array<string,mixed>
	 */
	private static function resolve_context( $atts ) {
		$s      = Lovelace_Booking_Settings::get_settings();
		$origin = Lovelace_Booking_API_Client::normalize_origin( (string) $s['booking_origin'] );
		$slug   = ! empty( $atts['property'] )
			? Lovelace_Booking_API_Client::sanitize_slug( (string) $atts['property'] )
			: Lovelace_Booking_API_Client::sanitize_slug( (string) $s['property_slug'] );

		$primary    = sanitize_hex_color( (string) $s['primary_color'] );
		$primary_on = sanitize_hex_color( (string) $s['primary_on_color'] );
		if ( empty( $primary ) ) {
			$primary = '#2F4F4A';
		}
		if ( empty( $primary_on ) ) {
			$primary_on = '#FFFFFF';
		}

		return array(
			'settings'   => $s,
			'origin'     => $origin,
			'slug'       => $slug,
			'primary'    => $primary,
			'primary_on' => $primary_on,
			'style_vars' => sprintf( '--lv-primary:%s;--lv-primary-on:%s;', $primary, $primary_on ),
		);
	}

	/**
	 * Renders a polite notice visible only to logged-in WordPress editors/admins when unconfigured.
	 *
	 * @return string
	 */
	private static function render_unconfigured_notice() {
		if ( ! current_user_can( 'edit_posts' ) ) {
			return '';
		}
		$url = admin_url( 'options-general.php?page=lovelace-booking' );
		return '<div class="lovelace-wp-admin-notice">'
			. esc_html__( 'Lovelace Booking is not configured yet.', 'lovelace-booking' ) . ' '
			. '<a href="' . esc_url( $url ) . '">' . esc_html__( 'Configure Booking URL & Property Slug →', 'lovelace-booking' ) . '</a>'
			. '</div>';
	}

	/**
	 * Formats minor currency units (e.g. pence/cents) into human-readable money string.
	 *
	 * @param int    $minor    Amount in minor units.
	 * @param string $currency ISO-4217 currency code.
	 * @return string
	 */
	public static function format_money( $minor, $currency ) {
		$cur = strtoupper( trim( (string) $currency ) );
		$sym = '';
		switch ( $cur ) {
			case 'GBP':
				$sym = '£';
				break;
			case 'EUR':
				$sym = '€';
				break;
			case 'USD':
				$sym = '$';
				break;
			default:
				$sym = $cur . ' ';
				break;
		}
		return $sym . number_format( ( (float) $minor ) / 100.0, 2, '.', ',' );
	}

	/**
	 * 1. [lovelace_booking] — Full embedded live booking section (/embed/<slug>).
	 *
	 * @param array<string,mixed>|string $raw_atts Attributes.
	 * @return string
	 */
	public static function render_booking_widget( $raw_atts = array() ) {
		$atts = shortcode_atts(
			array(
				'property'   => '',
				'adults'     => 2,
				'children'   => 0,
				'arrival'    => '',
				'departure'  => '',
				'category'   => '',
				'unit_type'  => '',
				'min_height' => 680,
			),
			is_array( $raw_atts ) ? $raw_atts : array(),
			'lovelace_booking'
		);

		$ctx = self::resolve_context( $atts );
		if ( '' === $ctx['origin'] || '' === $ctx['slug'] ) {
			return self::render_unconfigured_notice();
		}

		self::enqueue_frontend_assets();

		$query = array();
		$adults = max( 1, min( 30, absint( $atts['adults'] ) ) );
		if ( 2 !== $adults ) {
			$query['adults'] = (string) $adults;
		}
		$children = max( 0, min( 30, absint( $atts['children'] ) ) );
		if ( $children > 0 ) {
			$query['children'] = (string) $children;
		}
		if ( preg_match( '/^\d{4}-\d{2}-\d{2}$/', (string) $atts['arrival'] ) ) {
			$query['arrival'] = (string) $atts['arrival'];
		}
		if ( preg_match( '/^\d{4}-\d{2}-\d{2}$/', (string) $atts['departure'] ) ) {
			$query['departure'] = (string) $atts['departure'];
		}
		if ( preg_match( '/^[A-Za-z0-9_-]{1,32}$/', (string) $atts['category'] ) ) {
			$query['category'] = (string) $atts['category'];
		}
		$unit_type = Lovelace_Booking_API_Client::sanitize_slug( (string) $atts['unit_type'] );
		if ( '' !== $unit_type ) {
			$query['unit_type'] = $unit_type;
		}

		$embed_url = $ctx['origin'] . '/embed/' . rawurlencode( $ctx['slug'] );
		if ( ! empty( $query ) ) {
			$embed_url = add_query_arg( $query, $embed_url );
		}

		$min_h = max( 420, min( 1600, absint( $atts['min_height'] ) ) );
		$title = ! empty( $ctx['settings']['property_name'] )
			? sprintf( 'Book %s', (string) $ctx['settings']['property_name'] )
			: 'Online Booking';

		return sprintf(
			'<div class="lovelace-wp-embed-wrap" style="%s"><iframe class="lovelace-wp-iframe" src="%s" title="%s" loading="lazy" data-lovelace-origin="%s" data-lovelace-slug="%s" style="min-height:%dpx;"></iframe></div>',
			esc_attr( $ctx['style_vars'] ),
			esc_url( $embed_url ),
			esc_attr( $title ),
			esc_attr( $ctx['origin'] ),
			esc_attr( $ctx['slug'] ),
			$min_h
		);
	}

	/**
	 * 2. [lovelace_search] — Pure HTML/CSS availability search bar (with optional modal mode).
	 *
	 * @param array<string,mixed>|string $raw_atts Attributes.
	 * @return string
	 */
	public static function render_search_bar( $raw_atts = array() ) {
		$atts = shortcode_atts(
			array(
				'property'    => '',
				'layout'      => 'horizontal',
				'target'      => '_top',
				'button_text' => __( 'Check Availability', 'lovelace-booking' ),
			),
			is_array( $raw_atts ) ? $raw_atts : array(),
			'lovelace_search'
		);

		$ctx = self::resolve_context( $atts );
		if ( '' === $ctx['origin'] || '' === $ctx['slug'] ) {
			return self::render_unconfigured_notice();
		}

		self::enqueue_frontend_assets();

		$layout_class = 'compact' === $atts['layout'] ? 'lovelace-wp-search-compact' : 'lovelace-wp-search-horizontal';
		$target_mode  = in_array( $atts['target'], array( 'modal', '_blank', '_top' ), true ) ? (string) $atts['target'] : '_top';
		$form_target  = '_blank' === $target_mode ? '_blank' : '_top';
		$action_url   = $ctx['origin'] . '/stays';
		$embed_base   = $ctx['origin'] . '/embed/' . rawurlencode( $ctx['slug'] );
		$uid          = wp_unique_id( 'lv-s-' );

		ob_start();
		?>
		<form
			class="lovelace-wp-search-form <?php echo esc_attr( $layout_class ); ?>"
			action="<?php echo esc_url( $action_url ); ?>"
			method="get"
			target="<?php echo esc_attr( $form_target ); ?>"
			style="<?php echo esc_attr( $ctx['style_vars'] ); ?>"
			<?php if ( 'modal' === $target_mode ) : ?>
				data-lovelace-search-modal="<?php echo esc_url( $embed_base ); ?>"
				data-lovelace-origin="<?php echo esc_attr( $ctx['origin'] ); ?>"
				data-lovelace-slug="<?php echo esc_attr( $ctx['slug'] ); ?>"
			<?php endif; ?>
		>
			<input type="hidden" name="property" value="<?php echo esc_attr( $ctx['slug'] ); ?>" />
			<div class="lovelace-wp-field">
				<label for="<?php echo esc_attr( $uid . '-arr' ); ?>"><?php echo esc_html__( 'Check-in', 'lovelace-booking' ); ?></label>
				<input id="<?php echo esc_attr( $uid . '-arr' ); ?>" name="arrival" type="date" required />
			</div>
			<div class="lovelace-wp-field">
				<label for="<?php echo esc_attr( $uid . '-dep' ); ?>"><?php echo esc_html__( 'Check-out', 'lovelace-booking' ); ?></label>
				<input id="<?php echo esc_attr( $uid . '-dep' ); ?>" name="departure" type="date" required />
			</div>
			<div class="lovelace-wp-field lovelace-wp-field-sm">
				<label for="<?php echo esc_attr( $uid . '-ad' ); ?>"><?php echo esc_html__( 'Adults', 'lovelace-booking' ); ?></label>
				<select id="<?php echo esc_attr( $uid . '-ad' ); ?>" name="adults">
					<?php for ( $i = 1; $i <= 8; $i++ ) : ?>
						<option value="<?php echo esc_attr( (string) $i ); ?>" <?php selected( $i, 2 ); ?>>
							<?php echo esc_html( sprintf( _n( '%d Adult', '%d Adults', $i, 'lovelace-booking' ), $i ) ); ?>
						</option>
					<?php endfor; ?>
				</select>
			</div>
			<div class="lovelace-wp-field lovelace-wp-field-sm">
				<label for="<?php echo esc_attr( $uid . '-ch' ); ?>"><?php echo esc_html__( 'Children', 'lovelace-booking' ); ?></label>
				<select id="<?php echo esc_attr( $uid . '-ch' ); ?>" name="children">
					<?php for ( $c = 0; $c <= 6; $c++ ) : ?>
						<option value="<?php echo esc_attr( (string) $c ); ?>" <?php selected( $c, 0 ); ?>>
							<?php echo esc_html( sprintf( _n( '%d Child', '%d Children', $c, 'lovelace-booking' ), $c ) ); ?>
						</option>
					<?php endfor; ?>
				</select>
			</div>
			<button type="submit" class="lovelace-wp-btn">
				<?php echo esc_html( sanitize_text_field( (string) $atts['button_text'] ) ); ?>
			</button>
		</form>
		<?php
		return (string) ob_get_clean();
	}

	/**
	 * 3. [lovelace_book_button] — Branded CTA button (supports modal overlay or direct link).
	 *
	 * @param array<string,mixed>|string $raw_atts Attributes.
	 * @return string
	 */
	public static function render_book_button( $raw_atts = array() ) {
		$s    = Lovelace_Booking_Settings::get_settings();
		$atts = shortcode_atts(
			array(
				'property'  => '',
				'label'     => (string) $s['button_label'],
				'mode'      => (string) $s['default_mode'],
				'unit_type' => '',
			),
			is_array( $raw_atts ) ? $raw_atts : array(),
			'lovelace_book_button'
		);

		$ctx = self::resolve_context( $atts );
		if ( '' === $ctx['origin'] || '' === $ctx['slug'] ) {
			return self::render_unconfigured_notice();
		}

		self::enqueue_frontend_assets();

		$unit_type = Lovelace_Booking_API_Client::sanitize_slug( (string) $atts['unit_type'] );
		$direct_q  = array( 'property' => $ctx['slug'] );
		$embed_q   = array();
		if ( '' !== $unit_type ) {
			$direct_q['unit_type'] = $unit_type;
			$embed_q['unit_type']  = $unit_type;
		}

		$direct_path = '' !== $unit_type ? '/book' : '/stays';
		$direct_url  = add_query_arg( $direct_q, $ctx['origin'] . $direct_path );
		$embed_url   = $ctx['origin'] . '/embed/' . rawurlencode( $ctx['slug'] );
		if ( ! empty( $embed_q ) ) {
			$embed_url = add_query_arg( $embed_q, $embed_url );
		}

		$mode  = in_array( $atts['mode'], array( 'modal', 'new_tab', 'same_tab' ), true ) ? (string) $atts['mode'] : 'modal';
		$label = sanitize_text_field( (string) $atts['label'] );
		if ( '' === $label ) {
			$label = __( 'Book Direct', 'lovelace-booking' );
		}

		$extra_attrs = '';
		if ( 'modal' === $mode ) {
			$extra_attrs = sprintf(
				' data-lovelace-modal-url="%s" data-lovelace-origin="%s" data-lovelace-slug="%s"',
				esc_url( $embed_url ),
				esc_attr( $ctx['origin'] ),
				esc_attr( $ctx['slug'] )
			);
		} elseif ( 'new_tab' === $mode ) {
			$extra_attrs = ' target="_blank" rel="noopener noreferrer"';
		}

		return sprintf(
			'<span class="lovelace-wp-btn-wrap" style="%s"><a class="lovelace-wp-btn" href="%s"%s>%s</a></span>',
			esc_attr( $ctx['style_vars'] ),
			esc_url( $direct_url ),
			$extra_attrs,
			esc_html( $label )
		);
	}

	/**
	 * 4. [lovelace_accommodation] — Cached server-rendered SEO accommodation cards + optional JSON-LD.
	 *
	 * @param array<string,mixed>|string $raw_atts Attributes.
	 * @return string
	 */
	public static function render_accommodation_grid( $raw_atts = array() ) {
		$atts = shortcode_atts(
			array(
				'property'    => '',
				'columns'     => 2,
				'category'    => '',
				'button_mode' => 'modal',
				'schema'      => 'true',
			),
			is_array( $raw_atts ) ? $raw_atts : array(),
			'lovelace_accommodation'
		);

		$ctx = self::resolve_context( $atts );
		if ( '' === $ctx['origin'] || '' === $ctx['slug'] ) {
			return self::render_unconfigured_notice();
		}

		$catalog = Lovelace_Booking_API_Client::fetch_catalog(
			$ctx['origin'],
			$ctx['slug'],
			false,
			(int) $ctx['settings']['cache_ttl']
		);
		if ( is_wp_error( $catalog ) ) {
			if ( current_user_can( 'edit_posts' ) ) {
				return '<div class="lovelace-wp-admin-notice">' . esc_html( $catalog->get_error_message() ) . '</div>';
			}
			return self::render_book_button( array( 'property' => $ctx['slug'] ) );
		}

		self::enqueue_frontend_assets();

		$prop       = $catalog['property'];
		$currency   = ! empty( $prop['currency'] ) ? (string) $prop['currency'] : 'GBP';
		$unit_types = isset( $catalog['unit_types'] ) && is_array( $catalog['unit_types'] ) ? $catalog['unit_types'] : array();
		$cat_filter = strtoupper( trim( (string) $atts['category'] ) );

		if ( '' !== $cat_filter ) {
			$unit_types = array_values(
				array_filter(
					$unit_types,
					function ( $ut ) use ( $cat_filter ) {
						return isset( $ut['category'] ) && strtoupper( (string) $ut['category'] ) === $cat_filter;
					}
				)
			);
		}

		if ( empty( $unit_types ) ) {
			return '<p class="lovelace-wp-empty">' . esc_html__( 'No accommodation categories are currently listed.', 'lovelace-booking' ) . '</p>';
		}

		$cols        = 3 === absint( $atts['columns'] ) ? 3 : 2;
		$button_mode = 'link' === $atts['button_mode'] ? 'new_tab' : 'modal';

		ob_start();
		?>
		<div class="lovelace-wp-cards lovelace-wp-cols-<?php echo esc_attr( (string) $cols ); ?>" style="<?php echo esc_attr( $ctx['style_vars'] ); ?>">
			<?php foreach ( $unit_types as $ut ) : ?>
				<?php
				$code        = isset( $ut['code'] ) ? Lovelace_Booking_API_Client::sanitize_slug( (string) $ut['code'] ) : '';
				$name        = isset( $ut['name'] ) ? (string) $ut['name'] : '';
				$desc        = isset( $ut['description'] ) ? (string) $ut['description'] : '';
				$max_guests  = isset( $ut['max_guests'] ) ? absint( $ut['max_guests'] ) : 2;
				$min_nights  = isset( $ut['min_nights'] ) ? max( 1, absint( $ut['min_nights'] ) ) : 1;
				$pets        = ! empty( $ut['pets_allowed'] );
				$rate_pence  = isset( $ut['base_nightly_rate_pence'] ) ? absint( $ut['base_nightly_rate_pence'] ) : 0;
				$amenities   = isset( $ut['amenities'] ) && is_array( $ut['amenities'] ) ? array_slice( $ut['amenities'], 0, 4 ) : array();
				$img_url     = '';
				$img_alt     = $name;
				if ( ! empty( $ut['images'][0]['url'] ) ) {
					$raw_img = (string) $ut['images'][0]['url'];
					$img_url = 0 === strpos( $raw_img, '/' ) ? $ctx['origin'] . $raw_img : $raw_img;
					if ( ! empty( $ut['images'][0]['alt'] ) ) {
						$img_alt = (string) $ut['images'][0]['alt'];
					}
				}
				$direct_url = add_query_arg(
					array(
						'property'  => $ctx['slug'],
						'unit_type' => $code,
					),
					$ctx['origin'] . '/book'
				);
				$embed_url  = add_query_arg(
					array( 'unit_type' => $code ),
					$ctx['origin'] . '/embed/' . rawurlencode( $ctx['slug'] )
				);
				?>
				<article class="lovelace-wp-card">
					<?php if ( '' !== $img_url ) : ?>
						<div class="lovelace-wp-card-media">
							<img src="<?php echo esc_url( $img_url ); ?>" alt="<?php echo esc_attr( $img_alt ); ?>" loading="lazy" />
						</div>
					<?php endif; ?>
					<div class="lovelace-wp-card-body">
						<h3 class="lovelace-wp-card-title"><?php echo esc_html( $name ); ?></h3>
						<?php if ( '' !== $desc ) : ?>
							<p class="lovelace-wp-card-desc"><?php echo esc_html( $desc ); ?></p>
						<?php endif; ?>
						<div class="lovelace-wp-chips">
							<span class="lovelace-wp-chip">
								<?php echo esc_html( sprintf( __( 'Up to %d guests', 'lovelace-booking' ), $max_guests ) ); ?>
							</span>
							<span class="lovelace-wp-chip">
								<?php echo $pets ? esc_html__( 'Pets welcome', 'lovelace-booking' ) : esc_html__( 'No pets', 'lovelace-booking' ); ?>
							</span>
							<?php if ( $min_nights > 1 ) : ?>
								<span class="lovelace-wp-chip">
									<?php echo esc_html( sprintf( __( 'Min %d nights', 'lovelace-booking' ), $min_nights ) ); ?>
								</span>
							<?php endif; ?>
							<?php foreach ( $amenities as $am ) : ?>
								<span class="lovelace-wp-chip"><?php echo esc_html( ucwords( str_replace( array( '-', '_' ), ' ', (string) $am ) ) ); ?></span>
							<?php endforeach; ?>
						</div>
						<div class="lovelace-wp-card-footer">
							<div>
								<div class="lovelace-wp-price">
									<?php echo esc_html( sprintf( __( 'From %s', 'lovelace-booking' ), self::format_money( $rate_pence, $currency ) ) ); ?>
								</div>
								<div class="lovelace-wp-price-sub"><?php echo esc_html__( 'per night · direct rate', 'lovelace-booking' ); ?></div>
							</div>
							<a
								class="lovelace-wp-btn"
								href="<?php echo esc_url( $direct_url ); ?>"
								<?php if ( 'modal' === $button_mode ) : ?>
									data-lovelace-modal-url="<?php echo esc_url( $embed_url ); ?>"
									data-lovelace-origin="<?php echo esc_attr( $ctx['origin'] ); ?>"
									data-lovelace-slug="<?php echo esc_attr( $ctx['slug'] ); ?>"
								<?php else : ?>
									target="_blank" rel="noopener noreferrer"
								<?php endif; ?>
							>
								<?php echo esc_html__( 'Check Dates & Book', 'lovelace-booking' ); ?>
							</a>
						</div>
					</div>
				</article>
			<?php endforeach; ?>
		</div>
		<?php
		if ( 'true' === (string) $atts['schema'] && ! is_admin() ) {
			$schema = array(
				'@context'    => 'https://schema.org',
				'@type'       => 'LodgingBusiness',
				'name'        => ! empty( $prop['branding']['display_name'] ) ? (string) $prop['branding']['display_name'] : (string) $prop['name'],
				'description' => ! empty( $prop['tagline'] ) ? (string) $prop['tagline'] : '',
				'checkinTime' => ! empty( $prop['check_in_time'] ) ? (string) $prop['check_in_time'] : '',
				'checkoutTime'=> ! empty( $prop['check_out_time'] ) ? (string) $prop['check_out_time'] : '',
				'url'         => get_permalink() ? get_permalink() : home_url( '/' ),
			);
			echo '<script type="application/ld+json">' . wp_json_encode( $schema, JSON_UNESCAPED_SLASHES | JSON_HEX_TAG | JSON_HEX_AMP ) . '</script>';
		}
		return (string) ob_get_clean();
	}
}
