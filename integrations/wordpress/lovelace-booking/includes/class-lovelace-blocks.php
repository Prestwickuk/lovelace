<?php
/**
 * Registers Gutenberg dynamic blocks backed by Lovelace_Booking_Renderer.
 */

if ( ! defined( 'ABSPATH' ) ) {
	exit;
}

class Lovelace_Booking_Blocks {

	/**
	 * Hooks into WordPress block registration.
	 *
	 * @return void
	 */
	public static function init() {
		add_action( 'init', array( __CLASS__, 'register_blocks' ) );
		add_filter( 'block_categories_all', array( __CLASS__, 'register_category' ), 10, 1 );
	}

	/**
	 * Adds a dedicated "Lovelace Booking" category in the Gutenberg block inserter.
	 *
	 * @param array<int,array<string,mixed>> $categories Existing block categories.
	 * @return array<int,array<string,mixed>>
	 */
	public static function register_category( $categories ) {
		array_unshift(
			$categories,
			array(
				'slug'  => 'lovelace-booking',
				'title' => __( 'Lovelace Booking', 'lovelace-booking' ),
				'icon'  => 'calendar-alt',
			)
		);
		return $categories;
	}

	/**
	 * Registers editor script + 4 dynamic blocks.
	 *
	 * @return void
	 */
	public static function register_blocks() {
		if ( ! function_exists( 'register_block_type' ) ) {
			return;
		}

		wp_register_style(
			'lovelace-booking-editor-style',
			LOVELACE_BOOKING_PLUGIN_URL . 'assets/lovelace-frontend.css',
			array(),
			LOVELACE_BOOKING_VERSION
		);

		wp_register_script(
			'lovelace-booking-blocks-editor',
			LOVELACE_BOOKING_PLUGIN_URL . 'assets/lovelace-blocks-editor.js',
			array( 'wp-blocks', 'wp-element', 'wp-components', 'wp-block-editor', 'wp-server-side-render' ),
			LOVELACE_BOOKING_VERSION,
			true
		);

		register_block_type(
			'lovelace/booking-widget',
			array(
				'api_version'     => 2,
				'editor_script'   => 'lovelace-booking-blocks-editor',
				'editor_style'    => 'lovelace-booking-editor-style',
				'render_callback' => array( 'Lovelace_Booking_Renderer', 'render_booking_widget' ),
				'attributes'      => array(
					'property'   => array( 'type' => 'string', 'default' => '' ),
					'adults'     => array( 'type' => 'number', 'default' => 2 ),
					'children'   => array( 'type' => 'number', 'default' => 0 ),
					'category'   => array( 'type' => 'string', 'default' => '' ),
					'unit_type'  => array( 'type' => 'string', 'default' => '' ),
					'min_height' => array( 'type' => 'number', 'default' => 680 ),
				),
			)
		);

		register_block_type(
			'lovelace/search-bar',
			array(
				'api_version'     => 2,
				'editor_script'   => 'lovelace-booking-blocks-editor',
				'editor_style'    => 'lovelace-booking-editor-style',
				'render_callback' => array( 'Lovelace_Booking_Renderer', 'render_search_bar' ),
				'attributes'      => array(
					'property'    => array( 'type' => 'string', 'default' => '' ),
					'layout'      => array( 'type' => 'string', 'default' => 'horizontal' ),
					'target'      => array( 'type' => 'string', 'default' => 'modal' ),
					'button_text' => array( 'type' => 'string', 'default' => 'Check Availability' ),
				),
			)
		);

		register_block_type(
			'lovelace/book-button',
			array(
				'api_version'     => 2,
				'editor_script'   => 'lovelace-booking-blocks-editor',
				'editor_style'    => 'lovelace-booking-editor-style',
				'render_callback' => array( 'Lovelace_Booking_Renderer', 'render_book_button' ),
				'attributes'      => array(
					'property'  => array( 'type' => 'string', 'default' => '' ),
					'label'     => array( 'type' => 'string', 'default' => '' ),
					'mode'      => array( 'type' => 'string', 'default' => 'modal' ),
					'unit_type' => array( 'type' => 'string', 'default' => '' ),
				),
			)
		);

		register_block_type(
			'lovelace/accommodation-grid',
			array(
				'api_version'     => 2,
				'editor_script'   => 'lovelace-booking-blocks-editor',
				'editor_style'    => 'lovelace-booking-editor-style',
				'render_callback' => array( 'Lovelace_Booking_Renderer', 'render_accommodation_grid' ),
				'attributes'      => array(
					'property'    => array( 'type' => 'string', 'default' => '' ),
					'columns'     => array( 'type' => 'number', 'default' => 2 ),
					'category'    => array( 'type' => 'string', 'default' => '' ),
					'button_mode' => array( 'type' => 'string', 'default' => 'modal' ),
					'schema'      => array( 'type' => 'string', 'default' => 'true' ),
				),
			)
		);
	}
}
